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
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
)

// An event's rows answer as one, on the way out as well as on the way in.
//
// A bundle lands in one batch, and a group that commits together relies on
// that. But when a batch failed, the writer retried it one row at a time to
// find the bad row, and a row written on its own is written in its own
// transaction: the PostgreSQL row of an event committed while the MySQL row
// beside it went to the dead letter. Isolation now works one event at a time,
// and an event that fails is parked whole.
//
// The second half is a sink that refuses an event before writing it: a group
// whose transaction requires a row for every database, and an event that
// produced rows for only some of them.

// recordSink is a batch sink that records what it is given and can refuse.
type recordSink struct {
	mu        sync.Mutex
	batches   [][]string
	singles   []string
	failBatch func(ids []string) error
	validate  func(ids []string) error
}

func (s *recordSink) Write(_ context.Context, msg hermod.Message) error {
	s.mu.Lock()
	s.singles = append(s.singles, msg.ID())
	s.mu.Unlock()
	return nil
}

func (s *recordSink) WriteBatch(_ context.Context, msgs []hermod.Message) error {
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID())
	}
	s.mu.Lock()
	s.batches = append(s.batches, ids)
	s.mu.Unlock()
	if s.failBatch != nil {
		return s.failBatch(ids)
	}
	return nil
}

func (s *recordSink) Ping(context.Context) error { return nil }
func (s *recordSink) Close() error               { return nil }

func (s *recordSink) snapshot() (batches [][]string, singles []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]string(nil), s.batches...), append([]string(nil), s.singles...)
}

// bundleCheckingSink is a recordSink that also checks each event's rows.
type bundleCheckingSink struct{ *recordSink }

func (s bundleCheckingSink) ValidateBundle(_ context.Context, rows []hermod.Message) error {
	ids := make([]string, 0, len(rows))
	for _, m := range rows {
		ids = append(ids, m.ID())
	}
	return s.validate(ids)
}

// dlqRecorder is a dead-letter sink that records what it parks.
type dlqRecorder struct {
	mu     sync.Mutex
	parked []string
	errs   []string
	first  chan struct{}
	once   sync.Once
}

func newDLQRecorder() *dlqRecorder { return &dlqRecorder{first: make(chan struct{})} }

func (d *dlqRecorder) Write(_ context.Context, msg hermod.Message) error {
	d.mu.Lock()
	d.parked = append(d.parked, msg.ID())
	if v, ok := hermod.MetadataValue(msg, "_hermod_last_error"); ok {
		d.errs = append(d.errs, v)
	}
	d.mu.Unlock()
	d.once.Do(func() { close(d.first) })
	return nil
}

func (d *dlqRecorder) Ping(context.Context) error { return nil }
func (d *dlqRecorder) Close() error               { return nil }

func (d *dlqRecorder) snapshot() (parked, errs []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.parked...), append([]string(nil), d.errs...)
}

// runUntil starts the engine, waits for first, gives the rest a moment, and
// stops it.
func runUntil(t *testing.T, eng *Engine, first <-chan struct{}) {
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
		t.Fatal("nothing arrived")
	}
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the engine did not stop")
	}
}

func TestAFailedBatchIsNeverRetriedOneRowOfAnEventAtATime(t *testing.T) {
	src := &oneShotSource{}
	snk := &recordSink{failBatch: func(ids []string) error {
		if slices.Contains(ids, "m-1/b") {
			return errors.Join(hermod.ErrPermanent, errors.New("row b breaks a CHECK constraint"))
		}
		return nil
	}}
	dlq := newDLQRecorder()
	eng := NewEngine(src, []hermod.Sink{snk}, buffer.NewRingBuffer(8))
	eng.SetLogger(&testLogger{})
	eng.SetConfig(Config{MaxRetries: 1, RetryInterval: time.Millisecond, StatusInterval: time.Second})
	eng.SetSinkConfigs([]SinkConfig{{BatchSize: 1, BatchTimeout: time.Hour}})
	eng.SetDeadLetterSink(dlq)
	eng.SetRouter(threeRows)

	runUntil(t, eng, dlq.first)

	batches, singles := snk.snapshot()
	if len(singles) != 0 {
		t.Errorf("rows of one event were written on their own after the batch failed: %v "+
			"(a group would commit them in separate transactions)", singles)
	}
	for _, b := range batches {
		if len(b) != 3 {
			t.Errorf("the sink was handed %v, want only the whole event", b)
		}
	}
	parked, errs := dlq.snapshot()
	slices.Sort(parked)
	if want := []string{"m-1/a", "m-1/b", "m-1/c"}; !slices.Equal(parked, want) {
		t.Errorf("parked %v, want the whole event %v", parked, want)
	}
	if len(errs) != 3 {
		t.Errorf("every parked row should say why it is there, got %d reasons", len(errs))
	}
	if got := src.ackCount(); got != 1 {
		t.Errorf("the source was acknowledged %d time(s), want once: the event is parked", got)
	}
}

func TestASinkThatRefusesAnEventsRowsGetsNoneOfThemAndTheEventIsParked(t *testing.T) {
	src := &oneShotSource{}
	rec := &recordSink{validate: func(ids []string) error {
		return errors.New("transaction \"order\" needs a row for every database; none for invoices")
	}}
	dlq := newDLQRecorder()
	eng := NewEngine(src, []hermod.Sink{bundleCheckingSink{rec}}, buffer.NewRingBuffer(8))
	eng.SetLogger(&testLogger{})
	eng.SetConfig(Config{MaxRetries: 1, RetryInterval: time.Millisecond, StatusInterval: time.Second})
	eng.SetSinkConfigs([]SinkConfig{{BatchSize: 10, BatchTimeout: 10 * time.Millisecond}})
	eng.SetDeadLetterSink(dlq)
	eng.SetRouter(threeRows)

	runUntil(t, eng, dlq.first)

	if batches, singles := rec.snapshot(); len(batches)+len(singles) != 0 {
		t.Errorf("a refused event reached the sink: batches %v, singles %v", batches, singles)
	}
	parked, errs := dlq.snapshot()
	if len(parked) != 3 {
		t.Errorf("parked %v, want the event's three rows", parked)
	}
	for _, e := range errs {
		if e == "" || !strings.Contains(e, "every database") {
			t.Errorf("a parked row says %q, want the sink's reason", e)
		}
	}
	if got := src.ackCount(); got != 1 {
		t.Errorf("the source was acknowledged %d time(s), want once: the event is parked", got)
	}
}

func TestARefusedEventWithNoDeadLetterSinkIsNotAcknowledged(t *testing.T) {
	src := &oneShotSource{}
	checked := make(chan struct{})
	var once sync.Once
	rec := &recordSink{validate: func([]string) error {
		once.Do(func() { close(checked) })
		return errors.New("no row for invoices")
	}}
	eng := NewEngine(src, []hermod.Sink{bundleCheckingSink{rec}}, buffer.NewRingBuffer(8))
	eng.SetLogger(&testLogger{})
	eng.SetConfig(Config{MaxRetries: 1, RetryInterval: time.Millisecond, StatusInterval: time.Second})
	eng.SetSinkConfigs([]SinkConfig{{BatchSize: 1, BatchTimeout: time.Hour}})
	eng.SetRouter(threeRows)

	runUntil(t, eng, checked)

	if batches, singles := rec.snapshot(); len(batches)+len(singles) != 0 {
		t.Errorf("a refused event reached the sink: batches %v, singles %v", batches, singles)
	}
	if got := src.ackCount(); got != 0 {
		t.Errorf("the source was acknowledged %d time(s) for an event that went nowhere", got)
	}
}

func TestAnEventTheSinkAcceptsIsWrittenAsBefore(t *testing.T) {
	src := &oneShotSource{}
	written := make(chan struct{})
	var once sync.Once
	rec := &recordSink{
		validate: func([]string) error { return nil },
		failBatch: func([]string) error {
			once.Do(func() { close(written) })
			return nil
		},
	}
	eng := NewEngine(src, []hermod.Sink{bundleCheckingSink{rec}}, buffer.NewRingBuffer(8))
	eng.SetLogger(&testLogger{})
	eng.SetSinkConfigs([]SinkConfig{{BatchSize: 1, BatchTimeout: time.Hour}})
	eng.SetRouter(threeRows)

	runUntil(t, eng, written)

	if batches, _ := rec.snapshot(); len(batches) != 1 || len(batches[0]) != 3 {
		t.Errorf("batches %v, want the event's three rows in one", batches)
	}
	if got := src.ackCount(); got != 1 {
		t.Errorf("the source was acknowledged %d time(s), want once", got)
	}
}
