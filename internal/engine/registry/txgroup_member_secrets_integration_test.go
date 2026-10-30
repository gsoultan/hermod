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

package registry

import (
	"context"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
)

// A member's settings may hold secret and env references — a password is the
// usual one — and every other sink has them expanded before it is built. The
// group built its members straight from storage, so a member whose connection
// string was a reference was handed the reference text itself and could not
// connect.
func TestAGroupMemberConnectionStringMayBeAReference(t *testing.T) {
	dsn := reachabilityDSN(t)
	p := reachabilityPool(t, dsn)
	requirePrepared(t, p)

	t.Setenv("HERMOD_ENV_TXGROUP_MEMBER_DSN", dsn)

	tableA := makeReachabilityTable(t, p, "ref_a")
	tableB := makeReachabilityTable(t, p, "ref_b")
	memberA := storedPostgresSink("sink-a", "{{ env.TXGROUP_MEMBER_DSN }}", tableA)
	memberB := storedPostgresSink("sink-b", dsn, tableB)

	r := registryWithSinks(map[string]storage.Sink{"sink-a": memberA, "sink-b": memberB})

	group, err := r.createSinkInternal(context.Background(), factory.SinkConfig{
		ID:     "grp-ref",
		Type:   "txgroup",
		Config: map[string]string{"members": "sink-a,sink-b"},
	})
	if err != nil {
		t.Fatalf("building a group whose member's connection string is an env reference: %v", err)
	}
	defer func() { _ = group.Close() }()

	if err := group.(hermod.BatchSink).WriteBatch(context.Background(), []hermod.Message{
		reachabilityMessage("1", "one"),
	}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if got := reachabilityRowCount(t, p, tableA); got != 1 {
		t.Errorf("member A has %d rows, want 1", got)
	}
}

// Checking a group of two real, capable databases passes, and leaves the
// transaction log alone: a check run beside a live workflow must not recover,
// and so resolve, that workflow's in-flight transactions.
func TestCheckingAGroupOfRealDatabasesPassesAndLeavesTheLogAlone(t *testing.T) {
	dsn := reachabilityDSN(t)
	p := reachabilityPool(t, dsn)
	requirePrepared(t, p)

	r := registryWithSinks(map[string]storage.Sink{
		"sink-a": storedPostgresSink("sink-a", dsn, makeReachabilityTable(t, p, "chk_a")),
		"sink-b": storedPostgresSink("sink-b", dsn, makeReachabilityTable(t, p, "chk_b")),
	})
	log := r.stateStore.(*fakeStateStore)
	log.kv["twopc/someone-elses-round"] = []byte("in flight")

	if err := r.TestSink(context.Background(), factory.SinkConfig{
		Type:   "txgroup",
		Config: map[string]string{"members": "sink-a,sink-b"},
	}); err != nil {
		t.Fatalf("checking a group of two capable PostgreSQL sinks: %v", err)
	}
	if len(log.kv) != 1 || string(log.kv["twopc/someone-elses-round"]) != "in flight" {
		t.Errorf("the check changed the transaction log: %v", log.kv)
	}
}
