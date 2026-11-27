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
	newStream func(logs hermod.Logger) hubStream) (*hubSubscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.hubs[key]
	if h == nil {
		h = newSlotHub(key, publication, tables, newStream)
		r.hubs[key] = h
	}
	sub, err := h.join(ctx, publication, tables, logger)
	if err != nil && h.empty() {
		delete(r.hubs, key)
		h.close()
	}
	return sub, err
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
	}
	h.widenLocked(tables)
	go h.run(ctx)
	return h
}

// join adds a workflow and widens the publication to its tables.
func (h *slotHub) join(ctx context.Context, publication string, tables []string, logger hermod.Logger) (*hubSubscription, error) {
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
	}
	h.mu.Lock()
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
// acknowledged for it: it is gone, and holding the slot back for it would hold
// back every other workflow and the WAL the server keeps. It reports whether
// the hub is now empty.
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
	var settled []hermod.Message
	for lsn, n := range sub.outstanding {
		for range n {
			if msg := h.settleLocked(lsn); msg != nil {
				settled = append(settled, msg)
			}
		}
	}
	clear(sub.outstanding)
	empty := len(h.subs) == 0
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
			h.dispatch(ctx, msg)
		}
	}
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
	var targets []*hubSubscription
	for sub := range h.subs {
		if sub.match.matches(msg.Schema(), msg.Table()) {
			targets = append(targets, sub)
		}
	}
	if lsn != "" && len(targets) > 0 {
		h.pending[lsn] = append(h.pending[lsn], &fanout{msg: msg, left: len(targets)})
		for _, sub := range targets {
			sub.outstanding[lsn]++
		}
	}
	h.mu.Unlock()

	if len(targets) == 0 {
		if lsn != "" {
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

// hubSubscription is one workflow on a shared slot.
type hubSubscription struct {
	hub   *slotHub
	match tableMatcher
	ch    chan hermod.Message
	errs  chan error
	done  chan struct{}

	// Guarded by hub.mu.
	left        bool
	outstanding map[string]int
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
