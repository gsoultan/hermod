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

package registry

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
)

// --- "the query itself is slow" was a claim Hermod did not prove ---------------
//
// A lookup that ran out of time with Hermod's pool free and none of its own long
// queries in the way stays an error, because the query itself may be the slow
// part. Asked: Hermod must prove that, not assert it. These run the three real
// cases through PgBouncer and read what the error says:
//
//   - a query that is slow because no index serves it: the database answers a
//     ping, and the plan names the full scan and the index that fixes it;
//   - a query waiting on a lock another session holds: the database answers a
//     ping, and the plan says it is not the plan but a lock;
//   - a database that is down behind a PgBouncer that is up: no ping answers.
//
//	PGBOUNCER_SMALL_POOL_DSN='postgres://postgres@127.0.0.1:6433/postgres?sslmode=disable' \
//	PGBOUNCER_DOWN_DB_DSN='postgres://postgres@127.0.0.1:6433/downdb?sslmode=disable' \
//	go test -tags integration -run TestLookupTimeoutProves ./internal/engine/registry/
func TestLookupTimeoutProvesWhereTheTimeWent(t *testing.T) {
	dsn := os.Getenv("PGBOUNCER_SMALL_POOL_DSN")
	if dsn == "" {
		t.Skip("integration: set PGBOUNCER_SMALL_POOL_DSN to run")
	}
	direct, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = direct.Close() })
	for _, q := range []string{
		"DROP TABLE IF EXISTS proof_items",
		"CREATE TABLE proof_items (id BIGINT PRIMARY KEY, code TEXT NOT NULL, status TEXT)",
		"INSERT INTO proof_items SELECT g, 'C-' || g, 'open' FROM generate_series(1, 200000) g",
		"ANALYZE proof_items",
	} {
		if _, err := direct.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	r := proofRegistry(t, map[string]string{"pgb": dsn, "down": os.Getenv("PGBOUNCER_DOWN_DB_DSN")})

	t.Run("slow because no index serves it", func(t *testing.T) {
		// The condition wraps the column, so every row is hashed: a real
		// query that is slow on its own, with the pool and the database idle.
		err := runNode(t.Context(), r, "wf-proof", lookupNode("pgb",
			`SELECT status FROM proof_items WHERE md5(repeat(code, 500)) = md5(repeat({{.code}}, 500))`))
		t.Logf("error: %v", err)
		wantAll(t, err, "the time went on the lookup's own query", "the database answered a ping",
			"md5(repeat(code, 500))", "CREATE INDEX")
	})

	t.Run("waiting on a lock", func(t *testing.T) {
		tx, err := direct.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(t.Context(), "LOCK TABLE proof_items IN ACCESS EXCLUSIVE MODE"); err != nil {
			t.Fatal(err)
		}
		err = runNode(t.Context(), r, "wf-proof-lock", lookupNode("pgb",
			`SELECT status FROM proof_items WHERE id = {{.id}}`))
		t.Logf("error: %v", err)
		wantAll(t, err, "the time went on the lookup's own query", "the database answered a ping",
			"waiting on a lock, not on its plan", "public.proof_items is locked ACCESS EXCLUSIVE by session", "idle in transaction")
	})

	t.Run("database down behind PgBouncer", func(t *testing.T) {
		if os.Getenv("PGBOUNCER_DOWN_DB_DSN") == "" {
			t.Skip("set PGBOUNCER_DOWN_DB_DSN to a PgBouncer database whose server is not running")
		}
		err := runNode(t.Context(), r, "wf-proof-down", lookupNode("down",
			`SELECT status FROM proof_items WHERE id = {{.id}}`))
		t.Logf("error: %v", err)
		wantAll(t, err, "no answer from the database within", "did not answer a ping either")
	})
}

func lookupNode(source, query string) *storage.WorkflowNode {
	return &storage.WorkflowNode{ID: "proof-lookup", Type: "transformation", Config: map[string]any{
		"transType": "db_lookup", "sourceId": source, "mode": "query", "queryTemplate": query,
		"valueColumn": "status", "targetField": "status", "cacheTTL": "0s", "timeout": "500ms",
	}}
}

func proofRegistry(t *testing.T, dsns map[string]string) *Registry {
	t.Helper()
	meta, err := sql.Open("sqlite", "file:proof_"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	store := sqlstorage.NewSQLStorage(meta, "sqlite")
	if err := store.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	for id, dsn := range dsns {
		if dsn == "" {
			continue
		}
		if err := store.CreateSource(t.Context(), storage.Source{ID: id, Name: id, Type: "postgres",
			Config: map[string]string{"connection_string": dsn, "use_cdc": "false"}}); err != nil {
			t.Fatal(err)
		}
	}
	r := NewRegistry(store)
	t.Cleanup(r.Close)
	return r
}

func wantAll(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("the lookup succeeded")
	}
	for _, w := range wants {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error does not say %q", w)
		}
	}
}

