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
