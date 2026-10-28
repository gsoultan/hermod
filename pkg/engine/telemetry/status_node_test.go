// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package telemetry

import (
	"sync"
	"testing"
	"time"
)

// A node's handle writes the same counters the status reports, so the router
// can resolve it once per workflow and update it per message without a map
// lookup or a key allocation.
func TestNodeStatsFeedTheReportedNodeMetrics(t *testing.T) {
	s := NewStatusTracker()
	n := s.NodeStats("map")
	if s.NodeStats("map") != n {
		t.Fatal("NodeStats returned a different handle for the same node; counts would split")
	}

	n.Observe(10*time.Millisecond, false)
	n.Observe(10*time.Millisecond, true)
	n.Observe(10*time.Millisecond, false)

	if got := s.GetNodeMetrics()["map"]; got != 3 {
		t.Errorf("NodeMetrics[map] = %d, want 3 (every run counts, failed or not)", got)
	}
	if got := s.GetNodeErrorMetrics()["map"]; got != 1 {
		t.Errorf("NodeErrorMetrics[map] = %d, want 1", got)
	}
	if got := s.GetNodeLatencies()["map"]; got != 10*time.Millisecond {
		t.Errorf("NodeLatencies[map] = %v, want 10ms", got)
	}

	// The legacy entry point shares the counter.
	s.UpdateNodeMetric("map", 2)
	if got := s.GetNodeMetrics()["map"]; got != 5 {
		t.Errorf("NodeMetrics[map] = %d after UpdateNodeMetric(2), want 5", got)
	}
}

// Latency is a moving average, so one slow message does not become the node's
// figure, and a node that turns slow shows it within a few dozen messages.
func TestNodeStatsLatencyIsAMovingAverage(t *testing.T) {
	s := NewStatusTracker()
	n := s.NodeStats("ai")
	for range 50 {
		n.Observe(10*time.Millisecond, false)
	}
	n.Observe(time.Second, false)
	if got := s.GetNodeLatencies()["ai"]; got >= 200*time.Millisecond {
		t.Errorf("one 1s outlier moved the average to %v", got)
	}
	for range 100 {
		n.Observe(2*time.Second, false)
	}
	if got := s.GetNodeLatencies()["ai"]; got < 1900*time.Millisecond {
		t.Errorf("after 100 runs at 2s the average is %v; a slow node must show as slow", got)
	}
}

// A node that has never run has no latency entry rather than a zero.
func TestNodeLatenciesOmitNodesThatHaveNotRun(t *testing.T) {
	s := NewStatusTracker()
	n := s.NodeStats("src")
	n.Count()
	if _, ok := s.GetNodeLatencies()["src"]; ok {
		t.Error("a node counted without a duration reported a latency")
	}
	if got := s.GetNodeMetrics()["src"]; got != 1 {
		t.Errorf("NodeMetrics[src] = %d, want 1", got)
	}
}

// Samples are full payload copies and every one re-fires the editor's previews,
// so a node hands out at most one sampling slot per interval, whatever the
// message rate and however many workers race for it.
func TestNodeStatsClaimSampleAllowsOnePerInterval(t *testing.T) {
	s := NewStatusTracker()
	n := s.NodeStats("map")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()

	if !n.ClaimSample(base, time.Second) {
		t.Fatal("the first sample was refused")
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	for i := range 64 {
		wg.Go(func() {
			// All within the same second.
			if n.ClaimSample(base+int64(i)*int64(10*time.Millisecond), time.Second) {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if granted != 0 {
		t.Fatalf("%d further samples granted inside one interval; want 0", granted)
	}

	granted = 0
	for i := range 64 {
		wg.Go(func() {
			if n.ClaimSample(base+int64(time.Second)+int64(i), time.Second) {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if granted != 1 {
		t.Fatalf("%d samples granted once the interval elapsed; want exactly 1", granted)
	}
}

func TestEdgeCounterFeedsTheReportedEdgeMetrics(t *testing.T) {
	s := NewStatusTracker()
	c := s.EdgeCounter("src", "map")
	if s.EdgeCounter("src", "map") != c {
		t.Fatal("EdgeCounter returned a different counter for the same edge")
	}
	c.Add(3)
	s.UpdateEdgeMetric("src", "map", 1)
	if got := s.GetEdgeMetrics()["src->map"]; got != 4 {
		t.Errorf("EdgeMetrics[src->map] = %d, want 4", got)
	}
}

// A node that has taken a message and finished nothing since is hung, and the
// editor can only say so if the status reports it: a node that never returns
// never reaches Observe, so its counters simply stop moving, exactly as an idle
// node's do. Begin and End bracket each run so the in-flight work is visible.
func TestNodeBusyReportsInFlightWorkAndTimeSinceProgress(t *testing.T) {
	s := NewStatusTracker()
	t0 := time.UnixMilli(1_793_000_000_000)
	now := t0
	s.now = func() time.Time { return now }

	n := s.NodeStats("ai")
	n.Begin(t0)
	now = t0.Add(45 * time.Second)

	busy := s.GetNodeBusy()
	if got := busy["ai"]; got.InFlight != 1 || got.SinceProgressMs != 45_000 {
		t.Fatalf("NodeBusy[ai] = %+v, want 1 in flight with no progress for 45s", got)
	}

	// Finishing that run is progress; the next one starts a fresh clock.
	n.End(now)
	if _, ok := s.GetNodeBusy()["ai"]; ok {
		t.Fatalf("a node with nothing in flight is reported busy: %+v", s.GetNodeBusy())
	}
	n.Begin(now)
	n.Begin(now.Add(time.Second))
	now = now.Add(3 * time.Second)
	n.End(now.Add(-time.Second))
	if got := s.GetNodeBusy()["ai"]; got.InFlight != 1 || got.SinceProgressMs != 1_000 {
		t.Errorf("NodeBusy[ai] = %+v, want 1 in flight and 1s since the last run finished", got)
	}
}
