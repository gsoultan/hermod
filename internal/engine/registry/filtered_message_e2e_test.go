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
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// What becomes of a record a filter drops.
//
// A filter that lets a record go no further has done its job: the record was
// wanted nowhere. The engine cannot see that. A dropped record reaches it as
// "the workflow has sinks and routed this to none of them", which is also what
// a sink that could not be resolved looks like — and since the day that shape
// was found acknowledging records during a sink outage, the engine has refused
// to treat it as handled: it parks the record on the Dead Letters page, or with
// dead letters off leaves it unacknowledged, and counts it as undeliverable.
//
// That is the right answer for a record nothing could deliver and the wrong
// one for a record a filter was asked to drop.

// twoRecordSource emits one record the filter keeps and one it drops, and
// remembers which were acknowledged.
type twoRecordSource struct {
	mu    sync.Mutex
	sent  int
	acked map[string]bool
}

func (s *twoRecordSource) Read(ctx context.Context) (hermod.Message, error) {
	s.mu.Lock()
	n := s.sent
	if n < 2 {
		s.sent++
	}
	s.mu.Unlock()
	if n >= 2 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	msg := message.AcquireMessage()
	if n == 0 {
		msg.SetID("kept")
		msg.SetData("keep", "yes")
	} else {
		msg.SetID("dropped")
		msg.SetData("keep", "no")
	}
	return msg, nil
}

func (s *twoRecordSource) Ack(_ context.Context, msg hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.acked == nil {
		s.acked = make(map[string]bool)
	}
	if msg != nil {
		s.acked[msg.ID()] = true
	}
	return nil
}

func (s *twoRecordSource) Ping(context.Context) error { return nil }
func (s *twoRecordSource) Close() error               { return nil }

func (s *twoRecordSource) wasAcked(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acked[id]
}

func filterWorkflow(deadLetters bool) storage.Workflow {
	return storage.Workflow{
		ID:   "wf-filter",
		Name: "wf-filter",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s-records"},
			{ID: "only-kept", Type: "transformation", Config: map[string]any{
				"transType": "filter_data",
				"field":     "keep",
				"operator":  "=",
				"value":     "yes",
			}},
			{ID: "out", Type: "sink", RefID: "snk-out"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "only-kept"},
			{ID: "e2", SourceID: "only-kept", TargetID: "out"},
		},
		DeadLetters:   deadLetters,
		MaxRetries:    1,
		RetryInterval: "10ms",
	}
}

func TestARecordAFilterDropsIsNotDeadLettered(t *testing.T) {
	st := newPipeDeadLetterStorage()
	reg := NewRegistry(st)
	sinks := map[string]*pipeSink{"snk-out": {name: "out"}}
	src := &twoRecordSource{}

	stop := startForeachPipeline(t, reg, filterWorkflow(true), src, sinks)
	defer stop()

	if !waitUntil(t, 15*time.Second, "the kept record to be delivered", func() bool {
		return sinks["snk-out"].count() >= 1
	}) {
		t.Fatalf("the record the filter kept was never delivered")
	}
	// Give the dropped record time to go wherever it is going.
	waitUntil(t, 2*time.Second, "the dropped record to be acknowledged", func() bool {
		return src.wasAcked("dropped")
	})

	if got := sinks["snk-out"].count(); got != 1 {
		t.Errorf("the sink received %d records, want only the one the filter kept", got)
	}
	if got := len(st.all()); got != 0 {
		t.Errorf("the Dead Letters page holds %d record(s): a record the filter dropped "+
			"on purpose was parked as though it had failed", got)
	}
	if !src.wasAcked("dropped") {
		t.Error("the record the filter dropped was never acknowledged to its source")
	}
}

// With dead letters off the dropped record has nowhere to be parked, and the
// engine leaves it unacknowledged. For a replication slot that is a record the
// source believes is still outstanding.
func TestARecordAFilterDropsIsAcknowledged(t *testing.T) {
	reg := NewRegistry(newPipeStorage())
	sinks := map[string]*pipeSink{"snk-out": {name: "out"}}
	src := &twoRecordSource{}

	stop := startForeachPipeline(t, reg, filterWorkflow(false), src, sinks)
	defer stop()

	if !waitUntil(t, 15*time.Second, "the kept record to be delivered", func() bool {
		return sinks["snk-out"].count() >= 1
	}) {
		t.Fatalf("the record the filter kept was never delivered")
	}
	waitUntil(t, 2*time.Second, "the dropped record to be acknowledged", func() bool {
		return src.wasAcked("dropped")
	})

	if !src.wasAcked("kept") {
		t.Error("the record that was delivered was not acknowledged")
	}
	if !src.wasAcked("dropped") {
		t.Error("the record the filter dropped was never acknowledged to its source")
	}
}

// declaringSource emits one record that carries a verdict about itself,
// through a workflow in which nothing reaches that verdict.
type declaringSource struct {
	twoRecordSource
	marker string
}

func (s *declaringSource) Read(ctx context.Context) (hermod.Message, error) {
	s.mu.Lock()
	n := s.sent
	if n < 1 {
		s.sent++
	}
	s.mu.Unlock()
	if n >= 1 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	msg := message.AcquireMessage()
	msg.SetID("self-declared")
	msg.SetData("lines", []any{})
	// What a producer can do: a record's metadata is the producer's to write.
	msg.SetMetadata(s.marker, "true")
	return msg, nil
}

// Filtered, delivered inline and dead-lettered are verdicts the workflow and
// the engine reach about a message, and each makes the engine acknowledge one
// it routed nowhere. A record that arrives saying one of them of itself must
// not be believed: a producer that could set the marker could have its own
// records acknowledged and discarded.
//
// Here the record is ended by a foreach over an empty array — not a filter, not
// a write, not a park — and it must be left unacknowledged exactly as it would
// be with no marker at all.
func TestAProducerCannotDeclareAVerdictOnItsOwnRecord(t *testing.T) {
	for _, marker := range []string{"_hermod_filtered", "_hermod_delivered_inline", "_hermod_dead_lettered"} {
		t.Run(marker, func(t *testing.T) {
			reg := NewRegistry(newPipeStorage())
			sinks := map[string]*pipeSink{"snk-out": {name: "out"}}
			src := &declaringSource{marker: marker}

			wf := storage.Workflow{
				ID:   "wf-self-declared" + marker,
				Name: "wf-self-declared" + marker,
				Nodes: []storage.WorkflowNode{
					{ID: "src", Type: "source", RefID: "s-records"},
					{ID: "fan", Type: "foreach", Config: map[string]any{"arrayPath": "lines"}},
					{ID: "out", Type: "sink", RefID: "snk-out"},
				},
				Edges: []storage.WorkflowEdge{
					{ID: "e1", SourceID: "src", TargetID: "fan"},
					{ID: "e2", SourceID: "fan", TargetID: "out"},
				},
				MaxRetries:    1,
				RetryInterval: "10ms",
			}
			stop := startForeachPipeline(t, reg, wf, src, sinks)
			defer stop()

			if waitUntil(t, 2*time.Second, "the self-declared record to be acknowledged", func() bool {
				return src.wasAcked("self-declared")
			}) {
				t.Errorf("a record that arrived carrying %s was acknowledged, though nothing reached that verdict", marker)
			}
		})
	}
}
