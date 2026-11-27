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
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// busySink is a destination with no room until a given time: every write
// before then is refused as busy, as a database with no free connection or an
// API answering 429 refuses it. After that it accepts everything.
type busySink struct {
	until    time.Time
	busy     atomic.Int64
	mu       sync.Mutex
	accepted map[string]bool
}

func (s *busySink) Write(_ context.Context, m hermod.Message) error {
	if time.Now().Before(s.until) {
		s.busy.Add(1)
		return fmt.Errorf("no free connection: %w", hermod.ErrBusy)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accepted[m.ID()] = true
	return nil
}
func (s *busySink) Ping(context.Context) error { return nil }
func (s *busySink) Close() error               { return nil }

func (s *busySink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.accepted)
}

// countingDLQ counts what reaches it, here as the dead-letter sink.
type countingDLQ struct{ n atomic.Int64 }

func (s *countingDLQ) Write(context.Context, hermod.Message) error { s.n.Add(1); return nil }
func (s *countingDLQ) Ping(context.Context) error                  { return nil }
func (s *countingDLQ) Close() error                                { return nil }

// A destination that is busy for longer than the retry budget must make a
// workflow slower, never failed.
//
// A busy answer used to be an ordinary error. It used up MaxRetries (3, 100ms
// apart, by default) within a fraction of a second, so the message went to the
// dead letter or back unacknowledged; it counted towards the circuit breaker,
// which opened and showed the sink as error:circuit_breaker_open; and since
// nothing completed, the stall watchdog reported the workflow wedged and the
// supervisor restarted it, adding a reconnect to a destination that had no
// room. All of it for a destination that was up and only needed waiting for.
func TestBusyDestinationMakesTheWorkflowSlowerNeverFailed(t *testing.T) {
	const messages = 40
	snk := &busySink{until: time.Now().Add(3 * time.Second), accepted: map[string]bool{}}
	dlq := &countingDLQ{}

	eng := NewEngine(&mockSourceWithLimit{limit: messages}, []hermod.Sink{snk}, buffer.NewRingBuffer(256))
	cfg := stallTestConfig(time.Second)
	cfg.MaxRetries = 3
	cfg.RetryInterval = 10 * time.Millisecond
	eng.SetConfig(cfg)
	eng.SetIDs("wf-busy", "src", []string{"s1"})
	eng.SetDeadLetterSink(dlq)

	var stalls atomic.Int64
	eng.SetOnStall(func(string) { stalls.Add(1) })
	var mu sync.Mutex
	var bad []string
	eng.SetOnStatusChange(func(u telemetry.StatusUpdate) {
		for id, st := range u.SinkStatuses {
			if strings.HasPrefix(st, "error") {
				mu.Lock()
				bad = append(bad, id+"="+st)
				mu.Unlock()
			}
		}
	})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = eng.Start(ctx)
	}()
	deadline := time.Now().Add(20 * time.Second)
	for snk.count() < messages && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	if snk.busy.Load() == 0 {
		t.Fatal("precondition: the sink never answered busy")
	}
	if got := snk.count(); got != messages {
		t.Errorf("delivered %d of %d messages once the sink had room", got, messages)
	}
	if n := dlq.n.Load(); n != 0 {
		t.Errorf("%d messages went to the dead letter for a sink that was only busy", n)
	}
	if n := stalls.Load(); n != 0 {
		t.Errorf("the workflow was reported stalled %d times while its sink answered busy", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bad) != 0 {
		t.Errorf("a busy sink was shown as failed: %v", bad)
	}
}

// While a write waits for room, the sink shows "waiting", and the periodic
// health check must not turn that back into "running": the sink answers its
// ping, because it is up, but it is not taking writes.
func TestBusySinkShowsWaitingUntilItHasRoom(t *testing.T) {
	snk := &busySink{until: time.Now().Add(2 * time.Second), accepted: map[string]bool{}}
	eng := NewEngine(&mockSourceWithLimit{limit: 1}, []hermod.Sink{snk}, buffer.NewRingBuffer(16))
	eng.SetConfig(stallTestConfig(time.Second))
	eng.SetIDs("wf-busy", "src", []string{"s1"})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = eng.Start(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// Several health checks (every 100ms here) have run by now.
	time.Sleep(1500 * time.Millisecond)
	if st := eng.GetStatus().SinkStatuses["s1"]; st != "waiting" {
		t.Fatalf("sink status while busy is %q, want waiting", st)
	}
	deadline := time.Now().Add(10 * time.Second)
	for snk.count() < 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if st := eng.GetStatus().SinkStatuses["s1"]; st == "waiting" {
		t.Fatalf("sink status after it took the write is still %q", st)
	}
}

// A destination that says how long to wait is waited for that long, within
// reason; one that does not is asked again after the backoff, with jitter so
// writers refused together do not all return together.
func TestBusyDelay(t *testing.T) {
	asked := &hermod.Busy{Err: errors.New("429 Too Many Requests"), RetryAfter: 7 * time.Second}
	if d := busyDelay(fmt.Errorf("write: %w", asked), time.Second); d != 7*time.Second {
		t.Errorf("Retry-After 7s waited %s", d)
	}
	greedy := &hermod.Busy{RetryAfter: time.Hour}
	if d := busyDelay(greedy, time.Second); d != retryAfterCap {
		t.Errorf("Retry-After 1h waited %s, want the cap %s", d, retryAfterCap)
	}
	for range 100 {
		if d := busyDelay(hermod.ErrBusy, time.Second); d < 500*time.Millisecond || d > 1500*time.Millisecond {
			t.Fatalf("backoff of 1s waited %s, want 0.5s to 1.5s", d)
		}
	}
	if !errors.Is(asked, hermod.ErrBusy) {
		t.Error("a Busy answer does not match ErrBusy")
	}
}
