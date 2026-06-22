// Package runtimetune applies process-wide runtime tuning (soft memory limit
// and GC behaviour) so Hermod stays within its lightweight, low-memory target
// and returns unused memory to the operating system promptly.
package runtimetune

import (
	"context"
	"os"
	"runtime/debug"
	"strconv"
	"time"
)

const (
	// defaultMemoryLimitMB is the soft memory limit applied when neither the
	// standard GOMEMLIMIT nor HERMOD_MEMORY_LIMIT_MB is configured. It targets
	// the documented lightweight budget (max ~500 MB) with headroom.
	defaultMemoryLimitMB int64 = 450

	// defaultGCPercent makes the garbage collector run more aggressively than
	// the Go default of 100, trading a little CPU for a substantially smaller
	// resident heap. It is only applied when GOGC is not set explicitly.
	defaultGCPercent = 50

	bytesPerMB = 1 << 20

	// defaultScavengeInterval is how often the idle scavenger returns unused
	// memory to the operating system. A messaging daemon is rarely allocation
	// bound, so a couple of minutes keeps the reported resident set close to the
	// real live set without meaningful CPU cost.
	defaultScavengeInterval = 2 * time.Minute
)

// Apply configures the soft memory limit and GC percentage for the process.
//
// Precedence for the memory limit:
//  1. If GOMEMLIMIT is set, the Go runtime already honours it; we do nothing.
//  2. Else if HERMOD_MEMORY_LIMIT_MB is set to a positive integer, it is used.
//  3. Otherwise defaultMemoryLimitMB is applied.
//
// GC percentage is left untouched when GOGC is set; otherwise it is lowered to
// defaultGCPercent to keep the heap small.
func Apply() {
	applyMemoryLimit()
	applyGCPercent()
}

func applyMemoryLimit() {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}

	limitMB := defaultMemoryLimitMB
	if v := os.Getenv("HERMOD_MEMORY_LIMIT_MB"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			limitMB = n
		}
	}

	debug.SetMemoryLimit(limitMB * bytesPerMB)
}

func applyGCPercent() {
	if os.Getenv("GOGC") != "" {
		return
	}
	debug.SetGCPercent(defaultGCPercent)
}

// StartScavenger launches a background goroutine that periodically returns
// freed memory to the operating system via debug.FreeOSMemory, so the resident
// set drops during quiet periods instead of clinging to its peak. The goroutine
// exits when ctx is cancelled, giving it a clear, cancellable lifecycle.
//
// It is a no-op when HERMOD_SCAVENGE_DISABLE is set, and the cadence can be
// tuned with HERMOD_SCAVENGE_INTERVAL (a Go duration such as "90s").
func StartScavenger(ctx context.Context) {
	if os.Getenv("HERMOD_SCAVENGE_DISABLE") != "" {
		return
	}
	go scavengeLoop(ctx, scavengeIntervalFromEnv())
}

func scavengeLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			debug.FreeOSMemory()
		}
	}
}

func scavengeIntervalFromEnv() time.Duration {
	if v := os.Getenv("HERMOD_SCAVENGE_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultScavengeInterval
}
