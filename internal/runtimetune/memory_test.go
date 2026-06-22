package runtimetune

import (
	"context"
	"math"
	"runtime/debug"
	"testing"
	"time"
)

func TestApply_SetsMemoryLimitAndGC(t *testing.T) {
	// Ensure defaults are exercised (no env overrides).
	t.Setenv("GOMEMLIMIT", "")
	t.Setenv("HERMOD_MEMORY_LIMIT_MB", "")
	t.Setenv("GOGC", "")

	// Restore the runtime settings after the test to avoid leaking state.
	origLimit := debug.SetMemoryLimit(-1)
	origGC := debug.SetGCPercent(100)
	t.Cleanup(func() {
		debug.SetMemoryLimit(origLimit)
		debug.SetGCPercent(origGC)
	})

	Apply()

	gotLimit := debug.SetMemoryLimit(-1)
	wantLimit := defaultMemoryLimitMB * bytesPerMB
	if gotLimit != wantLimit {
		t.Fatalf("memory limit = %d; want %d", gotLimit, wantLimit)
	}

	gotGC := debug.SetGCPercent(defaultGCPercent)
	if gotGC != defaultGCPercent {
		t.Fatalf("gc percent = %d; want %d", gotGC, defaultGCPercent)
	}
}

func TestApply_RespectsGOMEMLIMIT(t *testing.T) {
	t.Setenv("GOMEMLIMIT", "1GiB")
	t.Setenv("GOGC", "")

	// math.MaxInt64 is the runtime's "no limit" sentinel; capture and restore.
	origLimit := debug.SetMemoryLimit(math.MaxInt64)
	origGC := debug.SetGCPercent(100)
	t.Cleanup(func() {
		debug.SetMemoryLimit(origLimit)
		debug.SetGCPercent(origGC)
	})

	applyMemoryLimit()

	got := debug.SetMemoryLimit(-1)
	if got != math.MaxInt64 {
		t.Fatalf("memory limit was modified despite GOMEMLIMIT being set: got %d", got)
	}
}

func TestScavengeIntervalFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want time.Duration
	}{
		{"Unset", "", defaultScavengeInterval},
		{"Valid", "90s", 90 * time.Second},
		{"Invalid", "not-a-duration", defaultScavengeInterval},
		{"Zero", "0s", defaultScavengeInterval},
		{"Negative", "-5s", defaultScavengeInterval},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HERMOD_SCAVENGE_INTERVAL", tc.env)
			if got := scavengeIntervalFromEnv(); got != tc.want {
				t.Fatalf("scavengeIntervalFromEnv() = %v; want %v", got, tc.want)
			}
		})
	}
}

func TestStartScavenger_StopsOnContextCancel(t *testing.T) {
	t.Setenv("HERMOD_SCAVENGE_DISABLE", "")
	// Keep the cadence long so the test never actually triggers FreeOSMemory;
	// we only assert the goroutine respects cancellation.
	t.Setenv("HERMOD_SCAVENGE_INTERVAL", "1h")

	ctx, cancel := context.WithCancel(t.Context())
	StartScavenger(ctx)
	cancel()

	done := make(chan struct{})
	go func() {
		// scavengeLoop returns promptly once ctx is done; run it directly to
		// observe the exit deterministically.
		cancelledCtx, c := context.WithCancel(t.Context())
		c()
		scavengeLoop(cancelledCtx, time.Hour)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scavengeLoop did not exit after context cancellation")
	}
}

func TestStartScavenger_DisabledIsNoop(t *testing.T) {
	t.Setenv("HERMOD_SCAVENGE_DISABLE", "1")
	// Should return without spawning a goroutine; nothing to assert beyond not
	// panicking and respecting the disable flag.
	StartScavenger(t.Context())
}
