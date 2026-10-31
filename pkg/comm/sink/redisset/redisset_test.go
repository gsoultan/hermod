// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package redisset

import (
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gsoultan/hermod"
	jsonfmt "github.com/gsoultan/hermod/pkg/comm/formatter/json"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

func row(t *testing.T, fields map[string]any) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(m.Release)
	message.PopulateFromMap(m, fields)
	return m
}

func newSink(t *testing.T, cfg map[string]string, f hermod.Formatter) (*RedisSetSink, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	if cfg["addr"] == "" {
		cfg["addr"] = mr.Addr()
	}
	s, err := New(cfg, f)
	if err != nil {
		t.Fatalf("New(%v): %v", cfg, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, mr
}

func TestRedisSetSinkWritesEachRecordToItsKey(t *testing.T) {
	s, mr := newSink(t, map[string]string{"key": "user:{{.after.id}}", "value": "{{.after.name}}", "ttl": "10m"}, nil)

	if err := s.Write(t.Context(), row(t, map[string]any{"after": map[string]any{"id": 1, "name": "Ada"}})); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, _ := mr.Get("user:1"); got != "Ada" {
		t.Fatalf("user:1 = %q", got)
	}
	if ttl := mr.TTL("user:1"); ttl != 10*time.Minute {
		t.Fatalf("ttl = %v, want 10m", ttl)
	}
}

// With no value, the record is written the way every other sink writes it:
// through the sink's format, so "Format: CDC" here means what it means there.
func TestRedisSetSinkWritesTheFormattedRecordWhenNoValueIsGiven(t *testing.T) {
	s, mr := newSink(t, map[string]string{"key": "k"}, jsonfmt.NewJSONFormatter())

	if err := s.Write(t.Context(), row(t, map[string]any{"name": "Ada"})); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, _ := mr.Get("k")
	if !strings.Contains(got, "Ada") {
		t.Fatalf("k = %q, want the formatted record", got)
	}
}

func TestRedisSetSinkWriteBatchWritesEveryRecordInOneRoundTrip(t *testing.T) {
	s, mr := newSink(t, map[string]string{"key": "n:{{.id}}", "value": "{{.id}}"}, nil)

	msgs := []hermod.Message{row(t, map[string]any{"id": 1}), nil, row(t, map[string]any{"id": 2})}
	if err := s.WriteBatch(t.Context(), msgs); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	for _, k := range []string{"n:1", "n:2"} {
		if !mr.Exists(k) {
			t.Errorf("%s not written", k)
		}
	}
}

// A batch with one record that cannot be keyed fails as a whole, before
// anything is written, so the retry does not find half the batch in Redis.
func TestRedisSetSinkWriteBatchRefusesARecordWithAnEmptyKeyToken(t *testing.T) {
	s, mr := newSink(t, map[string]string{"key": "n:{{.id}}", "value": "x"}, nil)

	err := s.WriteBatch(t.Context(), []hermod.Message{row(t, map[string]any{"id": 1}), row(t, map[string]any{"other": 2})})
	if err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("err = %v, want the empty token named", err)
	}
	if mr.Exists("n:1") {
		t.Fatal("n:1 was written by a batch that failed")
	}
}

func TestNewRefusesMisconfiguration(t *testing.T) {
	if _, err := New(map[string]string{"addr": "x:1"}, nil); err == nil {
		t.Fatal("a sink with no key was built")
	}
}

func TestRedisSetSinkPingReachesTheServer(t *testing.T) {
	s, mr := newSink(t, map[string]string{"key": "k"}, nil)
	if err := s.Ping(t.Context()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	mr.Close()
	if err := s.Ping(t.Context()); err == nil {
		t.Fatal("Ping succeeded against a stopped server")
	}
}

func TestRedisSetSinkWriteAfterCloseIsAnError(t *testing.T) {
	s, _ := newSink(t, map[string]string{"key": "k", "value": "v"}, nil)
	_ = s.Close()
	_ = s.Close()
	if err := s.Write(t.Context(), row(t, map[string]any{})); err == nil {
		t.Fatal("Write after Close returned no error")
	}
}
