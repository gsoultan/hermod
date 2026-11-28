// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package dbactivity

import (
	"context"
	"errors"
	"testing"
	"time"
)

// --- Long queries held every server connection; lookups queued behind them ---
//
// PgBouncer gave the hermod user 12 server connections and a dozen long report
// queries held them all, so every DB Lookup waited its full 30s. With a
// database's connection limit known, bulk queries now take a slot first and
// a quarter of the connections stay free for lookups.

func TestBulkLimitKeepsAQuarterForLookups(t *testing.T) {
	for _, tc := range []struct{ maxConns, reserve, want int }{
		{12, -1, 9},
		{20, -1, 15},
		{2, -1, 1},
		{1, -1, 1},
		{12, 6, 6},
		{12, 0, 12},
		{12, 40, 1}, // a reserve cannot stop every bulk query
		{0, -1, 0},  // limit unknown: no gate
	} {
		if got := BulkLimit(tc.maxConns, tc.reserve); got != tc.want {
			t.Errorf("BulkLimit(%d, %d) = %d, want %d", tc.maxConns, tc.reserve, got, tc.want)
		}
	}
}

func TestBulkQueriesWaitForASlotAndLookupsDoNot(t *testing.T) {
	d := newDatabase("t")
	d.SetLimit(2)

	end1, err := d.Begin(t.Context(), Query{Kind: SourcePoll, WorkflowID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	end2, err := d.Begin(t.Context(), Query{Kind: SQLStep, WorkflowID: "b"})
	if err != nil {
		t.Fatal(err)
	}

	// A third bulk query waits.
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := d.Begin(ctx, Query{Kind: SourcePoll, WorkflowID: "c"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a bulk query over the limit got %v, want it to wait until its context ended", err)
	}

	// A lookup does not.
	endL, err := d.Begin(ctx, Query{Kind: Lookup, WorkflowID: "l"})
	if err != nil {
		t.Fatalf("a lookup waited on the bulk limit: %v", err)
	}
	endL()

	// Releasing one lets the next in.
	got := make(chan error, 1)
	go func() {
		end, err := d.Begin(t.Context(), Query{Kind: SourcePoll, WorkflowID: "c"})
		if err == nil {
			end()
		}
		got <- err
	}()
	end1()
	end1() // twice is safe
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a released slot did not reach the waiting query")
	}
	end2()
	if all, bulk := d.Running(); all != 0 || bulk != 0 {
		t.Errorf("Running() = %d, %d after every query ended", all, bulk)
	}
}

func TestRaisingTheLimitWakesWaiters(t *testing.T) {
	d := newDatabase("t")
	d.SetLimit(1)
	end, _ := d.Begin(t.Context(), Query{Kind: SourcePoll})
	defer end()
	got := make(chan error, 1)
	go func() {
		e, err := d.Begin(t.Context(), Query{Kind: SourcePoll})
		if err == nil {
			e()
		}
		got <- err
	}()
	time.Sleep(20 * time.Millisecond)
	d.SetLimit(0)
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("removing the limit left a query waiting")
	}
}

func TestLongestIsOldestFirstAndSkipsYoungQueries(t *testing.T) {
	d := newDatabase("t")
	now := time.Now()
	for _, q := range []Query{
		{Kind: SourcePoll, WorkflowID: "mid", Started: now.Add(-10 * time.Minute)},
		{Kind: SQLStep, WorkflowID: "old", Started: now.Add(-2 * time.Hour)},
		{Kind: Lookup, WorkflowID: "new", Started: now},
	} {
		end, _ := d.Begin(t.Context(), q)
		defer end()
	}
	got := d.Longest(5, time.Second)
	if len(got) != 2 || got[0].WorkflowID != "old" || got[1].WorkflowID != "mid" {
		t.Fatalf("Longest = %+v, want old then mid", got)
	}
	if got := d.Longest(1, 0); len(got) != 1 || got[0].WorkflowID != "old" {
		t.Fatalf("Longest(1) = %+v", got)
	}
}

func TestForSharesOneDatabasePerKeyAndPoolsFindIt(t *testing.T) {
	k := Key("pgx", "DB.example", "6432", "athena", "hermod")
	if For(k) != For(Key("pgx", "db.example", "6432", "athena", "hermod")) {
		t.Fatal("two pools on the same database got different trackers")
	}
	pool := new(int)
	Bind(pool, For(k))
	if OfPool(pool) != For(k) {
		t.Fatal("OfPool did not find the bound database")
	}
	Unbind(pool)
	if OfPool(pool) != nil {
		t.Fatal("an unbound pool still resolved")
	}
}

func TestLongestBulkLeavesOutLookups(t *testing.T) {
	d := newDatabase("t")
	now := time.Now()
	for _, q := range []Query{
		{Kind: Lookup, WorkflowID: "lookup", Started: now.Add(-time.Hour)},
		{Kind: SourcePoll, WorkflowID: "poll", Started: now.Add(-time.Minute)},
	} {
		end, _ := d.Begin(t.Context(), q)
		defer end()
	}
	got := d.LongestBulk(5, 0)
	if len(got) != 1 || got[0].WorkflowID != "poll" {
		t.Fatalf("LongestBulk = %+v, want only the poll: a lookup is never what another lookup waits behind", got)
	}
}
