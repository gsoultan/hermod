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

package txsql

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/sink/txgroup"
	"github.com/gsoultan/hermod/pkg/engine/twopc"
)

// Two-phase commit against real servers, asserted against what an operator
// would look at during an incident: pg_prepared_xacts on PostgreSQL and XA
// RECOVER on MySQL, not the sink's own account of what it did.
//
// PostgreSQL needs max_prepared_transactions > 0, which needs a restart:
//
//	docker run ... postgres:16 -c max_prepared_transactions=20

func dialectFor(t *testing.T, driver string) dialect {
	t.Helper()
	for _, d := range dialects {
		if d.driver == driver {
			return d
		}
	}
	t.Fatalf("no dialect %s", driver)
	return dialect{}
}

type memStore struct{ kv map[string][]byte }

func (m *memStore) Get(_ context.Context, k string) ([]byte, error) {
	v, ok := m.kv[k]
	if !ok {
		return nil, twopc.ErrNotFound
	}
	return v, nil
}
func (m *memStore) Set(_ context.Context, k string, v []byte) error { m.kv[k] = v; return nil }
func (m *memStore) Delete(_ context.Context, k string) error        { delete(m.kv, k); return nil }
func (m *memStore) List(_ context.Context, prefix string) (map[string][]byte, error) {
	out := map[string][]byte{}
	for k, v := range m.kv {
		if strings.HasPrefix(k, prefix) {
			out[k] = v
		}
	}
	return out, nil
}

// inDoubt lists what the database holds prepared: PostgreSQL's gids, or
// MySQL's XA RECOVER rows for Hermod's format id.
func (f fixture) inDoubt(t *testing.T) []string {
	t.Helper()
	q := "SELECT gid FROM pg_prepared_xacts"
	if f.snk.driver == "mysql" {
		q = "XA RECOVER"
	}
	rows, err := f.db.QueryContext(t.Context(), q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		if f.snk.driver == "mysql" {
			var formatID, gtridLen, bqualLen int64
			var data []byte
			if err := rows.Scan(&formatID, &gtridLen, &bqualLen, &data); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out = append(out, string(data[:gtridLen]))
			continue
		}
		var gid string
		if err := rows.Scan(&gid); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, gid)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return out
}

func dsnFor(t *testing.T, d dialect) string {
	t.Helper()
	return os.Getenv(d.env)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func group(t *testing.T, members ...fixture) *txgroup.Sink {
	t.Helper()
	c, err := twopc.New(twopc.Options{Store: &memStore{kv: map[string][]byte{}}, WorkflowID: "txsql-it-" + uuid.NewString()})
	if err != nil {
		t.Fatalf("twopc.New: %v", err)
	}
	ms := make([]txgroup.Member, len(members))
	for i, f := range members {
		ms[i] = txgroup.Member{ID: f.snk.cfg.Driver, Sink: f.snk}
	}
	g, err := txgroup.New(ms, c, nil)
	if err != nil {
		t.Fatalf("txgroup.New: %v", err)
	}
	if err := g.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	return g
}

// The guarantee: one batch reaches PostgreSQL and MySQL together, parent and
// children, and nothing is left in doubt afterwards.
func TestIntegrationTwoPhaseCommitsPostgresAndMySQLTogether(t *testing.T) {
	pg := setup(t, dialectFor(t, "postgres"), dialectFor(t, "postgres").parent, dialectFor(t, "postgres").child)
	my := setup(t, dialectFor(t, "mysql"), dialectFor(t, "mysql").parent, dialectFor(t, "mysql").child)
	g := group(t, pg, my)

	batch := []hermod.Message{
		orderMsg(t, "m1", "O-1", map[string]any{"sku": "A", "qty": 1}, map[string]any{"sku": "B", "qty": 2}),
		orderMsg(t, "m2", "O-2", map[string]any{"sku": "A", "qty": 3}),
	}
	if err := g.WriteBatch(context.Background(), batch); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	for _, f := range []fixture{pg, my} {
		if n := f.count(t, f.orders); n != 2 {
			t.Errorf("%s: %d orders, want 2", f.snk.cfg.Driver, n)
		}
		if n := f.count(t, f.items); n != 3 {
			t.Errorf("%s: %d items, want 3", f.snk.cfg.Driver, n)
		}
		if left := f.inDoubt(t); len(left) != 0 {
			t.Errorf("%s: %v left in doubt after a commit", f.snk.cfg.Driver, left)
		}
	}
}

// One member failing rolls back the other, which had already written.
func TestIntegrationTwoPhaseAbortsBothWhenOneFails(t *testing.T) {
	pg := setup(t, dialectFor(t, "postgres"), dialectFor(t, "postgres").parent, dialectFor(t, "postgres").child)
	my := setup(t, dialectFor(t, "mysql"), dialectFor(t, "mysql").parent, dialectFor(t, "mysql").child)
	g := group(t, pg, my)

	// qty is NOT NULL on both; the item without one fails the batch.
	bad := orderMsg(t, "m1", "O-1", map[string]any{"sku": "A", "qty": 1}, map[string]any{"sku": "B"})
	if err := g.WriteBatch(context.Background(), []hermod.Message{bad}); err == nil {
		t.Fatal("a batch with an item no database accepts was committed")
	}
	for _, f := range []fixture{pg, my} {
		if n := f.count(t, f.orders); n != 0 {
			t.Errorf("%s: %d orders after an abort, want 0", f.snk.cfg.Driver, n)
		}
		if left := f.inDoubt(t); len(left) != 0 {
			t.Errorf("%s: %v left in doubt after an abort", f.snk.cfg.Driver, left)
		}
	}
}

// A prepared transaction outlives the sink that prepared it, and another
// sink -- the one recovery builds after a restart -- can resolve it either
// way; rolling back a name twice, or one never prepared, is success.
func TestIntegrationPreparedTransactionsAreResolvedFromAnotherSink(t *testing.T) {
	for _, driver := range []string{"postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			d := dialectFor(t, driver)
			f := setup(t, d, d.parent, d.child)
			ctx := context.Background()
			other, err := Open(f.snk.cfg, dsnFor(t, d))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			t.Cleanup(func() { _ = other.Close() })

			prepare := func(orderID string) string {
				t.Helper()
				txID := uuid.NewString()
				if err := f.snk.BeginNamed(ctx, txID); err != nil {
					t.Fatalf("BeginNamed: %v", err)
				}
				if err := f.snk.Write(ctx, orderMsg(t, "m-"+orderID, orderID, map[string]any{"sku": "A", "qty": 1})); err != nil {
					t.Fatalf("Write: %v", err)
				}
				got, err := f.snk.Prepare(ctx, txID)
				if err != nil || got != txID {
					t.Fatalf("Prepare = %q, %v; want %q", got, err, txID)
				}
				if !contains(f.inDoubt(t), txID) {
					t.Fatalf("%s is not in doubt on the server after Prepare", txID)
				}
				if n := f.count(t, f.orders); n != 0 {
					t.Fatalf("a prepared order is visible before it was committed")
				}
				return txID
			}

			rolled := prepare("O-1")
			if err := other.RollbackPrepared(ctx, rolled); err != nil {
				t.Fatalf("RollbackPrepared: %v", err)
			}
			if err := other.RollbackPrepared(ctx, rolled); err != nil {
				t.Fatalf("a second RollbackPrepared = %v; recovery must be able to run twice", err)
			}
			if err := other.RollbackPrepared(ctx, uuid.NewString()); err != nil {
				t.Fatalf("RollbackPrepared of a name never prepared = %v, want success", err)
			}
			if n := f.count(t, f.orders); n != 0 {
				t.Fatalf("%d orders after a rollback, want 0", n)
			}

			committed := prepare("O-2")
			if err := other.CommitPrepared(ctx, committed); err != nil {
				t.Fatalf("CommitPrepared: %v", err)
			}
			if n := f.count(t, f.orders); n != 1 {
				t.Fatalf("%d orders after a commit, want 1", n)
			}
			if n := f.count(t, f.items); n != 1 {
				t.Fatalf("%d items after a commit, want 1", n)
			}
			if left := f.inDoubt(t); contains(left, committed) || contains(left, rolled) {
				t.Fatalf("%v left in doubt", left)
			}
		})
	}
}

// Oracle runs inside Begin and Commit but cannot prepare, and the preflight
// says so before any data moves.
func TestIntegrationOracleRefusesTwoPhaseCommitAtPreflight(t *testing.T) {
	d := dialectFor(t, "oracle")
	f := setup(t, d, d.parent, d.child)
	err := f.snk.PreflightTwoPhaseCommit(context.Background())
	if err == nil || !strings.Contains(err.Error(), "oracle") {
		t.Fatalf("Preflight = %v, want a refusal naming oracle", err)
	}
}

// XA START takes no isolation level, so the sink sets it for the
// connection's next transaction first; the XA transaction runs at it.
func TestIntegrationXATransactionRunsAtTheConfiguredIsolation(t *testing.T) {
	d := dialectFor(t, "mysql")
	f := setup(t, d, d.parent, d.child)
	ctx := context.Background()
	cfg := f.snk.cfg
	cfg.Isolation = isolationLevels["read_committed"]
	snk, err := Open(cfg, dsnFor(t, d))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = snk.Close() })

	if err := snk.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { _ = snk.Rollback(ctx) })
	// InnoDB lists a transaction once it has written.
	if err := snk.Write(ctx, orderMsg(t, "m1", "O-1")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rows, err := snk.openQuerier().QueryContext(ctx,
		"SELECT trx_isolation_level FROM information_schema.innodb_trx WHERE trx_mysql_thread_id = CONNECTION_ID()")
	if err != nil {
		t.Fatalf("read the transaction's isolation: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var level string
	if !rows.Next() {
		t.Fatal("the XA transaction is not listed in innodb_trx")
	}
	if err := rows.Scan(&level); err != nil {
		t.Fatal(err)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if level != "READ COMMITTED" {
		t.Errorf("the XA transaction runs at %q, want READ COMMITTED (MySQL's default is REPEATABLE READ)", level)
	}
}
