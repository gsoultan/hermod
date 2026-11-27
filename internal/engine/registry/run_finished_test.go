// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"context"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/testutil"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/source"
)

// finishingSource records how runs ended and keeps a state that changes when
// one succeeds, as the cron source's "last successful run" does.
type finishingSource struct {
	hermod.Source
	mu       sync.Mutex
	finished map[string]bool
	state    map[string]string
}

func (s *finishingSource) RunFinished(m hermod.Message, ok bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished[m.ID()] = ok
	if ok {
		s.state = map[string]string{"last_success_at": m.ID()}
	}
	return true
}
func (s *finishingSource) GetState() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}
func (s *finishingSource) SetState(map[string]string) {}

type stateRecordingStorage struct {
	testutil.BaseMockStorage
	mu    sync.Mutex
	saved map[string]map[string]string
}

func (m *stateRecordingStorage) UpdateSourceState(_ context.Context, id string, state map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saved[id] = state
	return nil
}

// The registry wraps a workflow's sources twice -- to persist their state, and
// to merge several source nodes into one -- and the engine only sees the
// wrapper. A wrapper that did not pass hermod.RunFinisher on would leave a
// cron source believing its first run never ended. The state a run's success
// changes is saved when the source hears of it, not one run later.
func TestRegistryWrappersPassOnHowARunEnded(t *testing.T) {
	msg := func(id, node string) hermod.Message {
		m := message.AcquireMessage()
		m.SetID(id)
		m.SetMetadata("_source_node_id", node)
		return m
	}

	t.Run("stateful", func(t *testing.T) {
		inner := &finishingSource{finished: map[string]bool{}}
		store := &stateRecordingStorage{saved: map[string]map[string]string{}}
		var src hermod.Source = &statefulSource{Source: inner, registry: NewRegistry(store), sourceID: "s1"}
		f, ok := src.(hermod.RunFinisher)
		if !ok {
			t.Fatal("statefulSource does not pass on how a run ended")
		}
		f.RunFinished(msg("run-1", ""), true)
		if !inner.finished["run-1"] {
			t.Fatal("the source did not hear that run-1 succeeded")
		}
		if got := store.saved["s1"]["last_success_at"]; got != "run-1" {
			t.Fatalf("saved state %v, want the success recorded at once", store.saved["s1"])
		}
	})

	t.Run("multi", func(t *testing.T) {
		a := &finishingSource{finished: map[string]bool{}}
		b := &finishingSource{finished: map[string]bool{}}
		var src hermod.Source = &multiSource{sources: []*subSource{{nodeID: "na", source: a}, {nodeID: "nb", source: b}}}
		f, ok := src.(hermod.RunFinisher)
		if !ok {
			t.Fatal("multiSource does not pass on how a run ended")
		}
		f.RunFinished(msg("run-b", "nb"), false)
		if got, heard := b.finished["run-b"]; !heard || got {
			t.Fatalf("node nb heard %v (%v), want a failure", got, heard)
		}
		if len(a.finished) != 0 {
			t.Fatalf("node na heard about another node's run: %v", a.finished)
		}
	})
}

// pollingCursorSource stands in for a polling source: it keeps a cursor as
// state, and has nothing to do with how a run ended.
type pollingCursorSource struct{ hermod.Source }

func (pollingCursorSource) GetState() map[string]string { return map[string]string{"cursor": "42"} }
func (pollingCursorSource) SetState(map[string]string)  {}

// Only a source that acts on how a run ended has its state saved then.
//
// Every source is wrapped in a metrics decorator, which passes RunFinished on
// and so looks like a RunFinisher whatever it wraps. The stateful wrapper took
// that at face value and saved a polling source's cursor after every message:
// one storage write per message on the hot path, and a cursor stored as soon
// as one message finished, ahead of acknowledgements still in flight -- the
// position the source's own checkpoints are careful not to store early.
func TestOnlyASourceThatFinishesRunsHasItsStateSavedAfterOne(t *testing.T) {
	store := &stateRecordingStorage{saved: map[string]map[string]string{}}
	inner := source.NewMetricsSource(pollingCursorSource{}, "s1", "wf", nil)
	src := &statefulSource{Source: inner, registry: NewRegistry(store), sourceID: "s1"}

	m := message.AcquireMessage()
	defer m.Release()
	src.RunFinished(m, true)

	if saved, ok := store.saved["s1"]; ok {
		t.Fatalf("a polling source's cursor was saved after one message: %v", saved)
	}
}
