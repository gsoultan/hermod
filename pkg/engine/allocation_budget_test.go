package engine

// An allocation budget for the engine on its own — no workflow, no evaluator,
// just source to sink. The workflow-level budget lives in
// internal/engine/registry and covers a real pipeline; this one isolates the
// engine so a regression can be attributed to one side or the other.
//
// It budgets allocations rather than time on purpose: across three measurement
// sessions the allocation counts held within 0-2% while throughput swung
// 5-56% with machine load. A time gate on shared CI hardware would be a flake
// generator; this is not.

import (
	"runtime"
	"strconv"
	"testing"

	"github.com/gsoultan/hermod/pkg/engine/config"
	"github.com/gsoultan/hermod/pkg/infra/tracing"
)

// engineBudgetHeadroom is how far over the recorded figure a run may go before
// this fails. Wide enough for noise and small honest changes, far too tight
// for a per-message marshal to reappear.
const engineBudgetHeadroom = 1.25

type engineAllocBudget struct {
	payloadBytes int
	// perMessage is the measured allocations per message at the time of
	// writing. Move it with the change that moves it, and say why in the
	// commit message.
	perMessage float64
}

// Measured 2026-09-19 on Apple M5 Pro, go1.27.1, from BenchmarkEngineThroughput
// with benchLogger reporting DebugEnabled() == false, which is what every
// logger the engine actually runs with reports by default.
var engineAllocBudgets = []engineAllocBudget{
	{payloadBytes: 64, perMessage: 28},
	{payloadBytes: 1024, perMessage: 28},
	{payloadBytes: 16384, perMessage: 29},
}

func TestEngineAllocationBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("drives 50k messages through the engine per case; runs in the non-short CI job")
	}

	const messages = 50_000

	// A TracerProvider installed by an earlier test in this binary would make
	// this figure meaningless: span creation is gated on there being one, so
	// the measurement would include spans a production default never builds.
	// Loud rather than silent, because test order is not something this file
	// controls.
	if tracing.Installed() {
		t.Skip("a TracerProvider is installed in this process, so the per-message figure would " +
			"include spans the default configuration never creates; run this test on its own")
	}

	for _, budget := range engineAllocBudgets {
		t.Run("payload="+strconv.Itoa(budget.payloadBytes)+"B", func(t *testing.T) {
			got := engineAllocationsPerMessage(t, messages, budget.payloadBytes)
			limit := budget.perMessage * engineBudgetHeadroom

			t.Logf("%.1f allocations per message (recorded %.0f, limit %.0f)", got, budget.perMessage, limit)

			if got > limit {
				t.Errorf(`%.1f allocations per message at a %d-byte payload; the budget is %.0f (+%.0f%% headroom over the recorded %.0f).

Something on the engine's per-message path started allocating. Profile it:
  go test ./pkg/engine -bench='BenchmarkEngineThroughput$' -benchtime=1x -run='^$' \
    -memprofile=/tmp/mem.prof -o /tmp/engine.test
  go tool pprof -sample_index=alloc_space -list='Engine..writeToSink$' /tmp/engine.test /tmp/mem.prof

Prefer -list over -top: the top view names the allocator (bytes.Clone,
encoding/json), which is true and rarely the useful answer.

If the increase is deliberate, move the perMessage figure in
engineAllocBudgets to what you measured and say why in the commit message.`,
					got, budget.payloadBytes, limit, (engineBudgetHeadroom-1)*100, budget.perMessage)
			}
		})
	}
}

func engineAllocationsPerMessage(t *testing.T, messages int64, payloadBytes int) float64 {
	t.Helper()

	cfg := config.DefaultConfig()
	sinkCfg := config.SinkConfig{BackpressureBuffer: 4096}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	runThroughput(t, messages, payloadBytes, cfg, sinkCfg, false)

	runtime.ReadMemStats(&after)
	return float64(after.Mallocs-before.Mallocs) / float64(messages)
}
