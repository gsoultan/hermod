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
	"errors"
	"fmt"
	"sync"
	"time"
)

// SlowLookupPlanner reads the plan of a lookup query that ran out of time on a
// database that was answering, and says in a sentence why it is slow and what
// would fix it. The engine registry is one, for PostgreSQL; with none, or for
// a database it cannot plan, the error still carries the ping's evidence.
type SlowLookupPlanner interface {
	PlanSlowLookup(ctx context.Context, db *sql.DB, query string) (string, error)
}

// pingTimeout bounds the check that tells a slow query from a database that
// is not answering. A database that is up answers an empty query in
// milliseconds, even under load, so seconds is already a verdict.
const pingTimeout = 3 * time.Second

// planTimeout bounds reading the plan. Planning reads the catalog only.
const planTimeout = 5 * time.Second

// proofReuse is how long one check stands for its query. Lookups time out in
// bursts -- every message in flight at once -- and a database already in
// trouble should get one check, not one per message.
const proofReuse = time.Minute

// slowLookup is what Hermod found when it checked a lookup that ran out of
// time with its pool free and none of its own long queries in the way.
type slowLookup struct {
	// answered: the database answered a ping, so it was up with a connection
	// to give, and the query itself is what took the time.
	answered bool
	ping     time.Duration
	// plan is the plan's verdict, or why it could not be read; empty when
	// no plan was read.
	plan string
	err  error
}

func (s *slowLookup) Error() string {
	if !s.answered {
		return fmt.Sprintf("the database did not answer a ping within %s either: %v", pingTimeout, s.err)
	}
	return fmt.Sprintf("the database answered a ping in %s%s: %v", s.ping.Round(time.Millisecond), s.planText(), s.err)
}

func (s *slowLookup) planText() string {
	if s.plan == "" {
		return ""
	}
	return "; what Hermod found about the query: " + s.plan
}

func (s *slowLookup) Unwrap() error { return s.err }

// proofKey is one query on one pool.
type proofKey struct {
	db    *sql.DB
	query string
}

// proof is one check, shared by every lookup that timed out on the query
// while it ran or within proofReuse after.
type proof struct {
	done     chan struct{}
	at       time.Time
	answered bool
	ping     time.Duration
	plan     string
}

type proofs struct {
	mu sync.Mutex
	m  map[proofKey]*proof
}

var checked = &proofs{m: map[proofKey]*proof{}}

// get returns the check for key, running it when there is none recent. It
// waits for one already running, so a burst of timeouts shares its answer.
func (p *proofs) get(key proofKey, run func() *proof) *proof {
	p.mu.Lock()
	now := time.Now()
	if c, ok := p.m[key]; ok && (c.at.IsZero() || now.Sub(c.at) < proofReuse) {
		p.mu.Unlock()
		<-c.done
		return c
	}
	p.prune(now)
	c := &proof{done: make(chan struct{})}
	p.m[key] = c
	p.mu.Unlock()

	r := run()
	c.answered, c.ping, c.plan = r.answered, r.ping, r.plan
	p.mu.Lock()
	c.at = time.Now()
	p.mu.Unlock()
	close(c.done)
	return c
}

// prune drops finished checks that no longer stand, so the map holds only
// the queries timing out now. Called with p.mu held.
func (p *proofs) prune(now time.Time) {
	for k, c := range p.m {
		if !c.at.IsZero() && now.Sub(c.at) >= proofReuse {
			delete(p.m, k)
		}
	}
}

// prove checks a lookup that ran out of time and wraps err with what it found.
// It runs on a context of its own: the lookup's has already ended.
func (u poolUse) prove(err error) error {
	if u.pool == nil || u.query == "" {
		return err
	}
	c := checked.get(proofKey{u.pool, u.query}, u.check)
	return &slowLookup{answered: c.answered, ping: c.ping, plan: c.plan, err: err}
}

func (u poolUse) check() *proof {
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	began := time.Now()
	if err := u.pool.PingContext(ctx); err != nil {
		return &proof{}
	}
	out := &proof{answered: true, ping: time.Since(began)}
	if u.planner == nil {
		return out
	}
	pctx, pcancel := context.WithTimeout(context.Background(), planTimeout)
	defer pcancel()
	plan, err := u.planner.PlanSlowLookup(pctx, u.pool, u.query)
	switch {
	case err == nil:
		out.plan = plan
	case !errors.Is(err, ErrUnplannable):
		out.plan = "it could not be read (" + err.Error() + ")"
	}
	return out
}

// ErrUnplannable is a SlowLookupPlanner's way to say it does not plan this
// database, so the error says nothing about a plan rather than that reading
// one failed.
var ErrUnplannable = errors.New("lookup: this database's plans cannot be read")

// headline is the lookup's error once Hermod has checked: which of the two it
// was, so the operator fixes the right thing. Neither is waited out: a slow
// query would be waited on forever, and a database that is down is the honest
// limit.
func (s *slowLookup) headline(timeout time.Duration, err error) error {
	if s.answered {
		return fmt.Errorf("db_lookup: the time went on the lookup's own query: it ran past its %s limit while the database "+
			"was answering, so it was neither Hermod's pool nor the database being down, and what Hermod found about the "+
			"query says why; the step fails rather than wait on it, and its time limit is worth raising only if the query "+
			"is legitimately this slow: %w", timeout, err)
	}
	return fmt.Errorf("db_lookup: no answer from the database within %s, and it did not answer a ping either: it is down, "+
		"overloaded, or PgBouncer has no server connection to give; the step fails rather than hang: %w", timeout, err)
}
