// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build integration
// +build integration

package mongodb

import (
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// The same contract the SQL store keeps (internal/storage/sql/agents_test.go),
// against a live MongoDB.
func TestMongoAgents(t *testing.T) {
	s, _ := newTraceMongo(t)
	st, ok := s.(storage.AgentStore)
	if !ok {
		t.Fatal("the MongoDB store does not implement storage.AgentStore")
	}
	ctx := t.Context()
	if _, err := st.GetAgent(ctx, "a1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing agent: err = %v", err)
	}
	mk := func(id, vhost, name string) storage.Agent {
		return storage.Agent{ID: id, VHost: vhost, Name: name, TokenHash: "hash-" + id, CreatedBy: "ada",
			Devices: []storage.AgentDevice{{DeviceID: "d1", Kind: storage.AgentDeviceSerial,
				Serial: &storage.AgentSerial{BaudRate: 9600}}}}
	}
	if err := st.CreateAgent(ctx, storage.Agent{ID: "bad", VHost: "plant"}); err == nil {
		t.Fatal("an agent with no name was saved")
	}
	for _, a := range []storage.Agent{mk("a2", "plant", "b"), mk("a1", "plant", "a"), mk("a3", "office", "c")} {
		if err := st.CreateAgent(ctx, a); err != nil {
			t.Fatalf("CreateAgent: %v", err)
		}
	}
	got, err := st.GetAgent(ctx, "a1")
	if err != nil || got.Name != "a" || got.TokenHash != "hash-a1" || got.CreatedAt.IsZero() ||
		len(got.Devices) != 1 || got.Devices[0].Serial.BaudRate != 9600 {
		t.Fatalf("GetAgent = %+v, %v", got, err)
	}

	tcp := []storage.AgentDevice{{DeviceID: "tcp:plc", Kind: storage.AgentDeviceTCP,
		TCP: &storage.AgentTCP{Address: "10.0.0.5:502", Mode: "client"}}}
	if err := st.UpdateAgentDevices(ctx, "a1", "a one", tcp); err != nil {
		t.Fatalf("UpdateAgentDevices: %v", err)
	}
	if err := st.UpdateAgentDevices(ctx, "a1", "x", []storage.AgentDevice{{DeviceID: "d", Kind: "bluetooth"}}); err == nil {
		t.Fatal("settings the agent would refuse were saved")
	}
	seen := time.Now().UTC().Truncate(time.Millisecond)
	if err := st.UpdateAgentStatus(ctx, "a1", storage.AgentStatus{Hostname: "pi", LastSeenAt: seen,
		Spool: storage.AgentSpool{Waiting: 7}}); err != nil {
		t.Fatalf("UpdateAgentStatus: %v", err)
	}
	if err := st.UpdateAgentToken(ctx, "a1", "new-hash"); err != nil {
		t.Fatalf("UpdateAgentToken: %v", err)
	}
	got, _ = st.GetAgent(ctx, "a1")
	if got.Name != "a one" || got.Devices[0].TCP.Address != "10.0.0.5:502" || got.TokenHash != "new-hash" ||
		got.Status.Hostname != "pi" || !got.Status.LastSeenAt.Equal(seen) || got.Status.Spool.Waiting != 7 {
		t.Fatalf("updates were not kept: %+v", got)
	}
	for name, err := range map[string]error{
		"devices": st.UpdateAgentDevices(ctx, "nope", "x", nil),
		"status":  st.UpdateAgentStatus(ctx, "nope", storage.AgentStatus{}),
		"token":   st.UpdateAgentToken(ctx, "nope", "h"),
		"delete":  st.DeleteAgent(ctx, "nope"),
	} {
		if !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("%s of a missing agent: err = %v, want ErrNotFound", name, err)
		}
	}

	plant, err := st.ListAgents(ctx, "plant")
	if err != nil || len(plant) != 2 || plant[0].Name != "a one" || plant[1].Name != "b" {
		t.Fatalf("plant agents = %+v, %v", plant, err)
	}
	if all, _ := st.ListAgents(ctx, ""); len(all) != 3 {
		t.Fatalf("all agents = %d, want 3", len(all))
	}
	if err := s.(*mongoStorage).renameVHostContents(ctx, "plant", "factory"); err != nil {
		t.Fatalf("renameVHostContents: %v", err)
	}
	if moved, _ := st.ListAgents(ctx, "factory"); len(moved) != 2 {
		t.Fatalf("agents after the rename = %d, want 2", len(moved))
	}
	if err := st.DeleteAgents(ctx, "factory"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteAgent(ctx, "a3"); err != nil {
		t.Fatal(err)
	}
	if left, _ := st.ListAgents(ctx, ""); len(left) != 0 {
		t.Fatalf("agents left = %+v", left)
	}
}
