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
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gsoultan/hermod"
)

// A destination that answers busy -- no free connection, too many clients, a
// rate limit -- is up and has no room right now. Waiting is what fixes that,
// so a busy answer is waited out instead of being treated as a failure:
//
//   - it does not use up the sink's MaxRetries, so a destination busy for
//     longer than the retry budget no longer sends messages to the dead letter;
//   - the circuit breaker never hears of it, so a busy sink is not shown as
//     error:circuit_breaker_open;
//   - it counts as a sign of life for the stall watchdog, so the supervisor
//     does not restart a workflow whose destination is answering, which would
//     only add a reconnect to a destination with no room.
//
// The workflow gets slower and its sink shows "waiting". A destination that
// does not answer at all is still a stall, and an ordinary error is still
// retried and counted as before.
const (
	// busyBackoffFloor is the first wait when the sink's own retry interval
	// is shorter: a busy destination is not helped by being asked again in a
	// few milliseconds.
	busyBackoffFloor = 50 * time.Millisecond
	// busyBackoffCap bounds the wait between attempts, so a destination that
	// frees up is used again within half a minute.
	busyBackoffCap = 30 * time.Second
	// retryAfterCap bounds what a destination may ask for with Retry-After.
	retryAfterCap = 5 * time.Minute
)

// busyWaits is an engine's record of busy answers.
type busyWaits struct {
	answers atomic.Uint64
	mu      sync.Mutex
	waiting map[string]int
}

// begin records that a write to sinkID has started waiting.
func (b *busyWaits) begin(sinkID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.waiting == nil {
		b.waiting = make(map[string]int)
	}
	b.waiting[sinkID]++
}

// end records that a write to sinkID has stopped waiting.
func (b *busyWaits) end(sinkID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.waiting[sinkID]--; b.waiting[sinkID] <= 0 {
		delete(b.waiting, sinkID)
	}
}

// on reports whether any write to sinkID is waiting now.
func (b *busyWaits) on(sinkID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.waiting[sinkID] > 0
}

// any reports whether any write is waiting now.
func (b *busyWaits) any() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.waiting) > 0
}

// waitOutBusy runs attempt until it answers anything other than busy, and
// returns that answer, or the context's error if the context ends first.
// base is the sink's retry interval, where the waits start. While it waits,
// the sink shows "waiting".
func (e *Engine) waitOutBusy(ctx context.Context, sinkID string, base time.Duration, attempt func() error) error {
	return e.waitOut(ctx, sinkID, base, attempt, func(waiting bool, err error) {
		e.setSinkStatus(sinkID, sinkStatusWhenUp(waiting))
		if waiting {
			e.logger.Warn("Sink is busy; waiting for room rather than failing the write",
				"workflow_id", e.workflowID, "sink_id", sinkID, "error", err)
		}
	})
}

// WaitOutBusy waits out a busy workflow step, such as a lookup whose database
// has no free connection, the way a busy sink is waited out: the step is
// slower, it is not failed, and the stall watchdog does not count the wait as
// being stuck. The engine offers itself to the steps it runs through the
// context (hermod.WithBusyWaiter). It implements hermod.BusyWaiter.
func (e *Engine) WaitOutBusy(ctx context.Context, name string, attempt func() error) error {
	return e.waitOut(ctx, name, busyBackoffFloor, attempt, func(waiting bool, err error) {
		if waiting {
			e.logger.Warn("Step is busy; waiting for room rather than failing the message",
				"workflow_id", e.workflowID, "step", name, "error", err)
		}
	})
}

// waitOut is the loop behind both: it repeats attempt while it answers busy,
// with backoff, and calls changed when the wait starts (true) and ends.
func (e *Engine) waitOut(ctx context.Context, key string, base time.Duration, attempt func() error, changed func(waiting bool, err error)) error {
	wait := max(base, busyBackoffFloor)
	waiting := false
	defer func() {
		if waiting {
			e.busy.end(key)
			changed(false, nil)
		}
	}()
	for {
		err := attempt()
		if !errors.Is(err, hermod.ErrBusy) {
			return err
		}
		e.busy.answers.Add(1)
		if !waiting {
			waiting = true
			e.busy.begin(key)
			changed(true, err)
		}
		delay := busyDelay(err, wait)
		wait = min(wait*2, busyBackoffCap)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// busyDelay is how long to wait after a busy answer: what the destination
// asked for, when it said, otherwise wait with jitter so that writers that
// were refused together do not all come back together.
func busyDelay(err error, wait time.Duration) time.Duration {
	if d, ok := hermod.RetryAfter(err); ok {
		return min(d, retryAfterCap)
	}
	return time.Duration(float64(wait) * (0.5 + rand.Float64())) //nolint:gosec // G404: jitter only spreads retries out.
}

// sinkStatusWhenUp is the status of a sink that answers its health check.
func sinkStatusWhenUp(waiting bool) string {
	if waiting {
		return "waiting"
	}
	return "running"
}

var _ hermod.BusyWaiter = (*Engine)(nil)
