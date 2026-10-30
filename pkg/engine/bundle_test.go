// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// The rows one event produces for one sink stay together.
//
// A workflow can route one source message to the same sink more than once: two
// branches each build a row for a transactional group, or a fan-out produces
// several rows for one table. The runner used to queue each routed message on
// its own, so the writer could cut a batch between them and the rows of one
// event were committed at different times — which for a group that commits
// together is the one thing it must not do. The routed messages of one event
// for one sink are now queued as one bundle, and a batch never splits a bundle.

// batchesSink records every WriteBatch and Write it receives.
type batchesSink struct {
	mu      sync.Mutex
	batches [][]string
	singles []string
	first   chan struct{}
	once    sync.Once
	fail    func(id string) error
}

func newBatchesSink() *batchesSink { return &batchesSink{first: make(chan struct{})} }

func (s *batchesSink) Write(_ context.Context, msg hermod.Message) error {
	s.mu.Lock()
	s.singles = append(s.singles, msg.ID())
	s.mu.Unlock()
	s.once.Do(func() { close(s.first) })
	if s.fail != nil {
		return s.fail(msg.ID())
	}
	return nil
}

func (s *batchesSink) WriteBatch(_ context.Context, msgs []hermod.Message) error {
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID())
	}
	s.mu.Lock()
	s.batches = append(s.batches, ids)
	s.mu.Unlock()
	s.once.Do(func() { close(s.first) })
	return nil
}

func (s *batchesSink) Ping(context.Context) error { return nil }
func (s *batchesSink) Close() error               { return nil }

func (s *batchesSink) snapshot() (batches [][]string, singles []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]string(nil), s.batches...), append([]string(nil), s.singles...)
}

// writeOnlySink can only Write: it has no WriteBatch, so the engine takes the
// one-row-at-a-time path.
type writeOnlySink struct{ rec *batchesSink }

func (s writeOnlySink) Write(ctx context.Context, msg hermod.Message) error {
	return s.rec.Write(ctx, msg)
}
func (s writeOnlySink) Ping(context.Context) error { return nil }
func (s writeOnlySink) Close() error               { return nil }

// threeRows routes the one message to sink 0 three times, as three branches
// would, each copy named after the branch.
func threeRows(ctx context.Context, msg hermod.Message) ([]RoutedMessage, error) {
	out := make([]RoutedMessage, 0, 3)
	for _, branch := range []string{"a", "b", "c"} {
		c := message.AcquireMessage()
		c.SetID(msg.ID() + "/" + branch)
		c.SetPayload(msg.Payload())
		out = append(out, RoutedMessage{SinkIndex: 0, Message: c})
	}
	return out, nil
}

// runOneEvent starts the engine on a one-message source, waits for the sink's
// first write and a moment for the rest, then stops it.
func runOneEvent(t *testing.T, eng *Engine, first <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = eng.Start(ctx)
	}()
	select {
	case <-first:
	case <-time.After(3 * time.Second):
		t.Fatal("the sink was never written to")
	}
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the engine did not stop")
	}
}

func TestTheRowsOfOneEventForOneSinkArriveInOneBatch(t *testing.T) {
	src := &oneShotSource{}
	sink := newBatchesSink()
	eng := NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(8))
	eng.SetLogger(&testLogger{})
	// A batch of one is the harshest cut: without bundling every row is its
	// own batch.
	eng.SetSinkConfigs([]SinkConfig{{BatchSize: 1, BatchTimeout: time.Hour}})
	eng.SetRouter(threeRows)

	runOneEvent(t, eng, sink.first)

	batches, singles := sink.snapshot()
	if len(singles) != 0 {
		t.Errorf("rows were written one at a time: %v", singles)
	}
	if len(batches) != 1 || len(batches[0]) != 3 {
		t.Fatalf("the event's three rows arrived as %v, want one batch of three", batches)
	}
}

func TestASinkWithoutABatchPathStillGetsEveryRowOfTheBundle(t *testing.T) {
	src := &oneShotSource{}
	rec := newBatchesSink()
	eng := NewEngine(src, []hermod.Sink{writeOnlySink{rec: rec}}, buffer.NewRingBuffer(8))
	eng.SetLogger(&testLogger{})
	eng.SetSinkConfigs([]SinkConfig{{BatchSize: 1, BatchTimeout: time.Hour}})
	eng.SetRouter(threeRows)

	runOneEvent(t, eng, rec.first)

	_, singles := rec.snapshot()
	if len(singles) != 3 {
		t.Fatalf("a Write-only sink received %v, want the event's three rows", singles)
	}
	if got := src.ackCount(); got != 1 {
		t.Errorf("the source was acknowledged %d time(s) for one delivered event, want once", got)
	}
}

func TestOneRowOfABundleFailingFailsTheEvent(t *testing.T) {
	src := &oneShotSource{}
	rec := newBatchesSink()
	rec.fail = func(id string) error {
		if id == "m-1/b" {
			return errors.Join(hermod.ErrPermanent, errors.New("the destination rejected the row"))
		}
		return nil
	}
	eng := NewEngine(src, []hermod.Sink{writeOnlySink{rec: rec}}, buffer.NewRingBuffer(8))
	eng.SetLogger(&testLogger{})
	eng.SetConfig(Config{MaxRetries: 1, RetryInterval: time.Millisecond, StatusInterval: time.Second})
	eng.SetSinkConfigs([]SinkConfig{{BatchSize: 1, BatchTimeout: time.Hour}})
	eng.SetRouter(threeRows)

	runOneEvent(t, eng, rec.first)

	if got := src.ackCount(); got != 0 {
		t.Errorf("the source was acknowledged %d time(s) although one row of the event was refused", got)
	}
}
