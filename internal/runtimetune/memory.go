// Package runtimetune applies process-wide runtime tuning (soft memory limit
// and GC behaviour) so Hermod stays within its lightweight, low-memory target
// and returns unused memory to the operating system promptly.
package runtimetune

import (
	"os"
	"runtime/debug"
	"strconv"
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
