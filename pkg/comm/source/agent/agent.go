// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package agentsource is the Agent source: a workflow reading the devices of
// a hermod-agent. Records reach it through agenthub, and acknowledging a
// message tells the hub the workflow has finished with that record, which is
// what lets the agent delete it from its spool.
package agentsource

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/agenthub"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// MetaSeq is the metadata key holding the record's sequence number on the
// agent, which Ack hands back to the hub.
const MetaSeq = "agent_seq"

// Metadata keys naming where a record came from.
const (
	MetaAgentID  = "agent_id"
	MetaDeviceID = "agent_device_id"
)

// Formats a record's bytes can be read as.
const (
	FormatText   = "text"
	FormatJSON   = "json"
	FormatHex    = "hex"
	FormatBase64 = "base64"
)

// Config is what an Agent source reads.
type Config struct {
	AgentID string
	// Devices are the device ids to read; every device of the agent when
	// empty.
	Devices []string
	// Format is how a record's bytes are put in the message: text (the
	// default), json, hex or base64.
	Format string
	// VHost is the workflow's vhost. Only an agent of the same vhost is read.
	VHost string
}

// Source is the Agent source.
type Source struct {
	hub *agenthub.Hub
	cfg Config

	once sync.Once
	sub  *agenthub.Subscription
}

// New builds an Agent source on hub.
func New(hub *agenthub.Hub, cfg Config) (*Source, error) {
	if cfg.AgentID == "" {
		return nil, errors.New("agent source: choose the agent to read")
	}
	switch cfg.Format {
	case "", FormatText, FormatJSON, FormatHex, FormatBase64:
	default:
		return nil, fmt.Errorf("agent source: unknown format %q (text, json, hex or base64)", cfg.Format)
	}
	return &Source{hub: hub, cfg: cfg}, nil
}

// ParseDevices reads a comma-separated list of device ids.
func ParseDevices(s string) []string {
	var out []string
	for d := range strings.SplitSeq(s, ",") {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// subscription subscribes on the first Read rather than when the source is
// built, so a source built only to be checked holds nothing.
func (s *Source) subscription() *agenthub.Subscription {
	s.once.Do(func() { s.sub = s.hub.Subscribe(s.cfg.AgentID, s.cfg.VHost, s.cfg.Devices) })
	return s.sub
}

// Read waits for the next record.
func (s *Source) Read(ctx context.Context) (hermod.Message, error) {
	sub := s.subscription()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case d := <-sub.C():
		return s.message(d), nil
	}
}

// Ack tells the hub the workflow has finished with the record.
func (s *Source) Ack(_ context.Context, msg hermod.Message) error {
	v, ok := hermod.MetadataValue(msg, MetaSeq)
	if !ok {
		return nil
	}
	seq, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return fmt.Errorf("agent source: message %q has sequence %q: %w", msg.ID(), v, err)
	}
	s.subscription().Done(seq)
	return nil
}

// Ping reports the source healthy: whether the agent is connected is the
// agent's status, not the workflow's.
func (s *Source) Ping(context.Context) error { return nil }

// Close stops reading. Records read but not acknowledged are sent again by
// the agent.
func (s *Source) Close() error {
	s.once.Do(func() {}) // a source never read subscribes to nothing
	if s.sub != nil {
		s.sub.Close()
	}
	return nil
}

// message turns a record into a message. The payload is a JSON object naming
// the agent, the device and when the record was read, with the record's bytes
// under a key that says how they were read.
func (s *Source) message(d agenthub.Delivery) hermod.Message {
	rec := d.Record
	body := map[string]any{
		"agent_id":    d.AgentID,
		"device_id":   rec.GetDeviceId(),
		"seq":         rec.GetSeq(),
		"received_at": time.Unix(0, rec.GetReceivedAtUnixNano()).UTC().Format(time.RFC3339Nano),
	}
	if d.DeviceName != "" {
		body["device"] = d.DeviceName
	}
	if rec.GetPort() != "" {
		body["port"] = rec.GetPort()
	}
	putValue(body, s.cfg.Format, rec.GetRaw())
	payload, _ := json.Marshal(body) // only strings, numbers and decoded JSON

	table := d.DeviceName
	if table == "" {
		table = rec.GetDeviceId()
	}
	seq := strconv.FormatUint(rec.GetSeq(), 10)
	msg := message.AcquireMessage()
	msg.SetID(d.AgentID + ":" + seq)
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable(table)
	msg.SetPayload(payload)
	msg.SetMetadata(MetaSeq, seq)
	msg.SetMetadata(MetaAgentID, d.AgentID)
	msg.SetMetadata(MetaDeviceID, rec.GetDeviceId())
	return msg
}

func putValue(body map[string]any, format string, raw []byte) {
	switch format {
	case FormatHex:
		body["hex"] = hex.EncodeToString(raw)
	case FormatBase64:
		body["base64"] = base64.StdEncoding.EncodeToString(raw)
	case FormatJSON:
		var v any
		err := json.Unmarshal(raw, &v)
		if err == nil {
			body["data"] = v
			return
		}
		body["decode_error"] = err.Error()
		putText(body, raw)
	default:
		putText(body, raw)
	}
}

// putText puts the bytes in as text, or as hex when they are not text.
func putText(body map[string]any, raw []byte) {
	if utf8.Valid(raw) {
		body["text"] = string(raw)
		return
	}
	body["hex"] = hex.EncodeToString(raw)
}
