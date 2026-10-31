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
// +build integration

package sqlutil

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// ---------------------------------------------------------------------------
// The sandbox on PostgreSQL.
//
// PostgreSQL is where the sandbox has the most to get right: one failed
// statement aborts the whole transaction, so without the per-statement
// savepoint, a single node's duplicate key would fail every node after it in
// the same preview with "current transaction is aborted".
// ---------------------------------------------------------------------------

func newPostgresSandboxDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	table := "sandbox_" + strings.ToLower(t.Name())
	for _, q := range []string{
		"DROP TABLE IF EXISTS " + table,
		"CREATE TABLE " + table + " (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, code TEXT UNIQUE, status TEXT)",
	} {
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table) })
	return db, table
}

func pgCount(t *testing.T, q Querier, table, where string) int {
	t.Helper()
	rows, err := q.QueryContext(t.Context(), "SELECT count(*) FROM "+table+" WHERE "+where)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var n int
	if !rows.Next() {
		t.Fatalf("count: no row: %v", rows.Err())
	}
	if err := rows.Scan(&n); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return n
}

func TestPostgresSandboxSurvivesAFailedStatementAndRollsBack(t *testing.T) {
	db, table := newPostgresSandboxDB(t)
	sb := NewSandbox(t.Context())
	ctx := WithSandbox(t.Context(), sb)

	q, err := Use(ctx, db, "postgres")
	if err != nil {
		t.Fatalf("Use: %v", err)
	}
	rows, err := q.QueryContext(ctx, "INSERT INTO "+table+" (code, status) VALUES ($1, 'new') RETURNING id", "C-1")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if !rows.Next() {
		t.Fatalf("RETURNING gave no row: %v", rows.Err())
	}
	_ = rows.Close()

	// A node that fails: the duplicate key aborts the transaction.
	if _, err := q.ExecContext(ctx, "INSERT INTO "+table+" (code) VALUES ($1)", "C-1"); err == nil {
		t.Fatal("the duplicate insert succeeded")
	}

	// The next node must still work, and still see the first node's row.
	if _, err := q.ExecContext(ctx, "UPDATE "+table+" SET status = 'paid' WHERE code = $1", "C-1"); err != nil {
		t.Fatalf("the statement after a failed one could not run: %v", err)
	}
	if n := pgCount(t, q, table, "code = 'C-1' AND status = 'paid'"); n != 1 {
		t.Errorf("the run sees its updated row %d times, want 1", n)
	}

	// A node's own transaction (SCD) is a savepoint; its commit stays inside.
	tx, err := q.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+" (code) VALUES ('nested')"); err != nil {
		t.Fatalf("nested insert: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("nested commit: %v", err)
	}
	if n := pgCount(t, q, table, "code = 'nested'"); n != 1 {
		t.Errorf("the run does not see the nested commit: %d", n)
	}

	if err := sb.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if n := pgCount(t, db, table, "true"); n != 0 {
		t.Errorf("the preview left %d row(s) in the table", n)
	}
}

// A preview whose request is cancelled mid-run must not leave a transaction
// or its locks behind, and must not fail the cleanup that follows.
func TestPostgresSandboxRollbackAfterTheRequestIsGone(t *testing.T) {
	db, table := newPostgresSandboxDB(t)
	reqCtx, cancel := context.WithCancel(t.Context())
	sb := NewSandbox(reqCtx)
	ctx := WithSandbox(reqCtx, sb)

	q, err := Use(ctx, db, "pgx")
	if err != nil {
		t.Fatalf("Use: %v", err)
	}
	if _, err := q.ExecContext(ctx, "INSERT INTO "+table+" (code) VALUES ('gone')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	cancel()
	if err := sb.Rollback(); err != nil {
		t.Fatalf("Rollback after cancel: %v", err)
	}
	if n := pgCount(t, db, table, "true"); n != 0 {
		t.Errorf("a cancelled preview left %d row(s)", n)
	}
}
