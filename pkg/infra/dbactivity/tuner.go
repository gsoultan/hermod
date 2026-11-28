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
	"time"
)

// ProbeResult is what one canary query found.
type ProbeResult int

const (
	// ProbeAnswered means the canary ran; how long it took is the signal.
	ProbeAnswered ProbeResult = iota
	// ProbeRefused means the server turned the connection away for want of
	// room (too many connections).
	ProbeRefused
	// ProbeSkipped means the canary could not tell, for instance because
	// Hermod's own pool was full and it never reached the database.
	ProbeSkipped
)

// Probe runs one canary query, such as SELECT 1, on the database.
type Probe func(ctx context.Context) ProbeResult

// Tuner learns how many server connections Hermod really gets on a database
// and holds its bulk queries under that, so lookups keep connections free
// without anyone configuring it.
//
// The signal is a canary: a query that costs the server nothing. When it takes
// long to answer, or is refused, it was waiting for a connection, not for the
// server -- PgBouncer had none free for this user, or the server had no slot.
// At that moment every connection Hermod's share allows is held, and the
// queries Hermod has running are that share. The tuner learns it, which caps
// bulk queries at three quarters of it, and gives the room back one connection
// per RecoverEvery while the canary stays quick, so a share that grows is
// found again.
//
// Queries queued inside PgBouncer count as running, so the first share learned
// can be too high; each further queue lowers it again, by at least the
// reserve, until the canary stops queueing. Giving room back is deliberately
// slow: a slot handed to a report that runs for hours is not returned until it
// finishes, and a lookup that finds no room meanwhile waits rather than fails.
//
// One goroutine per database runs it (Run), owned by whoever bound the
// database's first pool, and it stops with that context.
type Tuner struct {
	d     *Database
	probe Probe
	// maxConns is the configured connection limit: once the share given back
	// reaches it, the learned share is forgotten. reserve is the lookup
	// reserve (< 0: a quarter).
	maxConns, reserve int

	// Interval is how often the canary runs.
	Interval time.Duration
	// QueuedAfter is how long a canary may take before it counts as queued.
	QueuedAfter time.Duration
	// ProbeTimeout bounds one canary.
	ProbeTimeout time.Duration
	// RecoverEvery is how long the canary must stay quick before one
	// connection is given back.
	RecoverEvery time.Duration

	now        func() time.Time
	lastChange time.Time
}

// NewTuner returns a tuner for d with the default timings.
func NewTuner(d *Database, probe Probe, maxConns, reserve int) *Tuner {
	return &Tuner{
		d: d, probe: probe, maxConns: maxConns, reserve: reserve,
		Interval:     5 * time.Second,
		QueuedAfter:  time.Second,
		ProbeTimeout: 3 * time.Second,
		RecoverEvery: 10 * time.Minute,
		now:          time.Now,
	}
}

// Run runs the canary every Interval until ctx ends.
func (t *Tuner) Run(ctx context.Context) {
	tick := time.NewTicker(t.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			t.Step(ctx)
		}
	}
}

// Step runs the canary once and adjusts the learned share.
func (t *Tuner) Step(ctx context.Context) {
	start := t.now()
	pctx, cancel := context.WithTimeout(ctx, t.ProbeTimeout)
	r := t.probe(pctx)
	cancel()
	if ctx.Err() != nil || r == ProbeSkipped {
		return
	}
	now := t.now()
	if r == ProbeRefused || now.Sub(start) >= t.QueuedAfter {
		t.queued(now)
		return
	}
	t.healthy(now)
}

func (t *Tuner) queued(now time.Time) {
	t.d.MarkQueued(now)
	running, _ := t.d.Running()
	if running == 0 {
		// The wait is not Hermod's doing; holding Hermod back would not help.
		return
	}
	t.d.Learn(running, t.reserve)
	t.lastChange = now
}

func (t *Tuner) healthy(now time.Time) {
	learned := t.d.Learned()
	if learned == 0 {
		return
	}
	if t.lastChange.IsZero() {
		t.lastChange = now
		return
	}
	if now.Sub(t.lastChange) < t.RecoverEvery {
		return
	}
	t.lastChange = now
	if next := learned + 1; t.maxConns <= 0 || next < t.maxConns {
		t.d.Learn(next, t.reserve)
		return
	}
	t.d.Learn(0, t.reserve)
}
