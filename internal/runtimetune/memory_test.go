package runtimetune

import (
	"math"
	"runtime/debug"
	"testing"
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
