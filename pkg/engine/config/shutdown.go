package config

import (
	"os"
	"strings"
	"time"
)

// ShutdownBudget is how long each stage of a graceful stop may take.
//
// These used to be independent magic numbers scattered across the worker, the
// registry and the engine — 60s for worker cleanup, 45s for StopAll, 35s for a
// single workflow stop, 10s to drain plus 10s of grace. Nothing related them,
// and the outermost was double the innermost, so the layers did not nest: a
// stage could be cut off by a *parent* deadline it had never heard of, halfway
// through the drain that exists to avoid losing data.
//
// Worse, the total exceeded the orchestrator's patience. Kubernetes sends
// SIGKILL after terminationGracePeriodSeconds — 30 seconds by default — so a
// 60-second cleanup was killed mid-drain, discarding exactly the messages the
// drain protects. A pipeline that stops cleanly on a laptop lost data on every
// rolling deploy.
//
// One total now governs everything and the stages are derived from it, so the
// nesting is arithmetic rather than a promise. The default sits below the
// default grace period with margin to spare.
type ShutdownBudget struct {
	// Total bounds the whole stop. Keep it under the orchestrator's grace
	// period, or the process is killed rather than allowed to finish.
	Total time.Duration
	// PerEngine bounds stopping one workflow, and the parallel StopAll over all
	// of them.
	PerEngine time.Duration
	// Drain bounds sink writes once shutdown has begun.
	Drain time.Duration
	// Grace is the extra time writers get to unwind after Drain expires. Their
	// write contexts are already cancelled by then, so this only covers
	// returning, not working.
	Grace time.Duration
}

// Defaults. Total is deliberately below Kubernetes' 30s default grace period:
// being killed at 30s while still draining is worse than giving up at 25s and
// leaving the remainder unacknowledged for the next start to replay.
const (
	defaultShutdownTotal = 25 * time.Second

	// Fractions of Total. PerEngine leaves room for the process to finish
	// closing storage; Drain leaves room for Grace inside PerEngine.
	perEngineFraction = 80 // percent of Total
	drainFraction     = 55 // percent of Total
	graceFraction     = 20 // percent of Total
)

// Shutdown returns the budget, honouring HERMOD_SHUTDOWN_TIMEOUT.
//
// Raising it is correct when the orchestrator's grace period has been raised to
// match; lowering it trades drain completeness for a faster stop. A value that
// cannot be parsed falls back to the default rather than failing startup — a
// typo in a tuning knob should not stop a worker from booting.
func Shutdown() ShutdownBudget {
	total := defaultShutdownTotal
	if raw := strings.TrimSpace(os.Getenv("HERMOD_SHUTDOWN_TIMEOUT")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			total = d
		}
	}
	return budgetFrom(total)
}

// budgetFrom derives the stages from a total. Split out so the nesting
// invariant can be tested across a range of totals rather than just the
// default.
func budgetFrom(total time.Duration) ShutdownBudget {
	if total <= 0 {
		total = defaultShutdownTotal
	}
	b := ShutdownBudget{
		Total:     total,
		PerEngine: total * perEngineFraction / 100,
		Drain:     total * drainFraction / 100,
		Grace:     total * graceFraction / 100,
	}
	// Floors keep a very small total from producing budgets so tight that
	// nothing can finish, which would make every stop look like a failure.
	if b.PerEngine < time.Second {
		b.PerEngine = time.Second
	}
	if b.Drain < 500*time.Millisecond {
		b.Drain = 500 * time.Millisecond
	}
	if b.Grace < 250*time.Millisecond {
		b.Grace = 250 * time.Millisecond
	}
	return b
}

// ClampDrain fits an operator-configured DrainTimeout inside the budget.
//
// SinkConfig.DrainTimeout is user-facing and predates this budget, so it can be
// set larger than the whole shutdown. Honour it when it fits and clamp when it
// does not, rather than letting a per-sink setting silently overrun the
// process-wide deadline.
func (b ShutdownBudget) ClampDrain(configured time.Duration) time.Duration {
	if configured <= 0 || configured > b.Drain {
		return b.Drain
	}
	return configured
}
