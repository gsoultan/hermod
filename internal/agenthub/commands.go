// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package agenthub

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

// commandGrace is how long past its reply timeout a command is waited for:
// the agent may queue it behind another command to the same device, and the
// device gets a few seconds to take the write.
const commandGrace = 15 * time.Second

// ErrAgentOffline is returned for a command to an agent not connected here.
// The command is not kept: it never reaches the device later.
var ErrAgentOffline = errors.New("the agent is not connected")

// CommandReply is what a command did on the device.
type CommandReply struct {
	// Replied is set when a reply was asked for and came; Reply holds it.
	Replied bool
	Reply   []byte
}

// CommandError is a command that failed. Written tells whether the device
// may have received it, so a caller knows whether sending it again could
// repeat it: true when the agent says it wrote it, and when nobody knows
// because the agent or the caller went away first.
type CommandError struct {
	Msg     string
	Written bool
	Err     error
}

func (e *CommandError) Error() string {
	if e.Written {
		return "command may have reached the device: " + e.Msg
	}
	return "command was not sent to the device: " + e.Msg
}

func (e *CommandError) Unwrap() error { return e.Err }

// SendCommand writes payload to the agent's device and waits for what the
// agent says happened. With replyTimeout above zero the agent waits that long
// for the device's reply. A command for an agent not connected here fails at
// once with ErrAgentOffline.
func (h *Hub) SendCommand(ctx context.Context, agentID, deviceID string, payload []byte, replyTimeout time.Duration) (CommandReply, error) {
	cmd := &pb.Command{
		CommandId: rand.Text(), DeviceId: deviceID, Payload: payload,
		ReplyTimeoutMs: uint32(max(replyTimeout, 0) / time.Millisecond),
	}
	sess, result, err := h.queueCommand(agentID, cmd)
	if err != nil {
		return CommandReply{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, max(replyTimeout, 0)+commandGrace)
	defer cancel()
	select {
	case r := <-result:
		if r.GetStatus() == pb.CommandStatus_COMMAND_STATUS_FAILED {
			return CommandReply{}, &CommandError{Msg: r.GetError(), Written: r.GetWritten()}
		}
		return CommandReply{Replied: r.GetStatus() == pb.CommandStatus_COMMAND_STATUS_REPLIED, Reply: r.GetReply()}, nil
	case <-sess.done:
		h.dropCommand(sess, cmd)
		return CommandReply{}, &CommandError{Msg: "the agent disconnected before it answered", Written: true, Err: ErrAgentOffline}
	case <-ctx.Done():
		sent := h.dropCommand(sess, cmd)
		return CommandReply{}, &CommandError{Msg: fmt.Sprintf("no answer from the agent: %v", ctx.Err()), Written: sent, Err: ctx.Err()}
	}
}

// queueCommand puts cmd in the outbox of the agent's stream.
func (h *Hub) queueCommand(agentID string, cmd *pb.Command) (*session, chan *pb.CommandResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st, ok := h.agents[agentID]
	if !ok || st.sess == nil {
		return nil, nil, ErrAgentOffline
	}
	sess := st.sess
	select {
	case <-sess.done:
		return nil, nil, ErrAgentOffline
	default:
	}
	result := make(chan *pb.CommandResult, 1)
	sess.waiters[cmd.GetCommandId()] = result
	sess.outbox = append(sess.outbox, cmd)
	sess.poke()
	return sess, result, nil
}

// dropCommand stops waiting for cmd, and reports whether it had been sent.
func (h *Hub) dropCommand(sess *session, cmd *pb.Command) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(sess.waiters, cmd.GetCommandId())
	for i, c := range sess.outbox {
		if c == cmd {
			sess.outbox = append(sess.outbox[:i:i], sess.outbox[i+1:]...)
			return false
		}
	}
	return true
}

// commandResult hands the agent's answer to whoever waits for it.
func (h *Hub) commandResult(sess *session, r *pb.CommandResult) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if w, ok := sess.waiters[r.GetCommandId()]; ok {
		delete(sess.waiters, r.GetCommandId())
		w <- r
	}
}

// RunCommand sends the agent's device the command saved under name, filled
// from fields. The agent is read from the store each time, so a template or
// allow_commands turned off takes effect at once. vhost is the caller's; an
// agent of another vhost is treated as not there.
//
// A refusal that sending again cannot fix (no such agent, device or
// command, commands not allowed, a field that cannot fill the template, an
// agent not connected, or a command that may have reached the device) wraps
// hermod.ErrPermanent.
func (h *Hub) RunCommand(ctx context.Context, vhost, agentID, deviceID, name string, fields map[string]any) (CommandReply, error) {
	as, ok := h.agentStore()
	if !ok {
		return CommandReply{}, errors.New("no database that can hold agents is configured yet")
	}
	a, err := as.GetAgent(ctx, agentID)
	if errors.Is(err, storage.ErrNotFound) || (err == nil && normVHost(a.VHost) != normVHost(vhost)) {
		return CommandReply{}, fmt.Errorf("%w: no agent %q", hermod.ErrPermanent, agentID)
	}
	if err != nil {
		return CommandReply{}, fmt.Errorf("read agent %q: %w", agentID, err)
	}
	_, cmd, err := a.FindCommand(deviceID, name)
	if err != nil {
		return CommandReply{}, fmt.Errorf("%w: %w", hermod.ErrPermanent, err)
	}
	payload, err := storage.RenderAgentCommand(cmd, fields)
	if err != nil {
		return CommandReply{}, fmt.Errorf("%w: command %q: %w", hermod.ErrPermanent, name, err)
	}
	reply, err := h.SendCommand(ctx, agentID, deviceID, payload, time.Duration(cmd.ReplyTimeoutMS)*time.Millisecond)
	var ce *CommandError
	switch {
	case errors.As(err, &ce) && ce.Written:
		// Sending it again could repeat it on the device.
		return reply, fmt.Errorf("%w: %w", hermod.ErrPermanent, err)
	case errors.Is(err, ErrAgentOffline):
		// Not retried, so a command never reaches a device long after the
		// event that asked for it.
		return reply, fmt.Errorf("%w: agent %q: %w", hermod.ErrPermanent, agentID, err)
	}
	return reply, err
}
