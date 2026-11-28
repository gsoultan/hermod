// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package lookup

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// --- A lookup whose query never gets an answer -------------------------------
//
// Reported as "failed to execute lookup query: context canceled" on a DB Lookup
// step reading through PgBouncer. The step had no time limit, so a query that
// waited on PgBouncer's queue, or on a connection whose peer had gone, waited as
// long as the message's context allowed. Nothing completed, the stall watchdog
// restarted the workflow after 60s, and the restart's cancellation was the error
// the operator saw -- naming neither the database nor the wait.

// hangingDriver answers no query: every one waits until its context ends.
type hangingDriver struct{ queries atomic.Int32 }

func (d *hangingDriver) Open(string) (driver.Conn, error) { return &hangingConn{d: d}, nil }

type hangingConn struct{ d *hangingDriver }

func (c *hangingConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("hangingConn: prepare not supported")
}
func (c *hangingConn) Close() error { return nil }
func (c *hangingConn) Begin() (driver.Tx, error) {
	return nil, errors.New("hangingConn: no transactions")
}

// Ping hangs too: the database is not answering at all.
func (c *hangingConn) Ping(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (c *hangingConn) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	c.d.queries.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}

var hangingDriverSeq atomic.Int32

// newHangingDB is a pool whose queries never return on their own.
func newHangingDB(t *testing.T) (*sql.DB, *hangingDriver) {
	t.Helper()
	d := &hangingDriver{}
	// sql.Register panics on a reused name.
	name := fmt.Sprintf("hermod-lookup-hanging-%d", hangingDriverSeq.Add(1))
	sql.Register(name, d)
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, d
}

func hangingLookupRegistry(t *testing.T) (*fullFakeRegistry, *hangingDriver) {
	db, d := newHangingDB(t)
	return &fullFakeRegistry{
		db: db,
		source: storage.Source{
			ID:     "src1",
			Name:   "orders",
			Type:   "postgres",
			Config: hermod.StringMap{"use_cdc": "false"},
		},
	}, d
}

// runHungLookup runs one lookup and fails the test if it is still running after
// the deadline: the bug is a step that never returns.
// hungLookupGrace is how long a lookup bounded at 100ms gets before it counts as
// hung: generous, so a slow CI machine does not fail it.
const hungLookupGrace = 5 * time.Second

func runHungLookup(ctx context.Context, t *testing.T, cfg map[string]any) error {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetData("id", 7)

	done := make(chan error, 1)
	go func() {
		_, err := (&DBLookupTransformer{}).Transform(ctx, msg, cfg)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(hungLookupGrace):
		t.Fatalf("the lookup was still waiting on the database after %s", hungLookupGrace)
		return nil
	}
}

func tableLookupConfig(extra map[string]any) map[string]any {
	cfg := map[string]any{
		"sourceId":    "src1",
		"table":       "delivery_order_items",
		"keyColumn":   "id",
		"keyField":    "id",
		"valueColumn": "status",
		"targetField": "status",
		"ttl":         "0",
	}
	for k, v := range extra {
		cfg[k] = v
	}
	return cfg
}

func TestDBLookupTimeoutEndsAQueryThatNeverAnswers(t *testing.T) {
	for name, cfg := range map[string]map[string]any{
		"table mode": tableLookupConfig(map[string]any{"timeout": "100ms"}),
		"query mode": tableLookupConfig(map[string]any{
			"timeout":       "100ms",
			"mode":          "query",
			"queryTemplate": "SELECT status FROM delivery_order_items WHERE id = {{id}}",
		}),
	} {
		t.Run(name, func(t *testing.T) {
			reg, d := hangingLookupRegistry(t)
			ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)

			err := runHungLookup(ctx, t, cfg)
			if err == nil {
				t.Fatal("a lookup that never got an answer reported success")
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("want a deadline error, got %v", err)
			}
			if !strings.Contains(err.Error(), "100ms") {
				t.Errorf("the error does not say the lookup ran out of its time: %v", err)
			}
			if d.queries.Load() == 0 {
				t.Error("the query was never sent, so this did not test the query wait")
			}
		})
	}
}

// The step has to be bounded with nothing configured: the workflows that hit
// this were saved before the setting existed. The default sits under the stall
// watchdog's 60s, so the step fails with its own error before the watchdog
// restarts the whole workflow over it.
func TestDBLookupHasATimeLimitByDefault(t *testing.T) {
	if defaultDBLookupTimeout <= 0 || defaultDBLookupTimeout >= 60*time.Second {
		t.Fatalf("defaultDBLookupTimeout = %s, want a limit under the 60s stall threshold", defaultDBLookupTimeout)
	}
	old := defaultDBLookupTimeout
	defaultDBLookupTimeout = 100 * time.Millisecond
	t.Cleanup(func() { defaultDBLookupTimeout = old })

	reg, _ := hangingLookupRegistry(t)
	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	err := runHungLookup(ctx, t, tableLookupConfig(nil))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want a deadline error from the default limit, got %v", err)
	}
}

// Batching runs the query on the batcher's own goroutine, on a background
// context, so bounding only the caller left the batch query -- and the pool
// connection under it -- waiting for good.
func TestDBLookupTimeoutBoundsTheBatchQueryToo(t *testing.T) {
	reg, d := hangingLookupRegistry(t)
	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	ctx = context.WithValue(ctx, hermod.NodeIDKey, "node_hung_batch")

	err := runHungLookup(ctx, t, tableLookupConfig(map[string]any{
		"timeout":      "100ms",
		"use_batching": true,
		"batchWait":    "1ms",
	}))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want a deadline error, got %v", err)
	}

	// The batch query itself must have been released, not just the caller.
	deadline := time.Now().Add(5 * time.Second)
	for reg.db.Stats().InUse > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the batch query still holds %d pool connection(s) after its time limit", reg.db.Stats().InUse)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if d.queries.Load() == 0 {
		t.Error("the batch query was never sent")
	}
}

func TestDBLookupRejectsATimeoutThatIsNotADuration(t *testing.T) {
	for _, bad := range []string{"30", "soon", "-1s", "0s"} {
		reg, d := hangingLookupRegistry(t)
		ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
		err := runHungLookup(ctx, t, tableLookupConfig(map[string]any{"timeout": bad}))
		if err == nil || !strings.Contains(err.Error(), "timeout") {
			t.Errorf("timeout %q: got %v, want an error naming the timeout", bad, err)
		}
		if n := d.queries.Load(); n != 0 {
			t.Errorf("timeout %q: a node with a bad timeout still sent %d query(ies)", bad, n)
		}
	}
}

// A stop is not a slow database. When the workflow's own context ends, the
// error must not claim the database ran out of time.
func TestDBLookupStopIsNotReportedAsATimeout(t *testing.T) {
	reg, _ := hangingLookupRegistry(t)
	parent, cancel := context.WithCancel(t.Context())
	ctx := context.WithValue(parent, hermod.RegistryKey, reg)
	time.AfterFunc(50*time.Millisecond, cancel)

	err := runHungLookup(ctx, t, tableLookupConfig(map[string]any{"timeout": "10s"}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want the stop's cancellation, got %v", err)
	}
	if strings.Contains(err.Error(), "within") {
		t.Errorf("a stop was reported as the database running out of time: %v", err)
	}
}
