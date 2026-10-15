// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package registry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// memDeadLetters keeps dead letters in memory, the way a backend that
// implements storage.DeadLetterStore does. Embed it in a test storage to give
// that storage a dead letter store.
type memDeadLetters struct {
	dlMu    sync.Mutex
	kept    []storage.DeadLetter
	purged  []time.Time
	settled map[string]storage.DeadLetterStatus
	// refuse, when set, is what every PutDeadLetter returns.
	refuse error
}

// deadLetterStorage is a storage with a dead letter store.
type deadLetterStorage struct {
	secretStorage
	memDeadLetters
}

func (s *memDeadLetters) PutDeadLetter(_ context.Context, d storage.DeadLetter) error {
	s.dlMu.Lock()
	defer s.dlMu.Unlock()
	if s.refuse != nil {
		return s.refuse
	}
	s.kept = append(s.kept, storage.CapDeadLetter(d))
	return nil
}

func (s *memDeadLetters) GetDeadLetter(_ context.Context, id string) (storage.DeadLetter, error) {
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

func (s *memDeadLetters) ListDeadLetters(_ context.Context, f storage.DeadLetterFilter) ([]storage.DeadLetter, int, error) {
	s.dlMu.Lock()
	defer s.dlMu.Unlock()
	var out []storage.DeadLetter
	for _, d := range s.kept {
		if st, ok := s.settled[d.ID]; ok {
			d.Status = st
		}
		if (f.WorkflowID != "" && d.WorkflowID != f.WorkflowID) || (f.Status != "" && d.Status != f.Status) {
			continue
		}
		out = append(out, d)
	}
	total := len(out)
	out = out[min(f.Offset, len(out)):]
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, total, nil
}

func (s *memDeadLetters) SettleDeadLetter(_ context.Context, id string, status storage.DeadLetterStatus, _ storage.DeadLetterEvent) error {
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

func (s *memDeadLetters) AddDeadLetterEvent(_ context.Context, id string, e storage.DeadLetterEvent) error {
	s.dlMu.Lock()
	defer s.dlMu.Unlock()
	for i := range s.kept {
		if s.kept[i].ID == id {
			s.kept[i].History = append(s.kept[i].History, e)
		}
	}
	return nil
}

func (s *memDeadLetters) DeadLetterStats(context.Context, []string, time.Time) (storage.DeadLetterStats, error) {
	return storage.DeadLetterStats{}, nil
}

func (s *memDeadLetters) PurgeDeadLetters(_ context.Context, cutoff time.Time) (int64, error) {
	s.dlMu.Lock()
	defer s.dlMu.Unlock()
	s.purged = append(s.purged, cutoff)
	return 0, nil
}

// status is where the dead letter id is now.
func (s *memDeadLetters) status(id string) storage.DeadLetterStatus {
	d, err := s.GetDeadLetter(context.Background(), id)
	if err != nil {
		return ""
	}
	return d.Status
}

func (s *memDeadLetters) all() []storage.DeadLetter {
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

// A workflow's dead-letter queue is Hermod's own store: the engine's park is
// the write that keeps the message on the Dead Letters page, with what failed
// it, where and why, read off the marks the engine put on it.
func TestTheDeadLetterQueueKeepsAParkedMessageForTheDeadLetterPage(t *testing.T) {
	reg, st := newDeadLetterRegistry(t)
	wf := storage.Workflow{ID: "wf-1", Name: "Orders sync", VHost: "tenant-a", DeadLetters: true}
	msg := parkedMessage(t)
	msg.SetMetadata("_hermod_last_error", `field total is not a number: got "12,50"`)
	msg.SetMetadata("_hermod_failed_at", "2026-10-14T04:52:55Z")

	dlq := reg.deadLetterQueue(wf)
	if dlq == nil {
		t.Fatal("a workflow that keeps dead letters got no dead-letter queue")
	}
	if err := dlq.Write(t.Context(), msg); err != nil {
		t.Fatalf("write: %v", err)
	}

	kept := st.all()
	if len(kept) != 1 {
		t.Fatalf("kept %d dead letters, want 1", len(kept))
	}
	at := time.Date(2026, 10, 14, 4, 52, 55, 0, time.UTC)
	d := kept[0]
	if d.ID == "" || d.VHost != "tenant-a" || d.WorkflowID != "wf-1" || d.MessageID != "m-1" || d.NodeID != "normalize" ||
		d.Error != `field total is not a number: got "12,50"` ||
		!d.FailedAt.Equal(at) || string(d.Payload) != `{"total":"12,50"}` || d.Metadata["_hermod_failed_node"] != "normalize" ||
		d.Origin.Operation != "update" || d.Origin.Table != "orders" || string(d.Origin.Before) != `{"total":"12.00"}` ||
		d.Status != storage.DeadLetterQueued || len(d.History) != 1 || d.History[0].Action != "failed" ||
		d.History[0].Detail != "normalize" {
		t.Fatalf("kept %+v", d)
	}
}

// The write is the park, so a store that refuses it is a park that failed: the
// engine is told, and leaves the message on its source instead of
// acknowledging a message that is kept nowhere.
func TestADeadLetterTheStoreRefusesIsAFailedPark(t *testing.T) {
	reg, st := newDeadLetterRegistry(t)
	st.refuse = errors.New("disk full")
	dlq := reg.deadLetterQueue(storage.Workflow{ID: "wf-1", DeadLetters: true})
	if err := dlq.Write(t.Context(), parkedMessage(t)); err == nil {
		t.Fatal("a dead letter the store refused was reported as parked")
	}
}

// Off, or on a backend that cannot keep dead letters, a workflow has no
// dead-letter queue: a message it cannot deliver stays on its source.
func TestNoDeadLetterQueueWhenOffOrUnsupported(t *testing.T) {
	reg, _ := newDeadLetterRegistry(t)
	if reg.deadLetterQueue(storage.Workflow{ID: "wf-1"}) != nil {
		t.Error("a workflow with dead letters off got a dead-letter queue")
	}
	plain := NewRegistry(&secretStorage{secrets: map[string]string{}})
	t.Cleanup(plain.Close)
	if plain.deadLetterQueue(storage.Workflow{ID: "wf-1", DeadLetters: true}) != nil {
		t.Error("a backend without a dead letter store got a dead-letter queue")
	}
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
