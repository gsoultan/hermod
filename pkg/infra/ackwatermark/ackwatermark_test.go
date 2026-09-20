package ackwatermark

import (
	"strconv"
	"sync"
	"testing"
)

// The whole point: a cursor may only move past an item once that item, and
// everything before it, has been acknowledged.
func TestMarkOnlyAdvancesOverAnAcknowledgedPrefix(t *testing.T) {
	tr := New()
	for i := range 5 {
		tr.Emitted("m"+strconv.Itoa(i), "c"+strconv.Itoa(i))
	}

	if got := tr.Mark(); got != "" {
		t.Errorf("mark = %q before anything was acknowledged, want empty", got)
	}

	// Acknowledge out of order: the third and fifth arrive first. Neither may
	// move the mark, because the first is still in flight.
	tr.Ack("m2")
	tr.Ack("m4")
	if got := tr.Mark(); got != "" {
		t.Fatalf("mark = %q with m0 still unacknowledged: the cursor stepped over an item in flight", got)
	}

	tr.Ack("m0")
	if got := tr.Mark(); got != "c0" {
		t.Errorf("mark = %q after m0, want c0", got)
	}

	// m1 completes the run m0..m2 — the mark jumps to c2, not c1.
	tr.Ack("m1")
	if got := tr.Mark(); got != "c2" {
		t.Errorf("mark = %q after m1 closed the prefix m0..m2, want c2", got)
	}

	tr.Ack("m3")
	if got := tr.Mark(); got != "c4" {
		t.Errorf("mark = %q once every item was acknowledged, want c4", got)
	}
	if n := tr.Pending(); n != 0 {
		t.Errorf("%d items still pending after all were acknowledged", n)
	}
}

func TestAckOfSomethingNeverEmittedDoesNothing(t *testing.T) {
	tr := New()
	tr.Emitted("m0", "c0")
	tr.Ack("stranger")

	if got := tr.Mark(); got != "" {
		t.Errorf("mark = %q after acknowledging an unknown id", got)
	}
	if n := tr.Pending(); n != 1 {
		t.Errorf("pending = %d, want 1", n)
	}
}

// A redelivered message is the same item. Re-emitting it must not move it to
// the back of the queue, or the mark could advance past its original place.
func TestReEmittingKeepsTheOriginalPosition(t *testing.T) {
	tr := New()
	tr.Emitted("m0", "c0")
	tr.Emitted("m1", "c1")
	tr.Emitted("m0", "c0") // redelivered

	if n := tr.Pending(); n != 2 {
		t.Fatalf("pending = %d after a redelivery, want 2", n)
	}

	tr.Ack("m1")
	if got := tr.Mark(); got != "" {
		t.Errorf("mark = %q with m0 unacknowledged, want empty", got)
	}
	tr.Ack("m0")
	if got := tr.Mark(); got != "c1" {
		t.Errorf("mark = %q once both were acknowledged, want c1", got)
	}
}

func TestSetMarkRestoresStoredState(t *testing.T) {
	tr := New()
	tr.SetMark("resumed")
	if got := tr.Mark(); got != "resumed" {
		t.Errorf("mark = %q after SetMark, want %q", got, "resumed")
	}

	// A restored mark is replaced by the first acknowledged prefix, not added to.
	tr.Emitted("m0", "c0")
	tr.Ack("m0")
	if got := tr.Mark(); got != "c0" {
		t.Errorf("mark = %q, want c0", got)
	}
}

func TestEmptyIDIsIgnored(t *testing.T) {
	tr := New()
	tr.Emitted("", "c0")
	if n := tr.Pending(); n != 0 {
		t.Errorf("pending = %d after emitting an empty id, want 0", n)
	}
}

// Past the cap the tracker stops recording rather than growing without bound.
// The mark then stops advancing, which means redelivery — the right way to
// fail for a source whose messages are never acknowledged.
func TestPendingIsBounded(t *testing.T) {
	tr := New()
	for i := range maxPending + 500 {
		tr.Emitted("m"+strconv.Itoa(i), "c"+strconv.Itoa(i))
	}

	if n := tr.Pending(); n > maxPending {
		t.Errorf("pending = %d, cap is %d", n, maxPending)
	}
	if tr.Dropped() != 500 {
		t.Errorf("dropped = %d, want 500", tr.Dropped())
	}
}

// Read and Ack run on different goroutines in every source that uses this.
func TestTrackerUnderConcurrency(t *testing.T) {
	tr := New()
	const n = 2000

	for i := range n {
		tr.Emitted("m"+strconv.Itoa(i), "c"+strconv.Itoa(i))
	}

	var wg sync.WaitGroup
	for w := range 8 {
		wg.Go(func() {
			for i := w; i < n; i += 8 {
				tr.Ack("m" + strconv.Itoa(i))
				_ = tr.Mark()
				_ = tr.Pending()
			}
		})
	}
	wg.Wait()

	if got := tr.Mark(); got != "c"+strconv.Itoa(n-1) {
		t.Errorf("mark = %q after every item was acknowledged, want c%d", got, n-1)
	}
	if p := tr.Pending(); p != 0 {
		t.Errorf("pending = %d, want 0", p)
	}
}

// A page-token API can only store its token once the whole page is
// acknowledged, so every item but the last carries no cursor of its own. An
// empty cursor must move the prefix forward without moving the mark — and in
// particular must not blank a mark already set.
func TestAnEmptyCursorAdvancesThePrefixButNotTheMark(t *testing.T) {
	tr := New()
	tr.Emitted("a1", "")
	tr.Emitted("a2", "")
	tr.Emitted("a3", "page-1") // last item of the page carries the token

	tr.Ack("a1")
	if got := tr.Mark(); got != "" {
		t.Errorf("mark = %q after one item of the page, want empty", got)
	}
	tr.Ack("a2")
	if got := tr.Mark(); got != "" {
		t.Errorf("mark = %q with the page still incomplete, want empty", got)
	}
	tr.Ack("a3")
	if got := tr.Mark(); got != "page-1" {
		t.Errorf("mark = %q once the page was complete, want page-1", got)
	}

	// A later page whose items are not all acknowledged must not blank it.
	tr.Emitted("b1", "")
	tr.Ack("b1")
	if got := tr.Mark(); got != "page-1" {
		t.Errorf("mark = %q after a cursor-less item, want page-1 still", got)
	}
}
