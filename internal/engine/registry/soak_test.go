package registry

import (
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/user/hermod/pkg/comm/message"
)

// ---------------------------------------------------------------------------
// Soak: resource behaviour over time rather than in a snapshot.
//
// Leaks do not show up in a thirty-second test. The sub-source reader goroutine
// that ignored cancellation looked perfectly healthy in isolation and only
// became obvious as a straight line upwards across repeated start/stop cycles;
// the same is true of heap growth from a pool that is fed but never drained.
// What distinguishes a leak from normal variation is the *trend*, so this
// samples over a sustained run and compares the second half against the first.
//
// Off by default — it is measured in minutes. Enable with:
//
//	HERMOD_SOAK=1 go test -run TestSoak ./internal/engine/registry/
//	HERMOD_SOAK=1 HERMOD_SOAK_DURATION=30m go test -run TestSoak -timeout 45m ./...
// ---------------------------------------------------------------------------

// resourceSample is one observation of the process's resource use.
type resourceSample struct {
	at         time.Time
	goroutines int
	heapAlloc  uint64
	heapObjs   uint64
}

func sampleResources() resourceSample {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return resourceSample{
		at:         time.Now(),
		goroutines: runtime.NumGoroutine(),
		heapAlloc:  ms.HeapAlloc,
		heapObjs:   ms.HeapObjects,
	}
}

// soakDuration is how long the soak runs; short enough by default to be usable
// on a laptop, overridable for an overnight run.
func soakDuration(t *testing.T) time.Duration {
	t.Helper()
	if raw := os.Getenv("HERMOD_SOAK_DURATION"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			t.Fatalf("HERMOD_SOAK_DURATION=%q is not a duration: %v", raw, err)
		}
		return d
	}
	return 2 * time.Minute
}

func requireSoak(t *testing.T) {
	t.Helper()
	if os.Getenv("HERMOD_SOAK") == "" {
		t.Skip("soak test: set HERMOD_SOAK=1 to run (takes minutes)")
	}
}

// TestSoakWorkflowChurnDoesNotLeak repeatedly starts, drains and stops
// workflows — the shape of a busy platform where pipelines are edited, paused
// and resumed all day. Every cycle builds sources, sinks, engines, buffers and
// goroutines and must give all of them back.
func TestSoakWorkflowChurnDoesNotLeak(t *testing.T) {
	requireSoak(t)

	duration := soakDuration(t)
	deadline := time.Now().Add(duration)

	// Warm up so one-time initialisation is not counted as growth.
	{
		store := newPipeStorage()
		reg := NewRegistry(store)
		top := soakTopology(0)
		stop := startMultiPipeline(t, reg, top.workflow, top.sources, top.sinks)
		waitUntil(t, 20*time.Second, "warmup drain", func() bool {
			for _, s := range top.sinks {
				if s.distinct() < top.wantPerSnk {
					return false
				}
			}
			return true
		})
		stop()
		reg.Close()
	}
	time.Sleep(time.Second)

	overReleasesBefore := message.OverReleaseCount()
	var samples []resourceSample
	samples = append(samples, sampleResources())

	cycles := 0
	for time.Now().Before(deadline) {
		store := newPipeStorage()
		reg := NewRegistry(store)
		top := soakTopology(cycles + 1)

		stop := startMultiPipeline(t, reg, top.workflow, top.sources, top.sinks)
		drained := waitUntil(t, 30*time.Second, "soak cycle drain", func() bool {
			for _, s := range top.sinks {
				if s.distinct() < top.wantPerSnk {
					return false
				}
			}
			return true
		})
		stop()
		reg.Close()

		if !drained {
			t.Fatalf("cycle %d did not drain; the pipeline stalled during the soak", cycles)
		}

		cycles++
		samples = append(samples, sampleResources())
	}

	if cycles < 4 {
		t.Fatalf("only %d cycles completed in %v; the soak needs more time to be meaningful", cycles, duration)
	}

	reportSoak(t, samples, cycles)

	if n := message.OverReleaseCount() - overReleasesBefore; n != 0 {
		t.Errorf("%d message over-release(s) during the soak", n)
	}
}

// soakTopology is a fixed, non-trivial shape: fan-in from three sources through
// a two-stage transformation chain, fanning out to three sinks. Fixed rather
// than random so a soak run is comparable against the previous one.
//
// The workflow id is deliberately stable across cycles. Production churn is the
// *same* pipeline being paused, edited and resumed; a fresh id per cycle would
// instead measure Prometheus label cardinality, since most engine metrics are
// labelled by workflow_id and a series is never reclaimed. That is a real (and
// separate) concern — see TestSoakDistinctWorkflowIDsGrowMetricCardinality —
// but it is not what this test is for.
func soakTopology(idx int) topology {
	perSource := 40
	wf := multiPipelineWorkflow("wf-soak-stable", []string{"x", "y", "z"})
	_ = idx
	sources := map[string]*pipeSource{
		"s-a": {name: "a", count: perSource},
		"s-b": {name: "b", count: perSource},
		"s-c": {name: "c", count: perSource},
	}
	sinks := map[string]*pipeSink{
		"snk-x": {name: "x"}, "snk-y": {name: "y"}, "snk-z": {name: "z"},
	}
	return topology{
		workflow: wf, sources: sources, sinks: sinks,
		perSource: perSource, wantPerSnk: perSource * 3,
	}
}

// reportSoak compares the second half of the run against the first. A leak is a
// trend, not a single high reading: comparing halves tolerates GC timing and
// ordinary variation while still catching steady growth. Absolute thresholds
// would either be too loose to catch a slow leak or too tight to survive a
// noisy CI machine.
func reportSoak(t *testing.T, samples []resourceSample, cycles int) {
	t.Helper()
	if len(samples) < 4 {
		t.Fatalf("only %d samples; not enough to judge a trend", len(samples))
	}

	mid := len(samples) / 2
	firstHalf, secondHalf := samples[:mid], samples[mid:]

	avgGoroutines := func(ss []resourceSample) float64 {
		var total int
		for _, s := range ss {
			total += s.goroutines
		}
		return float64(total) / float64(len(ss))
	}
	avgHeap := func(ss []resourceSample) float64 {
		var total uint64
		for _, s := range ss {
			total += s.heapAlloc
		}
		return float64(total) / float64(len(ss))
	}

	g1, g2 := avgGoroutines(firstHalf), avgGoroutines(secondHalf)
	h1, h2 := avgHeap(firstHalf), avgHeap(secondHalf)

	t.Logf("soak: %d cycles over %v", cycles, samples[len(samples)-1].at.Sub(samples[0].at).Round(time.Second))
	t.Logf("  goroutines: first half avg %.1f -> second half avg %.1f (%+.1f)", g1, g2, g2-g1)
	t.Logf("  heap alloc: first half avg %.1f MiB -> second half avg %.1f MiB (%+.1f%%)",
		h1/(1<<20), h2/(1<<20), (h2-h1)/h1*100)
	t.Logf("  final: %d goroutines, %.1f MiB heap, %d heap objects",
		samples[len(samples)-1].goroutines,
		float64(samples[len(samples)-1].heapAlloc)/(1<<20),
		samples[len(samples)-1].heapObjs)

	// Goroutines are the sharpest signal: churn should return every one, so the
	// steady-state count must not drift upward with cycle count. Allow a small
	// fixed slack for runtime bookkeeping, not a proportional one.
	if g2 > g1+float64(soakGoroutineSlack()) {
		t.Errorf("goroutine count trended upward across the soak: %.1f -> %.1f (slack %d). "+
			"Something started per cycle is not being stopped.", g1, g2, soakGoroutineSlack())
	}

	// Heap is noisier — pools retain by design — so this only fails on growth
	// large enough to be a real accumulation rather than pool retention.
	if h1 > 0 && h2 > h1*1.5 {
		t.Errorf("heap grew %.0f%% between the first and second half of the soak "+
			"(%.1f MiB -> %.1f MiB); something is being retained per cycle",
			(h2-h1)/h1*100, h1/(1<<20), h2/(1<<20))
	}
}

// soakGoroutineSlack is the tolerated absolute drift. Overridable because CI
// machines vary in how eagerly the runtime reaps.
func soakGoroutineSlack() int {
	if raw := os.Getenv("HERMOD_SOAK_GOROUTINE_SLACK"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			return n
		}
	}
	return 10
}

// TestSoakSustainedThroughputDoesNotLeak keeps one workflow running under
// continuous load, which is the other production shape: not churn, but a
// pipeline that has been up for weeks. Per-message accumulation that churn
// hides — a trace buffer, a dedup map, a sample cache — shows up here.
func TestSoakSustainedThroughputDoesNotLeak(t *testing.T) {
	requireSoak(t)

	duration := soakDuration(t)

	store := newPipeStorage()
	reg := NewRegistry(store)
	t.Cleanup(reg.Close)

	// A source that keeps producing for the whole run.
	const perSource = 1 << 30 // effectively unbounded
	sources := map[string]*pipeSource{
		"s-a": {name: "a", count: perSource},
		"s-b": {name: "b", count: perSource},
	}
	sinks := map[string]*pipeSink{"snk-x": {name: "x"}, "snk-y": {name: "y"}}

	wf := multiPipelineWorkflow("wf-soak-sustained", []string{"x", "y"})
	// Drop the two unused source nodes from the standard three-source shape.
	var nodes = wf.Nodes[:0]
	for _, n := range wf.Nodes {
		if n.ID == "src-c" {
			continue
		}
		nodes = append(nodes, n)
	}
	keptEdges := wf.Edges[:0]
	for _, e := range wf.Edges {
		if e.SourceID == "src-c" {
			continue
		}
		keptEdges = append(keptEdges, e)
	}
	wf.Nodes, wf.Edges = nodes, keptEdges

	overReleasesBefore := message.OverReleaseCount()
	stop := startMultiPipeline(t, reg, wf, sources, sinks)
	defer stop()

	// Let it reach steady state before the first sample, so ramp-up is not read
	// as growth.
	time.Sleep(5 * time.Second)

	var samples []resourceSample
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		samples = append(samples, sampleResources())
		time.Sleep(duration / 12)
	}
	samples = append(samples, sampleResources())

	delivered := sinks["snk-x"].count()
	if delivered == 0 {
		t.Fatal("no messages were delivered during the sustained soak")
	}
	t.Logf("sustained soak delivered %d messages to sink x", delivered)

	reportSoak(t, samples, len(samples))

	if n := message.OverReleaseCount() - overReleasesBefore; n != 0 {
		t.Errorf("%d message over-release(s) during the sustained soak", n)
	}
}
