// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package advisor reads a running flow's recent telemetry and says what to
// change to make it faster or steadier, and why.
//
// It keeps a short window of the status frames the engine already produces
// (see Advisor) and runs plain rules over it (see Analyze). Every finding
// carries the numbers it was drawn from. Nothing here changes a flow: a
// recommendation is advice until a person acts on it.
package advisor

import (
	"time"

	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// Sample is one reading of a running flow, taken from its status frame.
//
// It keeps counters and gauges only. The frame's payload samples are left
// out: the advisor never needs message contents, and keeping them would hold
// customer data in memory for the length of the window.
type Sample struct {
	At          time.Time
	Processed   uint64
	DeadLetters uint64
	Lag         uint64
	Throughput  float64
	// NodeRuns and NodeErrors are cumulative since the engine started; a
	// node's runs include its failed runs.
	NodeRuns   map[string]uint64
	NodeErrors map[string]uint64
	// NodeLatency is each node's moving-average run time at the reading.
	NodeLatency map[string]time.Duration
	NodeBusy    map[string]telemetry.NodeBusy
	// SinkFill and SinkBreaker are keyed by the sink connection's ID.
	SinkFill    map[string]float64
	SinkBreaker map[string]string
	// EdgeCounts is cumulative messages per canvas edge ID.
	EdgeCounts map[string]uint64
	// NodeTimes is each node's run times by bucket, cumulative since the
	// engine started. It is not in the status frame; the sampler sets it.
	NodeTimes map[string]telemetry.LatencyHistogram
}

// SampleFrom keeps what the advisor reads from a status frame.
//
// The maps are taken, not copied: Engine.GetStatus builds new ones on every
// call, and the caller hands the frame over.
func SampleFrom(at time.Time, s telemetry.StatusUpdate) Sample {
	return Sample{
		At:          at,
		Processed:   s.ProcessedCount,
		DeadLetters: s.DeadLetterCount,
		Lag:         s.Lag,
		Throughput:  s.Throughput,
		NodeRuns:    s.NodeMetrics,
		NodeErrors:  s.NodeErrorMetrics,
		NodeLatency: s.NodeLatencies,
		NodeBusy:    s.NodeBusy,
		SinkFill:    s.SinkBufferFill,
		SinkBreaker: s.SinkCBStatuses,
		EdgeCounts:  s.EdgeCounts,
	}
}

// continues reports whether next can follow prev in one window. The engine's
// counters only grow, so one that went back means the engine restarted and
// the window must begin again.
func continues(prev, next Sample) bool {
	if next.Processed < prev.Processed || next.DeadLetters < prev.DeadLetters {
		return false
	}
	for id, n := range prev.NodeRuns {
		if next.NodeRuns[id] < n {
			return false
		}
	}
	return true
}

// currentRun returns the samples since the last restart, oldest first.
func currentRun(samples []Sample) []Sample {
	start := 0
	for i := 1; i < len(samples); i++ {
		if !continues(samples[i-1], samples[i]) {
			start = i
		}
	}
	return samples[start:]
}
