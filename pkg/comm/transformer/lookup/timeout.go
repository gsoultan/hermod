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
	"strings"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/dbactivity"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

// defaultDBLookupTimeout is how long one lookup may wait on its database when
// the node does not say.
//
// There used to be no limit at all. A query waiting on PgBouncer's queue for a
// server connection, on a pooled connection whose peer had gone without closing
// it, or on a lock, waited for as long as the message's context allowed -- which
// on a running workflow is until the workflow stops. Nothing completed, the
// engine's stall watchdog (pkg/engine.DefaultStallThreshold, 60s) restarted the
// workflow, and the restart's cancellation surfaced as "failed to execute lookup
// query: context canceled": an error that named neither the database nor the
// wait, on every message that was in flight.
//
// 30s sits under that threshold, so a stuck lookup fails as itself and the
// message takes the node's error path (dead letter, onError) instead of the
// whole workflow being torn down over it. A lookup is a per-message point read;
// one that legitimately runs longer can say so with the node's timeout. A
// variable so tests can shorten it.
var defaultDBLookupTimeout = 30 * time.Second

// resolveLookupTimeout is the node's time limit: its timeout setting, or the
// default when the field is empty. A value that is not a positive duration is
// an error rather than "no limit", so a typo cannot quietly bring the unbounded
// wait back.
func resolveLookupTimeout(config map[string]any) (time.Duration, error) {
	s := strings.TrimSpace(core.GetConfigString(config, "timeout"))
	if s == "" {
		return defaultDBLookupTimeout, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("db_lookup: timeout %q is not a positive duration such as %q", s, "30s")
	}
	return d, nil
}

// lookupTimeoutError says that the lookup ran out of its own time, when it did.
//
// Only then: parent is the message's context, and when that one ended first --
// a stop, a drain, a preview the editor abandoned -- the database did nothing
// wrong and the error is passed on as it is, so the traversal still recognises
// it as an interruption.
func lookupTimeoutError(parent context.Context, timeout time.Duration, err error) error {
	if err == nil || parent.Err() != nil || !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, hermod.ErrBusy) {
		// It waited for a connection, not for its query: the step's time limit
		// is not what to change, and the engine waits it out.
		return fmt.Errorf("db_lookup: no free connection to the database within %s: %w", timeout, err)
	}
	if s, ok := errors.AsType[*slowLookup](err); ok {
		return s.headline(timeout, err)
	}
	return fmt.Errorf("db_lookup: no answer from the database within %s; "+
		"raise the step's time limit if the query is legitimately slow: %w", timeout, err)
}

// poolUse is how busy the source's pool was when a lookup asked it for a
// connection.
//
// A lookup that runs out of time has waited in one of two places: in Hermod,
// queued for one of the source's pooled connections, or in the database or
// PgBouncer after it got one. Only the second shows in SHOW POOLS, so an error
// that pointed at cl_waiting alone sent operators looking in the wrong place
// whenever it was the first. The counters are read before the query, because
// once it has failed its own connection is back in the pool.
type poolUse struct {
	inUse, max int
	// activity is what Hermod is running on the source's database, so a
	// timed-out lookup can name what it waited behind. Nil when unknown.
	activity *dbactivity.Database
	// asked is when the lookup asked for its connection.
	asked time.Time
	// pool and query are what a timed-out lookup is checked against, and
	// planner reads the query's plan; see prove.
	pool    *sql.DB
	query   string
	planner SlowLookupPlanner
}

// holderMinAge is how long a query must have run to be named as one a lookup
// waited behind. Younger ones are ordinary traffic, not the cause.
const holderMinAge = 5 * time.Second

// holdersNamed is how many of the longest queries an error names.
const holdersNamed = 3

// A preview runs on its own transaction's connection, not the pool, so it gets
// no note.
func poolUseOf(ctx context.Context, db *sql.DB, query string) poolUse {
	if db == nil || sqlutil.Sandboxed(ctx) {
		return poolUse{}
	}
	st := db.Stats()
	planner, _ := ctx.Value(hermod.RegistryKey).(SlowLookupPlanner)
	return poolUse{inUse: st.InUse, max: st.MaxOpenConnections, activity: dbactivity.OfPool(db), asked: time.Now(),
		pool: db, query: query, planner: planner}
}

// track records the lookup as running on the source's database until the
// returned func is called. A lookup never waits here: the bulk slots exist to
// keep connections free for it.
func (u poolUse) track(ctx context.Context) func() {
	if u.activity == nil {
		return func() {}
	}
	wf, _ := ctx.Value(hermod.StepWorkflowKey).(string)
	if wf == "" {
		wf, _ = ctx.Value(hermod.WorkflowIDKey).(string)
	}
	node, _ := ctx.Value(hermod.NodeIDKey).(string)
	end, err := u.activity.Begin(ctx, dbactivity.Query{Kind: dbactivity.Lookup, WorkflowID: wf, NodeID: node})
	if err != nil {
		return func() {}
	}
	return end
}

// queuedForConnection reports whether the lookup's wait was for a server
// connection rather than for its own query: the tuner saw the database with
// none free while it waited, or Hermod's own long queries were holding them.
// Without either, the query itself may be what is slow, and waiting it out
// would hide that, so it stays an error.
func (u poolUse) queuedForConnection() bool {
	if u.activity == nil {
		return false
	}
	return u.activity.QueuedSince(u.asked) || len(u.activity.LongestBulk(1, holderMinAge)) > 0
}

// holdersText names Hermod's own long-running queries on the database, with what
// to change, or says nothing when there are none.
func holdersText(qs []dbactivity.Query, now time.Time) string {
	if len(qs) == 0 {
		return ""
	}
	names := make([]string, len(qs))
	for i, q := range qs {
		names[i] = describeQuery(q, now)
	}
	return "; Hermod's longest queries on this database right now: " + strings.Join(names, ", ")
}

// shareText is how many connections Hermod lets its long queries hold on the
// database, in words, so the answer says what Hermod is doing about the wait.
func (u poolUse) shareText() string {
	if lim := u.activity.Limit(); lim > 0 {
		return fmt.Sprintf("%d connections, keeping the rest free for lookups", lim)
	}
	return "what the database gives it, which it learns while it waits, keeping some free for lookups"
}

func describeQuery(q dbactivity.Query, now time.Time) string {
	who := "an unnamed workflow"
	if q.WorkflowID != "" {
		who = fmt.Sprintf("workflow %q", q.WorkflowID)
	}
	if q.NodeID != "" {
		who += fmt.Sprintf(" step %q", q.NodeID)
	}
	return fmt.Sprintf("%s (%s, running %s)", who, q.Kind, now.Sub(q.Started).Round(time.Second))
}

// longQueries are Hermod's own long-running bulk queries on the database,
// longest first, or none when the database is not tracked.
func (u poolUse) longQueries() []dbactivity.Query {
	if u.activity == nil {
		return nil
	}
	return u.activity.LongestBulk(holdersNamed, holderMinAge)
}

// explain adds where the wait was to a lookup that ran out of time; any other
// error, or a pool with no limit, is returned as it is.
func (u poolUse) explain(err error) error {
	if u.max <= 0 || !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	now := time.Now()
	qs := u.longQueries()
	if u.inUse >= u.max {
		// It never reached the database, so nothing ran: the source had no
		// room, which is busy, and the engine waits for room rather than
		// failing the message.
		return &hermod.Busy{Err: fmt.Errorf("when the lookup asked, all %d of Hermod's connections to this source were busy, "+
			"so it queued inside Hermod%s; it waits its turn and asks again: %w", u.max, holdersText(qs, now), err),
			Holders: dbactivity.Holders(qs, now)}
	}
	if u.queuedForConnection() {
		// It waited for a server connection, not for its own query, and a
		// lookup is a read: the engine waits for room and asks again instead
		// of failing the message.
		return &hermod.Busy{Err: fmt.Errorf("when the lookup asked, %d of Hermod's %d connections to this source were busy, "+
			"so the wait was for a server connection in the database or PgBouncer%s; it waits its turn and asks again, "+
			"while Hermod holds its long queries to %s: %w", u.inUse, u.max, holdersText(qs, now), u.shareText(), err),
			Holders: dbactivity.Holders(qs, now)}
	}
	// Neither Hermod's pool nor its own queries were in the way, so either the
	// query is slow or the database is not answering. Hermod checks which,
	// rather than leave the operator to guess.
	return u.prove(fmt.Errorf("when the lookup asked, %d of Hermod's %d connections to this source were busy, "+
		"so the wait was in the database or PgBouncer%s: %w", u.inUse, u.max, holdersText(qs, now), err))
}
