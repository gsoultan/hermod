// Package ackwatermark turns out-of-order acknowledgements into a cursor that
// is safe to persist.
//
// A polling source fetches a page, hands the items out one at a time, and has
// to remember where it got to. The obvious place to record that is the fetch
// itself — and that is the bug: the cursor then says a hundred items were
// consumed the moment they were fetched, so a crash after delivering the first
// loses the other ninety-nine for ever. Nothing will fetch that window again.
//
// The cursor that may be persisted is the one behind which *everything* has
// been acknowledged. Not the highest acknowledged value: acknowledgements
// arrive out of order when several sinks run in parallel, and persisting the
// highest would step over an item still in flight. So a Tracker holds the
// emitted items in order and advances its mark across the acknowledged prefix
// only.
package ackwatermark

import "sync"

// maxPending bounds how many un-acknowledged items a Tracker will remember.
//
// The engine's in-flight cap is the real bound in a healthy pipeline. This is
// the backstop for an unhealthy one: a source whose messages are never
// acknowledged would otherwise grow this list until the process died of it.
// Past the cap the Tracker stops recording, which holds the mark still —
// redelivery rather than loss, which is the right way to fail.
const maxPending = 100_000

type entry struct {
	id     string
	cursor string
	acked  bool
}

// Tracker records what has been emitted and reports how far it is safe to
// persist.
//
// The zero value is ready to use, and sources embed it by value for that
// reason: a pointer field would need every constructor to remember to fill it,
// and a source that forgot would panic on its first message rather than fail a
// test. Do not copy a Tracker once used — it holds a mutex, and the sources
// that hold one are always used through a pointer.
type Tracker struct {
	mu      sync.Mutex
	pending []entry
	index   map[string]int
	mark    string
	dropped int
}

// New returns an empty Tracker.
func New() *Tracker { return &Tracker{} }

// Emitted records that a message has been handed to the pipeline, carrying the
// cursor value that message represents.
//
// An empty cursor means "this item does not advance the cursor by itself".
// That is what a page-token API needs: the token is only safe once the whole
// page has been acknowledged, so every item but the last carries no cursor and
// the last carries the token. Passing the token on every item would let it be
// stored as soon as the first item of the page was acknowledged, losing the
// rest — the same bug one level up.
//
// Emitting the same id twice keeps the first position: a redelivered message
// is the same item, and moving it to the back of the queue would let the mark
// advance past its original place.
func (t *Tracker) Emitted(id, cursor string) {
	if id == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.index == nil {
		t.index = make(map[string]int)
	}
	if _, seen := t.index[id]; seen {
		return
	}
	if len(t.pending) >= maxPending {
		t.dropped++
		return
	}
	t.index[id] = len(t.pending)
	t.pending = append(t.pending, entry{id: id, cursor: cursor})
}

// Ack marks a message acknowledged and advances the mark over every item at
// the front of the queue that has been acknowledged.
func (t *Tracker) Ack(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	i, ok := t.index[id]
	if !ok {
		return
	}
	t.pending[i].acked = true

	advanced := 0
	for _, e := range t.pending {
		if !e.acked {
			break
		}
		if e.cursor != "" {
			t.mark = e.cursor
		}
		delete(t.index, e.id)
		advanced++
	}
	if advanced == 0 {
		return
	}
	t.pending = t.pending[advanced:]
	for j := range t.pending {
		t.index[t.pending[j].id] = j
	}
}

// Mark returns the cursor behind which everything has been acknowledged. It is
// the only value safe to persist.
func (t *Tracker) Mark() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mark
}

// SetMark restores the mark, for a source resuming from stored state.
func (t *Tracker) SetMark(cursor string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.mark = cursor
}

// Pending reports how many emitted items are still unacknowledged.
func (t *Tracker) Pending() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending)
}

// Dropped reports how many items were not recorded because the pending list
// was full. Non-zero means acknowledgements are not arriving and the mark has
// stopped advancing; the data is not lost, it will be redelivered.
func (t *Tracker) Dropped() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dropped
}
