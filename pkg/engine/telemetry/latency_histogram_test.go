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
	"testing"
	"time"
)

func TestNodeLatencyHistogramCountsEveryRunInItsBucket(t *testing.T) {
	s := NewStatusTracker()
	n := s.NodeStats("lookup")
	for range 95 {
		n.Observe(3*time.Millisecond, false)
	}
	for range 5 {
		n.Observe(900*time.Millisecond, true)
	}
	h, ok := s.NodeLatencyHistograms()["lookup"]
	if !ok {
		t.Fatal("no histogram for a node that ran")
	}
	if got := h.Total(); got != 100 {
		t.Fatalf("histogram holds %d runs, want 100", got)
	}
	// 3ms is in (2.048ms, 4.096ms]; 900ms in (524ms, 1.049s].
	if got := h.Quantile(0.95); got != 4096*time.Microsecond {
		t.Fatalf("p95 = %s, want 4.096ms: 95 of 100 runs took 3ms", got)
	}
	if got := h.Quantile(0.99); got != 1048576*time.Microsecond {
		t.Fatalf("p99 = %s, want 1.048576s", got)
	}
}

func TestNodeLatencyHistogramHoldsTheExtremes(t *testing.T) {
	var h LatencyHistogram
	s := NewStatusTracker()
	n := s.NodeStats("x")
	n.Observe(0, false)
	n.Observe(10*time.Hour, false)
	h = s.NodeLatencyHistograms()["x"]
	if h.Total() != 2 || h[0] != 1 || h[len(h)-1] != 1 {
		t.Fatalf("histogram = %v", h)
	}
}

func TestLatencyHistogramSubtractsAnEarlierReading(t *testing.T) {
	s := NewStatusTracker()
	n := s.NodeStats("x")
	for range 100 {
		n.Observe(time.Millisecond, false)
	}
	earlier := s.NodeLatencyHistograms()["x"]
	for range 10 {
		n.Observe(2*time.Second, false)
	}
	d := s.NodeLatencyHistograms()["x"].Since(earlier)
	if d.Total() != 10 || d.Quantile(0.5) < time.Second {
		t.Fatalf("since the earlier reading: %d runs, median %s", d.Total(), d.Quantile(0.5))
	}
	// Counters that went back mean the engine restarted: the newer reading
	// stands alone.
	if r := earlier.Since(s.NodeLatencyHistograms()["x"]); r.Total() != earlier.Total() {
		t.Fatalf("a reading older than its baseline kept %d runs, want its own %d", r.Total(), earlier.Total())
	}
	var empty LatencyHistogram
	if empty.Quantile(0.95) != 0 {
		t.Fatal("an empty histogram has no quantile")
	}
}

func TestObserveDoesNotAllocate(t *testing.T) {
	n := NewStatusTracker().NodeStats("x")
	if a := testing.AllocsPerRun(1000, func() { n.Observe(3*time.Millisecond, false) }); a != 0 {
		t.Fatalf("Observe allocates %.1f times per run", a)
	}
}
