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
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/jackc/pglogrepl"
)

// fakeStream is a hub's stream with the changes a test feeds it.
type fakeStream struct {
	feed chan hermod.Message

	mu       sync.Mutex
	acked    []string
	tables   [][]string
	closed   bool
	tableErr error

	confirmedAt pglogrepl.LSN
	hold        pglogrepl.LSN
	holding     bool
	rewound     int
	rewoundFrom pglogrepl.LSN
}

func (f *fakeStream) confirmed() pglogrepl.LSN {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.confirmedAt
}

func (f *fakeStream) setConfirmed(lsn pglogrepl.LSN) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirmedAt = lsn
}

func (f *fakeStream) setHold(lsn pglogrepl.LSN, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hold, f.holding = lsn, ok
}

func (f *fakeStream) heldAt() (pglogrepl.LSN, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hold, f.holding
}

func (f *fakeStream) rewind(from pglogrepl.LSN) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rewound++
	f.rewoundFrom = from
}

func (f *fakeStream) rewinds() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rewound
}

func newFakeStream() *fakeStream { return &fakeStream{feed: make(chan hermod.Message, 16)} }

func (f *fakeStream) Read(ctx context.Context) (hermod.Message, error) {
	select {
	case m := <-f.feed:
		return m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *fakeStream) Ack(_ context.Context, msg hermod.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acked = append(f.acked, msg.Metadata()["lsn"])
	return nil
}

func (f *fakeStream) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeStream) setTables(_ context.Context, tables []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tables = append(f.tables, tables)
	return f.tableErr
}

func (f *fakeStream) ackedLSNs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.acked)
}

func change(table, lsn string) hermod.Message {
	m := message.AcquireMessage()
	m.SetSchema("public")
	m.SetTable(table)
	m.SetMetadata("lsn", lsn)
	return m
}

// hubWith starts a registry of its own with one hub on stream.
func hubWith(t *testing.T, stream *fakeStream) (*hubRegistry, func(tables ...string) *hubSubscription) {
	t.Helper()
	r := &hubRegistry{hubs: map[string]*slotHub{}}
	join := func(tables ...string) *hubSubscription {
		t.Helper()
		sub, err := r.join(t.Context(), "db\x00slot", "pub", tables, nil, func(hermod.Logger) hubStream { return stream }, "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.leave(sub) })
		return sub
	}
	return r, join
}

func readOne(t *testing.T, sub *hubSubscription) hermod.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := sub.read(ctx)
	if err != nil {
		t.Fatalf("no change arrived: %v", err)
	}
	return msg
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Each workflow gets the changes on its own tables, and a change no workflow
// follows is acknowledged at once: left unacknowledged, it would hold the
// slot's position back for good.
func TestSlotHubRoutesChangesByTable(t *testing.T) {
	stream := newFakeStream()
	_, join := hubWith(t, stream)
	orders, users := join("orders"), join("public.users")

	stream.feed <- change("users", "0/10")
	stream.feed <- change("orders", "0/20")
	stream.feed <- change("audit", "0/30")

	if got := readOne(t, users).Table(); got != "users" {
		t.Errorf("users workflow got a change on %q", got)
	}
	if got := readOne(t, orders).Table(); got != "orders" {
		t.Errorf("orders workflow got a change on %q", got)
	}
	waitFor(t, "the change nobody follows to be acknowledged", func() bool {
		return slices.Contains(stream.ackedLSNs(), "0/30")
	})
	if len(orders.ch)+len(users.ch) != 0 {
		t.Error("a workflow got a change on a table it does not follow")
	}
}

// The slot is confirmed past a change only when every workflow it went to has
// acknowledged it. Confirming on the first acknowledgement would let a crash
// of the slower workflow lose the change.
func TestSlotHubAcknowledgesTheStreamOnlyWhenEveryWorkflowHas(t *testing.T) {
	stream := newFakeStream()
	_, join := hubWith(t, stream)
	a, b := join("orders"), join("orders")

	stream.feed <- change("orders", "0/10")
	fromA, fromB := readOne(t, a), readOne(t, b)
	defer fromA.Release()
	defer fromB.Release()

	if err := a.ack(t.Context(), fromA); err != nil {
		t.Fatal(err)
	}
	if got := stream.ackedLSNs(); len(got) != 0 {
		t.Fatalf("stream acknowledged %v with one of two workflows still owing", got)
	}
	if pending := b.pending(); !pending {
		t.Error("the workflow that has not acknowledged reports nothing pending")
	}
	if err := b.ack(t.Context(), fromB); err != nil {
		t.Fatal(err)
	}
	if got := stream.ackedLSNs(); !slices.Equal(got, []string{"0/10"}) {
		t.Errorf("stream acknowledged %v, want [0/10]", got)
	}
}

// A workflow that stops while it owes acknowledgements must not hold the
// slot back for the others, or the server keeps WAL for it without end.
func TestSlotHubAcknowledgesForAWorkflowThatLeaves(t *testing.T) {
	stream := newFakeStream()
	r, join := hubWith(t, stream)
	a := join("orders")
	b, err := r.join(t.Context(), "db\x00slot", "pub", []string{"orders"}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	stream.feed <- change("orders", "0/10")
	fromA := readOne(t, a)
	defer fromA.Release()
	_ = a.ack(t.Context(), fromA)
	r.leave(b)

	if got := stream.ackedLSNs(); !slices.Equal(got, []string{"0/10"}) {
		t.Errorf("stream acknowledged %v after the owing workflow left, want [0/10]", got)
	}
	if _, err := b.read(t.Context()); !errors.Is(err, context.Canceled) {
		t.Errorf("read after leaving = %v, want context.Canceled", err)
	}
}

// A workflow joining with tables the publication lacks widens it to the
// union; one that adds nothing leaves it alone.
func TestSlotHubWidensThePublicationForNewTables(t *testing.T) {
	stream := newFakeStream()
	_, join := hubWith(t, stream)
	join("orders")
	join("users", "orders")
	join("users")

	if !slices.EqualFunc(stream.tables, [][]string{{"orders", "users"}}, slices.Equal) {
		t.Errorf("publication set to %v, want once to [orders users]", stream.tables)
	}
}

// A publication that cannot be widened turns the workflow away, and the
// tables it brought are not counted as covered: the next workflow to ask for
// them tries again.
func TestSlotHubRefusesAWorkflowWhosePublicationCannotWiden(t *testing.T) {
	stream := newFakeStream()
	r, join := hubWith(t, stream)
	join("orders")

	stream.tableErr = errors.New("permission denied")
	if _, err := r.join(t.Context(), "db\x00slot", "pub", []string{"users"}, nil, nil, ""); err == nil {
		t.Fatal("joined although the publication could not cover its tables")
	}
	stream.tableErr = nil
	join("users")
	if len(stream.tables) != 2 {
		t.Errorf("publication widened %d times, want 2 (the failure, then the retry)", len(stream.tables))
	}
}

// One slot streams one publication. A workflow naming another would get
// changes its publication does not define.
func TestSlotHubRefusesADifferentPublication(t *testing.T) {
	stream := newFakeStream()
	r, join := hubWith(t, stream)
	join("orders")
	if _, err := r.join(t.Context(), "db\x00slot", "other_pub", nil, nil, nil, ""); err == nil {
		t.Fatal("a workflow with another publication joined the slot")
	}
}

// The stream closes, dropping a temporary slot, only when its last workflow
// leaves.
func TestSlotHubClosesTheStreamWithTheLastWorkflow(t *testing.T) {
	stream := newFakeStream()
	r := &hubRegistry{hubs: map[string]*slotHub{}}
	newStream := func(hermod.Logger) hubStream { return stream }
	a, err := r.join(t.Context(), "k", "pub", nil, nil, newStream, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.join(t.Context(), "k", "pub", nil, nil, newStream, "")
	if err != nil {
		t.Fatal(err)
	}
	r.leave(a)
	if stream.closed {
		t.Fatal("stream closed while a workflow was still on it")
	}
	r.leave(b)
	if !stream.closed {
		t.Error("stream still open after the last workflow left")
	}
	if len(r.hubs) != 0 {
		t.Error("the empty hub is still registered")
	}
}
