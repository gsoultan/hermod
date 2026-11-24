// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package agentsource

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/agenthub"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

func record(seq uint64, device, raw string, at time.Time) *pb.Record {
	return &pb.Record{Seq: seq, DeviceId: device, Raw: []byte(raw), ReceivedAtUnixNano: at.UnixNano()}
}

func TestNewRefusesAConfigurationItCannotRead(t *testing.T) {
	hub := agenthub.New(nil, nil)
	if _, err := New(hub, Config{}); err == nil {
		t.Fatal("a source with no agent was built")
	}
	if _, err := New(hub, Config{AgentID: "a", Format: "xml"}); err == nil {
		t.Fatal("a source with an unknown format was built")
	}
	if _, err := New(hub, Config{AgentID: "a", Format: "json"}); err != nil {
		t.Fatalf("a valid source was refused: %v", err)
	}
}

func TestParseDevicesSplitsAndTrims(t *testing.T) {
	got := ParseDevices(" usb:1:2:3 , ,tcp:plc")
	if len(got) != 2 || got[0] != "usb:1:2:3" || got[1] != "tcp:plc" {
		t.Fatalf("ParseDevices = %q", got)
	}
}

func decode(t *testing.T, msg hermod.Message) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(msg.Payload(), &out); err != nil {
		t.Fatalf("payload %q is not JSON: %v", msg.Payload(), err)
	}
	return out
}

func TestBuildsAMessageInEachFormat(t *testing.T) {
	at := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		format, raw string
		key         string
		want        any
	}{
		{"", "W: 12.5 kg", "text", "W: 12.5 kg"},
		{"text", "\xff\x00", "hex", "ff00"},
		{"hex", "AB", "hex", "4142"},
		{"base64", "hi", "base64", "aGk="},
		{"json", `{"t":21.5}`, "data", map[string]any{"t": 21.5}},
		{"json", "not json", "text", "not json"},
	}
	for _, c := range cases {
		s := &Source{cfg: Config{AgentID: "agt1", Format: c.format}}
		msg := s.message(agenthub.Delivery{AgentID: "agt1", DeviceName: "scale", Record: record(7, "dev-a", c.raw, at)})
		got := decode(t, msg)
		if b, _ := json.Marshal(got[c.key]); string(b) != mustJSON(c.want) {
			t.Errorf("format %q of %q: %s = %v, want %v (payload %s)", c.format, c.raw, c.key, got[c.key], c.want, msg.Payload())
		}
		if got["agent_id"] != "agt1" || got["device_id"] != "dev-a" || got["device"] != "scale" ||
			got["seq"] != float64(7) || got["received_at"] != "2026-11-23T10:00:00Z" {
			t.Errorf("payload %s lacks the record's provenance", msg.Payload())
		}
		if msg.ID() != "agt1:7" || msg.Table() != "scale" || msg.Operation() != hermod.OpCreate {
			t.Errorf("id %q table %q operation %q", msg.ID(), msg.Table(), msg.Operation())
		}
		if v, _ := hermod.MetadataValue(msg, MetaSeq); v != "7" {
			t.Errorf("metadata %s = %q", MetaSeq, v)
		}
	}
	s := &Source{cfg: Config{AgentID: "agt1", Format: "json"}}
	msg := s.message(agenthub.Delivery{AgentID: "agt1", Record: record(1, "dev-a", "nope", at)})
	if got := decode(t, msg); got["decode_error"] == nil || msg.Table() != "dev-a" {
		t.Errorf("a record that is not JSON: payload %s, table %q", msg.Payload(), msg.Table())
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// The source reads from the hub and acknowledges through it: what it Acks is
// what the hub may let the agent delete.
func TestReadsAndAcknowledgesThroughTheHub(t *testing.T) {
	hub := agenthub.New(nil, nil)
	s, err := New(hub, Config{AgentID: "agt1", VHost: "plant"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := s.Read(ctx); err == nil {
		t.Fatal("Read returned a message with no agent connected")
	}
	if hub.Live("agt1").Workflows != 1 {
		t.Fatalf("the first Read did not subscribe: %+v", hub.Live("agt1"))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if hub.Live("agt1").Workflows != 0 {
		t.Fatal("Close did not unsubscribe")
	}
}
