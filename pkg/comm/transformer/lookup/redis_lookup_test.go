// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package lookup

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
)

// newRedisFixture is a redis_lookup transformer and a caching registry, with a
// miniredis server whose address the config can name.
func newRedisFixture(t *testing.T) (*RedisLookupTransformer, *cachingFakeRegistry, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	tr := newRedisLookupTransformer()
	t.Cleanup(tr.clients.closeAll)
	return tr, &cachingFakeRegistry{cache: map[string]any{}}, mr
}

func redisLookupErr(t *testing.T, tr *RedisLookupTransformer, reg *cachingFakeRegistry, cfg map[string]any, row map[string]any) (map[string]any, error) {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	message.PopulateFromMap(msg, row)
	out, err := tr.Transform(context.WithValue(t.Context(), hermod.RegistryKey, reg), msg, cfg)
	if out == nil {
		return nil, err
	}
	return out.Data(), err
}

func redisLookup(t *testing.T, tr *RedisLookupTransformer, reg *cachingFakeRegistry, cfg map[string]any, row map[string]any) map[string]any {
	t.Helper()
	data, err := redisLookupErr(t, tr, reg, cfg, row)
	if err != nil {
		t.Fatalf("Transform(%v): %v", row, err)
	}
	return data
}

func TestRedisLookupIsRegistered(t *testing.T) {
	if _, ok := transformer.Get("redis_lookup"); !ok {
		t.Fatal(`transformer "redis_lookup" is not registered`)
	}
}

// A JSON string value is merged as the object it holds, not as its text: the
// common way to cache a record in Redis is SET key <json>.
func TestRedisLookupGetMergesAJSONValueAsAnObject(t *testing.T) {
	tr, reg, mr := newRedisFixture(t)
	mr.Set("customer:42", `{"name":"Ada","tier":"gold"}`)

	got := redisLookup(t, tr, reg, map[string]any{
		"addr": mr.Addr(), "key": "customer:{{.customer_id}}", "targetField": "customer",
	}, map[string]any{"customer_id": 42})

	c, ok := got["customer"].(map[string]any)
	if !ok || c["name"] != "Ada" || c["tier"] != "gold" {
		t.Fatalf("customer = %#v, want the stored object", got["customer"])
	}
}

func TestRedisLookupGetKeepsAPlainStringAsText(t *testing.T) {
	tr, reg, mr := newRedisFixture(t)
	mr.Set("country:ID", "Indonesia")

	got := redisLookup(t, tr, reg, map[string]any{
		"addr": mr.Addr(), "key": "country:{{.code}}", "targetField": "country",
	}, map[string]any{"code": "ID"})

	if got["country"] != "Indonesia" {
		t.Fatalf("country = %#v, want %q", got["country"], "Indonesia")
	}
}

func TestRedisLookupValuePathPicksOneFieldOfAJSONValue(t *testing.T) {
	tr, reg, mr := newRedisFixture(t)
	mr.Set("customer:42", `{"profile":{"name":"Ada"}}`)

	got := redisLookup(t, tr, reg, map[string]any{
		"addr": mr.Addr(), "key": "customer:{{.id}}", "valuePath": "profile.name", "targetField": "name",
	}, map[string]any{"id": 42})

	if got["name"] != "Ada" {
		t.Fatalf("name = %#v, want %q", got["name"], "Ada")
	}
}

func TestRedisLookupHGetReadsATemplatedField(t *testing.T) {
	tr, reg, mr := newRedisFixture(t)
	mr.HSet("prices", "sku-1", "9.99")
	mr.HSet("prices", "sku-2", "4.50")

	got := redisLookup(t, tr, reg, map[string]any{
		"addr": mr.Addr(), "command": "hget", "key": "prices", "field": "{{.sku}}", "targetField": "price",
	}, map[string]any{"sku": "sku-2"})

	// Text stays text: decoding "4.50" as JSON would make it 4.5, and an id
	// such as "0812" or a bigint would not survive the same trip.
	if got["price"] != "4.50" {
		t.Fatalf("price = %#v, want %q", got["price"], "4.50")
	}
}

func TestRedisLookupHGetAllMergesTheHashAndFlattensIt(t *testing.T) {
	tr, reg, mr := newRedisFixture(t)
	mr.HSet("user:7", "name", "Ada", "plan", "pro")

	got := redisLookup(t, tr, reg, map[string]any{
		"addr": mr.Addr(), "command": "hgetall", "key": "user:{{.uid}}",
		"targetField": "user", "flattenInto": ".",
	}, map[string]any{"uid": 7})

	u, ok := got["user"].(map[string]any)
	if !ok || u["name"] != "Ada" || u["plan"] != "pro" {
		t.Fatalf("user = %#v, want the whole hash", got["user"])
	}
	// flattenInto "." writes each hash field at the top level, as db_lookup does
	// for a row's columns.
	if got["name"] != "Ada" || got["plan"] != "pro" {
		t.Fatalf("flattened name = %#v, plan = %#v, want Ada and pro", got["name"], got["plan"])
	}
}

// A CDC event's fields live under after.*; the key template must read them the
// way every other lookup's templates do.
func TestRedisLookupKeyResolvesCDCEnvelopePaths(t *testing.T) {
	tr, reg, mr := newRedisFixture(t)
	mr.Set("user:019a43e3-0000-7000-8000-000000000001", "Ada")

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	message.PopulateFromMap(msg, cdcSample())

	out, err := tr.Transform(context.WithValue(t.Context(), hermod.RegistryKey, reg), msg, map[string]any{
		"addr": mr.Addr(), "key": "user:{{.after.user_id}}", "targetField": "who",
	})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got := out.Data()["who"]; got != "Ada" {
		t.Fatalf("who = %#v, want %q", got, "Ada")
	}
}

// ---- misses ----------------------------------------------------------------

func TestRedisLookupMissingKeyFollowsOnMiss(t *testing.T) {
	cfg := func(extra map[string]any) map[string]any {
		c := map[string]any{"key": "absent:{{.id}}", "targetField": "v"}
		for k, v := range extra {
			c[k] = v
		}
		return c
	}

	t.Run("passthrough by default", func(t *testing.T) {
		tr, reg, mr := newRedisFixture(t)
		c := cfg(map[string]any{"addr": mr.Addr()})
		got := redisLookup(t, tr, reg, c, map[string]any{"id": 1})
		if _, ok := got["v"]; ok {
			t.Fatalf("v = %#v, want it unset", got["v"])
		}
	})
	t.Run("default value", func(t *testing.T) {
		tr, reg, mr := newRedisFixture(t)
		c := cfg(map[string]any{"addr": mr.Addr(), "defaultValue": "unknown"})
		if got := redisLookup(t, tr, reg, c, map[string]any{"id": 1}); got["v"] != "unknown" {
			t.Fatalf("v = %#v, want %q", got["v"], "unknown")
		}
	})
	t.Run("fail", func(t *testing.T) {
		tr, reg, mr := newRedisFixture(t)
		c := cfg(map[string]any{"addr": mr.Addr(), "onMiss": "fail"})
		_, err := redisLookupErr(t, tr, reg, c, map[string]any{"id": 1})
		if err == nil || !strings.Contains(err.Error(), "absent:1") {
			t.Fatalf("err = %v, want a miss naming the key absent:1", err)
		}
	})
}

// A key whose token found no value is not a key anyone wrote: "user:" would be
// looked up, and if it exists, its value merged into every message without a
// user. It is a miss, and Redis is not asked.
func TestRedisLookupEmptyKeyTokenIsAMissWithoutAQuery(t *testing.T) {
	tr, reg, mr := newRedisFixture(t)
	mr.Set("user:", "the wrong user")

	_, err := redisLookupErr(t, tr, reg, map[string]any{
		"addr": mr.Addr(), "key": "user:{{.user_id}}", "targetField": "who", "onMiss": "fail",
	}, map[string]any{"other": 1})

	if err == nil || !strings.Contains(err.Error(), "user_id") {
		t.Fatalf("err = %v, want a miss naming the empty token", err)
	}
	if n := mr.CommandCount(); n != 0 {
		t.Fatalf("Redis received %d commands, want none", n)
	}
}

func TestRedisLookupIncompleteConfigFollowsOnMiss(t *testing.T) {
	tr, reg, _ := newRedisFixture(t)
	_, err := redisLookupErr(t, tr, reg, map[string]any{"key": "k", "targetField": "v", "onMiss": "fail"}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "addr") {
		t.Fatalf("err = %v, want an incomplete-config error naming addr", err)
	}
}

// ---- failures --------------------------------------------------------------

// Redis being unreachable is not a miss. Like api_lookup, it fails the message
// unless a default value or policy says otherwise.
func TestRedisLookupUnreachableServerFailsByDefault(t *testing.T) {
	tr, reg, mr := newRedisFixture(t)
	addr := mr.Addr()
	mr.Close()

	cfg := map[string]any{"addr": addr, "key": "k", "targetField": "v", "timeout": "200ms"}
	if _, err := redisLookupErr(t, tr, reg, cfg, map[string]any{}); err == nil {
		t.Fatal("an unreachable Redis returned no error")
	}

	cfg["defaultValue"] = "fallback"
	got, err := redisLookupErr(t, tr, reg, cfg, map[string]any{})
	if err != nil || got["v"] != "fallback" {
		t.Fatalf("with a default: v = %#v, err = %v; want the default and no error", got["v"], err)
	}
}

func TestRedisLookupWrongTypeIsAnError(t *testing.T) {
	tr, reg, mr := newRedisFixture(t)
	mr.HSet("h", "f", "v")

	_, err := redisLookupErr(t, tr, reg, map[string]any{"addr": mr.Addr(), "key": "h", "targetField": "v"}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "WRONGTYPE") {
		t.Fatalf("err = %v, want Redis's WRONGTYPE", err)
	}
}

func TestRedisLookupRejectsAnUnknownCommand(t *testing.T) {
	tr, reg, mr := newRedisFixture(t)
	_, err := redisLookupErr(t, tr, reg, map[string]any{"addr": mr.Addr(), "command": "del", "key": "k", "targetField": "v"}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "del") {
		t.Fatalf("err = %v, want the unknown command refused", err)
	}
}

func TestRedisLookupRejectsAnUnparseableTTL(t *testing.T) {
	tr, reg, mr := newRedisFixture(t)
	_, err := redisLookupErr(t, tr, reg, map[string]any{"addr": mr.Addr(), "key": "k", "targetField": "v", "ttl": "5"}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "ttl") {
		t.Fatalf("err = %v, want the ttl refused", err)
	}
}

// ---- connection --------------------------------------------------------------

func TestRedisLookupUsesTheConfiguredDBAndPassword(t *testing.T) {
	tr, reg, mr := newRedisFixture(t)
	mr.RequireAuth("s3cret")
	mr.Select(3)
	mr.Set("k", "in db 3")

	got := redisLookup(t, tr, reg, map[string]any{
		"addr": mr.Addr(), "password": "s3cret", "db": float64(3), "key": "k", "targetField": "v",
	}, map[string]any{})
	if got["v"] != "in db 3" {
		t.Fatalf("v = %#v, want the value from db 3", got["v"])
	}
}

// The connection is the node's, never the record's: a token in addr or password
// must not let a payload choose where the lookup goes or what it sends.
func TestRedisLookupConnectionIgnoresRecordFields(t *testing.T) {
	tr, reg, mr := newRedisFixture(t)
	mr.Set("k", "v")

	_, err := redisLookupErr(t, tr, reg, map[string]any{
		"addr": "{{.target}}", "key": "k", "targetField": "v", "onMiss": "fail",
	}, map[string]any{"target": mr.Addr()})
	if err == nil {
		t.Fatal("addr was read from the record")
	}
	if n := mr.CommandCount(); n != 0 {
		t.Fatalf("Redis received %d commands from a record-chosen addr", n)
	}
}

// One client per connection, however many messages: a client per message
// would open a pool per message.
func TestRedisLookupReusesOneClientPerConnection(t *testing.T) {
	tr, reg, mr := newRedisFixture(t)
	mr.Set("a", "1")
	mr.Set("b", "2")
	var opened atomic.Int32
	open := tr.clients.open
	tr.clients.open = func(c redisConn) redisReader { opened.Add(1); return open(c) }
	cfg := map[string]any{"addr": mr.Addr(), "key": "{{.k}}", "targetField": "v", "ttl": "0"}

	redisLookup(t, tr, reg, cfg, map[string]any{"k": "a"})
	redisLookup(t, tr, reg, cfg, map[string]any{"k": "b"})
	if n := opened.Load(); n != 1 {
		t.Fatalf("%d clients opened, want 1", n)
	}

	cfg["db"] = "2"
	redisLookup(t, tr, reg, cfg, map[string]any{"k": "a"})
	if n := opened.Load(); n != 2 {
		t.Fatalf("%d clients opened after a second db, want 2", n)
	}
}

// ---- cache -------------------------------------------------------------------

func TestRedisLookupCachesAndTTLZeroReadsFresh(t *testing.T) {
	tr, reg, mr := newRedisFixture(t)
	mr.Set("k", "old")
	cfg := map[string]any{"addr": mr.Addr(), "key": "k", "targetField": "v"}

	redisLookup(t, tr, reg, cfg, map[string]any{})
	mr.Set("k", "new")
	if got := redisLookup(t, tr, reg, cfg, map[string]any{})["v"]; got != "old" {
		t.Fatalf("second read = %#v, want the cached %q", got, "old")
	}
	if reg.lastTTL != defaultRedisLookupTTL {
		t.Fatalf("cached for %v, want the %v default", reg.lastTTL, defaultRedisLookupTTL)
	}

	cfg["ttl"] = "0"
	gets := reg.gets
	if got := redisLookup(t, tr, reg, cfg, map[string]any{})["v"]; got != "new" {
		t.Fatalf("with ttl 0 = %#v, want the fresh %q", got, "new")
	}
	// A node that turned its cache off does not read one either.
	if reg.gets != gets {
		t.Fatalf("ttl 0 read the cache %d times, want none", reg.gets-gets)
	}
}

// Everything that selects the value has to be in the cache key: the resolved
// key, the field, the db, and who is asking.
func TestRedisLookupCacheKeySeparatesWhatSelectsTheValue(t *testing.T) {
	tr, reg, mr := newRedisFixture(t)
	mr.Set("user:1", "one")
	mr.Set("user:2", "two")
	mr.HSet("h", "a", "A", "b", "B")
	mr.Select(1)
	mr.Set("user:1", "one in db 1")
	mr.Select(0)

	cfg := map[string]any{"addr": mr.Addr(), "key": "user:{{.id}}", "targetField": "v"}
	if got := redisLookup(t, tr, reg, cfg, map[string]any{"id": 1})["v"]; got != "one" {
		t.Fatalf("id 1 = %#v", got)
	}
	if got := redisLookup(t, tr, reg, cfg, map[string]any{"id": 2})["v"]; got != "two" {
		t.Fatalf("id 2 = %#v, served another key's entry", got)
	}
	cfg["db"] = "1"
	if got := redisLookup(t, tr, reg, cfg, map[string]any{"id": 1})["v"]; got != "one in db 1" {
		t.Fatalf("db 1 = %#v, served db 0's entry", got)
	}

	hcfg := map[string]any{"addr": mr.Addr(), "command": "hget", "key": "h", "field": "{{.f}}", "targetField": "v"}
	redisLookup(t, tr, reg, hcfg, map[string]any{"f": "a"})
	if got := redisLookup(t, tr, reg, hcfg, map[string]any{"f": "b"})["v"]; got != "B" {
		t.Fatalf("field b = %#v, served field a's entry", got)
	}
}

func TestRedisLookupCacheKeyHoldsNoPassword(t *testing.T) {
	tr, reg, mr := newRedisFixture(t)
	mr.RequireAuth("hunter2-password")
	mr.Set("k", "v")

	redisLookup(t, tr, reg, map[string]any{"addr": mr.Addr(), "password": "hunter2-password", "key": "k", "targetField": "v"}, map[string]any{})
	for key := range reg.cache {
		if strings.Contains(key, "hunter2-password") {
			t.Fatalf("cache key %q holds the password in the clear", key)
		}
	}
}

// ---- shared calls ------------------------------------------------------------

// blockingReader answers only once release is closed, as a slow Redis would.
type blockingReader struct {
	arrived chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (b *blockingReader) read(ctx context.Context, _ redisRequest) (any, error) {
	b.calls.Add(1)
	b.arrived <- struct{}{}
	select {
	case <-b.release:
		return "v", nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *blockingReader) Close() error { return nil }

// Identical concurrent lookups share one call, and the message that started it
// being cancelled must not fail the others -- the bug PR #328 fixed in
// api_lookup, not to be brought back here.
func TestRedisLookupSharedCallSurvivesTheLeadersCancellation(t *testing.T) {
	fake := &blockingReader{arrived: make(chan struct{}, 8), release: make(chan struct{})}
	tr := newRedisLookupTransformer()
	tr.clients.open = func(redisConn) redisReader { return fake }
	reg := &cachingFakeRegistry{cache: map[string]any{}}
	cfg := map[string]any{"addr": "redis:6379", "key": "k", "targetField": "v", "ttl": "0"}

	run := func(ctx context.Context) <-chan lookupResult {
		out := make(chan lookupResult, 1)
		go func() {
			msg := message.AcquireMessage()
			defer msg.Release()
			res, err := tr.Transform(context.WithValue(ctx, hermod.RegistryKey, reg), msg, cfg)
			var data map[string]any
			if res != nil {
				data = res.Data()
			}
			out <- lookupResult{data: data, err: err}
		}()
		return out
	}

	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	defer cancelLeader()
	leader := run(leaderCtx)
	waitOrFail(t, fake.arrived, "the leader's read")

	follower := run(t.Context())
	time.Sleep(100 * time.Millisecond)

	cancelLeader()
	if r := <-leader; !errors.Is(r.err, context.Canceled) {
		t.Errorf("the cancelled message got err=%v, want its own context.Canceled", r.err)
	}
	close(fake.release)

	r := <-follower
	if r.err != nil {
		t.Fatalf("a message whose own context was live failed with %v", r.err)
	}
	if r.data["v"] != "v" {
		t.Fatalf("follower v = %#v, want %q", r.data["v"], "v")
	}
}

// A message cancelled mid-read reports its cancellation, even with a default
// value set: it did not fail a lookup, and nobody will deliver it.
func TestRedisLookupCancelledMessageIsNotFilledWithTheDefault(t *testing.T) {
	fake := &blockingReader{arrived: make(chan struct{}, 1), release: make(chan struct{})}
	defer close(fake.release)
	tr := newRedisLookupTransformer()
	tr.clients.open = func(redisConn) redisReader { return fake }
	reg := &cachingFakeRegistry{cache: map[string]any{}}

	ctx, cancel := context.WithCancel(t.Context())
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	done := make(chan error, 1)
	go func() {
		_, err := tr.Transform(context.WithValue(ctx, hermod.RegistryKey, reg), msg, map[string]any{
			"addr": "redis:6379", "key": "k", "targetField": "v", "defaultValue": "fallback", "ttl": "0",
		})
		done <- err
	}()
	waitOrFail(t, fake.arrived, "the read")
	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if v, ok := msg.Data()["v"]; ok {
		t.Fatalf("v = %#v, want nothing written to a cancelled message", v)
	}
}
