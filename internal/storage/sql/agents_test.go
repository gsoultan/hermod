// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package sql

import (
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

func agentStore(t *testing.T) (storage.AgentStore, *sqlStorage) {
	t.Helper()
	s := newRotationStorage(t)
	var st storage.Storage = s
	as, ok := st.(storage.AgentStore)
	if !ok {
		t.Fatal("the SQL store does not implement AgentStore")
	}
	return as, s
}

func sampleAgent(id, vhost, name string) storage.Agent {
	return storage.Agent{
		ID: id, VHost: vhost, Name: name, TokenHash: "hash-" + id, CreatedBy: "ada",
		Devices: []storage.AgentDevice{{
			DeviceID: "usb:0403:6001:A1", Kind: storage.AgentDeviceSerial, Name: "scale", Enabled: true,
			Serial:  &storage.AgentSerial{BaudRate: 9600},
			Framing: storage.AgentFraming{Kind: storage.AgentFramingLine},
		}},
	}
}

func TestAgentRoundTrips(t *testing.T) {
	st, _ := agentStore(t)
	ctx := t.Context()
	if _, err := st.GetAgent(ctx, "a1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing agent: err = %v, want ErrNotFound", err)
	}
	if err := st.CreateAgent(ctx, sampleAgent("a1", "plant", "line 1")); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	got, err := st.GetAgent(ctx, "a1")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if got.VHost != "plant" || got.Name != "line 1" || got.TokenHash != "hash-a1" || got.CreatedBy != "ada" ||
		got.CreatedAt.IsZero() || len(got.Devices) != 1 || got.Devices[0].Serial.BaudRate != 9600 {
		t.Fatalf("round trip lost fields: %+v", got)
	}

	devices := []storage.AgentDevice{{DeviceID: "tcp:plc", Kind: storage.AgentDeviceTCP,
		TCP: &storage.AgentTCP{Address: "10.0.0.5:502", Mode: "client"}}}
	if err := st.UpdateAgentDevices(ctx, "a1", "line one", devices); err != nil {
		t.Fatalf("UpdateAgentDevices: %v", err)
	}
	seen := time.Now().UTC().Truncate(time.Second)
	status := storage.AgentStatus{Hostname: "pi", OS: "linux", Arch: "arm64", Version: "1.0", LastSeenAt: seen,
		Ports: []storage.AgentPort{{DeviceID: "port:/dev/ttyS0", Kind: storage.AgentDeviceSerial, Present: true}},
		Spool: storage.AgentSpool{Waiting: 7, Dropped: 1}}
	if err := st.UpdateAgentStatus(ctx, "a1", status); err != nil {
		t.Fatalf("UpdateAgentStatus: %v", err)
	}
	if err := st.UpdateAgentToken(ctx, "a1", "new-hash"); err != nil {
		t.Fatalf("UpdateAgentToken: %v", err)
	}
	got, err = st.GetAgent(ctx, "a1")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if got.Name != "line one" || len(got.Devices) != 1 || got.Devices[0].TCP.Address != "10.0.0.5:502" ||
		got.TokenHash != "new-hash" || got.Status.Hostname != "pi" || !got.Status.LastSeenAt.Equal(seen) ||
		got.Status.Spool.Waiting != 7 || len(got.Status.Ports) != 1 {
		t.Fatalf("updates were not kept: %+v", got)
	}
}

func TestAgentUpdatesRefuseBadSettingsAndMissingAgents(t *testing.T) {
	st, _ := agentStore(t)
	ctx := t.Context()
	if err := st.CreateAgent(ctx, storage.Agent{ID: "a1", VHost: "plant"}); err == nil {
		t.Fatal("an agent with no name was saved")
	}
	if err := st.CreateAgent(ctx, sampleAgent("a1", "plant", "x")); err != nil {
		t.Fatal(err)
	}
	bad := []storage.AgentDevice{{DeviceID: "d", Kind: "bluetooth"}}
	if err := st.UpdateAgentDevices(ctx, "a1", "x", bad); err == nil {
		t.Fatal("settings the agent would refuse were saved")
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
}

func TestAgentsAreListedPerVHostAndGoWithIt(t *testing.T) {
	st, s := agentStore(t)
	ctx := t.Context()
	for _, a := range []storage.Agent{
		sampleAgent("a2", "plant", "b"), sampleAgent("a1", "plant", "a"), sampleAgent("a3", "office", "c"),
	} {
		if err := st.CreateAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	plant, err := st.ListAgents(ctx, "plant")
	if err != nil {
		t.Fatal(err)
	}
	if len(plant) != 2 || plant[0].Name != "a" || plant[1].Name != "b" {
		t.Fatalf("plant agents = %+v", plant)
	}
	all, err := st.ListAgents(ctx, "")
	if err != nil || len(all) != 3 {
		t.Fatalf("all agents = %d, %v", len(all), err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.renameVHostContents(ctx, tx, "plant", "factory"); err != nil {
		t.Fatalf("renameVHostContents: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if moved, _ := st.ListAgents(ctx, "factory"); len(moved) != 2 {
		t.Fatalf("agents after the vhost was renamed = %d, want 2", len(moved))
	}
	if err := st.DeleteAgents(ctx, "factory"); err != nil {
		t.Fatal(err)
	}
	if left, _ := st.ListAgents(ctx, ""); len(left) != 1 || left[0].ID != "a3" {
		t.Fatalf("agents left = %+v", left)
	}
	if err := st.DeleteAgent(ctx, "a3"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetAgent(ctx, "a3"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("deleted agent: err = %v", err)
	}
}
