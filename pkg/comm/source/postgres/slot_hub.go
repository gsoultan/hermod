// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package postgres

import (
	"context"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	sourcebuf "github.com/gsoultan/hermod/pkg/comm/source"
	"github.com/jackc/pglogrepl"
)

// A replication slot admits one walsender, and every slot decodes the whole
// WAL again, so workflows that name the same slot on the same database share
// one stream: a slotHub. It holds the one replication connection and hands
// each change to every workflow whose tables it is on.
//
// Workflows on a shared slot share its position. The slot is confirmed past a
// change only once every workflow it went to has acknowledged it, and a
// workflow that stops does not catch up on what the others read while it was
// stopped. A workflow that needs its own position names its own slot.

// hubStream is what a slotHub reads from: in production the hub's own
// PostgresSource, in tests a fake.
type hubStream interface {
	Read(ctx context.Context) (hermod.Message, error)
	Ack(ctx context.Context, msg hermod.Message) error
	Close() error
	setTables(ctx context.Context, tables []string) error
	// confirmed is the position the stream has confirmed to the slot.
	confirmed() pglogrepl.LSN
	// setHold stops the stream confirming the slot past lsn, or with ok
	// false lets it go on.
	setHold(lsn pglogrepl.LSN, ok bool)
	// rewind restarts the stream from the slot's confirmed position, and
	// confirms nothing past from until it has.
	rewind(from pglogrepl.LSN)
}

// hubMember names a workflow on a shared slot, so the hub knows it again
// when it comes back. Empty for a source no workflow named.
type hubMember string

// sharedSlotHoldLimit is how long the slot is held for a workflow that
// stopped. The server keeps WAL from the held position for that long, so a
// workflow that is never started again, or deleted, costs disk only up to
// this; then it resumes where the stream is.
const sharedSlotHoldLimit = 24 * time.Hour

// absence is a stopped workflow the slot is held for.
type absence struct {
	// safe is the commit position up to which it acknowledged every
	// transaction it was handed: replaying it more would be duplicates.
	safe pglogrepl.LSN
	// freeze is where the slot was confirmed when it stopped, and must stay
	// until it is back, so the slot can replay what it missed.
	freeze pglogrepl.LSN
	timer  *time.Timer
}

// hubReadErrorPause keeps a stream that keeps failing from spinning the
// hub's reader. The stream reconnects on its own; this only paces the retry.
const hubReadErrorPause = 200 * time.Millisecond

// slotHubs holds the hubs of this process, by database and slot.
var slotHubs = &hubRegistry{hubs: map[string]*slotHub{}}

type hubRegistry struct {
	mu   sync.Mutex
	hubs map[string]*slotHub
}

func hubKey(connString, slot string) string {
	return connString + "\x00" + slot
}

// join adds a workflow to the hub for its slot, starting the hub if it is
// the first. newStream builds the hub's stream, logging to logs; it runs
// under the registry lock and must not do I/O.
//
// The registry lock is held through the join, which widens the publication
// when the workflow brings new tables: that serialises joins, but keeps a hub
// from closing under a workflow that is joining it.
func (r *hubRegistry) join(ctx context.Context, key, publication string, tables []string, logger hermod.Logger,
	newStream func(logs hermod.Logger) hubStream, member hubMember) (*hubSubscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.hubs[key]
	if h == nil {
		h = newSlotHub(key, publication, tables, newStream)
		r.hubs[key] = h
	}
	sub, err := h.join(ctx, publication, tables, logger, member)
	if err != nil && h.empty() {
		delete(r.hubs, key)
		h.close()
	}
	return sub, err
}

// streamName is the application_name the hub for key streams under, or ""
// when this process has no hub for it. A slot held under that name is this
// worker's own stream, which a workflow joins rather than competes with.
func (r *hubRegistry) streamName(key string) string {
	r.mu.Lock()
	h := r.hubs[key]
	r.mu.Unlock()
	if h == nil {
		return ""
	}
	if stream, ok := h.stream.(*PostgresSource); ok {
		return stream.appName
	}
	return ""
}

// leave removes a workflow, and closes its hub when it was the last one.
func (r *hubRegistry) leave(sub *hubSubscription) {
	r.mu.Lock()
	h := sub.hub
	last := h.leave(sub)
	if last && r.hubs[h.key] == h {
		delete(r.hubs, h.key)
	}
	r.mu.Unlock()
	if last {
		h.close()
	}
}

type slotHub struct {
	key         string
	publication string
	stream      hubStream
	logs        *fanLogger
	cancel      context.CancelFunc
	done        chan struct{}

	mu   sync.Mutex
	subs map[*hubSubscription]struct{}
	// pending holds, by WAL position, each change handed to workflows that
	// some of them have not acknowledged yet. The stream is acknowledged for a
	// change once all of them have.
	pending map[string][]*fanout
	// tables is the union the publication covers; nil once any workflow
	// follows every table.
	tables map[string]struct{}
	all    bool
	// absent holds the workflows that stopped while others went on. See
	// absence.
	absent map[hubMember]*absence
	// complete is the newest commit whose changes have all been dispatched.
	complete  pglogrepl.LSN
	holdLimit time.Duration
}

// fanout is one change from the stream and how many acknowledgements it
// still waits for.
type fanout struct {
	msg  hermod.Message
	left int
}

// newSlotHub starts a hub whose stream covers tables, the first workflow's.
func newSlotHub(key, publication string, tables []string, newStream func(logs hermod.Logger) hubStream) *slotHub {
	ctx, cancel := context.WithCancel(context.Background())
	logs := &fanLogger{loggers: map[*hubSubscription]hermod.Logger{}}
	h := &slotHub{
		key:         key,
		publication: publication,
		stream:      newStream(logs),
		logs:        logs,
		cancel:      cancel,
		done:        make(chan struct{}),
		subs:        map[*hubSubscription]struct{}{},
		pending:     map[string][]*fanout{},
		tables:      map[string]struct{}{},
		absent:      map[hubMember]*absence{},
		holdLimit:   sharedSlotHoldLimit,
	}
	h.widenLocked(tables)
	go h.run(ctx)
	return h
}

// join adds a workflow and widens the publication to its tables.
func (h *slotHub) join(ctx context.Context, publication string, tables []string, logger hermod.Logger, member hubMember) (*hubSubscription, error) {
	if publication != h.publication {
		return nil, fmt.Errorf("replication slot is already streaming publication %q in this worker, not %q; "+
			"workflows on one slot must use the same publication, or name a slot of their own", h.publication, publication)
	}
	sub := &hubSubscription{
		hub:         h,
		match:       newTableMatcher(tables),
		ch:          make(chan hermod.Message, sourcebuf.DefaultSourceBuffer),
		errs:        make(chan error, 1),
		done:        make(chan struct{}),
		outstanding: map[string]int{},
		member:      member,
	}
	h.mu.Lock()
	h.returnLocked(sub)
	wasAll := h.all
	added := h.widenLocked(tables)
	widened := h.all != wasAll || len(added) > 0
	union := h.unionLocked()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()
	if widened {
		if err := h.stream.setTables(ctx, union); err != nil {
			h.mu.Lock()
			h.all = wasAll
			for _, t := range added {
				delete(h.tables, t)
			}
			h.mu.Unlock()
			h.leave(sub)
			return nil, fmt.Errorf("widening publication %q for a workflow joining its slot: %w", publication, err)
		}
	}
	if logger != nil {
		h.logs.add(sub, logger)
	}
	return sub, nil
}

// returnLocked picks up where a returning workflow left off: the stream
// replays from the slot, held where it stopped, and every workflow skips
// what it already acknowledged, so only this one gets what it missed.
//
// The rewind comes before the hold is lifted, so nothing past the held
// position is confirmed in between, and the workflow is on the hub in the
// same critical section, so no replayed change is dispatched without it.
func (h *slotHub) returnLocked(sub *hubSubscription) {
	away, ok := h.absent[sub.member]
	if sub.member == "" || !ok {
		return
	}
	away.timer.Stop()
	delete(h.absent, sub.member)
	sub.skip = away.safe
	h.stream.rewind(away.freeze)
	h.applyHoldLocked()
}

// awayLocked holds the slot for a workflow that stopped while others go on.
func (h *slotHub) awayLocked(sub *hubSubscription) {
	if sub.member == "" || len(h.subs) == 0 {
		return
	}
	for other := range h.subs {
		if other.member == sub.member {
			return // the same workflow is running again already
		}
	}
	away := &absence{safe: max(sub.skip, sub.doneLocked()), freeze: h.stream.confirmed()}
	away.timer = time.AfterFunc(h.holdLimit, func() { h.expire(sub.member, away) })
	h.absent[sub.member] = away
	h.applyHoldLocked()
}

// expire lets the slot go on past a workflow that stayed away too long.
func (h *slotHub) expire(member hubMember, away *absence) {
	h.mu.Lock()
	if h.absent[member] != away {
		h.mu.Unlock()
		return
	}
	delete(h.absent, member)
	h.applyHoldLocked()
	h.mu.Unlock()
	h.logs.Warn("A workflow on a shared replication slot stayed stopped past the hold limit; "+
		"the slot is no longer held for it, and when it starts again it misses the changes made meanwhile",
		"workflow", string(member), "hold_limit", h.holdLimit.String())
}

// applyHoldLocked holds the stream at the earliest place a stopped workflow
// needs, or releases it.
func (h *slotHub) applyHoldLocked() {
	var hold pglogrepl.LSN
	held := false
	for _, away := range h.absent {
		if !held || away.freeze < hold {
			hold, held = away.freeze, true
		}
	}
	h.stream.setHold(hold, held)
}

// widenLocked adds tables to the union and returns the ones it added. An
// empty list means every table.
func (h *slotHub) widenLocked(tables []string) []string {
	if h.all {
		return nil
	}
	if len(tables) == 0 {
		h.all = true
		return nil
	}
	var added []string
	for _, t := range tables {
		t = strings.TrimSpace(t)
		if _, ok := h.tables[t]; !ok {
			h.tables[t] = struct{}{}
			added = append(added, t)
		}
	}
	return added
}

// unionLocked is the table list the publication covers, sorted; nil is every
// table.
func (h *slotHub) unionLocked() []string {
	if h.all {
		return nil
	}
	return slices.Sorted(maps.Keys(h.tables))
}

// empty reports whether no workflow is on the hub.
func (h *slotHub) empty() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs) == 0
}

// leave removes a workflow. Changes it was handed and did not acknowledge are
// acknowledged for it while other workflows remain: it is gone, and holding the
// slot back for it would hold back every other workflow and the WAL the server
// keeps. The last workflow's are not, so the slot replays them to the next
// source. It reports whether the hub is now empty.
func (h *slotHub) leave(sub *hubSubscription) bool {
	h.mu.Lock()
	if sub.left {
		empty := len(h.subs) == 0
		h.mu.Unlock()
		return empty
	}
	sub.left = true
	close(sub.done)
	delete(h.subs, sub)
	h.logs.remove(sub)
	// Before its acknowledgements are settled: settling lets the stream
	// confirm past changes this workflow never processed.
	h.awayLocked(sub)
	empty := len(h.subs) == 0
	var settled []hermod.Message
	for lsn, n := range sub.outstanding {
		if empty {
			// The stream closes next and confirms what was acknowledged:
			// settling here would confirm changes nobody processed, and the
			// next source on the slot would never get them.
			break
		}
		for range n {
			if msg := h.settleLocked(lsn); msg != nil {
				settled = append(settled, msg)
			}
		}
	}
	clear(sub.outstanding)
	h.mu.Unlock()
	// Changes handed over and never read are the workflow's no longer.
	for {
		select {
		case msg := <-sub.ch:
			msg.Release()
			continue
		default:
		}
		break
	}
	if err := h.ackStream(context.Background(), settled...); err != nil {
		h.logs.Warn("Acknowledging changes for a workflow leaving a shared replication slot failed", "error", err)
	}
	return empty
}

// settleLocked counts one acknowledgement at lsn, and returns the change once
// it has all of its acknowledgements.
func (h *slotHub) settleLocked(lsn string) hermod.Message {
	queue := h.pending[lsn]
	if len(queue) == 0 {
		return nil
	}
	f := queue[0]
	f.left--
	if f.left > 0 {
		return nil
	}
	if len(queue) == 1 {
		delete(h.pending, lsn)
	} else {
		h.pending[lsn] = queue[1:]
	}
	return f.msg
}

func (h *slotHub) ackStream(ctx context.Context, msgs ...hermod.Message) error {
	var first error
	for _, msg := range msgs {
		if err := h.stream.Ack(ctx, msg); err != nil && first == nil {
			first = err
		}
		msg.Release()
	}
	return first
}

// close stops the reader and the stream. The stream's Close drops a slot
// that is not persistent, which is right only once no workflow is left on it.
func (h *slotHub) close() {
	h.mu.Lock()
	for member, away := range h.absent {
		away.timer.Stop()
		delete(h.absent, member)
	}
	h.mu.Unlock()
	h.cancel()
	_ = h.stream.Close()
	<-h.done
	h.mu.Lock()
	var stale []hermod.Message
	for lsn, queue := range h.pending {
		for _, f := range queue {
			stale = append(stale, f.msg)
		}
		delete(h.pending, lsn)
	}
	h.mu.Unlock()
	for _, msg := range stale {
		msg.Release()
	}
}

// run is the hub's one reader: it owns the stream's Read.
func (h *slotHub) run(ctx context.Context) {
	defer close(h.done)
	for {
		msg, err := h.stream.Read(ctx)
		if ctx.Err() != nil {
			if msg != nil {
				msg.Release()
			}
			return
		}
		if err != nil {
			h.broadcast(err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(hubReadErrorPause):
			}
			continue
		}
		if msg != nil {
			h.route(ctx, msg)
		}
	}
}

// hubTxnDoneKey marks the stream's note that a transaction is complete: every
// change of the one committed at its value has been handed to the hub.
const hubTxnDoneKey = "hub_txn_done"

// route takes a completion note, or dispatches a change.
func (h *slotHub) route(ctx context.Context, msg hermod.Message) {
	raw, ok := msg.Metadata()[hubTxnDoneKey]
	if !ok {
		h.dispatch(ctx, msg)
		return
	}
	msg.Release()
	commit, err := pglogrepl.ParseLSN(raw)
	if err != nil {
		return
	}
	h.mu.Lock()
	h.complete = max(h.complete, commit)
	h.mu.Unlock()
}

// broadcast tells every workflow the stream failed, without waiting on any.
func (h *slotHub) broadcast(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		select {
		case sub.errs <- err:
		default:
		}
	}
}

// dispatch hands a copy of msg to every workflow whose tables it is on. A
// change no workflow follows is acknowledged at once, or it would hold the
// slot back forever.
func (h *slotHub) dispatch(ctx context.Context, msg hermod.Message) {
	lsn := msg.Metadata()["lsn"]
	h.mu.Lock()
	targets := h.targetsLocked(msg, lsn)
	unowned := len(h.subs) == 0
	h.mu.Unlock()

	if len(targets) == 0 {
		// With no workflow on the hub it is closing, and the change belongs
		// to the next source on the slot: acknowledging it would confirm it away.
		if lsn != "" && !unowned {
			_ = h.stream.Ack(ctx, msg)
		}
		msg.Release()
		return
	}
	for _, sub := range targets {
		out := msg.Clone()
		select {
		case sub.ch <- out:
		case <-sub.done:
			// It left after it was counted; leave acknowledged for it.
			out.Release()
		case <-ctx.Done():
			out.Release()
			return
		}
	}
	if lsn == "" {
		msg.Release()
	}
}

// targetsLocked picks the workflows msg goes to and counts it as owed by each.
func (h *slotHub) targetsLocked(msg hermod.Message, lsn string) []*hubSubscription {
	commit := commitOf(msg)
	row, _ := pglogrepl.ParseLSN(lsn)
	var targets []*hubSubscription
	for sub := range h.subs {
		if sub.match.matches(msg.Schema(), msg.Table()) && !sub.hasLocked(commit, row) {
			targets = append(targets, sub)
			sub.noteHandedLocked(commit, row)
		}
	}
	if lsn == "" || len(targets) == 0 {
		return targets
	}
	h.pending[lsn] = append(h.pending[lsn], &fanout{msg: msg, left: len(targets)})
	for _, sub := range targets {
		sub.outstanding[lsn]++
		if commit != 0 {
			sub.window.emit(commit)
		}
	}
	return targets
}

// hubSubscription is one workflow on a shared slot.
type hubSubscription struct {
	hub   *slotHub
	match tableMatcher
	ch    chan hermod.Message
	errs  chan error
	done  chan struct{}

	member hubMember

	// Guarded by hub.mu.
	left        bool
	outstanding map[string]int
	// window orders this workflow's acknowledgements by transaction commit,
	// and skip is where it left off when it last stopped: together they say
	// which transactions it has acknowledged in full, which a replay skips.
	window ackWindow
	skip   pglogrepl.LSN
	// handedCommit and handedRow are the newest change it was handed.
	handedCommit pglogrepl.LSN
	handedRow    pglogrepl.LSN
}

// hasLocked reports whether this workflow has the change at row, in the
// transaction committed at commit, already: it acknowledged that transaction
// in full, or it is the newest one and the change is one it was handed. A
// change with no commit position is never skipped.
func (s *hubSubscription) hasLocked(commit, row pglogrepl.LSN) bool {
	if commit == 0 {
		return false
	}
	if commit <= max(s.skip, s.doneLocked()) {
		return true
	}
	return commit == s.handedCommit && row != 0 && row <= s.handedRow
}

// noteHandedLocked records the newest change this workflow was handed. A
// transaction's changes arrive in order, so a replay of the newest one is
// skipped up to here and delivered from there on.
func (s *hubSubscription) noteHandedLocked(commit, row pglogrepl.LSN) {
	if commit == 0 {
		return
	}
	if commit > s.handedCommit || (commit == s.handedCommit && row > s.handedRow) {
		s.handedCommit, s.handedRow = commit, row
	}
}

// doneLocked is the last commit up to which this workflow acknowledged every
// transaction in full. The window's safe point is not quite that: acknowledging
// the rows of the newest transaction it was handed makes it safe while the rest
// of that transaction may not have arrived yet. A transaction is known whole
// once the stream says it is complete (markCommitted), or a later one has
// begun; until then the newest stays open.
func (s *hubSubscription) doneLocked() pglogrepl.LSN {
	if safe := s.window.safe; safe < s.window.maxEmitted || safe <= s.hub.complete {
		return safe
	}
	return max(s.window.maxEmitted, 1) - 1
}

// commitOf is the commit position of the transaction msg belongs to, or 0.
func commitOf(msg hermod.Message) pglogrepl.LSN {
	raw := msg.Metadata()["commit_lsn"]
	if raw == "" {
		return 0
	}
	lsn, err := pglogrepl.ParseLSN(raw)
	if err != nil {
		return 0
	}
	return lsn
}

// read returns the next change for this workflow.
func (s *hubSubscription) read(ctx context.Context) (hermod.Message, error) {
	select {
	case <-s.done:
		return nil, context.Canceled
	default:
	}
	select {
	case msg := <-s.ch:
		return msg, nil
	case err := <-s.errs:
		return nil, err
	case <-s.done:
		return nil, context.Canceled
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ack records this workflow's acknowledgement, and acknowledges the stream
// once every workflow the change went to has.
func (s *hubSubscription) ack(ctx context.Context, msg hermod.Message) error {
	lsn := msg.Metadata()["lsn"]
	if lsn == "" {
		return nil
	}
	h := s.hub
	h.mu.Lock()
	n := s.outstanding[lsn]
	if s.left || n == 0 {
		h.mu.Unlock()
		return nil
	}
	if n == 1 {
		delete(s.outstanding, lsn)
	} else {
		s.outstanding[lsn] = n - 1
	}
	if commit := commitOf(msg); commit != 0 {
		s.window.ack(commit)
	}
	settled := h.settleLocked(lsn)
	h.mu.Unlock()
	if settled == nil {
		return nil
	}
	return h.ackStream(ctx, settled)
}

// pending reports whether this workflow holds changes it has not acknowledged.
func (s *hubSubscription) pending() bool {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	return len(s.outstanding) > 0 || len(s.ch) > 0
}

// tableMatcher says whether a change is on one of a workflow's tables. A
// configured "schema.table" names that table; a bare "table" names it in any
// schema, as the publication check does. No tables means every table.
type tableMatcher struct {
	all    bool
	tables map[string]struct{}
}

func newTableMatcher(tables []string) tableMatcher {
	if len(tables) == 0 {
		return tableMatcher{all: true}
	}
	m := tableMatcher{tables: make(map[string]struct{}, len(tables))}
	for _, t := range tables {
		m.tables[strings.TrimSpace(t)] = struct{}{}
	}
	return m
}

func (m tableMatcher) matches(schema, table string) bool {
	if m.all {
		return true
	}
	if _, ok := m.tables[table]; ok {
		return true
	}
	_, ok := m.tables[schema+"."+table]
	return ok
}

// fanLogger sends the hub stream's log lines to the live log of every
// workflow on the hub, so none of them is left without the stream's story.
type fanLogger struct {
	mu      sync.RWMutex
	loggers map[*hubSubscription]hermod.Logger
}

func (l *fanLogger) add(sub *hubSubscription, logger hermod.Logger) {
	l.mu.Lock()
	l.loggers[sub] = logger
	l.mu.Unlock()
}

func (l *fanLogger) remove(sub *hubSubscription) {
	l.mu.Lock()
	delete(l.loggers, sub)
	l.mu.Unlock()
}

// each logs to every workflow, or to the process log when none gave a
// logger, as a PostgresSource without one does.
func (l *fanLogger) each(level, msg string, kv []any, logTo func(hermod.Logger)) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if len(l.loggers) == 0 {
		log.Printf("[%s] %s %v", level, msg, kv)
		return
	}
	for _, logger := range l.loggers {
		logTo(logger)
	}
}

func (l *fanLogger) Debug(msg string, kv ...any) {
	l.each("DEBUG", msg, kv, func(g hermod.Logger) { g.Debug(msg, kv...) })
}

func (l *fanLogger) Info(msg string, kv ...any) {
	l.each("INFO", msg, kv, func(g hermod.Logger) { g.Info(msg, kv...) })
}

func (l *fanLogger) Warn(msg string, kv ...any) {
	l.each("WARN", msg, kv, func(g hermod.Logger) { g.Warn(msg, kv...) })
}

func (l *fanLogger) Error(msg string, kv ...any) {
	l.each("ERROR", msg, kv, func(g hermod.Logger) { g.Error(msg, kv...) })
}
