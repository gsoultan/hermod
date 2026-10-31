// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package service

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	_ "modernc.org/sqlite"
)

// The SQL builder's preview -- the one that fills Available Fields for an
// Execute SQL node -- ran the node's query on the database to see what came
// back. For an INSERT or UPDATE that was a real write, made while somebody was
// still typing the query. A write now runs in a transaction that is rolled back
// once its rows are read.

type dbCreator struct {
	fakeCreator
	db *sql.DB
}

func (d *dbCreator) GetDB(context.Context, string, map[string]string) (*sql.DB, error) {
	return d.db, nil
}

// executorSource is a source with its own SQL path. That path runs on the
// source's own connection and commits, so a write must never be sent there.
type executorSource struct {
	t *testing.T
}

func (e *executorSource) Read(context.Context) (hermod.Message, error) { return nil, nil }
func (e *executorSource) Ack(context.Context, hermod.Message) error    { return nil }
func (e *executorSource) Ping(context.Context) error                   { return nil }
func (e *executorSource) Close() error                                 { return nil }
func (e *executorSource) ExecuteSQL(_ context.Context, query string) ([]map[string]any, error) {
	e.t.Errorf("a write was sent to the source's own SQL path, which commits: %q", query)
	return nil, nil
}

func newDiscoveryOrdersDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "orders.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(t.Context(),
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, code TEXT, status TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	return db
}

func TestExecuteSQLPreviewOfAWriteShowsItsRowsAndWritesNothing(t *testing.T) {
	db := newDiscoveryOrdersDB(t)
	svc := NewDiscoveryService(&dbCreator{
		fakeCreator: fakeCreator{src: &executorSource{t: t}},
		db:          db,
	})

	rows, err := svc.ExecuteSQL(t.Context(),
		factory.SourceConfig{Type: "sqlite", Config: map[string]string{"path": "unused"}},
		`INSERT INTO orders (code, status) VALUES ('C-1', 'new') RETURNING id, code, status`,
		map[string]any{"code": "C-1"})
	if err != nil {
		t.Fatalf("ExecuteSQL: %v", err)
	}
	if len(rows) != 1 || rows[0]["code"] != "C-1" || rows[0]["status"] != "new" {
		t.Errorf("rows = %v, want the one RETURNING row; it is what fills Available Fields", rows)
	}

	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM orders").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("previewing the query wrote %d row(s) to the table", n)
	}
}
