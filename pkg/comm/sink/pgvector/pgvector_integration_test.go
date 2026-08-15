//go:build integration

package pgvector

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
)

// The pgvector sink, against a real server with the extension loaded.
//
// pgvector was Beta: substantial and unit-tested, never shown to store a
// vector. Beyond the ordinary upsert-and-delete, one thing here is worth a
// server specifically — the sink builds its statements by interpolating
// identifiers, which SECURITY.md says never to do:
//
//	Identifiers, which cannot be bound as parameters, go through
//	sqlutil.QuoteIdent / ValidateIdent — never string interpolation.
//
// A table or column name that needs quoting therefore produces a broken
// statement rather than a quoted identifier, and a name chosen to be hostile
// produces whatever it likes. These come from sink configuration rather than
// from a message, so an editor is already trusted — but the rule exists because
// "trusted today" is not a property that survives a feature being reused.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 PGVECTOR_DSN='postgres://postgres:postgres@127.0.0.1:5432/hermod_it?sslmode=disable' \
//	go test -tags=integration ./pkg/comm/sink/pgvector/

func requirePgvector(t *testing.T) (string, *pgxpool.Pool, string) {
	t.Helper()
	dsn := os.Getenv("PGVECTOR_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q PGVECTOR_DSN=%q in CI, where a pgvector server "+
				"is started for exactly this", os.Getenv("HERMOD_INTEGRATION"), dsn)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and PGVECTOR_DSN to run")
	}

	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(t.Context(), "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		t.Fatalf("the vector extension is not available on this server: %v", err)
	}

	table := "pgv_" + strings.ToLower(t.Name())
	drop := func() {
		_, _ = pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+table)
	}
	drop()
	t.Cleanup(drop)
	return dsn, pool, table
}

func newVecMsg(t *testing.T, id string, op hermod.Operation, vec []float64) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(m.Release)
	m.SetID(id)
	m.SetOperation(op)
	m.SetData("id", id)
	m.SetData("embedding", vec)
	return m
}

func countRows(t *testing.T, pool *pgxpool.Pool, table, id string) int {
	t.Helper()
	var n int
	q := fmt.Sprintf("SELECT count(*) FROM %s WHERE id = $1", table)
	if err := pool.QueryRow(context.Background(), q, id).Scan(&n); err != nil {
		t.Fatalf("counting: %v", err)
	}
	return n
}

// The ordinary path: a vector lands, a redelivery upserts rather than
// duplicating, and a delete removes.
func TestAVectorIsStoredUpsertedAndDeleted(t *testing.T) {
	dsn, pool, table := requirePgvector(t)

	sink := NewSink(dsn, table, "embedding", "id", "", nil, false, "", "", "")
	t.Cleanup(func() { _ = sink.Close() })

	ctx := t.Context()
	if err := sink.Write(ctx, newVecMsg(t, "a", hermod.OpCreate, []float64{1, 2, 3})); err != nil {
		t.Fatalf("write: %v", err)
	}
	if n := countRows(t, pool, table, "a"); n != 1 {
		t.Fatalf("stored %d rows for a, want 1", n)
	}

	// At-least-once: the same record again must overwrite, not accumulate.
	if err := sink.Write(ctx, newVecMsg(t, "a", hermod.OpCreate, []float64{4, 5, 6})); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if n := countRows(t, pool, table, "a"); n != 1 {
		t.Errorf("after a redelivery there are %d rows for a, want 1 — the upsert is not "+
			"collapsing duplicates", n)
	}

	if err := sink.Write(ctx, newVecMsg(t, "a", hermod.OpDelete, nil)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n := countRows(t, pool, table, "a"); n != 0 {
		t.Errorf("the deleted row is still present %d time(s)", n)
	}
}

// An identifier that needs quoting.
//
// SECURITY.md's rule is that identifiers go through sqlutil.QuoteIdent rather
// than string interpolation. This sink interpolates, so a perfectly ordinary
// mixed-case or reserved-word column name — the kind a vector store picks up
// from somebody else's schema — produces a statement PostgreSQL folds or
// rejects rather than the column that was asked for.
func TestAnIdentifierNeedingQuotesIsQuoted(t *testing.T) {
	dsn, pool, table := requirePgvector(t)

	// "order" is reserved, and unquoted it is a syntax error rather than a
	// column. Nothing exotic: it is a normal thing to call a column.
	const idCol = "order"

	if _, err := pool.Exec(t.Context(), fmt.Sprintf(
		`CREATE TABLE %s ("order" TEXT PRIMARY KEY, embedding vector(3))`, table)); err != nil {
		t.Fatalf("creating the table: %v", err)
	}

	sink := NewSink(dsn, table, "embedding", idCol, "", nil, true, "", "", "")
	t.Cleanup(func() { _ = sink.Close() })

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("a")
	msg.SetOperation(hermod.OpCreate)
	msg.SetData("embedding", []float64{1, 2, 3})

	if err := sink.Write(t.Context(), msg); err != nil {
		t.Fatalf("writing to a table whose id column needs quoting failed: %v\n"+
			"the statement is built with fmt.Sprintf, so the identifier arrives unquoted "+
			"and PostgreSQL reads a reserved word instead of a column name. SECURITY.md "+
			"says identifiers go through sqlutil.QuoteIdent for exactly this reason", err)
	}

	var n int
	if err := pool.QueryRow(t.Context(),
		fmt.Sprintf(`SELECT count(*) FROM %s WHERE "order" = $1`, table), "a").Scan(&n); err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if n != 1 {
		t.Errorf("stored %d rows, want 1", n)
	}
}
