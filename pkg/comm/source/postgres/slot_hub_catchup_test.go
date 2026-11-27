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
	"slices"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// orderChange is a change on orders, in the transaction committed at commit.
func orderChange(lsn, commit string) hermod.Message {
	m := change("orders", lsn)
	m.SetMetadata("commit_lsn", commit)
	return m
}

func joinAs(t *testing.T, r *hubRegistry, stream *fakeStream, member string, tables ...string) *hubSubscription {
	t.Helper()
	sub, err := r.join(t.Context(), "db\x00slot", "pub", tables, nil, func(hermod.Logger) hubStream { return stream },
		hubMember(member))
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func readAck(t *testing.T, sub *hubSubscription) hermod.Message {
	t.Helper()
	msg := readOne(t, sub)
	if err := sub.ack(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	return msg
}

// A workflow on a shared slot that stops gets, when it starts again, the
// changes the others read while it was stopped, and the others do not get
// them twice. Before, it rejoined at the stream's current position and those
// changes never reached it.
func TestSlotHubReplaysToAWorkflowThatComesBack(t *testing.T) {
	stream := newFakeStream()
	r := &hubRegistry{hubs: map[string]*slotHub{}}
	a := joinAs(t, r, stream, "wf-a", "orders")
	b := joinAs(t, r, stream, "wf-b", "orders")
	t.Cleanup(func() { r.leave(a) })

	stream.feed <- orderChange("0/90", "0/100")
	stream.feed <- orderChange("0/140", "0/150")
	for range 2 {
		readAck(t, a).Release()
		readAck(t, b).Release()
	}

	stream.setConfirmed(0x100)
	r.leave(b)
	if hold, ok := stream.heldAt(); !ok || hold != 0x100 {
		t.Fatalf("slot held at %v (%v) after a workflow left, want 0/100", hold, ok)
	}

	stream.feed <- orderChange("0/190", "0/200")
	readAck(t, a).Release()

	b = joinAs(t, r, stream, "wf-b", "orders")
	t.Cleanup(func() { r.leave(b) })
	if stream.rewinds() != 1 || stream.rewoundFrom != 0x100 {
		t.Fatalf("stream rewound %d times from %v when the workflow came back, want once from 0/100",
			stream.rewinds(), stream.rewoundFrom)
	}
	if _, ok := stream.heldAt(); ok {
		t.Error("slot still held after the workflow it was held for came back")
	}

	// The rewound stream replays from the slot.
	// 0/100 it acknowledged in full. 0/150 it acknowledged too, but as the
	// newest transaction it was handed it may have had rows still to come, so
	// it is replayed rather than skipped: a duplicate, never a loss.
	stream.feed <- orderChange("0/90", "0/100")
	stream.feed <- orderChange("0/140", "0/150")
	stream.feed <- orderChange("0/190", "0/200")

	for _, want := range []string{"0/150", "0/200"} {
		got := readAck(t, b)
		if lsn := got.Metadata()["commit_lsn"]; lsn != want {
			t.Errorf("returning workflow got the change committed at %s, want %s: 0/100 it had acknowledged", lsn, want)
		}
		got.Release()
	}
	waitFor(t, "the replayed changes nobody needs to be acknowledged", func() bool {
		return len(stream.ackedLSNs()) >= 6
	})
	if len(a.ch) != 0 {
		t.Error("the workflow that never stopped got the replay a second time")
	}
}

// One stopped workflow must not keep the server's WAL for ever: after the
// limit, the hold is released and the workflow resumes where the stream is.
func TestSlotHubReleasesTheHoldAfterTheLimit(t *testing.T) {
	stream := newFakeStream()
	r := &hubRegistry{hubs: map[string]*slotHub{}}
	a := joinAs(t, r, stream, "wf-a", "orders")
	t.Cleanup(func() { r.leave(a) })
	a.hub.holdLimit = 20 * time.Millisecond
	b := joinAs(t, r, stream, "wf-b", "orders")

	r.leave(b)
	if _, ok := stream.heldAt(); !ok {
		t.Fatal("slot not held after a workflow left")
	}
	waitFor(t, "the hold to be released", func() bool {
		_, ok := stream.heldAt()
		return !ok
	})
	b = joinAs(t, r, stream, "wf-b", "orders")
	t.Cleanup(func() { r.leave(b) })
	if stream.rewinds() != 0 {
		t.Error("a workflow whose hold was released was replayed to anyway")
	}
}

// Without a workflow to name it, a source has no position to come back to,
// so leaving holds nothing.
func TestSlotHubHoldsNothingForAnAnonymousSource(t *testing.T) {
	stream := newFakeStream()
	r := &hubRegistry{hubs: map[string]*slotHub{}}
	a := joinAs(t, r, stream, "wf-a")
	t.Cleanup(func() { r.leave(a) })
	b := joinAs(t, r, stream, "")
	r.leave(b)
	if _, ok := stream.heldAt(); ok {
		t.Error("slot held for a source no workflow named")
	}
}

// A transaction is not done when its first rows are acknowledged: the rest may
// not have reached the hub yet. Taking it for done skipped them as already
// delivered, so a workflow that acknowledged row 1 before row 2 arrived never
// got row 2, and the stream was acknowledged past it:
// TestCloseBetweenTwoRowsOfATransactionReplaysIt failed in CI with "read:
// context deadline exceeded" on the second row.
func TestSlotHubDeliversTheRestOfATransactionWhoseFirstRowsAreAcknowledged(t *testing.T) {
	stream := newFakeStream()
	_, join := hubWith(t, stream)
	sub := join("orders")

	stream.feed <- orderChange("0/10", "0/30")
	first := readOne(t, sub)
	_ = sub.ack(t.Context(), first)
	first.Release()

	stream.feed <- orderChange("0/20", "0/30")
	second := readOne(t, sub)
	defer second.Release()
	if got := second.Metadata()["lsn"]; got != "0/20" {
		t.Fatalf("got the change at %s, want the transaction's second row at 0/20", got)
	}
	if got := stream.ackedLSNs(); !slices.Equal(got, []string{"0/10"}) {
		t.Errorf("stream acknowledged %v before the second row was, want [0/10]", got)
	}
}

// committedNote is the stream's note that the transaction committed at commit
// has been handed over whole.
func committedNote(commit string) hermod.Message {
	m := message.AcquireMessage()
	m.SetMetadata(hubTxnDoneKey, commit)
	return m
}

// Once the stream says a transaction is complete, a workflow that acknowledged
// all of it does not get it again when it comes back; without the note the
// hub cannot tell the newest transaction is whole, and replays it.
func TestSlotHubSkipsACompleteTransactionForAWorkflowThatComesBack(t *testing.T) {
	stream := newFakeStream()
	r := &hubRegistry{hubs: map[string]*slotHub{}}
	a := joinAs(t, r, stream, "wf-a", "orders")
	b := joinAs(t, r, stream, "wf-b", "orders")
	t.Cleanup(func() { r.leave(a) })

	stream.feed <- orderChange("0/90", "0/100")
	stream.feed <- committedNote("0/100")
	readAck(t, a).Release()
	readAck(t, b).Release()
	waitFor(t, "the completion note", func() bool {
		a.hub.mu.Lock()
		defer a.hub.mu.Unlock()
		return a.hub.complete == 0x100
	})
	stream.setConfirmed(0x100)
	r.leave(b)

	b = joinAs(t, r, stream, "wf-b", "orders")
	t.Cleanup(func() { r.leave(b) })
	stream.feed <- orderChange("0/90", "0/100")
	stream.feed <- orderChange("0/190", "0/200")

	got := readAck(t, b)
	defer got.Release()
	if lsn := got.Metadata()["commit_lsn"]; lsn != "0/200" {
		t.Errorf("returning workflow got the change committed at %s first, want 0/200: 0/100 was complete and acknowledged", lsn)
	}
	if acked := stream.ackedLSNs(); slices.Contains(acked, "") {
		t.Errorf("the completion note was acknowledged: %v", acked)
	}
}
