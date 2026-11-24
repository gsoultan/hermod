// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package txgroup_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	sinkpostgres "github.com/gsoultan/hermod/pkg/comm/sink/postgres"
	"github.com/gsoultan/hermod/pkg/comm/sink/txgroup"
	"github.com/gsoultan/hermod/pkg/engine/twopc"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Two-phase commit against a real PostgreSQL.
//
// Every other test in this package uses fakes, which proves the protocol is
// implemented but says nothing about whether PREPARE TRANSACTION behaves the way
// the coordinator assumes. That gap matters more here than in most features: the
// whole risk profile is "an unresolved prepared transaction holds locks and
// blocks VACUUM cluster-wide", and none of it is observable without a server.
//
// So this asserts against pg_prepared_xacts directly — the same view an operator
// would check during an incident — rather than trusting the sink's own account of
// what it did.
//
// Requires max_prepared_transactions > 0, which PostgreSQL disables by default
// and which needs a server restart to change. scripts/create-postgres.sh sets it.
// ---------------------------------------------------------------------------

func itDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}
	return dsn
}

func pool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// requirePreparedTransactions fails loudly rather than skipping: a run that
// silently skips the only test covering the hazard is worse than no test, since
// the suite still reports green.
func requirePreparedTransactions(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	var maxPrepared int
	if err := p.QueryRow(context.Background(),
		"SELECT current_setting('max_prepared_transactions')::int").Scan(&maxPrepared); err != nil {
		t.Fatalf("reading max_prepared_transactions: %v", err)
	}
	if maxPrepared == 0 {
		t.Fatal("max_prepared_transactions is 0, so PREPARE TRANSACTION cannot work. " +
			"Set it (server restart required) — scripts/create-postgres.sh does this.")
	}
}

// makeTable creates a scratch destination and returns its name.
func makeTable(t *testing.T, p *pgxpool.Pool, suffix string) string {
	t.Helper()
	name := fmt.Sprintf("txgroup_it_%s_%d", suffix, time.Now().UnixNano())
	ctx := context.Background()
	if _, err := p.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE %s (id TEXT PRIMARY KEY, val TEXT)`, name)); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = p.Exec(context.Background(), "DROP TABLE IF EXISTS "+name)
	})
	return name
}

func rowCount(t *testing.T, p *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := p.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// preparedCount counts the prepared transactions that still hold locks on the
// given tables. It does not count all of pg_prepared_xacts, because other
// packages prepare transactions on the same server while this one runs; a
// prepared transaction's locks stay in pg_locks with no backend pid.
func preparedCount(t *testing.T, p *pgxpool.Pool, tables ...string) int {
	t.Helper()
	var n int
	if err := p.QueryRow(context.Background(),
		`SELECT count(DISTINCT virtualtransaction) FROM pg_locks
		  WHERE pid IS NULL AND locktype = 'relation'
		    AND relation = ANY (SELECT to_regclass(x)::oid FROM unnest($1::text[]) AS x)`,
		tables).Scan(&n); err != nil {
		t.Fatalf("count prepared transactions on %v: %v", tables, err)
	}
	return n
}

func newSink(dsn, table string) *sinkpostgres.PostgresSink {
	return sinkpostgres.NewPostgresSink(dsn, table, []sqlutil.ColumnMapping{
		{SourceField: "id", TargetColumn: "id", IsPrimaryKey: true},
		{SourceField: "val", TargetColumn: "val"},
	}, true, "", "", "", "", false, false)
}

func msg(id, val string) hermod.Message {
	m := message.AcquireMessage()
	m.SetID(id)
	m.SetOperation(hermod.OpCreate)
	m.SetData("id", id)
	m.SetData("val", val)
	return m
}

// memStore is a twopc.Store held in memory. Durability across a process restart
// is covered by the unit tests; what matters here is the database side.
type memStore struct{ kv map[string][]byte }

func newMemStore() *memStore { return &memStore{kv: map[string][]byte{}} }

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
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			out[k] = v
		}
	}
	return out, nil
}

// TestPreflightAcceptsARealServer proves the preflight query actually runs. It
// was written against the documentation and never executed until now.
func TestPreflightAcceptsARealServer(t *testing.T) {
	dsn := itDSN(t)
	p := pool(t, dsn)
	requirePreparedTransactions(t, p)

	a := newSink(dsn, makeTable(t, p, "pf_a"))
	b := newSink(dsn, makeTable(t, p, "pf_b"))
	defer a.Close()
	defer b.Close()

	c, err := twopc.New(twopc.Options{Store: newMemStore(), WorkflowID: "it-preflight"})
	if err != nil {
		t.Fatalf("twopc.New: %v", err)
	}
	g, err := txgroup.New([]txgroup.Member{{ID: "a", Sink: a}, {ID: "b", Sink: b}}, c, nil)
	if err != nil {
		t.Fatalf("txgroup.New: %v", err)
	}

	if err := g.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight rejected a correctly configured server: %v", err)
	}
}

// TestCommitLandsInBothTables is the guarantee, observed in the databases rather
// than inferred from the sinks.
func TestCommitLandsInBothTables(t *testing.T) {
	dsn := itDSN(t)
	p := pool(t, dsn)
	requirePreparedTransactions(t, p)

	tableA := makeTable(t, p, "commit_a")
	tableB := makeTable(t, p, "commit_b")

	a := newSink(dsn, tableA)
	b := newSink(dsn, tableB)
	defer a.Close()
	defer b.Close()

	c, _ := twopc.New(twopc.Options{Store: newMemStore(), WorkflowID: "it-commit"})
	g, err := txgroup.New([]txgroup.Member{{ID: "a", Sink: a}, {ID: "b", Sink: b}}, c, nil)
	if err != nil {
		t.Fatalf("txgroup.New: %v", err)
	}

	before := preparedCount(t, p, tableA, tableB)

	if err := g.WriteBatch(context.Background(), []hermod.Message{
		msg("1", "one"), msg("2", "two"),
	}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	if got := rowCount(t, p, tableA); got != 2 {
		t.Errorf("table A has %d rows, want 2", got)
	}
	if got := rowCount(t, p, tableB); got != 2 {
		t.Errorf("table B has %d rows, want 2", got)
	}

	// Nothing may be left in doubt after a clean commit. This is the assertion
	// that would have caught a coordinator that resolved with the wrong gid.
	if got := preparedCount(t, p, tableA, tableB); got != before {
		t.Errorf("pg_prepared_xacts went from %d to %d; the transaction is still holding locks", before, got)
	}
}

// TestCrashAfterPrepareLeavesRowsInvisibleUntilRecovery is the property the
// whole design turns on: prepared writes are durable but not visible, and the
// coordinator's log decides what happens to them.
func TestCrashAfterPrepareLeavesRowsInvisibleUntilRecovery(t *testing.T) {
	dsn := itDSN(t)
	p := pool(t, dsn)
	requirePreparedTransactions(t, p)

	tableA := makeTable(t, p, "crash_a")
	tableB := makeTable(t, p, "crash_b")

	a := newSink(dsn, tableA)
	b := newSink(dsn, tableB)
	defer a.Close()
	defer b.Close()

	store := newMemStore()
	c, _ := twopc.New(twopc.Options{Store: store, WorkflowID: "it-crash"})
	g, err := txgroup.New([]txgroup.Member{{ID: "a", Sink: a}, {ID: "b", Sink: b}}, c, nil)
	if err != nil {
		t.Fatalf("txgroup.New: %v", err)
	}

	ctx := context.Background()
	// Drive both participants to PREPARED and stop, the way a crash would.
	// Only this test's own transactions are counted and rolled back: other
	// packages run 2PC tests against the same server at the same time.
	gids, err := prepareOnlyViaSinks(ctx, a, b, tableA, tableB)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	// Prepared, therefore in doubt: the rows exist but are invisible.
	if got := preparedAmong(t, p, gids); got != 2 {
		t.Fatalf("pg_prepared_xacts holds %d of this test's transactions, want 2 — they did not stay in doubt", got)
	}
	if got := rowCount(t, p, tableA); got != 0 {
		t.Errorf("table A shows %d rows from a prepared-but-uncommitted transaction, want 0", got)
	}

	// Roll them back the way Recover would, and confirm the locks are released.
	if err := rollbackPrepared(ctx, p, gids); err != nil {
		t.Fatalf("rollback prepared: %v", err)
	}
	if got := preparedAmong(t, p, gids); got != 0 {
		t.Errorf("pg_prepared_xacts still holds %d of this test's transactions after rollback, want 0", got)
	}
	if got := rowCount(t, p, tableA); got != 0 {
		t.Errorf("table A has %d rows after rollback, want 0", got)
	}
	_ = g
}

// prepareOnlyViaSinks writes to both sinks and prepares them, leaving both in
// doubt. It drives the sinks directly because the point is to stop between the
// vote and the decision, which the coordinator deliberately does not expose.
func prepareOnlyViaSinks(ctx context.Context, a, b *sinkpostgres.PostgresSink, _, _ string) ([]string, error) {
	// Begin before writing, the same order the coordinator uses: a sink cannot
	// prepare work that is not already inside a transaction it owns.
	if err := a.Begin(ctx); err != nil {
		return nil, fmt.Errorf("begin a: %w", err)
	}
	if err := b.Begin(ctx); err != nil {
		return nil, fmt.Errorf("begin b: %w", err)
	}
	if err := a.Write(ctx, msg("x1", "a")); err != nil {
		return nil, fmt.Errorf("write a: %w", err)
	}
	if err := b.Write(ctx, msg("x1", "b")); err != nil {
		return nil, fmt.Errorf("write b: %w", err)
	}
	gidA, err := a.Prepare(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("prepare a: %w", err)
	}
	gidB, err := b.Prepare(ctx, "")
	if err != nil {
		return []string{gidA}, fmt.Errorf("prepare b: %w", err)
	}
	return []string{gidA, gidB}, nil
}

// preparedAmong counts how many of gids are still prepared on the server.
func preparedAmong(t *testing.T, p *pgxpool.Pool, gids []string) int {
	t.Helper()
	var n int
	if err := p.QueryRow(context.Background(),
		"SELECT count(*) FROM pg_prepared_xacts WHERE gid = ANY($1)", gids).Scan(&n); err != nil {
		t.Fatalf("count pg_prepared_xacts: %v", err)
	}
	return n
}

// rollbackPrepared resolves the given prepared transactions by name, which is
// the manual procedure documented in docs/transactional-sink-groups.md for a
// lost coordinator log.
func rollbackPrepared(ctx context.Context, p *pgxpool.Pool, gids []string) error {
	for _, gid := range gids {
		// gid came from PostgresSink.Prepare, which only prepares under a UUID.
		if _, err := p.Exec(ctx, fmt.Sprintf("ROLLBACK PREPARED '%s'", gid)); err != nil {
			return fmt.Errorf("rollback prepared %q: %w", gid, err)
		}
	}
	return nil
}
