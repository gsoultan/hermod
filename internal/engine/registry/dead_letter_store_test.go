package registry

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// deadLetterStorage keeps dead letters in memory, the way a backend that
// implements storage.DeadLetterStore does.
type deadLetterStorage struct {
	secretStorage
	dlMu    sync.Mutex
	kept    []storage.DeadLetter
	block   chan struct{}
	purged  []time.Time
	settled map[string]storage.DeadLetterStatus
}

func (s *deadLetterStorage) PutDeadLetter(_ context.Context, d storage.DeadLetter) error {
	if s.block != nil {
		<-s.block
	}
	s.dlMu.Lock()
	defer s.dlMu.Unlock()
	s.kept = append(s.kept, storage.CapDeadLetter(d))
	return nil
}

func (s *deadLetterStorage) GetDeadLetter(_ context.Context, id string) (storage.DeadLetter, error) {
	s.dlMu.Lock()
	defer s.dlMu.Unlock()
	for _, d := range s.kept {
		if d.ID == id {
			if st, ok := s.settled[id]; ok {
				d.Status = st
			}
			return d, nil
		}
	}
	return storage.DeadLetter{}, storage.ErrNotFound
}

func (s *deadLetterStorage) ListDeadLetters(context.Context, storage.DeadLetterFilter) ([]storage.DeadLetter, int, error) {
	s.dlMu.Lock()
	defer s.dlMu.Unlock()
	return append([]storage.DeadLetter(nil), s.kept...), len(s.kept), nil
}

func (s *deadLetterStorage) SettleDeadLetter(_ context.Context, id string, status storage.DeadLetterStatus, _ storage.DeadLetterEvent) error {
	s.dlMu.Lock()
	defer s.dlMu.Unlock()
	if s.settled == nil {
		s.settled = map[string]storage.DeadLetterStatus{}
	}
	if _, done := s.settled[id]; done {
		return storage.ErrDeadLetterSettled
	}
	s.settled[id] = status
	return nil
}

func (s *deadLetterStorage) AddDeadLetterEvent(context.Context, string, storage.DeadLetterEvent) error {
	return nil
}

func (s *deadLetterStorage) DeadLetterStats(context.Context, []string, time.Time) (storage.DeadLetterStats, error) {
	return storage.DeadLetterStats{}, nil
}

func (s *deadLetterStorage) PurgeDeadLetters(_ context.Context, cutoff time.Time) (int64, error) {
	s.dlMu.Lock()
	defer s.dlMu.Unlock()
	s.purged = append(s.purged, cutoff)
	return 0, nil
}

func (s *deadLetterStorage) all() []storage.DeadLetter {
	s.dlMu.Lock()
	defer s.dlMu.Unlock()
	return append([]storage.DeadLetter(nil), s.kept...)
}

func newDeadLetterRegistry(t *testing.T) (*Registry, *deadLetterStorage) {
	t.Helper()
	st := &deadLetterStorage{secretStorage: secretStorage{secrets: map[string]string{}}}
	reg := NewRegistry(st)
	t.Cleanup(reg.Close)
	return reg, st
}

func parkedMessage(t *testing.T) *message.DefaultMessage {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("m-1")
	msg.SetOperation("update")
	msg.SetTable("orders")
	msg.SetBefore([]byte(`{"total":"12.00"}`))
	msg.SetPayload([]byte(`{"total":"12,50"}`))
	msg.SetMetadata("_hermod_failed_node", "normalize")
	return msg
}

// Every parked message is kept in Hermod's own store, whatever the
// workflow's dead-letter sink is, so the dead letter page can show it.
func TestParkedMessagesAreKeptForTheDeadLetterPage(t *testing.T) {
	reg, st := newDeadLetterRegistry(t)
	wf := storage.Workflow{ID: "wf-1", Name: "Orders sync", VHost: "tenant-a"}
	at := time.Date(2026, 10, 14, 4, 52, 55, 0, time.UTC)

	reg.deadLetterObserver(wf)(pkgengine.DeadLetter{
		WorkflowID: wf.ID, MessageID: "m-1", NodeID: "normalize",
		Error: `field total is not a number: got "12,50"`, FailedAt: at, Message: parkedMessage(t),
	})
	reg.FlushDeadLetterRecords(time.Second)

	kept := st.all()
	if len(kept) != 1 {
		t.Fatalf("kept %d dead letters, want 1", len(kept))
	}
	d := kept[0]
	if d.ID == "" || d.VHost != "tenant-a" || d.WorkflowID != "wf-1" || d.MessageID != "m-1" || d.NodeID != "normalize" ||
		!d.FailedAt.Equal(at) || string(d.Payload) != `{"total":"12,50"}` || d.Metadata["_hermod_failed_node"] != "normalize" ||
		d.Origin.Operation != "update" || d.Origin.Table != "orders" || string(d.Origin.Before) != `{"total":"12.00"}` ||
		d.Status != storage.DeadLetterQueued || len(d.History) != 1 || d.History[0].Action != "failed" {
		t.Fatalf("kept %+v", d)
	}
}

// Keeping a dead letter must never hold up the pipeline that parked it: a
// slow store fills a bounded queue, and what does not fit is dropped and
// counted rather than waited for.
func TestKeepingDeadLettersNeverBlocksTheEngine(t *testing.T) {
	reg, st := newDeadLetterRegistry(t)
	st.block = make(chan struct{})
	wf := storage.Workflow{ID: "wf-1"}
	observe := reg.deadLetterObserver(wf)
	msg := parkedMessage(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range deadLetterQueueSize + 50 {
			observe(pkgengine.DeadLetter{WorkflowID: wf.ID, MessageID: "m", Message: msg})
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the observer blocked on a slow store")
	}
	if dropped := reg.deadLetterRecords.dropped.Load(); dropped < 49 {
		t.Fatalf("dropped %d, want at least 49 of the 50 that did not fit", dropped)
	}
	close(st.block)
	reg.FlushDeadLetterRecords(5 * time.Second)
	if n := len(st.all()); n < deadLetterQueueSize {
		t.Fatalf("kept %d, want the %d the queue held", n, deadLetterQueueSize)
	}
}

// A backend that cannot keep dead letters is not an error: the queue in the
// sink still holds them, and the mail still goes.
func TestDeadLettersWithoutAStoreAreSkipped(t *testing.T) {
	reg := NewRegistry(&secretStorage{secrets: map[string]string{}})
	t.Cleanup(reg.Close)
	reg.deadLetterObserver(storage.Workflow{ID: "wf-1"})(pkgengine.DeadLetter{WorkflowID: "wf-1", Message: parkedMessage(t)})
	reg.FlushDeadLetterRecords(time.Second)
}

func TestRetentionPurgesOldDeadLetters(t *testing.T) {
	t.Setenv("HERMOD_DEAD_LETTER_RETENTION", "48h")
	reg, st := newDeadLetterRegistry(t)
	before := time.Now()
	reg.purgeDeadLetters(context.Background(), st)
	st.dlMu.Lock()
	defer st.dlMu.Unlock()
	// The start-up sweep may have run too; every sweep keeps 48h.
	if len(st.purged) == 0 {
		t.Fatal("dead letters were not purged")
	}
	for _, cutoff := range st.purged {
		if age := before.Sub(cutoff); age < 47*time.Hour || age > 49*time.Hour {
			t.Fatalf("cutoff is %v ago, want 48h", age)
		}
	}
}
