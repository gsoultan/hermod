// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package twopc

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// What a run's caller can tell about how it ended.
//
// A view of a transaction's runs has to say which database refused and whether
// a failed commit is finished or still owed by recovery. Both used to be only
// in the error text. The participant that failed is now a ParticipantError,
// and a commit whose decision stands but did not reach everyone is
// ErrCommitIncomplete. Pending lists what the log still holds, for a view that
// reads the log without resolving anything.

func TestAParticipantThatCannotPrepareIsNamedInTheError(t *testing.T) {
	c := newTestCoordinator(t, newMemStore())
	bad := &fakeParticipant{name: "bad", prepareErr: errors.New("disk full")}

	err := c.Run(context.Background(), []Participant{
		{ID: "good", Sink: &fakeParticipant{name: "good"}}, {ID: "bad", Sink: bad},
	}, func(context.Context) error { return nil })

	var pe *ParticipantError
	if !errors.As(err, &pe) {
		t.Fatalf("error %v is not a ParticipantError", err)
	}
	if pe.ID != "bad" || pe.Op != "prepare" {
		t.Errorf("ParticipantError{ID: %q, Op: %q}, want bad/prepare", pe.ID, pe.Op)
	}
	if errors.Is(err, ErrCommitIncomplete) {
		t.Errorf("an aborted transaction reported as an incomplete commit: %v", err)
	}
}

func TestACommitThatDidNotReachEveryoneIsIncomplete(t *testing.T) {
	store := newMemStore()
	c := newTestCoordinator(t, store)
	stuck := &fakeParticipant{name: "stuck", commitErr: errors.New("connection lost")}

	err := c.Run(context.Background(), []Participant{
		{ID: "ok", Sink: &fakeParticipant{name: "ok"}}, {ID: "stuck", Sink: stuck},
	}, func(context.Context) error { return nil })
	if !errors.Is(err, ErrCommitIncomplete) {
		t.Fatalf("error %v is not ErrCommitIncomplete", err)
	}

	pending, err := c.Pending(context.Background())
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("Pending = %+v, want the one transaction recovery still owes", pending)
	}
	p := pending[0]
	if p.State != "committing" || !slices.Equal(p.Participants, []string{"stuck"}) {
		t.Errorf("Pending = %+v, want state committing with only stuck unresolved", p)
	}
	if p.StartedAt.IsZero() || p.PreparedAt.IsZero() {
		t.Errorf("Pending = %+v, want both times", p)
	}
}

func TestPendingIsEmptyWhenEverythingResolved(t *testing.T) {
	c := newTestCoordinator(t, newMemStore())
	if err := c.Run(context.Background(), []Participant{
		{ID: "a", Sink: &fakeParticipant{name: "a"}}, {ID: "b", Sink: &fakeParticipant{name: "b"}},
	}, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Run: %v", err)
	}
	pending, err := c.Pending(context.Background())
	if err != nil || len(pending) != 0 {
		t.Errorf("Pending = %+v, %v; want none", pending, err)
	}
}

// writeCountingStore is a plainStore that counts writes.
type writeCountingStore struct {
	*plainStore
	writes int
}

func (w *writeCountingStore) Set(ctx context.Context, key string, value []byte) error {
	w.writes++
	return w.plainStore.Set(ctx, key, value)
}

func (w *writeCountingStore) Delete(ctx context.Context, key string) error {
	w.writes++
	return w.plainStore.Delete(ctx, key)
}

func TestReadPendingReadsTheLogOfAGroupItIsNot(t *testing.T) {
	base := newPlainStore()
	c, err := New(Options{Store: NewIndexedStore(base, "twopc-index/wf-1/order"), WorkflowID: "wf-1/order"})
	if err != nil {
		t.Fatal(err)
	}
	stuck := &fakeParticipant{name: "stuck", commitErr: errors.New("connection lost")}
	_ = c.Run(context.Background(), []Participant{
		{ID: "ok", Sink: &fakeParticipant{name: "ok"}}, {ID: "stuck", Sink: stuck},
	}, func(context.Context) error { return nil })

	got, err := ReadPending(context.Background(), base, "twopc-index/wf-1/order", "wf-1/order")
	if err != nil || len(got) != 1 || !slices.Equal(got[0].Participants, []string{"stuck"}) {
		t.Errorf("ReadPending = %+v, %v; want the one commit still owed to stuck", got, err)
	}
}

// A view reading the log beside a live group must not write to it. The live
// group indexes a record before writing it, so a reader that pruned an entry
// whose record it could not see yet would unindex a transaction in flight —
// and recovery could never find it.
func TestReadPendingNeverWritesEvenWhenAnEntryLooksStale(t *testing.T) {
	base := &writeCountingStore{plainStore: newPlainStore()}
	_ = base.plainStore.Set(context.Background(), "idx", []byte(`["twopc/wf/being-written"]`))

	got, err := ReadPending(context.Background(), base, "idx", "wf")
	if err != nil || len(got) != 0 {
		t.Errorf("ReadPending = %+v, %v; want nothing", got, err)
	}
	if base.writes != 0 {
		t.Errorf("ReadPending wrote %d time(s) to the log", base.writes)
	}
}

func TestReadPendingOfALogThatWasNeverWrittenIsEmpty(t *testing.T) {
	got, err := ReadPending(context.Background(), newPlainStore(), "idx", "wf")
	if err != nil || len(got) != 0 {
		t.Errorf("ReadPending = %+v, %v; want nothing", got, err)
	}
}
