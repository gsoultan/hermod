//go:build integration

package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Rolling back a prepared transaction has to be safe to do twice, and safe to
// do for one that was never prepared.
//
// Recovery rolls back every identifier a coordinator recorded. Two ordinary
// situations leave one missing: an earlier recovery attempt already rolled it
// back, or the process died between the coordinator recording the name and this
// sink preparing anything under it. That second case is deliberate — the
// coordinator now records the name first, precisely so that a crash leaves a
// name without a transaction rather than a transaction without a name — which
// makes "identifier does not exist" an expected outcome rather than a fault.
//
// PostgreSQL answers such a rollback with an error:
//
//	ERROR: 42704: prepared transaction with identifier "…" does not exist
//
// confirmed against a live server rather than taken from memory. Returning that
// error would strand the record: recovery would retry the same identifier
// forever and never reach the participants that do have something prepared.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 \
//	POSTGRES_DSN='postgres://postgres:postgres@127.0.0.1:5432/hermod_it?sslmode=disable' \
//	go test -tags=integration ./pkg/comm/sink/postgres/
//
// The server needs max_prepared_transactions above zero, which CI's does.
func preparedDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q POSTGRES_DSN=%q in CI, where PostgreSQL is "+
				"started for exactly this", os.Getenv("HERMOD_INTEGRATION"), dsn)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}
	return dsn
}

// An identifier that was never prepared rolls back cleanly.
func TestRollingBackATransactionThatWasNeverPreparedSucceeds(t *testing.T) {
	dsn := preparedDSN(t)
	sink := NewPostgresSink(dsn, "", nil, true, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	// A well-formed identifier the coordinator could have recorded, for a
	// transaction that never came into being.
	orphan := uuid.New().String()

	if err := sink.RollbackPrepared(t.Context(), orphan); err != nil {
		t.Fatalf("rolling back an identifier that was never prepared returned an error: %v\n"+
			"recovery rolls back every recorded identifier, and the coordinator now "+
			"records the name before the transaction exists — so this is an expected "+
			"state, and an error here makes recovery retry it forever", err)
	}
}

// Rolling back the same real transaction twice is safe: the second call finds
// nothing and says so quietly.
func TestRollingBackTwiceIsSafe(t *testing.T) {
	dsn := preparedDSN(t)

	table := "hermod_prepared_idem"
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(t.Context(),
		fmt.Sprintf("DROP TABLE IF EXISTS %s; CREATE TABLE %s (id TEXT PRIMARY KEY, data JSONB)", table, table)); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
	})

	sink := NewPostgresSink(dsn, table, nil, true, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	if err := sink.Begin(t.Context()); err != nil {
		t.Fatalf("begin: %v", err)
	}
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("a")
	msg.SetOperation(hermod.OpCreate)
	msg.SetPayload([]byte(`{"v":1}`))
	if err := sink.Write(t.Context(), msg); err != nil {
		t.Fatalf("write: %v", err)
	}

	txID := uuid.New().String()
	got, err := sink.Prepare(t.Context(), txID)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if got != txID {
		t.Fatalf("the sink prepared under %q, not the coordinator's %q; the coordinator "+
			"recorded the latter and recovery would act on it", got, txID)
	}

	if err := sink.RollbackPrepared(t.Context(), txID); err != nil {
		t.Fatalf("first rollback: %v", err)
	}
	if err := sink.RollbackPrepared(t.Context(), txID); err != nil {
		t.Errorf("the second rollback of the same identifier failed: %v\n"+
			"recovery must be safe to run twice", err)
	}

	var n int
	if err := pool.QueryRow(t.Context(),
		fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&n); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if n != 0 {
		t.Errorf("the rolled-back transaction left %d row(s) behind", n)
	}
}
