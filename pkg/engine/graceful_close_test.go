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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// closeTrackingSource records whether Close was invoked. It blocks in Read
// until the context is cancelled, mimicking a streaming source whose Read
// returns only on shutdown.
type closeTrackingSource struct {
	closed atomic.Bool
}

func (s *closeTrackingSource) Read(ctx context.Context) (hermod.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *closeTrackingSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *closeTrackingSource) Ping(ctx context.Context) error                    { return nil }

func (s *closeTrackingSource) Close() error {
	s.closed.Store(true)
	return nil
}

// TestGracefulStopClosesSource guards against a regression where the source was
// closed only by HardStop. On a normal stop/restart the source must be closed
// so streaming resources (e.g. a Postgres replication slot) are released and do
// not leak into the next source instance.
func TestGracefulStopClosesSource(t *testing.T) {
	src := &closeTrackingSource{}
	sink := &mockSink{received: make(chan hermod.Message, 1)}
	rb := buffer.NewRingBuffer(10)

	eng := NewEngine(src, []hermod.Sink{sink}, rb)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		_ = eng.Start(ctx)
		close(done)
	}()

	// Let the engine reach its running loops, then request a graceful stop.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("engine did not stop within timeout")
	}

	if !src.closed.Load() {
		t.Fatal("expected source.Close to be called on graceful stop, but it was not")
	}
}

// ackAfterCloseSource hands out one message, then blocks like a stream. Like
// a real broker or CDC source, it can no longer acknowledge once closed: the
// RabbitMQ channel is gone, and a Postgres source has stopped reporting its
// position to the slot.
type ackAfterCloseSource struct {
	sent       atomic.Bool
	closed     atomic.Bool
	acked      atomic.Int64
	ackRefused atomic.Int64
}

func (s *ackAfterCloseSource) Read(ctx context.Context) (hermod.Message, error) {
	if s.sent.CompareAndSwap(false, true) {
		m := message.AcquireMessage()
		m.SetID("in-flight")
		m.SetData("id", "in-flight")
		return m, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *ackAfterCloseSource) Ack(ctx context.Context, msg hermod.Message) error {
	if s.closed.Load() {
		s.ackRefused.Add(1)
		return errors.New("channel not connected")
	}
	s.acked.Add(1)
	return nil
}

func (s *ackAfterCloseSource) Ping(ctx context.Context) error { return nil }

func (s *ackAfterCloseSource) Close() error {
	s.closed.Store(true)
	return nil
}

// gatedSink holds a write until the test releases it, so a stop can be
// requested while a message is still being delivered.
type gatedSink struct {
	entered chan struct{}
	release chan struct{}
}

func (s *gatedSink) Write(ctx context.Context, msg hermod.Message) error {
	close(s.entered)
	<-s.release
	return nil
}

func (s *gatedSink) Ping(ctx context.Context) error { return nil }
func (s *gatedSink) Close() error                   { return nil }

// TestGracefulStopAcknowledgesInFlightMessagesBeforeClosingTheSource is the
// stop/restart case of the branching load test: a message that was being
// written when the stop arrived is delivered by the drain, and must then be
// acknowledged. The source used to be closed before the drain began, so the
// acknowledgement failed ("rabbitmq channel not connected") and the broker
// redelivered a message the sink already had: a duplicate per in-flight
// message per restart.
func TestGracefulStopAcknowledgesInFlightMessagesBeforeClosingTheSource(t *testing.T) {
	src := &ackAfterCloseSource{}
	sink := &gatedSink{entered: make(chan struct{}), release: make(chan struct{})}
	eng := NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(10))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		_ = eng.Start(ctx)
		close(done)
	}()

	select {
	case <-sink.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the message never reached the sink")
	}
	cancel()
	// Give the stop time to reach the source before the write finishes.
	time.Sleep(100 * time.Millisecond)
	close(sink.release)

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("engine did not stop")
	}

	if n := src.ackRefused.Load(); n != 0 {
		t.Errorf("%d delivered message(s) were acknowledged after the source was closed; "+
			"the source will deliver them again", n)
	}
	if n := src.acked.Load(); n != 1 {
		t.Errorf("acknowledged %d message(s), want 1", n)
	}
	if !src.closed.Load() {
		t.Error("the source was not closed on stop")
	}
}

// endlessSource always has another message ready.
type endlessSource struct{ n atomic.Int64 }

func (s *endlessSource) Read(ctx context.Context) (hermod.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m := message.AcquireMessage()
	m.SetID("m" + strconv.FormatInt(s.n.Add(1), 10))
	return m, nil
}
func (s *endlessSource) Ack(context.Context, hermod.Message) error { return nil }
func (s *endlessSource) Ping(context.Context) error                { return nil }
func (s *endlessSource) Close() error                              { return nil }

// heldSink blocks every write until released.
type heldSink struct {
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (s *heldSink) Write(ctx context.Context, msg hermod.Message) error {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return nil
}
func (s *heldSink) Ping(context.Context) error { return nil }
func (s *heldSink) Close() error               { return nil }

// A stop while the source loop waits on a full buffer is not an error: the
// message it was holding was never acknowledged, so the source still has it.
// It used to log "Failed to write message to buffer: context canceled" at
// ERROR on every restart under load.
func TestGracefulStopWithAFullBufferLogsNoError(t *testing.T) {
	sink := &heldSink{entered: make(chan struct{}), release: make(chan struct{})}
	eng := NewEngine(&endlessSource{}, []hermod.Sink{sink}, buffer.NewRingBuffer(1))
	logs := &testLogger{}
	eng.SetLogger(logs)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		_ = eng.Start(ctx)
		close(done)
	}()
	select {
	case <-sink.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("nothing reached the sink")
	}
	// Long enough for the buffer and the queues behind the held sink to fill.
	time.Sleep(200 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)
	close(sink.release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("engine did not stop")
	}

	logs.mu.Lock()
	defer logs.mu.Unlock()
	for _, e := range logs.errs {
		if strings.Contains(e, "buffer") {
			t.Errorf("a normal stop logged an error: %q", e)
		}
	}
}
