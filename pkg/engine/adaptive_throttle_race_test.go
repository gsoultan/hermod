// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package engine

import (
	"context"
	"sync"
	"testing"
	"time"
)

// adaptiveThrottle runs in the deferred tail of every processed message, and a
// workflow processes on up to MaxInflight (128) goroutines at once. The
// five-second adjustment window is guarded by e.lastPollAdjust, which was a
// plain time.Time read and written by all of them: a data race under -race,
// and outside it several workers passed the "five seconds elapsed" check
// together and each added its own 100ms step, so one window could throttle
// ingestion by seconds instead of 100ms.
func TestAdaptiveThrottleConcurrentWorkersAdjustOncePerWindow(t *testing.T) {
	e := NewEngine(nil, nil, nil)
	cfg := e.config
	cfg.AdaptiveThroughput = true
	e.SetConfig(cfg)

	const workers = 128
	ctx := context.Background()

	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	for range workers {
		done.Go(func() {
			start.Wait()
			// Well over the 500ms threshold, so every adjustment that runs
			// adds a throttle step.
			e.adaptiveThrottle(ctx, 2*time.Second)
		})
	}
	start.Done()
	done.Wait()

	e.mu.RLock()
	delay := e.throttleDelay
	e.mu.RUnlock()

	if delay != 100*time.Millisecond {
		t.Fatalf("throttleDelay = %v after one burst of %d concurrent workers; want exactly one 100ms step per 5s window",
			delay, workers)
	}
}
