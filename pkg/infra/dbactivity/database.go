// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package dbactivity knows what Hermod is running on each physical database
// in this process, and keeps short per-message queries from being starved by
// long ones.
//
// A source's poll, an Execute SQL step and a DB Lookup can all reach the same
// database through different pools and different Hermod sources. Behind
// PgBouncer they then share one set of server connections, which no Hermod
// pool can see. In the case that led here, PgBouncer gave the hermod user 12
// server connections, a dozen long report queries held all of them, and every
// DB Lookup queued until its 30s limit -- with nothing in Hermod able to say
// which workflow held the database.
//
// A Database is keyed by what the server sees (driver, host, port, database,
// user), not by Hermod source, so every pool that reaches it shares one.
package dbactivity

import (
	"context"
	"slices"
	"sync"
	"time"
)

// Kind is what a tracked query is doing, in the words an error uses.
type Kind string

const (
	// SourcePoll is a polling source reading its next batch.
	SourcePoll Kind = "source poll"
	// SQLStep is an Execute SQL step.
	SQLStep Kind = "SQL step"
	// Lookup is a DB Lookup step.
	Lookup Kind = "lookup"
)

// Bulk reports whether queries of this kind take a bulk slot. Lookups do not:
// the slots exist to keep connections free for them.
func (k Kind) Bulk() bool { return k != Lookup }

// Query is one query Hermod has running on a database.
type Query struct {
	Kind       Kind
	WorkflowID string
	NodeID     string
	Started    time.Time
}

// Database is what Hermod is running on one physical database, and the gate
// that bounds its bulk queries.
type Database struct {
	key string

	mu      sync.Mutex
	running map[uint64]Query
	nextID  uint64
	// ceiling is how many bulk queries the configuration allows at once; 0
	// means no bound.
	ceiling int
	// learned is the share of server connections Hermod has found it really
	// gets, 0 when it has found none; learnedBulk is the bulk limit that share
	// leaves after the lookup reserve. The limit in force is the lower of
	// ceiling and learnedBulk.
	learned, learnedBulk int
	// queuedAt is when the database was last seen with no connection free.
	queuedAt time.Time
	inBulk   int
	// freed is closed, and replaced, whenever a bulk slot is released or the
	// limit changes, waking every waiter to look again.
	freed chan struct{}
}

func newDatabase(key string) *Database {
	return &Database{key: key, running: map[uint64]Query{}, freed: make(chan struct{})}
}

// Key is the database this tracks.
func (d *Database) Key() string { return d.key }

// SetLimit bounds how many bulk queries may run at once; 0 or less removes the
// bound. It is how a connection budget, fixed or adaptive, drives the gate.
func (d *Database) SetLimit(n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ceiling = max(n, 0)
	d.wakeLocked()
}

// Learn records that Hermod really gets serverConns connections on this
// database, keeping reserve of them for lookups (reserve < 0: a quarter). It
// can only lower the limit SetLimit set, never raise it. 0 forgets.
func (d *Database) Learn(serverConns, reserve int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.learned = max(serverConns, 0)
	d.learnedBulk = BulkLimit(d.learned, reserve)
	d.wakeLocked()
}

// Learned is the share of server connections Hermod has learned it gets, 0
// when it has learned none.
func (d *Database) Learned() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.learned
}

// MarkQueued records that the database was just seen with no connection free.
func (d *Database) MarkQueued(at time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.queuedAt = at
}

// QueuedSince reports whether the database was seen with no connection free
// at or after t.
func (d *Database) QueuedSince(t time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.queuedAt.IsZero() && !d.queuedAt.Before(t)
}

// Limit is the bulk limit in force, 0 for none.
func (d *Database) Limit() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.limitLocked()
}

func (d *Database) limitLocked() int {
	switch {
	case d.learnedBulk == 0:
		return d.ceiling
	case d.ceiling == 0:
		return d.learnedBulk
	default:
		return min(d.ceiling, d.learnedBulk)
	}
}

func (d *Database) wakeLocked() {
	close(d.freed)
	d.freed = make(chan struct{})
}

// Begin records a query and, for a bulk kind, first waits for a bulk slot. The
// returned end must be called once the query's result has been read; it is
// safe to call more than once. Waiting ends with ctx, returning its error.
func (d *Database) Begin(ctx context.Context, q Query) (end func(), err error) {
	bulk := q.Kind.Bulk()
	if bulk {
		if err := d.acquire(ctx); err != nil {
			return nil, err
		}
	}
	if q.Started.IsZero() {
		q.Started = time.Now()
	}
	d.mu.Lock()
	d.nextID++
	id := d.nextID
	d.running[id] = q
	d.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			delete(d.running, id)
			if bulk {
				d.inBulk--
				d.wakeLocked()
			}
		})
	}, nil
}

func (d *Database) acquire(ctx context.Context) error {
	for {
		d.mu.Lock()
		if limit := d.limitLocked(); limit == 0 || d.inBulk < limit {
			d.inBulk++
			d.mu.Unlock()
			return nil
		}
		wait := d.freed
		d.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Longest is up to n of the queries running for at least minAge, longest
// first.
func (d *Database) Longest(n int, minAge time.Duration) []Query {
	return d.longest(n, minAge, func(Query) bool { return true })
}

// LongestBulk is Longest without the lookups: the queries a lookup can be
// waiting behind.
func (d *Database) LongestBulk(n int, minAge time.Duration) []Query {
	return d.longest(n, minAge, func(q Query) bool { return q.Kind.Bulk() })
}

func (d *Database) longest(n int, minAge time.Duration, keep func(Query) bool) []Query {
	now := time.Now()
	d.mu.Lock()
	out := make([]Query, 0, len(d.running))
	for _, q := range d.running {
		if keep(q) && now.Sub(q.Started) >= minAge {
			out = append(out, q)
		}
	}
	d.mu.Unlock()
	slices.SortFunc(out, func(a, b Query) int { return a.Started.Compare(b.Started) })
	return out[:min(n, len(out))]
}

// Running is how many queries are running, and how many of them are bulk.
func (d *Database) Running() (all, bulk int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.running), d.inBulk
}

// Snapshot is one reading of a Database, taken under one lock so its numbers
// agree with each other.
type Snapshot struct {
	// Ceiling is the bulk limit the configuration allows, 0 for none.
	Ceiling int
	// Learned is the share of server connections Hermod found it gets, 0
	// when it has found none.
	Learned int
	// Limit is the bulk limit in force: the lower of Ceiling and what
	// Learned leaves after the lookup reserve.
	Limit int
	// Running is every query Hermod has running here, InBulk the bulk ones.
	Running, InBulk int
	// QueuedAt is when the database was last seen with no connection free.
	QueuedAt time.Time
	// Queries are the running queries, oldest first.
	Queries []Query
}

// Snapshot reads the database's state.
func (d *Database) Snapshot() Snapshot {
	d.mu.Lock()
	s := Snapshot{
		Ceiling: d.ceiling, Learned: d.learned, Limit: d.limitLocked(),
		Running: len(d.running), InBulk: d.inBulk, QueuedAt: d.queuedAt,
		Queries: make([]Query, 0, len(d.running)),
	}
	for _, q := range d.running {
		s.Queries = append(s.Queries, q)
	}
	d.mu.Unlock()
	slices.SortFunc(s.Queries, func(a, b Query) int { return a.Started.Compare(b.Started) })
	return s
}
