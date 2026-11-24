// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package agentsink

import (
	"context"
	"errors"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/agenthub"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

type call struct {
	vhost, agent, device, name string
	fields                     map[string]any
}

type fakeHub struct {
	calls []call
	err   error
}

func (f *fakeHub) RunCommand(_ context.Context, vhost, agentID, deviceID, name string, fields map[string]any) (agenthub.CommandReply, error) {
	f.calls = append(f.calls, call{vhost, agentID, deviceID, name, fields})
	return agenthub.CommandReply{}, f.err
}

func TestEachMessageRunsTheSavedCommandWithItsFields(t *testing.T) {
	hub := &fakeHub{}
	s, err := New(hub, Config{AgentID: "agt1", DeviceID: "dev-a", Command: "set", VHost: "plant"})
	if err != nil {
		t.Fatal(err)
	}
	msg := message.AcquireMessage()
	msg.SetData("target", 12.5)
	if err := s.Write(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	c := hub.calls[0]
	if c.vhost != "plant" || c.agent != "agt1" || c.device != "dev-a" || c.name != "set" || c.fields["target"] != 12.5 {
		t.Fatalf("call = %+v", c)
	}
}

func TestTheHubsErrorIsReturnedAsIs(t *testing.T) {
	want := errors.Join(hermod.ErrPermanent, errors.New("commands are not allowed"))
	s, _ := New(&fakeHub{err: want}, Config{AgentID: "a", DeviceID: "d", Command: "c"})
	if err := s.Write(t.Context(), message.AcquireMessage()); !errors.Is(err, hermod.ErrPermanent) {
		t.Fatalf("err = %v", err)
	}
}

func TestConfigNeedsAgentDeviceAndCommand(t *testing.T) {
	for _, c := range []Config{{DeviceID: "d", Command: "c"}, {AgentID: "a", Command: "c"}, {AgentID: "a", DeviceID: "d"}} {
		if _, err := New(&fakeHub{}, c); err == nil {
			t.Errorf("%+v was accepted", c)
		}
	}
}
