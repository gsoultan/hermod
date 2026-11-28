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

	"github.com/gsoultan/hermod"
)

// --- "no answer from the database within 30s" did not prove where the time went ---
//
// With Hermod's pool free and none of its long queries running, a lookup that
// ran out of time stayed an error -- rightly, because the query itself may be
// what is slow -- but the error only told the operator to raise the time
// limit. Hermod now checks straight away: it pings the database, and when the
// database answers it reads the lookup query's plan. The error then says which
// it was, with the evidence: the query is slow (and why, with the fix), or
// the database did not answer at all.

// slowQueryDriver answers a ping at once and hangs every query: a database
// that is up, with a connection free, running a query that does not finish.
type slowQueryDriver struct{ queries, pings atomic.Int32 }

func (d *slowQueryDriver) Open(string) (driver.Conn, error) { return &slowQueryConn{d: d}, nil }

type slowQueryConn struct{ d *slowQueryDriver }

func (c *slowQueryConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("slowQueryConn: prepare not supported")
}
func (c *slowQueryConn) Close() error { return nil }
func (c *slowQueryConn) Begin() (driver.Tx, error) {
	return nil, errors.New("slowQueryConn: no transactions")
}
func (c *slowQueryConn) Ping(context.Context) error {
	c.d.pings.Add(1)
	return nil
}

func (c *slowQueryConn) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	c.d.queries.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}

var slowQueryDriverSeq atomic.Int32

func slowQueryRegistry(t *testing.T) (*fullFakeRegistry, *slowQueryDriver) {
	t.Helper()
	d := &slowQueryDriver{}
	name := fmt.Sprintf("hermod-lookup-slow-query-%d", slowQueryDriverSeq.Add(1))
	sql.Register(name, d)
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(5)
	reg, _ := hangingLookupRegistry(t)
	reg.db = db
	return reg, d
}

// planningRegistry reads plans the way the engine registry does, with a
// recorded verdict.
type planningRegistry struct {
	*fullFakeRegistry
	verdict string
	planned atomic.Int32
	query   atomic.Pointer[string]
}

func (p *planningRegistry) PlanSlowLookup(_ context.Context, _ *sql.DB, query string) (string, error) {
	p.planned.Add(1)
	p.query.Store(&query)
	return p.verdict, nil
}

func TestDBLookupTimeoutProvesTheQueryItselfWasSlow(t *testing.T) {
	reg, d := slowQueryRegistry(t)
	pr := &planningRegistry{fullFakeRegistry: reg,
		verdict: `The query reads all about 2000000 rows of public.delivery_order_items to find matches on id. Suggested index: CREATE INDEX ...`}

	err := runHungLookup(context.WithValue(t.Context(), hermod.RegistryKey, pr), t, tableLookupConfig(map[string]any{"timeout": "100ms"}))
	if err == nil {
		t.Fatal("a query that never answered succeeded")
	}
	if d.pings.Load() == 0 {
		t.Error("Hermod did not check whether the database was answering")
	}
	for _, want := range []string{
		"the time went on the lookup's own query",
		"the database answered a ping",
		"reads all about 2000000 rows of public.delivery_order_items",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not contain %q:\n%v", want, err)
		}
	}
	if q := pr.query.Load(); q == nil || !strings.Contains(*q, "delivery_order_items") {
		t.Errorf("the plan read was not the lookup's own query: %v", q)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the timeout is no longer in the chain: %v", err)
	}
	if errors.Is(err, hermod.ErrBusy) {
		t.Errorf("a slow query was answered as busy, which the engine would wait out forever: %v", err)
	}
}

func TestDBLookupTimeoutSaysTheDatabaseDidNotAnswerAPingEither(t *testing.T) {
	reg, _ := hangingLookupRegistry(t)
	reg.db.SetMaxOpenConns(5)
	pr := &planningRegistry{fullFakeRegistry: reg, verdict: "unused"}

	err := runHungLookup(context.WithValue(t.Context(), hermod.RegistryKey, pr), t, tableLookupConfig(map[string]any{"timeout": "100ms"}))
	if err == nil {
		t.Fatal("a query that never answered succeeded")
	}
	if !strings.Contains(err.Error(), "did not answer a ping") {
		t.Errorf("error does not say the database was not answering at all:\n%v", err)
	}
	if pr.planned.Load() != 0 {
		t.Error("Hermod tried to read a plan from a database that was not answering")
	}
}

func TestDBLookupTimeoutChecksTheDatabaseOncePerQueryNotPerMessage(t *testing.T) {
	reg, d := slowQueryRegistry(t)
	pr := &planningRegistry{fullFakeRegistry: reg, verdict: "a verdict"}
	ctx := context.WithValue(t.Context(), hermod.RegistryKey, pr)
	for range 3 {
		if err := runHungLookup(ctx, t, tableLookupConfig(map[string]any{"timeout": "50ms"})); err == nil ||
			!strings.Contains(err.Error(), "a verdict") {
			t.Fatalf("a repeated timeout lost its evidence: %v", err)
		}
	}
	if n := pr.planned.Load(); n != 1 {
		t.Errorf("the plan was read %d times for one query; a database already struggling gets one check, not one per message", n)
	}
	if n := d.pings.Load(); n != 1 {
		t.Errorf("the database was pinged %d times for one query, want 1", n)
	}
}
