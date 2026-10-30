// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package rediskv

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

func settings(t *testing.T, cfg map[string]string) Settings {
	t.Helper()
	s, err := ParseSettings(func(k string) string { return cfg[k] })
	if err != nil {
		t.Fatalf("ParseSettings(%v): %v", cfg, err)
	}
	return s
}

func msgOf(t *testing.T, row map[string]any) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(m.Release)
	message.PopulateFromMap(m, row)
	return m
}

// recordJSON is the record writer the transformation uses: the data map as JSON.
func recordJSON(m hermod.Message) ([]byte, error) { return json.Marshal(m.Data()) }

// write builds and applies one write against a fresh miniredis.
func write(t *testing.T, cfg map[string]string, row map[string]any) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	cfg["addr"] = mr.Addr()
	s := settings(t, cfg)
	op, err := s.Build(msgOf(t, row), recordJSON)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	c := NewClient(s.Conn)
	t.Cleanup(func() { _ = c.Close() })
	if err := Apply(t.Context(), c, op); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return mr
}

func TestSetWritesTheWholeRecordAsJSONWhenNoValueIsGiven(t *testing.T) {
	mr := write(t, map[string]string{"key": "customer:{{.id}}"}, map[string]any{"id": 42, "name": "Ada"})

	got, err := mr.Get("customer:42")
	if err != nil {
		t.Fatalf("customer:42 not written: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(got), &rec); err != nil || rec["name"] != "Ada" {
		t.Fatalf("customer:42 = %q, want the record as JSON", got)
	}
}

func TestSetWritesATemplatedValue(t *testing.T) {
	mr := write(t, map[string]string{"key": "name:{{.id}}", "value": "{{.name}}"}, map[string]any{"id": 1, "name": "Ada"})
	if got, _ := mr.Get("name:1"); got != "Ada" {
		t.Fatalf("name:1 = %q, want %q", got, "Ada")
	}
}

// An object field is written as the JSON object it is, and a JSON template is
// resolved inside the JSON, so a value holding a quote stays valid JSON.
func TestSetWritesObjectsAndJSONTemplatesAsJSON(t *testing.T) {
	mr := write(t, map[string]string{"key": "p", "value": "{{.after.profile}}"},
		map[string]any{"after": map[string]any{"profile": map[string]any{"tier": "gold"}}})
	if got, _ := mr.Get("p"); got != `{"tier":"gold"}` {
		t.Fatalf("p = %q, want the object as JSON", got)
	}

	mr = write(t, map[string]string{"key": "q", "value": `{"name":"{{.name}}"}`}, map[string]any{"name": `Ada "the first"`})
	got, _ := mr.Get("q")
	var v map[string]any
	if err := json.Unmarshal([]byte(got), &v); err != nil || v["name"] != `Ada "the first"` {
		t.Fatalf("q = %q, want valid JSON holding the quoted name", got)
	}
}

func TestSetAppliesTheTTL(t *testing.T) {
	mr := write(t, map[string]string{"key": "k", "value": "v", "ttl": "90s"}, map[string]any{})
	if ttl := mr.TTL("k"); ttl != 90*time.Second {
		t.Fatalf("ttl = %v, want 90s", ttl)
	}
}

func TestSetOnlyIfAbsentKeepsAnExistingValue(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.Set("k", "first")
	s := settings(t, map[string]string{"addr": mr.Addr(), "key": "k", "value": "second", "onlyIfAbsent": "true"})
	op, err := s.Build(msgOf(t, map[string]any{}), recordJSON)
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(s.Conn)
	defer c.Close()
	// Not written is not an error: the key already holds what the node defers to.
	if err := Apply(t.Context(), c, op); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got, _ := mr.Get("k"); got != "first" {
		t.Fatalf("k = %q, want the existing %q", got, "first")
	}
}

func TestHSetWritesOneTemplatedField(t *testing.T) {
	mr := write(t, map[string]string{"command": "hset", "key": "prices", "field": "{{.sku}}", "value": "{{.price}}"},
		map[string]any{"sku": "sku-1", "price": 9.5})
	if got := mr.HGet("prices", "sku-1"); got != "9.5" {
		t.Fatalf("prices[sku-1] = %q, want 9.5", got)
	}
}

// With no field, HSET writes every top-level field of the record, so a row
// becomes a hash a reader can HGET one column of.
func TestHSetWithoutAFieldWritesTheRecordAsAHash(t *testing.T) {
	mr := write(t, map[string]string{"command": "hset", "key": "user:{{.id}}", "ttl": "1h"},
		map[string]any{"id": 7, "name": "Ada", "tags": []any{"a", "b"}})

	if got := mr.HGet("user:7", "name"); got != "Ada" {
		t.Fatalf("name = %q", got)
	}
	if got := mr.HGet("user:7", "id"); got != "7" {
		t.Fatalf("id = %q, want 7", got)
	}
	if got := mr.HGet("user:7", "tags"); got != `["a","b"]` {
		t.Fatalf("tags = %q, want the list as JSON", got)
	}
	if ttl := mr.TTL("user:7"); ttl != time.Hour {
		t.Fatalf("ttl = %v, want 1h", ttl)
	}
}

func TestHSetWithoutAFieldNeedsAnObjectValue(t *testing.T) {
	mr := miniredis.RunT(t)
	s := settings(t, map[string]string{"addr": mr.Addr(), "command": "hset", "key": "k", "value": "{{.name}}"})
	if _, err := s.Build(msgOf(t, map[string]any{"name": "Ada"}), recordJSON); err == nil {
		t.Fatal("a text value was accepted as a whole hash")
	}
}

// A key whose token has no value is not a key anyone meant: "user:" would be
// written for every record without a user, each overwriting the last.
func TestBuildRefusesAKeyWithAnEmptyToken(t *testing.T) {
	s := settings(t, map[string]string{"addr": "x:1", "key": "user:{{.user_id}}"})
	_, err := s.Build(msgOf(t, map[string]any{"other": 1}), recordJSON)
	if err == nil || !strings.Contains(err.Error(), "user_id") {
		t.Fatalf("err = %v, want the empty token named", err)
	}
}

func TestBuildResolvesCDCEnvelopePaths(t *testing.T) {
	mr := write(t, map[string]string{"key": "user:{{.after.id}}", "value": "{{.after.name}}"},
		map[string]any{"operation": "insert", "after": map[string]any{"id": "u-1", "name": "Ada"}})
	if got, _ := mr.Get("user:u-1"); got != "Ada" {
		t.Fatalf("user:u-1 = %q", got)
	}
}

func TestParseSettingsRefusesMisconfiguration(t *testing.T) {
	cases := map[string]map[string]string{
		"no addr":                 {"key": "k"},
		"no key":                  {"addr": "x:1"},
		"unknown command":         {"addr": "x:1", "key": "k", "command": "del"},
		"ttl without a unit":      {"addr": "x:1", "key": "k", "ttl": "60"},
		"negative ttl":            {"addr": "x:1", "key": "k", "ttl": "-1s"},
		"bad db":                  {"addr": "x:1", "key": "k", "db": "two"},
		"only-if-absent for hset": {"addr": "x:1", "key": "k", "command": "hset", "onlyIfAbsent": "true"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSettings(func(k string) string { return cfg[k] }); err == nil {
				t.Fatalf("%v was accepted", cfg)
			}
		})
	}
}

func TestApplyAllPipelinesEveryWrite(t *testing.T) {
	mr := miniredis.RunT(t)
	s := settings(t, map[string]string{"addr": mr.Addr(), "key": "n:{{.id}}", "value": "{{.id}}"})
	var ops []Op
	for _, id := range []int{1, 2, 3} {
		op, err := s.Build(msgOf(t, map[string]any{"id": id}), recordJSON)
		if err != nil {
			t.Fatal(err)
		}
		ops = append(ops, op)
	}
	c := NewClient(s.Conn)
	defer c.Close()
	if err := ApplyAll(t.Context(), c, ops); err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}
	for _, k := range []string{"n:1", "n:2", "n:3"} {
		if !mr.Exists(k) {
			t.Errorf("%s not written", k)
		}
	}
}

func TestApplyReportsAWrongType(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.Set("k", "a string")
	s := settings(t, map[string]string{"addr": mr.Addr(), "command": "hset", "key": "k", "field": "f", "value": "v"})
	op, err := s.Build(msgOf(t, map[string]any{}), recordJSON)
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(s.Conn)
	defer c.Close()
	if err := Apply(t.Context(), c, op); err == nil || !strings.Contains(err.Error(), "WRONGTYPE") {
		t.Fatalf("err = %v, want WRONGTYPE", err)
	}
}

func TestNewClientUsesDBAndPassword(t *testing.T) {
	mr := write(t, map[string]string{"key": "k", "value": "v", "db": "2"}, map[string]any{})
	mr.Select(2)
	if got, _ := mr.Get("k"); got != "v" {
		t.Fatalf("k in db 2 = %q", got)
	}
}

func TestPoolOpensOneClientPerConnection(t *testing.T) {
	var p Pool
	defer p.Close()
	a := p.Get(Conn{Addr: "x:1"})
	if p.Get(Conn{Addr: "x:1"}) != a {
		t.Fatal("a second Get for one connection opened another client")
	}
	if p.Get(Conn{Addr: "x:1", DB: 2}) == a {
		t.Fatal("two databases shared a client")
	}
	if n := p.Len(); n != 2 {
		t.Fatalf("Len = %d, want 2", n)
	}
	p.Close()
	if n := p.Len(); n != 0 {
		t.Fatalf("Len after Close = %d, want 0", n)
	}
}
