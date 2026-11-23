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

package lookup

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/pgxutil"
	"github.com/jackc/pgx/v5/stdlib"
)

// The DB Lookup time limit against a real PostgreSQL, reached two ways: directly
// and through PgBouncer in transaction pooling mode. The reported failure was
// "failed to execute lookup query: context canceled" on a lookup behind
// PgBouncer -- a query that never got an answer until the stall watchdog
// restarted the workflow. Each way here makes the query wait for real, the way
// it does in production, and checks the step now gives up on its own.
//
// Run with (PgBouncer with default_pool_size = 1, pool_mode = transaction):
//
//	POSTGRES_DSN='postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable' \
//	PGBOUNCER_DSN='postgres://postgres@127.0.0.1:6432/postgres?sslmode=disable&pgbouncer=true' \
//	  go test -tags=integration -run TestDBLookupTimeLimitOnPostgres ./pkg/comm/transformer/lookup/

// openLikeTheRegistry opens dsn the way the registry opens a Postgres pool
// (openSQLDB): pgx through pgxutil, which turns statement caching off for a
// DSN that declares a pooler.
func openLikeTheRegistry(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	cfg, _, err := pgxutil.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func lookupOn(t *testing.T, db *sql.DB, cfg map[string]any) (hermod.Message, error) {
	t.Helper()
	reg := &fullFakeRegistry{db: db, source: storage.Source{
		ID: "src1", Name: "orders", Type: "postgres",
		Config: hermod.StringMap{"use_cdc": "false"},
	}}
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetData("id", 7)
	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	return (&DBLookupTransformer{}).Transform(ctx, msg, cfg)
}

func TestDBLookupTimeLimitOnPostgres(t *testing.T) {
	direct := os.Getenv("POSTGRES_DSN")
	if direct == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	admin := openLikeTheRegistry(t, direct)
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS lookup_timeout_items`,
		`CREATE TABLE lookup_timeout_items (id int PRIMARY KEY, status text)`,
		`INSERT INTO lookup_timeout_items VALUES (7, 'shipped')`,
	} {
		if _, err := admin.ExecContext(t.Context(), stmt); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), `DROP TABLE IF EXISTS lookup_timeout_items`) })

	cfg := map[string]any{
		"sourceId": "src1", "table": "lookup_timeout_items",
		"keyColumn": "id", "keyField": "id", "valueColumn": "status",
		"targetField": "status", "ttl": "0", "timeout": "1s",
	}

	// wantTimedOut checks the lookup gave up on its own limit, promptly, and
	// said so -- not "context canceled".
	wantTimedOut := func(t *testing.T, db *sql.DB) {
		t.Helper()
		start := time.Now()
		_, err := lookupOn(t, db, cfg)
		took := time.Since(start)
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "within 1s") {
			t.Fatalf("want the lookup's own 1s limit, got %v", err)
		}
		if took > 5*time.Second {
			t.Errorf("the lookup took %s to give up on a 1s limit", took)
		}
	}

	paths := map[string]string{"direct": direct}
	if pb := os.Getenv("PGBOUNCER_DSN"); pb != "" {
		paths["pgbouncer"] = pb
	} else {
		t.Log("PGBOUNCER_DSN not set; checking the direct connection only")
	}

	for name, dsn := range paths {
		t.Run(name+"/answers", func(t *testing.T) {
			out, err := lookupOn(t, openLikeTheRegistry(t, dsn), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got := out.Data()["status"]; got != "shipped" {
				t.Fatalf("status = %v, want shipped", got)
			}
		})

		// A lock held by another session: the same wait with or without a
		// pooler in between. An ACCESS EXCLUSIVE lock (TRUNCATE, ALTER, LOCK
		// TABLE) blocks even a plain SELECT.
		t.Run(name+"/blocked by a lock", func(t *testing.T) {
			tx, err := admin.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.ExecContext(t.Context(), `LOCK TABLE lookup_timeout_items IN ACCESS EXCLUSIVE MODE`); err != nil {
				t.Fatal(err)
			}
			wantTimedOut(t, openLikeTheRegistry(t, dsn))
		})
	}

	// PgBouncer's own wait: every server connection in the pool is held by an
	// open transaction elsewhere, so the lookup queues in PgBouncer
	// (cl_waiting) and Postgres never sees it at all.
	if pb, ok := paths["pgbouncer"]; ok {
		t.Run("pgbouncer/pool exhausted", func(t *testing.T) {
			holder := openLikeTheRegistry(t, pb)
			tx, err := holder.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.ExecContext(t.Context(), `SELECT 1`); err != nil {
				t.Fatal(err)
			}
			wantTimedOut(t, openLikeTheRegistry(t, pb))
		})
	}
}
