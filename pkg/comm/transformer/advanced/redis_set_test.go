// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package advanced

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
)

func redisSet(t *testing.T, tr *RedisSetTransformer, cfg map[string]any, row map[string]any) (map[string]any, error) {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	message.PopulateFromMap(msg, row)
	out, err := tr.Transform(context.Background(), msg, cfg)
	if out == nil {
		return nil, err
	}
	return out.Data(), err
}

func newRedisSetFixture(t *testing.T) (*RedisSetTransformer, *miniredis.Miniredis) {
	t.Helper()
	tr := newRedisSetTransformer()
	t.Cleanup(tr.clients.Close)
	return tr, miniredis.RunT(t)
}

func TestRedisSetIsRegistered(t *testing.T) {
	if _, ok := transformer.Get("redis_set"); !ok {
		t.Fatal(`transformer "redis_set" is not registered`)
	}
}

// The step writes the key and hands the record on unchanged, so the steps and
// sinks after it still run.
func TestRedisSetWritesTheKeyAndPassesTheRecordOn(t *testing.T) {
	tr, mr := newRedisSetFixture(t)
	got, err := redisSet(t, tr, map[string]any{
		"addr": mr.Addr(), "key": "user:{{.id}}", "value": "{{.name}}", "ttl": "5m", "db": float64(0),
	}, map[string]any{"id": 7, "name": "Ada"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if v, _ := mr.Get("user:7"); v != "Ada" {
		t.Fatalf("user:7 = %q", v)
	}
	if got["name"] != "Ada" || len(got) != 2 {
		t.Fatalf("record = %v, want it unchanged", got)
	}
}

func TestRedisSetWithNoValueWritesTheRecordAsJSON(t *testing.T) {
	tr, mr := newRedisSetFixture(t)
	if _, err := redisSet(t, tr, map[string]any{"addr": mr.Addr(), "key": "k"}, map[string]any{"name": "Ada"}); err != nil {
		t.Fatal(err)
	}
	if v, _ := mr.Get("k"); v != `{"name":"Ada"}` {
		t.Fatalf("k = %q", v)
	}
}

func TestRedisSetReadsNumberAndBooleanSettings(t *testing.T) {
	tr, mr := newRedisSetFixture(t)
	mr.Set("k", "first")
	if _, err := redisSet(t, tr, map[string]any{
		"addr": mr.Addr(), "key": "k", "value": "second", "onlyIfAbsent": true, "db": float64(0),
	}, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if v, _ := mr.Get("k"); v != "first" {
		t.Fatalf("onlyIfAbsent saved as a boolean was ignored: k = %q", v)
	}
}

// A failed write fails the record by default: a cache the pipeline believes it
// filled and did not is the silent outcome to avoid. passthrough is a choice.
func TestRedisSetFailedWriteFailsUnlessTold(t *testing.T) {
	tr, mr := newRedisSetFixture(t)
	addr := mr.Addr()
	mr.Close()
	cfg := map[string]any{"addr": addr, "key": "k", "value": "v", "timeout": "300ms"}

	if _, err := redisSet(t, tr, cfg, map[string]any{}); err == nil {
		t.Fatal("a write to a stopped server reported success")
	}
	cfg["onError"] = "passthrough"
	if got, err := redisSet(t, tr, cfg, map[string]any{"a": 1}); err != nil || got["a"] == nil {
		t.Fatalf("passthrough: err = %v, record = %v", err, got)
	}
}

func TestRedisSetEmptyKeyTokenFails(t *testing.T) {
	tr, mr := newRedisSetFixture(t)
	_, err := redisSet(t, tr, map[string]any{"addr": mr.Addr(), "key": "user:{{.id}}", "value": "v"}, map[string]any{"x": 1})
	if err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("err = %v", err)
	}
	if n := mr.CommandCount(); n != 0 {
		t.Fatalf("Redis received %d commands", n)
	}
}

// The connection is the node's, never the record's.
func TestRedisSetConnectionIgnoresRecordFields(t *testing.T) {
	tr, mr := newRedisSetFixture(t)
	if _, err := redisSet(t, tr, map[string]any{"addr": "{{.target}}", "key": "k", "value": "v"},
		map[string]any{"target": mr.Addr()}); err == nil {
		t.Fatal("addr was read from the record")
	}
	if n := mr.CommandCount(); n != 0 {
		t.Fatalf("Redis received %d commands from a record-chosen addr", n)
	}
}

func TestRedisSetMisconfigurationFailsEvenWithPassthrough(t *testing.T) {
	tr, mr := newRedisSetFixture(t)
	_, err := redisSet(t, tr, map[string]any{"addr": mr.Addr(), "key": "k", "ttl": "60", "onError": "passthrough"}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "ttl") {
		t.Fatalf("err = %v, want the ttl refused", err)
	}
}

func TestRedisSetReusesOneClientPerConnection(t *testing.T) {
	tr, mr := newRedisSetFixture(t)
	cfg := map[string]any{"addr": mr.Addr(), "key": "{{.k}}", "value": "v"}
	for _, k := range []string{"a", "b", "c"} {
		if _, err := redisSet(t, tr, cfg, map[string]any{"k": k}); err != nil {
			t.Fatal(err)
		}
	}
	if n := tr.clients.Len(); n != 1 {
		t.Fatalf("%d clients, want 1", n)
	}
}
