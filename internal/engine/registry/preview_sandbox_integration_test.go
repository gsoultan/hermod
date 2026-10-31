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

package registry

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
	"github.com/gsoultan/hermod/pkg/comm/message"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// The preview sandbox against a real PostgreSQL, through a stored source and
// the registry's pooled pgx connection: the path an operator configures.
func TestRunPreviewOnPostgresSeesItsWritesAndCommitsNone(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, q := range []string{
		"DROP TABLE IF EXISTS orders",
		"CREATE TABLE orders (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, code TEXT UNIQUE, status TEXT)",
	} {
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS orders") })

	meta, err := sql.Open("sqlite", "file:pgsandbox_"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open metadata db: %v", err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	store := sqlstorage.NewSQLStorage(meta, "sqlite")
	if err := store.Init(t.Context()); err != nil {
		t.Fatalf("init store: %v", err)
	}
	if err := store.CreateSource(t.Context(), storage.Source{
		ID: "orders-db", Name: "orders", Type: "postgres",
		Config: map[string]string{"connection_string": dsn, "use_cdc": "false"},
	}); err != nil {
		t.Fatalf("create source: %v", err)
	}
	reg := NewRegistry(store)

	for i := range 2 {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"code":"C-1"}`))
		msg.SetPayload([]byte(`{"code":"C-1"}`))
		results, err := reg.TestTransformationPipeline(t.Context(), insertLookupUpdateLookup(), msg)
		msg.Release()
		if err != nil {
			t.Fatalf("preview %d: %v", i+1, err)
		}
		last := results[len(results)-1]
		if last == nil {
			t.Fatalf("preview %d: the last step emitted nothing", i+1)
		}
		assertSandboxPayload(t, last.ToMap())
		for _, r := range results {
			if r != nil {
				r.Release()
			}
		}
	}

	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM orders").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("the preview wrote %d row(s) to PostgreSQL", n)
	}
}
