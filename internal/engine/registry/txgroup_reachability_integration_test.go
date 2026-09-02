//go:build integration

package registry

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Reachability: a transactional group as a workflow actually builds one.
//
// The group had unit tests proving its refusals, and integration tests proving
// two-phase commit against a real PostgreSQL. Both passed for months while the
// feature could not start at all, because each covered one side of a seam and
// nothing covered the join: a configured workflow reads its sinks from storage
// and builds them through the factory, which wrapped them in decorators that do
// not forward Prepare or CommitPrepared. The group then rejected every member.
//
// The tests that construct sinks directly could never have caught that. This one
// goes the way a workflow goes — stored configuration, registry, factory, group,
// real databases — and asserts rows land in both.
//
// That is the general lesson, not a fact about two-phase commit: a feature
// configured through storage needs one test that starts from storage. See the
// reachability section of AGENTS.md.
// ---------------------------------------------------------------------------

func reachabilityDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}
	return dsn
}

func reachabilityPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// requirePrepared skips when the server cannot prepare transactions, rather than
// reporting a failure that is really a server setting.
func requirePrepared(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	var maxPrepared int
	if err := p.QueryRow(context.Background(),
		"SELECT current_setting('max_prepared_transactions')::int").Scan(&maxPrepared); err != nil {
		t.Fatalf("reading max_prepared_transactions: %v", err)
	}
	if maxPrepared < 1 {
		t.Skip("max_prepared_transactions is 0; two-phase commit is unavailable on this server")
	}
}

func makeReachabilityTable(t *testing.T, p *pgxpool.Pool, name string) string {
	t.Helper()
	table := fmt.Sprintf("reach_%s_%d", name, os.Getpid())
	ctx := context.Background()
	if _, err := p.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (id TEXT PRIMARY KEY, val TEXT)`, table)); err != nil {
		t.Fatalf("create %s: %v", table, err)
	}
	if _, err := p.Exec(ctx, fmt.Sprintf(`TRUNCATE %s`, table)); err != nil {
		t.Fatalf("truncate %s: %v", table, err)
	}
	t.Cleanup(func() {
		_, _ = p.Exec(context.Background(), fmt.Sprintf(`DROP TABLE IF EXISTS %s`, table))
	})
	return table
}

func reachabilityRowCount(t *testing.T, p *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := p.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT count(*) FROM %s`, table)).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func reachabilityPreparedCount(t *testing.T, p *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := p.QueryRow(context.Background(),
		`SELECT count(*) FROM pg_prepared_xacts`).Scan(&n); err != nil {
		t.Fatalf("count pg_prepared_xacts: %v", err)
	}
	return n
}

// storedPostgresSink is a sink record of the kind an operator creates in the UI.
func storedPostgresSink(id, dsn, table string) storage.Sink {
	return storage.Sink{
		ID:   id,
		Name: id,
		Type: "postgres",
		Config: map[string]string{
			"connection_string":  dsn,
			"table":              table,
			"use_existing_table": "true",
			"column_mappings": `[{"source_field":"id","target_column":"id","is_primary_key":true},` +
				`{"source_field":"val","target_column":"val"}]`,
		},
	}
}

func reachabilityMessage(id, val string) hermod.Message {
	m := message.AcquireMessage()
	m.SetID(id)
	m.SetOperation(hermod.OpCreate)
	m.SetData("id", id)
	m.SetData("val", val)
	return m
}

// TestAGroupBuiltFromStoredConfigurationWrites is the test whose absence let a
// shipped feature be unusable for its whole life.
func TestAGroupBuiltFromStoredConfigurationWrites(t *testing.T) {
	dsn := reachabilityDSN(t)
	p := reachabilityPool(t, dsn)
	requirePrepared(t, p)

	tableA := makeReachabilityTable(t, p, "a")
	tableB := makeReachabilityTable(t, p, "b")

	r := registryWithSinks(map[string]storage.Sink{
		"sink-a": storedPostgresSink("sink-a", dsn, tableA),
		"sink-b": storedPostgresSink("sink-b", dsn, tableB),
	})

	// Exactly what the engine calls for a node of type txgroup.
	group, err := r.createSinkInternal(context.Background(), factory.SinkConfig{
		ID:     "grp",
		Type:   "txgroup",
		Config: map[string]string{"members": "sink-a,sink-b"},
	})
	if err != nil {
		t.Fatalf("building a group from stored configuration: %v\n"+
			"this is the path every configured workflow takes, and it is the one that was broken", err)
	}
	defer func() { _ = group.Close() }()

	// A group that is not a BatchSink cannot share a transaction boundary at
	// all: the engine would write each message on its own.
	batch, ok := group.(hermod.BatchSink)
	if !ok {
		t.Fatalf("the group built from configuration is %T, which is not a hermod.BatchSink", group)
	}

	before := reachabilityPreparedCount(t, p)

	if err := batch.WriteBatch(context.Background(), []hermod.Message{
		reachabilityMessage("1", "one"),
		reachabilityMessage("2", "two"),
	}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	if got := reachabilityRowCount(t, p, tableA); got != 2 {
		t.Errorf("member A has %d rows, want 2", got)
	}
	if got := reachabilityRowCount(t, p, tableB); got != 2 {
		t.Errorf("member B has %d rows, want 2", got)
	}

	// A clean commit must leave nothing in doubt. A prepared transaction left
	// behind holds locks and blocks VACUUM until somebody resolves it by hand.
	if got := reachabilityPreparedCount(t, p); got != before {
		t.Errorf("pg_prepared_xacts went from %d to %d; a transaction is still holding locks",
			before, got)
	}
}

// TestAGroupFromStoredConfigurationIsAtomic. Landing in one member and not the
// other is the exact outcome the group exists to prevent, and the reason anyone
// accepts the cost of two-phase commit.
//
// Making a member fail takes some care: pointing one at a table that does not
// exist is not a failure, because the sink creates it. So the second member gets
// a table whose CHECK constraint the write must violate — a failure the database
// raises at INSERT, after the first member has already written.
func TestAGroupFromStoredConfigurationIsAtomic(t *testing.T) {
	dsn := reachabilityDSN(t)
	p := reachabilityPool(t, dsn)
	requirePrepared(t, p)

	tableA := makeReachabilityTable(t, p, "atomic_a")

	tableB := fmt.Sprintf("reach_atomic_b_%d", os.Getpid())
	ctx := context.Background()
	if _, err := p.Exec(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tableB)); err != nil {
		t.Fatalf("drop %s: %v", tableB, err)
	}
	if _, err := p.Exec(ctx, fmt.Sprintf(
		`CREATE TABLE %s (id TEXT PRIMARY KEY, val TEXT CHECK (val = 'nothing writes this'))`,
		tableB)); err != nil {
		t.Fatalf("create %s: %v", tableB, err)
	}
	t.Cleanup(func() {
		_, _ = p.Exec(context.Background(), fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tableB))
	})

	r := registryWithSinks(map[string]storage.Sink{
		"sink-a": storedPostgresSink("sink-a", dsn, tableA),
		"sink-b": storedPostgresSink("sink-b", dsn, tableB),
	})

	group, err := r.createSinkInternal(context.Background(), factory.SinkConfig{
		ID:     "grp-atomic",
		Type:   "txgroup",
		Config: map[string]string{"members": "sink-a,sink-b"},
	})
	if err != nil {
		t.Fatalf("building the group: %v", err)
	}
	defer func() { _ = group.Close() }()

	batch, ok := group.(hermod.BatchSink)
	if !ok {
		t.Fatalf("the group built from configuration is %T, which is not a hermod.BatchSink", group)
	}

	before := reachabilityPreparedCount(t, p)

	if err := batch.WriteBatch(context.Background(), []hermod.Message{
		reachabilityMessage("1", "one"),
	}); err == nil {
		t.Fatal("a write that could not reach every member reported success")
	}

	if got := reachabilityRowCount(t, p, tableA); got != 0 {
		t.Errorf("member A kept %d rows after the group failed; the write was not atomic, "+
			"which is the whole reason for two-phase commit", got)
	}
	if got := reachabilityPreparedCount(t, p); got != before {
		t.Errorf("pg_prepared_xacts went from %d to %d; a failed write left a transaction in doubt "+
			"holding locks and blocking VACUUM", before, got)
	}
}
