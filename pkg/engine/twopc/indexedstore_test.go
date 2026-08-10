package twopc

import (
	"context"
	"sync"
	"testing"

	"github.com/user/hermod"
)

// plainStore is a hermod.StateStore: Get/Set/Delete and no enumeration. That
// absence is the whole reason IndexedStore exists.
type plainStore struct {
	mu sync.Mutex
	kv map[string][]byte
}

func newPlainStore() *plainStore { return &plainStore{kv: map[string][]byte{}} }

func (p *plainStore) Get(_ context.Context, key string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.kv[key]
	if !ok {
		return nil, ErrNotFound
	}
	return v, nil
}

func (p *plainStore) Set(_ context.Context, key string, value []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.kv[key] = value
	return nil
}

func (p *plainStore) Delete(_ context.Context, key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.kv, key)
	return nil
}

func TestIndexedStoreListsWhatItStored(t *testing.T) {
	ctx := context.Background()
	s := NewIndexedStore(newPlainStore(), "idx")

	if err := s.Set(ctx, "twopc/wf/a", []byte("1")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set(ctx, "twopc/wf/b", []byte("2")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set(ctx, "other/c", []byte("3")); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := s.List(ctx, "twopc/wf/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("List returned %d records, want 2 (prefix must be honoured)", len(got))
	}
}

func TestIndexedStoreForgetsDeletedRecords(t *testing.T) {
	ctx := context.Background()
	s := NewIndexedStore(newPlainStore(), "idx")

	_ = s.Set(ctx, "twopc/wf/a", []byte("1"))
	if err := s.Delete(ctx, "twopc/wf/a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := s.List(ctx, "twopc/wf/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List returned %d records after delete, want 0; recovery would replay them forever", len(got))
	}
}

// TestIndexedStorePrunesEntriesWhoseRecordVanished covers the crash window: the
// record was deleted but the index write that should have followed did not
// land. List must not hand recovery a key it cannot read.
func TestIndexedStorePrunesEntriesWhoseRecordVanished(t *testing.T) {
	ctx := context.Background()
	base := newPlainStore()
	s := NewIndexedStore(base, "idx")

	_ = s.Set(ctx, "twopc/wf/a", []byte("1"))
	_ = s.Set(ctx, "twopc/wf/b", []byte("2"))

	// Delete underneath the index, simulating the interrupted sequence.
	_ = base.Delete(ctx, "twopc/wf/a")

	got, err := s.List(ctx, "twopc/wf/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, present := got["twopc/wf/a"]; present {
		t.Error("List returned a key whose record is gone")
	}
	if len(got) != 1 {
		t.Errorf("List returned %d records, want 1", len(got))
	}

	// And the pruning must stick, or every List pays for it again.
	again, _ := s.List(ctx, "twopc/wf/")
	if len(again) != 1 {
		t.Errorf("second List returned %d, want 1", len(again))
	}
}

// TestIndexedStoreSurvivesConcurrentWriters: the index is a read-modify-write,
// so without serialisation concurrent transactions silently lose entries — and
// a lost entry is an in-doubt transaction recovery cannot see.
func TestIndexedStoreSurvivesConcurrentWriters(t *testing.T) {
	ctx := context.Background()
	s := NewIndexedStore(newPlainStore(), "idx")

	const n = 50
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "twopc/wf/" + string(rune('a'+i%26)) + string(rune('0'+i/26))
			_ = s.Set(ctx, key, []byte("v"))
		}(i)
	}
	wg.Wait()

	got, err := s.List(ctx, "twopc/wf/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != n {
		t.Errorf("List returned %d of %d concurrently written records; the index lost entries", len(got), n)
	}
}

// TestCoordinatorWorksOverAPlainStateStore is the integration that matters:
// the whole point of the adapter is that an existing StateStore backend can be
// the transaction log.
func TestCoordinatorWorksOverAPlainStateStore(t *testing.T) {
	ctx := context.Background()
	base := newPlainStore()
	store := NewIndexedStore(base, "twopc-index/wf-1")

	c, err := New(Options{Store: store, WorkflowID: "wf-1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	a := &fakeParticipant{name: "a"}
	b := &fakeParticipant{name: "b"}

	// Crash after preparing, then recover through a second coordinator over the
	// same plain store.
	if _, err := c.prepareOnly(ctx, []Participant{{ID: "a", Sink: a}, {ID: "b", Sink: b}},
		func(context.Context) error { return nil }); err != nil {
		t.Fatalf("prepareOnly: %v", err)
	}

	second, err := New(Options{Store: NewIndexedStore(base, "twopc-index/wf-1"), WorkflowID: "wf-1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := second.Recover(ctx, map[string]hermod.TwoPhaseCommit{"a": a, "b": b}); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	for _, p := range []*fakeParticipant{a, b} {
		if _, committed, rolledBack := p.counts(); committed != 0 || rolledBack != 1 {
			t.Errorf("%s: committed=%d rolledBack=%d, want 0/1 (presumed abort)", p.name, committed, rolledBack)
		}
	}
}
