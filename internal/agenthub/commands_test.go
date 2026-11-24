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
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

func (f *fixture) connectAnswer(answer func(*pb.Command) *pb.CommandResult) *agent {
	f.t.Helper()
	a, err := f.connectAnswering(f.token, &pb.Hello{}, answer)
	if err != nil {
		f.t.Fatalf("connect: %v", err)
	}
	return a
}

func TestSendCommandReachesTheAgentAndReturnsItsReply(t *testing.T) {
	f := newFixture(t)
	a := f.connectAnswer(func(c *pb.Command) *pb.CommandResult {
		return &pb.CommandResult{CommandId: c.GetCommandId(), Status: pb.CommandStatus_COMMAND_STATUS_REPLIED,
			Reply: []byte("12.40 kg"), Written: true}
	})
	res, err := f.hub.SendCommand(t.Context(), "agt1", "dev-a", []byte("W\r\n"), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Replied || string(res.Reply) != "12.40 kg" {
		t.Fatalf("result = %+v", res)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.commands[0]
	if c.GetDeviceId() != "dev-a" || string(c.GetPayload()) != "W\r\n" || c.GetReplyTimeoutMs() != 2000 || c.GetCommandId() == "" {
		t.Fatalf("command = %v", c)
	}
}

// A command for an agent that is not connected fails at once: it is never
// queued to reach a device hours later.
func TestSendCommandToAnOfflineAgentFailsAtOnce(t *testing.T) {
	f := newFixture(t)
	start := time.Now()
	_, err := f.hub.SendCommand(t.Context(), "agt1", "dev-a", []byte("T"), 0)
	if !errors.Is(err, ErrAgentOffline) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("the offline failure waited")
	}
}

func TestSendCommandReportsTheAgentsFailureAndWhetherItWrote(t *testing.T) {
	f := newFixture(t)
	f.connectAnswer(func(c *pb.Command) *pb.CommandResult {
		return &pb.CommandResult{CommandId: c.GetCommandId(), Status: pb.CommandStatus_COMMAND_STATUS_FAILED,
			Error: "no reply within 1s", Written: true}
	})
	_, err := f.hub.SendCommand(t.Context(), "agt1", "dev-a", []byte("W"), time.Second)
	var ce *CommandError
	if !errors.As(err, &ce) || !ce.Written || ce.Msg != "no reply within 1s" {
		t.Fatalf("err = %#v", err)
	}
}

// When the agent drops off with a command in flight, nobody knows whether
// it reached the device, so it is reported as possibly written.
func TestSendCommandFailsWhenTheAgentDisconnects(t *testing.T) {
	f := newFixture(t)
	var a *agent
	got := make(chan struct{})
	a = f.connectAnswer(func(*pb.Command) *pb.CommandResult {
		close(got)
		a.cancel()
		return nil
	})
	_ = a
	_, err := f.hub.SendCommand(t.Context(), "agt1", "dev-a", []byte("T"), 0)
	<-got
	var ce *CommandError
	if !errors.As(err, &ce) || !ce.Written {
		t.Fatalf("err = %#v", err)
	}
}

func TestSendCommandGivesUpWhenTheContextEnds(t *testing.T) {
	f := newFixture(t)
	f.connectAnswer(nil) // never answers
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := f.hub.SendCommand(ctx, "agt1", "dev-a", []byte("T"), 0)
	var ce *CommandError
	if !errors.As(err, &ce) || !ce.Written || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %#v", err)
	}
}

func TestSettingsCarryAllowCommands(t *testing.T) {
	f := newFixture(t)
	_ = f.store.update("agt1", func(a *storage.Agent) { a.Devices[0].AllowCommands = true })
	a := f.connect(&pb.Hello{})
	a.waitFor("settings", func() bool { return len(a.settings) == 1 })
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.settings[0].GetDevices()[0].GetAllowCommands() || a.settings[0].GetDevices()[1].GetAllowCommands() {
		t.Fatalf("settings = %v", a.settings[0])
	}
}
