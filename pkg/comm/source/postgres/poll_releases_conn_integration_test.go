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

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/infra/pgxutil"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// A polling source used to hold its pooled connection for as long as the
// workflow took to accept the rows: poll handed each row to the workflow while
// the query's result was still open. Every workflow on one source database
// shares one pool, so as many slow workflows as the pool had connections
// pinned all of them, and every other workflow on that database waited for a
// connection until it timed out. With ~1000 workflows on one source node that
// is the whole platform failing with database timeouts.
//
// The pool here has one connection. The consumer reads nothing, so the
// source's buffer fills and poll is left waiting to hand over a row. Another
// caller on the same database must still get the connection.
func TestPollReleasesConnectionBeforeDeliveringRows(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}
	dsn = withOneConnPool(dsn)

	db, err := sql.Open("pgx", os.Getenv("POSTGRES_DSN"))
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	const table = "poll_releases_conn"
	const rows = 300 // well past the source's buffer, so delivery blocks
	mustExec(t, db, "DROP TABLE IF EXISTS "+table)
	mustExec(t, db, "CREATE TABLE "+table+" (id int PRIMARY KEY, v text)")
	t.Cleanup(func() { mustExec(t, db, "DROP TABLE IF EXISTS "+table) })
	mustExec(t, db, "INSERT INTO "+table+" SELECT g, 'v' || g FROM generate_series(1, $1) g", rows)

	src := NewPostgresSource(dsn, "", "", nil, false,
		"SELECT id, v FROM "+table+" WHERE id > $1 ORDER BY id", time.Hour)
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	first, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	got := []int{idOf(t, first.After())}

	// Give poll time to fill the buffer and block on the next row.
	time.Sleep(500 * time.Millisecond)

	pool, err := pgxutil.DefaultPooler.Get(ctx, dsn)
	if err != nil {
		t.Fatalf("shared pool: %v", err)
	}
	queryCtx, queryCancel := context.WithTimeout(ctx, 3*time.Second)
	defer queryCancel()
	var one int
	if err := pool.QueryRow(queryCtx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("another caller on the same database could not get a connection "+
			"while the polling source waited to deliver rows: %v", err)
	}

	for len(got) < rows {
		msg, err := src.Read(ctx)
		if err != nil {
			t.Fatalf("read %d: %v", len(got)+1, err)
		}
		got = append(got, idOf(t, msg.After()))
	}
	for i, id := range got {
		if id != i+1 {
			t.Fatalf("row %d has id %d; rows must arrive once each, in order", i, id)
		}
	}
}

// withOneConnPool caps the pool the source shares at one connection, and makes
// the DSN distinct so the test gets a pool of its own from the shared pooler.
func withOneConnPool(dsn string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "pool_max_conns=1&application_name=poll_releases_conn"
}

func idOf(t *testing.T, after []byte) int {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal(after, &rec); err != nil {
		t.Fatalf("decode row: %v", err)
	}
	id, ok := rec["id"].(float64)
	if !ok {
		t.Fatalf("row has no numeric id: %s", after)
	}
	return int(id)
}
