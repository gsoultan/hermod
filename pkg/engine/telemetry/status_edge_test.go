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

// Edges are counted by the canvas edge's own ID as well as by the pair of nodes
// it joins. Two edges between the same two nodes — two switch cases into one
// sink — shared the pair's counter, so the editor drew both with one number.
func TestEdgeCountsAreKeptPerEdgeID(t *testing.T) {
	s := NewStatusTracker()
	a := s.EdgeIDCounter("edge-a")
	if s.EdgeIDCounter("edge-a") != a {
		t.Fatal("EdgeIDCounter returned a different counter for the same edge; counts would split")
	}
	a.Add(3)
	s.EdgeIDCounter("edge-b").Add(1)

	counts, _ := s.SampleEdgeCounts()
	if counts["edge-a"] != 3 || counts["edge-b"] != 1 {
		t.Errorf("SampleEdgeCounts = %v, want edge-a:3 edge-b:1", counts)
	}
}

// The counts are cumulative, so a reader can only turn them into a rate if it
// knows when they were read. Status frames go out on engine events and on the
// health tick, at uneven intervals, so the time of the read travels with them.
func TestEdgeCountsCarryTheTimeTheyWereRead(t *testing.T) {
	s := NewStatusTracker()
	at := time.UnixMilli(1_793_182_983_120)
	s.now = func() time.Time { return at }

	_, sampledAt := s.SampleEdgeCounts()
	if sampledAt != at.UnixMilli() {
		t.Errorf("sampledAt = %d, want %d", sampledAt, at.UnixMilli())
	}
}
