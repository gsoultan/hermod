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
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/dbactivity"
)

// The incident, end to end: PgBouncer gives the hermod user 12 server
// connections, Hermod is never told, and long statements keep all 12 busy.
// Every DB Lookup behind them used to time out after 30s. Hermod now learns its
// share from a canary that queues, keeps a quarter of it free, and lookups get
// through while the long statements go on.
//
// Run with a PgBouncer whose default_pool_size is 12:
//
//	PGBOUNCER12_DSN='postgres://postgres@127.0.0.1:6433/postgres?sslmode=disable' \
//	go test -tags integration -run TestSQLPool_LookupsGetThroughWhenLongQueriesFillPgBouncer ./internal/engine/registry/
func TestSQLPool_LookupsGetThroughWhenLongQueriesFillPgBouncer(t *testing.T) {
	dsn := os.Getenv("PGBOUNCER12_DSN")
	if dsn == "" {
		t.Skip("integration: set PGBOUNCER12_DSN (transaction pooling, default_pool_size = 12) to run")
	}
	tunerInterval = 200 * time.Millisecond
	t.Cleanup(func() { tunerInterval = 0 })

	r := NewRegistry(&mockStorage{})
	t.Cleanup(r.Close)
	db, err := r.GetOrOpenDB(storage.Source{ID: "pgb12", Type: "postgres", Config: map[string]string{"connection_string": dsn}})
	if err != nil {
		t.Fatal(err)
	}
	d := dbactivity.OfPool(db)
	if d == nil {
		t.Fatal("the pool is not tracked")
	}

	// Twenty workers of long statements, more than PgBouncer has room for.
	ctx, stop := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	t.Cleanup(func() { stop(); wg.Wait() })
	for range 20 {
		wg.Go(func() {
			for ctx.Err() == nil {
				end, err := d.Begin(ctx, dbactivity.Query{Kind: dbactivity.SQLStep, WorkflowID: "report"})
				if err != nil {
					return
				}
				_, _ = db.ExecContext(ctx, "SELECT pg_sleep(1.5)")
				end()
			}
		})
	}

	deadline := time.Now().Add(45 * time.Second)
	for d.Limit() == 0 || d.Limit() > 9 {
		if time.Now().After(deadline) {
			t.Fatalf("Hermod did not learn its share: learned %d, limit %d", d.Learned(), d.Limit())
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("learned share %d, bulk limit %d", d.Learned(), d.Limit())
	// Let statements started before the limit fell finish.
	time.Sleep(2 * time.Second)

	var slowest time.Duration
	for i := range 40 {
		qctx, cancel := context.WithTimeout(t.Context(), time.Second)
		start := time.Now()
		var one int
		err := db.QueryRowContext(qctx, "SELECT 1").Scan(&one)
		cancel()
		if err != nil {
			t.Fatalf("lookup %d did not get through within 1s while the long statements ran: %v (learned %d, limit %d)",
				i, err, d.Learned(), d.Limit())
		}
		slowest = max(slowest, time.Since(start))
		time.Sleep(50 * time.Millisecond)
	}
	if _, bulk := d.Running(); bulk > d.Limit() {
		t.Errorf("%d long statements running, over the limit of %d", bulk, d.Limit())
	}
	t.Logf("slowest lookup %s", slowest)
}
