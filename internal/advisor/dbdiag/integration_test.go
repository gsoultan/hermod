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

package dbdiag

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Run with a real server, directly and through PgBouncer:
//
//	DIAG_PG_DSN='postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable' \
//	DIAG_PGB_DSN='postgres://postgres@127.0.0.1:6432/postgres?sslmode=disable&default_query_exec_mode=exec' \
//	go test -tags integration ./internal/advisor/dbdiag/
func TestRunNamesWhoHoldsALockDirectly(t *testing.T) {
	dsn := os.Getenv("DIAG_PG_DSN")
	if dsn == "" {
		t.Skip("integration: set DIAG_PG_DSN")
	}
	db := open(t, dsn)
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE IF NOT EXISTS dbdiag_t (id int)"); err != nil {
		t.Fatal(err)
	}

	// Hermod's report holds a lock in an open transaction; a DBA's statement
	// waits on it.
	holder := open(t, dsn+"&application_name=hermod-sql-steps")
	tx, err := holder.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := tx.ExecContext(t.Context(), "LOCK TABLE dbdiag_t IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	waiter := open(t, dsn+"&application_name=psql")
	wctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = waiter.ExecContext(wctx, "ALTER TABLE dbdiag_t ADD COLUMN IF NOT EXISTS x int")
	}()
	t.Cleanup(func() { cancel(); <-done })
	time.Sleep(300 * time.Millisecond)

	r, err := Run(t.Context(), db, dsn)
	if err != nil {
		t.Fatal(err)
	}
	locks := checkByID(t, r, "locks")
	if locks.Status != StatusWarn || !strings.Contains(locks.Detail, "ALTER TABLE") || !strings.Contains(locks.Detail, "Hermod (hermod-sql-steps)") {
		t.Fatalf("locks = %s %q", locks.Status, locks.Detail)
	}
	if r.Route.ThroughPooler {
		t.Error("a direct connection was reported as behind a pooler")
	}
	if !strings.HasPrefix(r.Route.ServerVersion, "1") {
		t.Errorf("server version = %q", r.Route.ServerVersion)
	}
	t.Logf("%+v", r.Checks)
}

func TestRunThroughPgBouncerReadsItsPool(t *testing.T) {
	dsn := os.Getenv("DIAG_PGB_DSN")
	if dsn == "" {
		t.Skip("integration: set DIAG_PGB_DSN")
	}
	db := open(t, dsn)
	r, err := Run(t.Context(), db, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Route.ThroughPooler {
		t.Fatalf("route = %+v, want through a pooler", r.Route)
	}
	if r.Pooler == nil || !r.Pooler.Readable || r.Pooler.PoolSize == 0 {
		t.Fatalf("pooler = %+v, want PgBouncer's figures for a stats user", r.Pooler)
	}
	t.Logf("pooler %+v; checks %+v", *r.Pooler, r.Checks)
}

func open(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// A user PgBouncer does not list in stats_users: the pool figures are not
// readable, the report says why without the credentials, and the rest of the
// diagnosis still runs. Set DIAG_PGB_PLAIN_DSN to such a user.
func TestRunThroughPgBouncerWithoutStatsAccess(t *testing.T) {
	dsn := os.Getenv("DIAG_PGB_PLAIN_DSN")
	if dsn == "" {
		t.Skip("integration: set DIAG_PGB_PLAIN_DSN to a user outside stats_users")
	}
	r, err := Run(t.Context(), open(t, dsn), dsn)
	if err != nil {
		t.Fatal(err)
	}
	c := checkByID(t, r, "pooler")
	if c.Status != StatusUnknown || !strings.Contains(c.Detail, "SQLSTATE") || !strings.Contains(c.Detail, "stats_users") {
		t.Fatalf("pooler = %s %q", c.Status, c.Detail)
	}
	if len(r.Checks) < 6 {
		t.Errorf("the rest of the diagnosis did not run: %+v", r.Checks)
	}
	t.Log(c.Detail)
}
