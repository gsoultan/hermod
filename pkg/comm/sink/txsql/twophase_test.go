// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package txsql

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
)

var (
	_ hermod.TwoPhaseCommit          = (*Sink)(nil)
	_ hermod.NamedBegin              = (*Sink)(nil)
	_ hermod.TwoPhaseCommitPreflight = (*Sink)(nil)
)

func countOrders(t *testing.T, snk *Sink) int {
	t.Helper()
	var n int
	if err := snk.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM orders").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// Between Begin and the end of the transaction, a write joins the open
// transaction instead of committing one of its own: that is what lets a group
// decide for every member together.
func TestWritesInsideBeginWaitForTheOutcome(t *testing.T) {
	ctx := t.Context()
	snk, _ := newSQLiteSink(t, orderStatements, nil)

	if err := snk.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := snk.WriteBatch(ctx, []hermod.Message{newMsg(t, "m1", order("A-1", map[string]any{"sku": "X", "qty": 1}))}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if err := snk.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if n := countOrders(t, snk); n != 0 {
		t.Fatalf("%d orders after Rollback, want 0", n)
	}

	if err := snk.Begin(ctx); err != nil {
		t.Fatalf("Begin again: %v", err)
	}
	if err := snk.Write(ctx, newMsg(t, "m2", order("A-2", map[string]any{"sku": "X", "qty": 1}))); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := snk.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if n := countOrders(t, snk); n != 1 {
		t.Fatalf("%d orders after Commit, want 1", n)
	}

	// With nothing open, a write commits on its own again.
	if err := snk.Write(ctx, newMsg(t, "m3", order("A-3", map[string]any{"sku": "X", "qty": 1}))); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n := countOrders(t, snk); n != 2 {
		t.Fatalf("%d orders, want 2", n)
	}
}

func TestBeginTwiceIsRefused(t *testing.T) {
	ctx := t.Context()
	snk, _ := newSQLiteSink(t, orderStatements, nil)
	if err := snk.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { _ = snk.Rollback(ctx) })
	if err := snk.Begin(ctx); err == nil || !strings.Contains(err.Error(), "already open") {
		t.Fatalf("second Begin = %v, want a refusal", err)
	}
}

// With nothing open, Rollback is a no-op: the coordinator rolls back every
// participant after an early failure, including ones it never began.
func TestRollbackWithNothingOpenIsANoOp(t *testing.T) {
	snk, _ := newSQLiteSink(t, orderStatements, nil)
	if err := snk.Rollback(t.Context()); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if err := snk.Commit(t.Context()); err == nil {
		t.Fatal("Commit with nothing open succeeded; it must say there was nothing to commit")
	}
}

func TestDatabasesWithoutTwoPhaseCommitAreRefusedAtPreflight(t *testing.T) {
	ctx := t.Context()
	snk, _ := newSQLiteSink(t, orderStatements, nil)
	err := snk.PreflightTwoPhaseCommit(ctx)
	if err == nil {
		t.Fatal("a SQLite sink passed the two-phase preflight")
	}
	for _, want := range []string{"sqlite", "postgres", "mysql"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("preflight error %q does not mention %q", err, want)
		}
	}

	if err := snk.BeginNamed(ctx, uuid.NewString()); err != nil {
		t.Fatalf("BeginNamed: %v", err)
	}
	if _, err := snk.Prepare(ctx, ""); err == nil {
		t.Fatal("a SQLite sink prepared a transaction it cannot hold in doubt")
	}
	// A refused Prepare leaves nothing open behind it.
	if err := snk.Begin(ctx); err != nil {
		t.Fatalf("Begin after a refused Prepare: %v", err)
	}
	_ = snk.Rollback(ctx)
}

// The identifier is interpolated into PREPARE TRANSACTION and XA statements,
// which take no bind parameters, so anything but a canonical UUID is refused
// before the database is reached.
func TestTransactionIDsMustBeCanonicalUUIDs(t *testing.T) {
	ctx := t.Context()
	for _, driver := range []string{"postgres", "mysql"} {
		cfg, err := ParseConfig(map[string]string{"driver": driver, "statements": `[{"sql":"UPDATE t SET a = 1"}]`})
		if err != nil {
			t.Fatalf("ParseConfig: %v", err)
		}
		conn := "postgres://u:p@127.0.0.1:1/d?connect_timeout=1"
		if driver == "mysql" {
			conn = "u:p@tcp(127.0.0.1:1)/d?timeout=1s"
		}
		snk, err := Open(cfg, conn)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { _ = snk.Close() })
		for _, bad := range []string{"x'; DROP TABLE t; --", "{" + uuid.NewString() + "}", strings.ToUpper(uuid.NewString())} {
			for name, call := range map[string]func() error{
				"BeginNamed":       func() error { return snk.BeginNamed(ctx, bad) },
				"CommitPrepared":   func() error { return snk.CommitPrepared(ctx, bad) },
				"RollbackPrepared": func() error { return snk.RollbackPrepared(ctx, bad) },
			} {
				err := call()
				if err == nil || !strings.Contains(err.Error(), "UUID") {
					t.Errorf("%s %s(%q) = %v, want a refusal naming the UUID rule", driver, name, bad, err)
				}
			}
		}
	}
}

func TestCloseRollsBackAnOpenTransaction(t *testing.T) {
	ctx := t.Context()
	snk, _ := newSQLiteSink(t, orderStatements, nil)
	if err := snk.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := snk.Write(ctx, newMsg(t, "m1", order("A-1"))); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := snk.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
