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
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
	"github.com/gsoultan/hermod/pkg/infra/dbactivity"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	_ "github.com/gsoultan/hermod/pkg/comm/transformer/advanced"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/lookup"
)

// --- v1.27.1: lookups still timed out behind Hermod's own SQL steps ----------
//
// Reported on v1.27.1, which has the connection gate and the PgBouncer
// tuner: PgBouncer's default_pool_size was 5 with a reserve_pool_size of 50,
// the source leaves Max connections blank (a lookup pool of 20), Execute SQL steps of 20-30s run
// alongside per-message DB Lookups, and a lookup failed with
//
//	db_lookup: no answer from the database within 30s; ... 10 of Hermod's 20
//	connections to this source were busy ... Hermod's longest queries on this
//	database right now: an unnamed workflow (SQL step, running 30s), ...
//
// The lookups were never lost: each answered busy and the engine waited it
// out. But every busy attempt was recorded as a failed trace step, so the run
// showed the error on the Runs tab as though it had failed. Alongside that,
// the steps carried no workflow ("an unnamed workflow"), a blank Max
// connections left SQL steps with no ceiling, the share the tuner learned
// could rise while the database queued, and the error asked the operator to
// copy PgBouncer's pool_size into Hermod.
//
// Scaled down in time (statements of 6s, lookups limited to 3s) so it runs
// in a minute; the ratios are the reported ones: statements longer than a
// lookup's limit, more of them than PgBouncer has room for, two connections
// held by something Hermod does not run, and lookups in parallel.
//
// Run against the reported PgBouncer (pool_mode = transaction,
// default_pool_size = 5, reserve_pool_size = 50, max_client_conn = 3000), and
// against a plain pool of 12 with no reserve:
//
//	PGBOUNCER_SMALL_POOL_DSN='postgres://postgres@127.0.0.1:6433/postgres?sslmode=disable' \
//	go test -tags integration -run TestLookupsNeverFailBehindSQLStepsOnASmallPgBouncerPool ./internal/engine/registry/
func TestLookupsNeverFailBehindSQLStepsOnASmallPgBouncerPool(t *testing.T) {
	dsn := os.Getenv("PGBOUNCER_SMALL_POOL_DSN")
	if dsn == "" {
		t.Skip("integration: set PGBOUNCER_SMALL_POOL_DSN (transaction pooling, a small default_pool_size) to run")
	}
	// REPRO_REAL_TIMINGS=1 runs at the reported timings instead: 25s
	// statements, the lookups' default 30s limit and the tuner's own interval,
	// for four minutes.
	realTimings := os.Getenv("REPRO_REAL_TIMINGS") == "1"
	stmt, lookupTimeout, runFor := "SELECT pg_sleep(6)", "3s", time.Minute
	if realTimings {
		stmt, lookupTimeout, runFor = "SELECT pg_sleep(25)", "", 4*time.Minute
	} else {
		tunerInterval = 500 * time.Millisecond
		t.Cleanup(func() { tunerInterval = 0 })
	}

	direct, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = direct.Close() })
	for _, q := range []string{
		"DROP TABLE IF EXISTS repro_codes",
		"CREATE TABLE repro_codes (code TEXT PRIMARY KEY, tier TEXT)",
		"INSERT INTO repro_codes VALUES ('C-1', 'gold')",
	} {
		if _, err := direct.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	meta, err := sql.Open("sqlite", "file:repro_"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	store := sqlstorage.NewSQLStorage(meta, "sqlite")
	if err := store.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	// No pool_max_conns: the form's Max connections left at its default.
	if err := store.CreateSource(t.Context(), storage.Source{
		ID: "pgb12", Name: "athena", Type: "postgres",
		Config: map[string]string{"connection_string": dsn, "use_cdc": "false"},
	}); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(store)
	t.Cleanup(r.Close)

	eng := pkgengine.NewEngine(nil, nil, nil)
	ctx, stop := context.WithCancel(hermod.WithBusyWaiter(t.Context(), eng))
	var wg sync.WaitGroup
	t.Cleanup(func() { stop(); wg.Wait() })

	// Two server connections held by something Hermod does not run.
	for range 2 {
		wg.Go(func() {
			for ctx.Err() == nil {
				_, _ = direct.ExecContext(ctx, stmt)
			}
		})
	}

	sqlStep := &storage.WorkflowNode{ID: "report-step", Type: "transformation", Config: map[string]any{
		"transType": "execute_sql", "sourceId": "pgb12", "queryTemplate": stmt,
	}}
	lookup := &storage.WorkflowNode{ID: "totals-lookup", Type: "transformation", Config: map[string]any{
		"transType": "db_lookup", "sourceId": "pgb12", "mode": "table", "table": "repro_codes",
		"keyColumn": "code", "valueColumn": "tier", "keyField": "code", "targetField": "tier",
		"cacheTTL": "0s",
	}}
	if lookupTimeout != "" {
		lookup.Config["timeout"] = lookupTimeout
	}

	// More SQL steps than PgBouncer has connections.
	for range 16 {
		wg.Go(func() {
			for ctx.Err() == nil {
				runNode(ctx, r, "wf-report", sqlStep)
			}
		})
	}

	var ok, failed atomic.Int64
	var firstFailure atomic.Value
	lookupsUntil := time.Now().Add(runFor)
	var slowest atomic.Int64
	var lookups sync.WaitGroup
	for range 12 {
		lookups.Go(func() {
			for time.Now().Before(lookupsUntil) && ctx.Err() == nil {
				// What a message allows a lookup in all, waits included.
				mctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
				began := time.Now()
				err := runNode(mctx, r, "wf-totals", lookup)
				cancel()
				for took := int64(time.Since(began)); ; {
					cur := slowest.Load()
					if took <= cur || slowest.CompareAndSwap(cur, took) {
						break
					}
				}
				if err != nil {
					failed.Add(1)
					firstFailure.CompareAndSwap(nil, err.Error())
					continue
				}
				ok.Add(1)
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
	watch := make(chan struct{})
	go func() {
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-watch:
				return
			case <-tick.C:
				if d := dbactivity.For("postgres|127.0.0.1|6433|postgres|postgres"); d != nil {
					all, bulk := d.Running()
					t.Logf("learned %d, limit %d, running %d (%d bulk)", d.Learned(), d.Limit(), all, bulk)
				}
			}
		}
	}()
	lookups.Wait()
	close(watch)

	db, _ := r.GetOrOpenDB(storage.Source{ID: "pgb12", Type: "postgres", Config: map[string]string{"connection_string": dsn}})
	d := dbactivity.OfPool(db)
	t.Logf("lookups: %d ok, %d failed, slowest %s; learned share %d, bulk limit %d",
		ok.Load(), failed.Load(), time.Duration(slowest.Load()), d.Learned(), d.Limit())
	if n := failed.Load(); n > 0 {
		t.Errorf("%d lookups failed behind Hermod's own SQL steps; the first:\n%v", n, firstFailure.Load())
	}
	if ok.Load() == 0 {
		t.Error("no lookup succeeded")
	}
	// Hermod found it was queueing, and keeps connections free for lookups
	// below whatever share it learned. With a reserve pool the share is not a
	// fixed number -- PgBouncer adds reserve connections once a client has
	// waited -- so only that relation is asserted, not a value.
	if l, lim := d.Learned(), d.Limit(); l == 0 || lim == 0 || lim >= l {
		t.Errorf("learned share %d, bulk limit %d: want a learned share with connections kept free below it", l, lim)
	}
}

// runNode runs one node on a fresh message the way a running workflow does.
func runNode(ctx context.Context, r *Registry, workflowID string, node *storage.WorkflowNode) error {
	raw, _ := json.Marshal(map[string]any{"code": "C-1"})
	msg := message.AcquireMessage()
	msg.SetAfter(raw)
	msg.SetPayload(raw)
	defer msg.Release()
	out, _, err := r.runWorkflowNode(ctx, workflowID, node, msg)
	for _, m := range out {
		m.Release()
	}
	if err != nil && errors.Is(ctx.Err(), context.Canceled) {
		return nil
	}
	if err != nil && strings.Contains(err.Error(), "context canceled") {
		return nil
	}
	return err
}
