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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
)

// A step -- a lookup, an SQL step -- whose database is busy for longer than
// the stall threshold must make the workflow slower, never stalled.
//
// A step had no way to wait: the engine did not offer itself to the steps it
// ran, so a busy answer failed the message, and a step that waited by itself
// looked stuck to the stall watchdog, which had the supervisor restart the
// workflow -- adding a reconnect to a database that had no room.
func TestABusyStepIsWaitedOutByTheEngineWithoutAStall(t *testing.T) {
	const messages = 10
	until := time.Now().Add(3 * time.Second)
	var busyAnswers atomic.Int64

	snk := &busySink{accepted: map[string]bool{}}
	eng := NewEngine(&mockSourceWithLimit{limit: messages}, []hermod.Sink{snk}, buffer.NewRingBuffer(64))
	eng.SetConfig(stallTestConfig(time.Second))
	eng.SetIDs("wf-busy-step", "src", []string{"s1"})
	eng.SetRouter(func(ctx context.Context, m hermod.Message) ([]RoutedMessage, error) {
		w, ok := hermod.BusyWaiterFrom(ctx)
		if !ok {
			return nil, errors.New("the engine did not offer itself to the step")
		}
		err := w.WaitOutBusy(ctx, "step:lookup", func() error {
			if time.Now().Before(until) {
				busyAnswers.Add(1)
				return fmt.Errorf("no free connection: %w", hermod.ErrBusy)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		m.Retain() // the router hands the engine a reference of its own
		return []RoutedMessage{{SinkIndex: 0, Message: m}}, nil
	})

	var stalls atomic.Int64
	eng.SetOnStall(func(string) { stalls.Add(1) })

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

	if busyAnswers.Load() == 0 {
		t.Fatal("precondition: the step never answered busy")
	}
	if got := snk.count(); got != messages {
		t.Errorf("delivered %d of %d messages once the step had room", got, messages)
	}
	if n := stalls.Load(); n != 0 {
		t.Errorf("the workflow was reported stalled %d times while a step waited for room", n)
	}
}

// A step that waits for room is how heavy traffic is meant to look, not a
// problem, so it is logged as information, and once for the step while it is
// waiting rather than once for every message that waits.
//
// It was a warning for every message: an API that refused one order in fifty
// with a 503 filled the log with warnings for a workflow that lost nothing,
// and under heavy traffic the log writes grew with the very load that made the
// step wait.
func TestAWaitingStepIsLoggedOnceAsInformation(t *testing.T) {
	log := &testLogger{}
	eng := NewEngine(&mockSourceWithLimit{limit: 1}, nil, buffer.NewRingBuffer(8))
	eng.SetLogger(log)

	const messages = 20
	release := make(chan struct{})
	var started, done sync.WaitGroup
	for range messages {
		started.Add(1)
		done.Go(func() {
			first := true
			_ = eng.WaitOutBusy(t.Context(), "step:lookup", func() error {
				if first {
					first = false
					started.Done()
					return fmt.Errorf("429: %w", hermod.ErrBusy)
				}
				<-release
				return nil
			})
		})
	}
	started.Wait()
	close(release)
	done.Wait()

	log.mu.Lock()
	defer log.mu.Unlock()
	if len(log.warns) != 0 || len(log.errs) != 0 {
		t.Errorf("a step waiting for room logged %d warnings and %d errors: %v %v",
			len(log.warns), len(log.errs), log.warns, log.errs)
	}
	if len(log.infos) != 1 {
		t.Errorf("%d messages waiting on one step logged %d lines, want 1: %v", messages, len(log.infos), log.infos)
	}
}
