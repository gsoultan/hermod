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
	"testing"
	"time"
)

// --- Hermod had to be told how many connections it really had -------------
//
// Reported behind PgBouncer, whose pool gave the hermod user 12 server
// connections while Hermod's own setting allowed 20. Hermod filled all 12 with
// long reports and its lookups queued behind them. The only fix was a person
// finding PgBouncer's pool_size and copying it into the source. The tuner now
// notices the queueing itself and learns the real share from it.

func TestLearnLowersTheLimitBelowTheConfiguredOne(t *testing.T) {
	d := newDatabase("t")
	d.SetLimit(BulkLimit(20, -1)) // 15
	d.Learn(12, -1)
	if got := d.Limit(); got != 9 {
		t.Fatalf("Limit after learning 12 connections = %d, want 9 (a quarter kept for lookups)", got)
	}
	if got := d.Learned(); got != 12 {
		t.Fatalf("Learned = %d, want 12", got)
	}
	d.Learn(0, -1)
	if got := d.Limit(); got != 15 {
		t.Fatalf("Limit after forgetting = %d, want the configured 15", got)
	}
}

func TestLearnNeverRaisesTheConfiguredLimit(t *testing.T) {
	d := newDatabase("t")
	d.SetLimit(5)
	d.Learn(40, -1)
	if got := d.Limit(); got != 5 {
		t.Fatalf("Limit = %d, want the configured 5: learning must only ever take room away", got)
	}
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestTuner(d *Database, probe Probe, clock *fakeClock) *Tuner {
	tu := NewTuner(d, probe, 20, -1)
	tu.now = clock.now
	return tu
}

func holdQueries(t *testing.T, d *Database, n int) {
	t.Helper()
	for range n {
		end, err := d.Begin(t.Context(), Query{Kind: SourcePoll, WorkflowID: "report"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(end)
	}
}

func TestTunerLearnsTheShareWhenTheCanaryQueues(t *testing.T) {
	d := newDatabase("t")
	d.SetLimit(BulkLimit(20, -1))
	holdQueries(t, d, 12)
	clock := &fakeClock{t: time.Unix(0, 0)}
	// PgBouncer has no server connection free: the canary waits.
	queued := func(context.Context) ProbeResult { clock.advance(3 * time.Second); return ProbeAnswered }
	tu := newTestTuner(d, queued, clock)

	tu.Step(t.Context())
	if got := d.Learned(); got != 12 {
		t.Fatalf("Learned = %d, want the 12 queries Hermod had running when the canary queued", got)
	}
	if got := d.Limit(); got != 9 {
		t.Fatalf("Limit = %d, want 9", got)
	}
}

func TestTunerGivesRoomBackOnceTheQueueingStops(t *testing.T) {
	d := newDatabase("t")
	d.SetLimit(BulkLimit(20, -1))
	d.Learn(12, -1)
	clock := &fakeClock{t: time.Unix(0, 0)}
	fast := func(context.Context) ProbeResult { clock.advance(time.Millisecond); return ProbeAnswered }
	tu := newTestTuner(d, fast, clock)

	tu.Step(t.Context())
	if got := d.Learned(); got != 12 {
		t.Fatalf("Learned moved to %d at once; it should give room back slowly", got)
	}
	clock.advance(tu.RecoverEvery)
	tu.Step(t.Context())
	if got := d.Learned(); got != 13 {
		t.Fatalf("Learned = %d after a healthy interval, want 13", got)
	}
	for range 20 {
		clock.advance(tu.RecoverEvery)
		tu.Step(t.Context())
	}
	if got := d.Learned(); got != 0 {
		t.Fatalf("Learned = %d, want 0 (forgotten) once it is back at the configured size", got)
	}
}

func TestTunerIgnoresACanaryThatCouldNotTell(t *testing.T) {
	d := newDatabase("t")
	holdQueries(t, d, 4)
	clock := &fakeClock{t: time.Unix(0, 0)}
	// Hermod's own pool was full, so the canary never reached PgBouncer.
	skipped := func(context.Context) ProbeResult { clock.advance(3 * time.Second); return ProbeSkipped }
	tu := newTestTuner(d, skipped, clock)
	tu.Step(t.Context())
	if got := d.Learned(); got != 0 {
		t.Fatalf("Learned = %d from a canary that never reached the database", got)
	}
}

func TestTunerLearnsFromARefusal(t *testing.T) {
	d := newDatabase("t")
	holdQueries(t, d, 7)
	clock := &fakeClock{t: time.Unix(0, 0)}
	refused := func(context.Context) ProbeResult { return ProbeRefused }
	tu := newTestTuner(d, refused, clock)
	tu.Step(t.Context())
	if got := d.Learned(); got != 7 {
		t.Fatalf("Learned = %d after the server refused a connection with 7 running, want 7", got)
	}
}

func TestTunerRunStopsWithItsContext(t *testing.T) {
	d := newDatabase("t")
	tu := NewTuner(d, func(context.Context) ProbeResult { return ProbeAnswered }, 20, -1)
	tu.Interval = time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { tu.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
}
