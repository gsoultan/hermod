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
