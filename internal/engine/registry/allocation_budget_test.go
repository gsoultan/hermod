package registry

// An allocation budget for a real workflow.
//
// Twice now a single log line has quietly cost a third of everything the
// pipeline allocated, because Go evaluates a call's arguments whatever the
// logger then does with them, and in both cases the argument marshalled the
// message. Neither was visible in a test, a lint, or a review — only in an
// allocation profile somebody happened to take.
//
// This is the thing that would have caught them. It budgets *allocations*, not
// time: across three separate measurement sessions the allocation counts held
// within 0-2% while throughput swung 5-56% with machine load, so a time-based
// gate on shared CI hardware would be a flake generator and this is not.
//
// It is deliberately coarse. The point is to catch a regression that doubles
// the cost, not to police a 5% drift.

import (
	"runtime"
	"testing"
)

// budgetHeadroom is how far over the recorded figure a run may go before this
// fails. Wide enough that ordinary noise and small honest changes pass; far
// too tight for a whole-message marshal to sneak back in.
const budgetHeadroom = 1.25

type allocBudget struct {
	cols int
	// perMessage is the measured allocations per message at the time of
	// writing. Update it together with the change that moves it, and say in
	// the commit message why it moved.
	perMessage float64
}

// Measured 2026-09-19 on Apple M5 Pro, go1.27.1, from BenchmarkWorkflowThroughput.
var workflowAllocBudgets = []allocBudget{
	{cols: 8, perMessage: 116},
	{cols: 32, perMessage: 158},
	{cols: 128, perMessage: 326},
}

func TestWorkflowAllocationBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("drives 20k messages through a real workflow per case; runs in the non-short CI job")
	}

	const messages = 20_000

	for _, b := range workflowAllocBudgets {
		t.Run(columnsName(b.cols), func(t *testing.T) {
			got := allocationsPerMessage(t, messages, b.cols)
			limit := b.perMessage * budgetHeadroom

			t.Logf("%.1f allocations per message (recorded %.0f, limit %.0f)", got, b.perMessage, limit)

			if got > limit {
				t.Errorf(`%.1f allocations per message at %d columns; the budget is %.0f (+%.0f%% headroom over the recorded %.0f).

Something on the per-message path started allocating. The usual cause is a value
built for a consumer that then discards it -- a log line whose arguments are
evaluated whatever the level does with them is how this happened twice before.

To find it:
  go test ./internal/engine/registry -bench='BenchmarkWorkflowThroughput/cols=%d' \
    -benchtime=1x -run='^$' -memprofile=/tmp/mem.prof -o /tmp/wf.test
  go tool pprof -sample_index=alloc_objects -top /tmp/wf.test /tmp/mem.prof

Use -list='<func>' on the top entries; the -top view names the allocator, not
the caller, which is rarely the useful answer.

If the increase is deliberate, move the perMessage figure in
workflowAllocBudgets to what you measured and say why in the commit message.`,
					got, b.cols, limit, (budgetHeadroom-1)*100, b.perMessage, b.cols)
			}
		})
	}
}

// allocationsPerMessage runs one full workflow and reports how many heap
// allocations it took per message.
//
// It counts process-wide mallocs rather than using testing.AllocsPerRun,
// because the work happens across the engine's goroutines and not on the
// caller's. That makes the figure inclusive of everything the pipeline does,
// which is the point.
func allocationsPerMessage(t *testing.T, messages, cols int) float64 {
	t.Helper()

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	runWorkflowOnce(t, messages, cols, 0)

	runtime.ReadMemStats(&after)
	return float64(after.Mallocs-before.Mallocs) / float64(messages)
}

func columnsName(cols int) string {
	switch cols {
	case 8:
		return "cols=8"
	case 32:
		return "cols=32"
	case 128:
		return "cols=128"
	}
	return "cols=?"
}
