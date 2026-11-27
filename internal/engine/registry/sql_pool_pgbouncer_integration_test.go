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
	"context"
	"os"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// The pool DB Lookup and Execute SQL share, opened against a real PgBouncer in
// transaction mode with max_prepared_statements = 0, and a Max connections
// value on the source -- the setup reported with ~1000 workflows on one
// database. Two things used to go wrong here:
//
//   - pool_max_conns reached the server as a startup parameter, and PgBouncer
//     refused every connection ("unsupported startup parameter").
//   - PR 436 taught the shared pgxpool to find a PgBouncer the DSN does not
//     name, but not this pool, which kept server-side prepared statements:
//     transaction pooling hands the next statement to another server
//     connection, where the statement does not exist.
//
// Run with:
//
//	PGBOUNCER_DSN='postgres://postgres@127.0.0.1:6432/postgres?sslmode=disable' \
//	go test -tags integration -run TestSQLPool_ThroughPgBouncer ./internal/engine/registry/
func TestSQLPool_ThroughPgBouncer(t *testing.T) {
	dsn := os.Getenv("PGBOUNCER_DSN")
	if dsn == "" {
		t.Skip("integration: set PGBOUNCER_DSN (transaction pooling, no pgbouncer marker) to run")
	}
	src := storage.Source{ID: "pgb", Type: "postgres", Config: map[string]string{
		"connection_string": dsn + "&pool_max_conns=40",
	}}
	r := NewRegistry(&mockStorage{})
	t.Cleanup(r.Close)

	db, err := r.GetOrOpenDB(src)
	if err != nil {
		t.Fatal(err)
	}
	if got := db.Stats().MaxOpenConnections; got != 40 {
		t.Errorf("MaxOpenConnections = %d, want the source's 40", got)
	}

	// Many parameterised statements on many connections at once, as
	// transaction pooling shuffles them across server connections.
	var wg sync.WaitGroup
	errs := make(chan error, 400)
	for i := range 400 {
		wg.Go(func() {
			var n int
			if err := db.QueryRowContext(context.Background(), "SELECT $1::int + 1", i).Scan(&n); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("query through PgBouncer failed: %v", err)
	}
}
