// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package agentsink is the Agent device sink: a workflow writing a command to
// a device read by a hermod-agent. Only a command saved on the device, with
// Allow commands on, is ever sent; the message only fills its placeholders.
// Put an Approval node before it to have a person approve each command.
package agentsink

import (
	"context"
	"errors"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/agenthub"
)

// Hub runs commands on agents' devices; *agenthub.Hub is one.
type Hub interface {
	RunCommand(ctx context.Context, vhost, agentID, deviceID, name string, fields map[string]any) (agenthub.CommandReply, error)
}

// Config is what an Agent device sink writes to.
type Config struct {
	AgentID  string
	DeviceID string
	// Command is the name of a command saved on the device.
	Command string
	// VHost is the workflow's vhost. Only an agent of the same vhost is
	// written to.
	VHost string
}

// Sink is the Agent device sink.
type Sink struct {
	hub Hub
	cfg Config
}

// New builds an Agent device sink on hub.
func New(hub Hub, cfg Config) (*Sink, error) {
	switch {
	case cfg.AgentID == "":
		return nil, errors.New("agent device sink: choose the agent")
	case cfg.DeviceID == "":
		return nil, errors.New("agent device sink: choose the device")
	case cfg.Command == "":
		return nil, errors.New("agent device sink: choose a command saved on the device")
	}
	return &Sink{hub: hub, cfg: cfg}, nil
}

// Write sends the command, its placeholders filled from the message's
// fields, and returns once the agent says it was written, or replied when
// the command waits for a reply. A failure another attempt cannot fix, or a
// command that may already have reached the device, wraps
// hermod.ErrPermanent, so it is not sent twice.
func (s *Sink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	_, err := s.hub.RunCommand(ctx, s.cfg.VHost, s.cfg.AgentID, s.cfg.DeviceID, s.cfg.Command, msg.Data())
	return err
}

// Ping always succeeds: an agent that is offline now may be back before the
// next message, and a command to it then fails on its own.
func (s *Sink) Ping(context.Context) error { return nil }

// Close does nothing; the sink holds nothing open.
func (s *Sink) Close() error { return nil }
