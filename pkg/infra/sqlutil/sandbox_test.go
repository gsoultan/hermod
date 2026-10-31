// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package sqlutil

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// The preview sandbox.
//
// A simulation or a Run Preview used to run an Execute SQL node's INSERT or
// UPDATE on the pool, which commits. The sandbox gives the run one transaction
// per pool, shared by every node, and rolls it back when the run ends: a later
// node sees the write, the table never does.
// ---------------------------------------------------------------------------

func newSandboxDB(t *testing.T) *sql.DB {
	t.Helper()
	// A file, not :memory:. Every connection to :memory: is its own database,
	// so the pool would not see what the sandbox's connection wrote even
	// after a commit, and the test would pass for the wrong reason.
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "sandbox.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(t.Context(),
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, code TEXT UNIQUE, status TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO orders (code, status) VALUES ('seed', 'new')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

func countOrders(t *testing.T, q Querier, where string) int {
	t.Helper()
	rows, err := q.QueryContext(t.Context(), "SELECT count(*) FROM orders WHERE "+where)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var n int
	if !rows.Next() {
		t.Fatalf("count returned no row: %v", rows.Err())
	}
	if err := rows.Scan(&n); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return n
}

// Outside a preview nothing changes: the node gets the pool and its write
// commits, exactly as it always has.
func TestUseOutsideAPreviewIsThePool(t *testing.T) {
	db := newSandboxDB(t)

	q, err := Use(t.Context(), db, "sqlite")
	if err != nil {
		t.Fatalf("Use: %v", err)
	}
	if _, err := q.ExecContext(t.Context(), `INSERT INTO orders (code, status) VALUES ('live', 'new')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if n := countOrders(t, db, "code = 'live'"); n != 1 {
		t.Errorf("a live write is visible on the pool %d times, want 1", n)
	}
}

// The reported case and the follow-up in one: the write is seen by the next
// statement of the same run -- an insert and then an update of that row --
// and by nothing once the run is over.
func TestSandboxShowsTheRunItsOwnWritesAndRollsThemBack(t *testing.T) {
	db := newSandboxDB(t)
	sb := NewSandbox(t.Context())
	ctx := WithSandbox(t.Context(), sb)

	q, err := Use(ctx, db, "sqlite")
	if err != nil {
		t.Fatalf("Use: %v", err)
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO orders (code, status) VALUES ('C-1', 'new')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// A second node asks again and must get the same transaction, not a
	// fresh connection that cannot see the uncommitted row.
	q2, err := Use(ctx, db, "sqlite")
	if err != nil {
		t.Fatalf("Use again: %v", err)
	}
	if _, err := q2.ExecContext(ctx, `UPDATE orders SET status = 'paid' WHERE code = 'C-1'`); err != nil {
		t.Fatalf("update: %v", err)
	}
	if n := countOrders(t, q2, "code = 'C-1' AND status = 'paid'"); n != 1 {
		t.Errorf("the run sees its updated row %d times, want 1", n)
	}

	if err := sb.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if n := countOrders(t, db, "code = 'C-1'"); n != 0 {
		t.Errorf("the preview's row reached the table: %d rows", n)
	}
	if n := countOrders(t, db, "code = 'seed' AND status = 'new'"); n != 1 {
		t.Errorf("the seeded row changed: %d matching rows, want 1", n)
	}
}

// A run that is over is over. A late node -- or a goroutine that outlived the
// request -- must not quietly reopen a transaction nobody will roll back, or
// worse, fall back to the pool and commit.
func TestSandboxRefusesUseAfterRollback(t *testing.T) {
	db := newSandboxDB(t)
	sb := NewSandbox(t.Context())
	ctx := WithSandbox(t.Context(), sb)
	if err := sb.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, err := Use(ctx, db, "sqlite"); err == nil {
		t.Fatal("Use after Rollback succeeded; a write would have nowhere safe to go")
	}
}

// A node that manages its own transaction (SCD) commits it. In the sandbox
// that commit must stay inside the run's transaction, or it would publish the
// node's write and everything before it.
func TestSandboxNestedTransactionCommitStaysInsideTheRun(t *testing.T) {
	db := newSandboxDB(t)
	sb := NewSandbox(t.Context())
	ctx := WithSandbox(t.Context(), sb)

	q, err := Use(ctx, db, "sqlite")
	if err != nil {
		t.Fatalf("Use: %v", err)
	}
	tx, err := q.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO orders (code, status) VALUES ('nested', 'new')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if n := countOrders(t, q, "code = 'nested'"); n != 1 {
		t.Errorf("the run does not see its nested commit: %d rows", n)
	}

	// And a nested rollback undoes only the nested work.
	tx2, err := q.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx 2: %v", err)
	}
	if _, err := tx2.ExecContext(ctx, `INSERT INTO orders (code, status) VALUES ('undone', 'new')`); err != nil {
		t.Fatalf("insert 2: %v", err)
	}
	if err := tx2.Rollback(); err != nil {
		t.Fatalf("Rollback nested: %v", err)
	}
	if n := countOrders(t, q, "code = 'undone'"); n != 0 {
		t.Errorf("a nested rollback left its row: %d", n)
	}
	if n := countOrders(t, q, "code = 'nested'"); n != 1 {
		t.Errorf("a nested rollback took the earlier nested commit with it: %d", n)
	}

	if err := sb.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if n := countOrders(t, db, "code IN ('nested', 'undone')"); n != 0 {
		t.Errorf("a nested commit reached the table: %d rows", n)
	}
}

// Every statement through the sandbox passes the guard, so a template cannot
// commit the run's transaction from inside it.
func TestSandboxRefusesAStatementThatWouldEscapeIt(t *testing.T) {
	db := newSandboxDB(t)
	sb := NewSandbox(t.Context())
	t.Cleanup(func() { _ = sb.Rollback() })
	ctx := WithSandbox(t.Context(), sb)

	q, err := Use(ctx, db, "sqlite")
	if err != nil {
		t.Fatalf("Use: %v", err)
	}
	_, err = q.ExecContext(ctx, `INSERT INTO orders (code) VALUES ('x'); COMMIT`)
	if err == nil {
		t.Fatal("a statement carrying a COMMIT ran in the sandbox")
	}
	if !strings.Contains(err.Error(), "preview") {
		t.Errorf("the refusal does not say it is about the preview: %v", err)
	}
}

func TestCheckSandboxStatement(t *testing.T) {
	cases := []struct {
		driver, query string
		refused       bool
	}{
		{"pgx", `INSERT INTO t (a) VALUES ($1) RETURNING id`, false},
		{"pgx", `UPDATE t SET a = 'commit; rollback' WHERE id = 1`, false},
		{"pgx", `-- commit later
UPDATE t SET a = 1`, false},
		{"pgx", `/* ROLLBACK */ DELETE FROM t WHERE id = 1`, false},
		{"pgx", `UPDATE t SET a = $$; COMMIT$$`, false},
		{"pgx", `SELECT 1;`, false},
		{"pgx", `WITH x AS (UPDATE t SET a = 1 RETURNING *) SELECT * FROM x`, false},
		{"pgx", `CREATE TEMP TABLE scratch (a int)`, false},
		{"sqlserver", `INSERT INTO [commit] (a) OUTPUT inserted.* VALUES (1)`, false},
		{"mysql", "UPDATE `rollback` SET a = 1", false},
		// A backslash escapes a quote in MySQL only. Read the MySQL way on
		// PostgreSQL, this would hide the COMMIT inside a string.
		{"mysql", `UPDATE t SET a = 'x\'; COMMIT; --'`, false},
		{"pgx", `UPDATE t SET a = 'x\'; COMMIT; --'`, true},

		{"pgx", `INSERT INTO t (a) VALUES (1); COMMIT`, true},
		{"pgx", `COMMIT`, true},
		{"pgx", `END`, true},
		{"pgx", `BEGIN`, true},
		{"pgx", `SELECT 1; SELECT 2`, true},
		{"pgx", `RELEASE SAVEPOINT hermod_stmt`, true},
		{"sqlserver", "INSERT INTO t (a) VALUES (1)\nCOMMIT TRANSACTION", true},
		{"sqlserver", `BEGIN TRAN; UPDATE t SET a = 1`, true},
		{"mysql", `START TRANSACTION`, true},
		{"mysql", `SET autocommit = 1`, true},
		{"sqlserver", `SET IMPLICIT_TRANSACTIONS OFF`, true},

		// DDL commits on its own on these engines, taking the sandbox with it.
		{"mysql", `CREATE TABLE x (a int)`, true},
		{"mysql", `TRUNCATE TABLE t`, true},
		{"mysql", `CALL do_things()`, true},
		{"oracle", `ALTER TABLE t ADD (b NUMBER)`, true},
		{"oracle", `BEGIN do_things; END;`, true},
		{"snowflake", `DROP TABLE t`, true},
		// ClickHouse cannot roll back at all: reads only.
		{"clickhouse", `SELECT * FROM t`, false},
		{"clickhouse", `INSERT INTO t VALUES (1)`, true},
		{"clickhouse", `ALTER TABLE t UPDATE a = 1 WHERE 1`, true},
	}
	for _, c := range cases {
		err := CheckSandboxStatement(c.driver, c.query)
		if (err != nil) != c.refused {
			t.Errorf("%s %q: err = %v, want refused=%v", c.driver, c.query, err, c.refused)
		}
	}
}

// The discovery path decides from the text whether a query can be run
// straight on the pool. Anything that is not plainly a read goes through the
// sandbox; guessing "read" wrong is the only costly mistake.
func TestStatementMayWrite(t *testing.T) {
	cases := map[string]bool{
		`SELECT * FROM t`:                      false,
		`  select 1`:                           false,
		`(SELECT 1)`:                           false,
		`WITH x AS (SELECT 1) SELECT * FROM x`: false,
		`SHOW TABLES`:                          false,
		`EXPLAIN SELECT 1`:                     false,
		`INSERT INTO t VALUES (1)`:             true,
		`update t set a = 1`:                   true,
		`WITH x AS (DELETE FROM t RETURNING *) SELECT * FROM x`:          true,
		`SELECT * INTO new_t FROM t`:                                     true,
		`MERGE INTO t USING s ON (t.id = s.id) WHEN MATCHED THEN DELETE`: true,
		`EXEC proc`: true,
		``:          true,
	}
	for q, want := range cases {
		if got := StatementMayWrite("pgx", q); got != want {
			t.Errorf("StatementMayWrite(%q) = %v, want %v", q, got, want)
		}
	}
}

// The sandbox never hands out the pool, so a test that forgets to roll back
// cannot leak a write; but the call that ends the run must also be safe to
// repeat, because both a defer and an explicit path may reach it.
func TestSandboxRollbackIsIdempotent(t *testing.T) {
	sb := NewSandbox(context.Background())
	if err := sb.Rollback(); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := sb.Rollback(); err != nil {
		t.Fatalf("second: %v", err)
	}
	if !Sandboxed(WithSandbox(context.Background(), sb)) {
		t.Error("Sandboxed is false on a context carrying a sandbox")
	}
	if Sandboxed(context.Background()) {
		t.Error("Sandboxed is true on a bare context")
	}
}
