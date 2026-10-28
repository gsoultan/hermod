// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package engine

import (
	"testing"
	"time"
)

// Per-node latency is what makes a slow node — an AI call, a lookup against a
// slow database — visible in the editor rather than only as a slower workflow.
func TestGetStatusReportsPerNodeTelemetry(t *testing.T) {
	e := NewEngine(nil, nil, nil)

	e.NodeStats("ai").Observe(1500*time.Millisecond, false)
	e.NodeStats("ai").Observe(1500*time.Millisecond, true)
	e.NodeStats("src").Count()
	e.EdgeCounter("src", "ai").Add(2)

	st := e.GetStatus()
	if got := st.NodeMetrics["ai"]; got != 2 {
		t.Errorf("NodeMetrics[ai] = %d, want 2", got)
	}
	if got := st.NodeErrorMetrics["ai"]; got != 1 {
		t.Errorf("NodeErrorMetrics[ai] = %d, want 1", got)
	}
	if got := st.NodeLatencies["ai"]; got != 1500*time.Millisecond {
		t.Errorf("NodeLatencies[ai] = %v, want 1.5s", got)
	}
	if _, ok := st.NodeLatencies["src"]; ok {
		t.Error("a source node with no run time reported a latency")
	}
	if got := st.EdgeMetrics["src->ai"]; got != 2 {
		t.Errorf("EdgeMetrics[src->ai] = %d, want 2", got)
	}
}

// The status frame carries each canvas edge's count under the edge's own ID,
// with the time the counts were read, so the editor can turn successive frames
// into a rate instead of reading a lifetime total as messages per second.
func TestGetStatusReportsEdgeCountsByEdgeIDWithTheirReadTime(t *testing.T) {
	e := NewEngine(nil, nil, nil)
	e.EdgeIDCounter("edge-1").Add(7)

	before := time.Now().UnixMilli()
	st := e.GetStatus()
	after := time.Now().UnixMilli()

	if got := st.EdgeCounts["edge-1"]; got != 7 {
		t.Errorf("EdgeCounts[edge-1] = %d, want 7", got)
	}
	if st.SampledAt < before || st.SampledAt > after {
		t.Errorf("SampledAt = %d, want a time between %d and %d", st.SampledAt, before, after)
	}
}

// A node with a run in flight is in the status frame, so the editor can tell a
// hung node from an idle one.
func TestGetStatusReportsNodesWithWorkInFlight(t *testing.T) {
	e := NewEngine(nil, nil, nil)
	e.NodeStats("lookup").Begin(time.Now().Add(-2 * time.Second))
	e.NodeStats("map").Observe(time.Millisecond, false)

	st := e.GetStatus()
	busy, ok := st.NodeBusy["lookup"]
	if !ok || busy.InFlight != 1 || busy.SinceProgressMs < 2000 {
		t.Errorf("NodeBusy[lookup] = %+v (present %v), want 1 in flight for at least 2s", busy, ok)
	}
	if _, ok := st.NodeBusy["map"]; ok {
		t.Error("a node with nothing in flight is reported busy")
	}
}
