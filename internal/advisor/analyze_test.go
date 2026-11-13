// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package advisor

import (
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

var t0 = time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)

// series builds n samples 30s apart; step fills sample i from the previous one.
func series(n int, step func(i int, s *Sample)) []Sample {
	out := make([]Sample, 0, n)
	for i := range n {
		s := Sample{
			At:          t0.Add(time.Duration(i) * 30 * time.Second),
			NodeRuns:    map[string]uint64{},
			NodeErrors:  map[string]uint64{},
			NodeLatency: map[string]time.Duration{},
			SinkFill:    map[string]float64{},
			SinkBreaker: map[string]string{},
			EdgeCounts:  map[string]uint64{},
		}
		step(i, &s)
		out = append(out, s)
	}
	return out
}

// chain is src -> lookup -> filter -> out(sink snk-1).
func chain() Flow {
	return Flow{
		ID:              "wf-1",
		TraceSampleRate: 0.05,
		MaxInflight:     128,
		Nodes: []Node{
			{ID: "src", Type: "source"},
			{ID: "lookup", Type: "db_lookup", Label: "Find customer", Config: map[string]any{"transType": "db_lookup"}},
			{ID: "filter", Type: "condition", Label: "Only paid"},
			{ID: "out", Type: "sink", RefID: "snk-1"},
		},
		Edges: []Edge{
			{ID: "e1", From: "src", To: "lookup"},
			{ID: "e2", From: "lookup", To: "filter"},
			{ID: "e3", From: "filter", To: "out"},
		},
		Sinks: map[string]SinkSettings{"snk-1": {Name: "Warehouse", BatchSize: 0}},
	}
}

func find(r Report, rule string) *Finding {
	for i := range r.Findings {
		if r.Findings[i].Rule == rule {
			return &r.Findings[i]
		}
	}
	return nil
}

func TestAnalyzeWaitsForEnoughData(t *testing.T) {
	samples := series(2, func(i int, s *Sample) { s.Processed = uint64(i * 10) })
	r := Analyze(samples, chain())
	if r.State != StateCollecting {
		t.Fatalf("state = %q, want %q", r.State, StateCollecting)
	}
	if find(r, RuleBottleneck) != nil {
		t.Fatal("a bottleneck was named from 30 seconds and 10 messages")
	}
}

func TestAnalyzeWithNoSamplesIsOff(t *testing.T) {
	r := Analyze(nil, chain())
	if r.State != StateNotRunning {
		t.Fatalf("state = %q, want %q", r.State, StateNotRunning)
	}
}

// steady is 10 minutes at 100 msg/s where the lookup takes 40ms and the
// filter 1ms, and the filter lets 20% through.
func steady(mut func(i int, s *Sample)) []Sample {
	return series(21, func(i int, s *Sample) {
		n := uint64(i * 3000)
		s.Processed = n
		s.Throughput = 100
		s.NodeRuns["src"] = n
		s.NodeRuns["lookup"] = n
		s.NodeRuns["filter"] = n
		s.NodeRuns["out"] = n / 5
		s.NodeLatency["lookup"] = 40 * time.Millisecond
		s.NodeLatency["filter"] = time.Millisecond
		s.NodeLatency["out"] = 2 * time.Millisecond
		s.EdgeCounts["e1"] = n
		s.EdgeCounts["e2"] = n
		s.EdgeCounts["e3"] = n / 5
		s.SinkFill["snk-1"] = 0.05
		s.SinkBreaker["snk-1"] = "closed"
		if mut != nil {
			mut(i, s)
		}
	})
}

func TestAnalyzeNamesTheBottleneckStep(t *testing.T) {
	r := Analyze(steady(nil), chain())
	if r.State != StateReady {
		t.Fatalf("state = %q, want ready", r.State)
	}
	f := find(r, RuleBottleneck)
	if f == nil {
		t.Fatalf("no bottleneck finding in %+v", r.Findings)
	}
	if f.NodeID != "lookup" {
		t.Fatalf("bottleneck = %q, want lookup", f.NodeID)
	}
	if r.BottleneckNodeID != "lookup" {
		t.Fatalf("report bottleneck = %q, want lookup", r.BottleneckNodeID)
	}
	if !strings.Contains(f.Title, "Find customer") {
		t.Fatalf("title %q does not name the step by its label", f.Title)
	}
	if !strings.Contains(f.Recommendation, "cache") {
		t.Fatalf("a slow lookup's advice should be about its cache, got %q", f.Recommendation)
	}
}

func TestAnalyzeSuggestsFilteringBeforeAnExpensiveStep(t *testing.T) {
	r := Analyze(steady(nil), chain())
	f := find(r, RuleFilterEarlier)
	if f == nil {
		t.Fatalf("no filter-earlier finding in %+v", r.Findings)
	}
	if f.NodeID != "filter" {
		t.Fatalf("node = %q, want filter", f.NodeID)
	}
	if !strings.Contains(f.Detail, "80%") {
		t.Fatalf("detail %q should say how much the filter drops", f.Detail)
	}
}

func TestAnalyzeDoesNotMoveAFilterThatKeepsMostMessages(t *testing.T) {
	r := Analyze(steady(func(_ int, s *Sample) {
		s.EdgeCounts["e3"] = s.EdgeCounts["e2"] * 9 / 10
	}), chain())
	if f := find(r, RuleFilterEarlier); f != nil {
		t.Fatalf("filter that keeps 90%% was flagged: %+v", f)
	}
}

func TestAnalyzeTurnsOnBatchingForAFullUnbatchedSink(t *testing.T) {
	r := Analyze(steady(func(_ int, s *Sample) { s.SinkFill["snk-1"] = 0.95 }), chain())
	f := find(r, RuleSinkBackpressure)
	if f == nil {
		t.Fatalf("no backpressure finding in %+v", r.Findings)
	}
	if f.SinkID != "snk-1" || !strings.Contains(f.Title, "Warehouse") {
		t.Fatalf("finding does not name the sink: %+v", f)
	}
	if !strings.Contains(f.Recommendation, "batch size to 100") {
		t.Fatalf("recommendation = %q", f.Recommendation)
	}
}

func TestAnalyzeNeverAdvisesABatchAboveMaxInflight(t *testing.T) {
	flow := chain()
	flow.Sinks["snk-1"] = SinkSettings{Name: "Warehouse", BatchSize: 100}
	r := Analyze(steady(func(_ int, s *Sample) { s.SinkFill["snk-1"] = 0.95 }), flow)
	f := find(r, RuleSinkBackpressure)
	if f == nil {
		t.Fatal("no backpressure finding")
	}
	if !strings.Contains(f.Recommendation, "128") || strings.Contains(f.Recommendation, "200") {
		t.Fatalf("batch advice must stop at max in-flight (128): %q", f.Recommendation)
	}

	flow.Sinks["snk-1"] = SinkSettings{Name: "Warehouse", BatchSize: 128}
	f = find(Analyze(steady(func(_ int, s *Sample) { s.SinkFill["snk-1"] = 0.95 }), flow), RuleSinkBackpressure)
	if f == nil || !strings.Contains(f.Recommendation, "shard") {
		t.Fatalf("at the in-flight limit the advice should be sharding, got %+v", f)
	}
}

func TestAnalyzeIgnoresABriefBufferSpike(t *testing.T) {
	r := Analyze(steady(func(i int, s *Sample) {
		if i == 7 {
			s.SinkFill["snk-1"] = 0.99
		}
	}), chain())
	if f := find(r, RuleSinkBackpressure); f != nil {
		t.Fatalf("one full reading in 21 was flagged: %+v", f)
	}
}

func TestAnalyzeReportsAnOpeningCircuitBreaker(t *testing.T) {
	r := Analyze(steady(func(i int, s *Sample) {
		if i == 5 || i == 6 || i == 15 {
			s.SinkBreaker["snk-1"] = "open"
		}
	}), chain())
	f := find(r, RuleSinkBreaker)
	if f == nil {
		t.Fatalf("no breaker finding in %+v", r.Findings)
	}
	if f.Severity != SeverityHigh {
		t.Fatalf("severity = %q", f.Severity)
	}
	if !strings.Contains(f.Detail, "2 times") {
		t.Fatalf("detail %q should count the openings, not the open readings", f.Detail)
	}
}

func TestAnalyzeFlagsAFailingStep(t *testing.T) {
	r := Analyze(steady(func(i int, s *Sample) { s.NodeErrors["lookup"] = uint64(i * 300) }), chain())
	f := find(r, RuleStepErrors)
	if f == nil || f.NodeID != "lookup" {
		t.Fatalf("no error finding for lookup in %+v", r.Findings)
	}
	if !strings.Contains(f.Detail, "10%") {
		t.Fatalf("detail %q should give the failure share", f.Detail)
	}
}

func TestAnalyzeUsesOnlyTheWindowNotTheLifetime(t *testing.T) {
	// 5000 failures happened before the window; none in it.
	r := Analyze(steady(func(_ int, s *Sample) { s.NodeErrors["lookup"] = 5000 }), chain())
	if f := find(r, RuleStepErrors); f != nil {
		t.Fatalf("old failures were reported as current: %+v", f)
	}
}

func TestAnalyzeReportsDeadLettersPiling(t *testing.T) {
	r := Analyze(steady(func(i int, s *Sample) { s.DeadLetters = uint64(i * 12) }), chain())
	f := find(r, RuleDeadLetters)
	if f == nil {
		t.Fatalf("no dead-letter finding in %+v", r.Findings)
	}
	if !strings.Contains(f.Detail, "240") {
		t.Fatalf("detail %q should give the count in the window", f.Detail)
	}
}

func TestAnalyzeReportsAStuckStep(t *testing.T) {
	r := Analyze(steady(func(i int, s *Sample) {
		if i == 20 {
			s.NodeBusy = map[string]telemetry.NodeBusy{"lookup": {InFlight: 3, SinceProgressMs: 95_000}}
		}
	}), chain())
	f := find(r, RuleStuckStep)
	if f == nil || f.NodeID != "lookup" || f.Severity != SeverityHigh {
		t.Fatalf("no stuck finding for lookup: %+v", r.Findings)
	}
}

func TestAnalyzeReportsGrowingSourceLag(t *testing.T) {
	r := Analyze(steady(func(i int, s *Sample) { s.Lag = uint64(1000 + i*5000) }), chain())
	if f := find(r, RuleSourceLag); f == nil {
		t.Fatalf("no lag finding in %+v", r.Findings)
	}
	r = Analyze(steady(func(i int, s *Sample) { s.Lag = 50_000 }), chain())
	if f := find(r, RuleSourceLag); f != nil {
		t.Fatalf("steady lag reported as growing: %+v", f)
	}
}

func TestAnalyzeFlagsALookupWithItsCacheOff(t *testing.T) {
	flow := chain()
	flow.Nodes[1].Config = map[string]any{"transType": "db_lookup", "ttl": "0"}
	r := Analyze(steady(nil), flow)
	f := find(r, RuleLookupCacheOff)
	if f == nil || f.NodeID != "lookup" {
		t.Fatalf("no cache-off finding: %+v", r.Findings)
	}
	r = Analyze(steady(nil), chain())
	if f := find(r, RuleLookupCacheOff); f != nil {
		t.Fatalf("a lookup with the default cache was flagged: %+v", f)
	}
}

func TestAnalyzeSuggestsTraceSamplingWhenOff(t *testing.T) {
	flow := chain()
	flow.TraceSampleRate = 0
	if f := find(Analyze(steady(nil), flow), RuleTraceSampling); f == nil || f.Severity != SeverityLow {
		t.Fatalf("no low trace-sampling finding: %+v", f)
	}
	if f := find(Analyze(steady(nil), chain()), RuleTraceSampling); f != nil {
		t.Fatalf("trace sampling already on but flagged: %+v", f)
	}
}

func TestAnalyzeRanksHighSeverityFirst(t *testing.T) {
	flow := chain()
	flow.TraceSampleRate = 0
	r := Analyze(steady(func(i int, s *Sample) {
		if i == 10 {
			s.SinkBreaker["snk-1"] = "open"
		}
	}), flow)
	if len(r.Findings) < 2 {
		t.Fatalf("findings = %+v", r.Findings)
	}
	if r.Findings[0].Severity != SeverityHigh {
		t.Fatalf("first finding is %q, not high", r.Findings[0].Severity)
	}
	if r.Findings[len(r.Findings)-1].Severity != SeverityLow {
		t.Fatalf("last finding is %q, not low", r.Findings[len(r.Findings)-1].Severity)
	}
}

func TestAnalyzeRestartsTheWindowWhenCountersGoBack(t *testing.T) {
	samples := steady(nil)
	// The engine restarted at sample 18: counters began again from zero.
	for i := 18; i < len(samples); i++ {
		samples[i].Processed = uint64((i - 18) * 10)
		samples[i].NodeRuns = map[string]uint64{"lookup": uint64((i - 18) * 10)}
	}
	r := Analyze(samples, chain())
	if r.State != StateCollecting {
		t.Fatalf("state = %q: a restart must not be read as negative traffic", r.State)
	}
}

func TestBottleneckAdviceFitsTheStep(t *testing.T) {
	for typ, want := range map[string]string{
		"db_lookup":   "cache",
		"api_lookup":  "cache",
		"execute_sql": "query plan",
		"ai_prompt":   "model",
		"mimir_ask":   "model",
		"sink":        "batch size",
		"rate_limit":  "rate limit",
		"geo":         "Simplify",
		"set":         "Simplify",
		"":            "Simplify",
	} {
		if got := bottleneckAdvice(Node{Type: typ}); !strings.Contains(got, want) {
			t.Errorf("%q: advice %q does not mention %q", typ, got, want)
		}
	}
}

func TestPercentNeverRoundsToAllOrNothing(t *testing.T) {
	for v, want := range map[float64]string{0.999: ">99%", 1: "100%", 0.001: "<1%", 0: "0%", 0.204: "20%"} {
		if got := pct(v); got != want {
			t.Errorf("pct(%v) = %q, want %q", v, got, want)
		}
	}
}

func TestBottleneckAdviceForALookupWithItsCacheOffSaysToTurnItOn(t *testing.T) {
	got := bottleneckAdvice(Node{Type: "api_lookup", Config: map[string]any{"ttl": "0"}})
	if !strings.Contains(got, "cache is off") {
		t.Fatalf("advice %q should say the cache is off", got)
	}
}

func TestFilterEarlierCountsTheRunsItWouldSpare(t *testing.T) {
	f := find(Analyze(steady(nil), chain()), RuleFilterEarlier)
	if f == nil || !strings.Contains(f.Recommendation, "48,000 of its 60,000 runs") {
		t.Fatalf("recommendation should count spared runs: %+v", f)
	}
}

func TestFindingsThatAProposalCanApplyNameTheirFix(t *testing.T) {
	flow := chain()
	flow.TraceSampleRate = 0
	flow.Nodes[1].Config = map[string]any{"transType": "db_lookup", "ttl": "0"}
	r := Analyze(steady(nil), flow)
	if f := find(r, RuleTraceSampling); f == nil || f.Fix != FixTraceSampling {
		t.Fatalf("trace sampling fix = %+v", f)
	}
	if f := find(r, RuleLookupCacheOff); f == nil || f.Fix != FixLookupCache {
		t.Fatalf("lookup cache fix = %+v", f)
	}
	// Moving a step is a design change only a person can make.
	if f := find(r, RuleFilterEarlier); f == nil || f.Fix != "" {
		t.Fatalf("filter-earlier must not offer a fix: %+v", f)
	}
}

func TestReportSummarisesTheWindow(t *testing.T) {
	r := Analyze(steady(func(i int, s *Sample) {
		s.NodeErrors["lookup"] = uint64(i * 30)
		s.DeadLetters = uint64(i * 30)
	}), chain())
	s := r.Summary
	if s == nil {
		t.Fatal("a ready report has no summary")
	}
	if s.Messages != 60000 || s.WindowSeconds != 600 || s.Throughput != 100 {
		t.Fatalf("summary = %+v", s)
	}
	// lookup 40ms + filter 1ms on every message, sink 2ms on a fifth.
	if s.StepTimeMs < 41.3 || s.StepTimeMs > 41.5 {
		t.Fatalf("step time = %v ms, want about 41.4", s.StepTimeMs)
	}
	// 600 failed runs + 600 dead letters over 60000 messages.
	if s.ErrorRate < 0.0199 || s.ErrorRate > 0.0201 {
		t.Fatalf("error rate = %v, want 0.02", s.ErrorRate)
	}
	if c := Analyze(steady(nil)[:2], chain()); c.Summary != nil {
		t.Fatal("a window too short to judge must not offer a summary")
	}
}

func TestCompareSaysWhetherAChangeHelped(t *testing.T) {
	before := Summary{Throughput: 40, StepTimeMs: 40, ErrorRate: 0.01, Messages: 7000, WindowSeconds: 180}
	for name, tc := range map[string]struct {
		after Summary
		want  string
	}{
		"faster steps":      {Summary{Throughput: 40, StepTimeMs: 8, ErrorRate: 0.01, Messages: 7000, WindowSeconds: 180}, VerdictBetter},
		"slower steps":      {Summary{Throughput: 40, StepTimeMs: 60, ErrorRate: 0.01, Messages: 7000, WindowSeconds: 180}, VerdictWorse},
		"more failures":     {Summary{Throughput: 40, StepTimeMs: 8, ErrorRate: 0.10, Messages: 7000, WindowSeconds: 180}, VerdictWorse},
		"about the same":    {Summary{Throughput: 41, StepTimeMs: 39, ErrorRate: 0.011, Messages: 7000, WindowSeconds: 180}, VerdictSame},
		"fewer failures":    {Summary{Throughput: 40, StepTimeMs: 40, ErrorRate: 0.001, Messages: 7000, WindowSeconds: 180}, VerdictBetter},
		"only less traffic": {Summary{Throughput: 10, StepTimeMs: 40, ErrorRate: 0.01, Messages: 1800, WindowSeconds: 180}, VerdictSame},
	} {
		if got := Compare(before, tc.after).Verdict; got != tc.want {
			t.Errorf("%s: verdict = %q, want %q", name, got, tc.want)
		}
	}
}

func TestSummarySinceReadsOnlyAfterTheChange(t *testing.T) {
	samples := steady(func(i int, s *Sample) {
		if i >= 10 {
			s.NodeLatency["lookup"] = 5 * time.Millisecond
		}
	})
	since := samples[10].At
	got, ok := SummarySince(samples, chain(), since)
	if !ok {
		t.Fatal("5 minutes after the change should be enough to summarise")
	}
	if got.StepTimeMs > 7 {
		t.Fatalf("step time since the change = %v ms; readings from before it leaked in", got.StepTimeMs)
	}
	if _, ok := SummarySince(samples, chain(), samples[19].At); ok {
		t.Fatal("30 seconds after a change is too soon to judge")
	}
}

// hist is a cumulative run-time histogram with n runs in each named bucket;
// bucket k holds runs of at most 2^k microseconds.
func hist(buckets map[int]uint64) telemetry.LatencyHistogram {
	var h telemetry.LatencyHistogram
	for k, n := range buckets {
		h[k] = n
	}
	return h
}

// A step that is fast for most messages and very slow for a few has a calm
// average; only the spread of its run times shows the problem.
func TestAnalyzeFlagsAStepThatIsOccasionallyVerySlow(t *testing.T) {
	samples := steady(func(i int, s *Sample) {
		n := uint64(i * 3000)
		s.NodeLatency["lookup"] = 2 * time.Millisecond
		// 90% of runs within ~1ms (bucket 10), 10% within ~524ms (bucket 19).
		s.NodeTimes = map[string]telemetry.LatencyHistogram{
			"lookup": hist(map[int]uint64{10: n * 9 / 10, 19: n / 10}),
			"filter": hist(map[int]uint64{9: n}),
		}
	})
	r := Analyze(samples, chain())
	f := find(r, RuleSlowTail)
	if f == nil {
		t.Fatalf("no %s finding in %+v", RuleSlowTail, r.Findings)
	}
	if f.NodeID != "lookup" || f.Severity != SeverityMedium {
		t.Errorf("finding = node %q severity %q, want lookup/medium", f.NodeID, f.Severity)
	}
	if !strings.Contains(f.Detail, "1 in 20") || !strings.Contains(f.Detail, "524 ms") {
		t.Errorf("detail does not give the slow runs: %q", f.Detail)
	}
}

func TestAnalyzeDoesNotFlagAnEvenStep(t *testing.T) {
	samples := steady(func(i int, s *Sample) {
		n := uint64(i * 3000)
		s.NodeTimes = map[string]telemetry.LatencyHistogram{
			// Slow, but evenly slow: that is the bottleneck rule's business.
			"lookup": hist(map[int]uint64{15: n * 9 / 10, 16: n / 10}),
		}
	})
	if f := find(Analyze(samples, chain()), RuleSlowTail); f != nil {
		t.Errorf("flagged an even step: %+v", f)
	}
}

// The spread is read over the window, not the engine's lifetime: slow runs
// from before the window must not count.
func TestSlowTailReadsOnlyTheWindow(t *testing.T) {
	samples := steady(func(i int, s *Sample) {
		n := uint64(i * 3000)
		s.NodeTimes = map[string]telemetry.LatencyHistogram{
			"lookup": hist(map[int]uint64{10: 1000 + n, 19: 5000}),
		}
	})
	if f := find(Analyze(samples, chain()), RuleSlowTail); f != nil {
		t.Errorf("counted runs from before the window: %+v", f)
	}
}

func TestBottleneckShowsHowSlowTheSlowRunsAre(t *testing.T) {
	samples := steady(func(i int, s *Sample) {
		n := uint64(i * 3000)
		s.NodeTimes = map[string]telemetry.LatencyHistogram{
			"lookup": hist(map[int]uint64{15: n * 9 / 10, 16: n / 10}),
		}
	})
	f := find(Analyze(samples, chain()), RuleBottleneck)
	if f == nil {
		t.Fatal("no bottleneck finding")
	}
	var got string
	for _, e := range f.Evidence {
		if e.Label == "95% of runs finish within" {
			got = e.Value
		}
	}
	if got != "65 ms" {
		t.Errorf("p95 evidence = %q, want 65 ms (in %+v)", got, f.Evidence)
	}
}
