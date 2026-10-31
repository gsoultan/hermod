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
	"math"
	"testing"
	"time"
)

// The workflow's Tuning tab reads error_rate and backpressure from this frame.
// Neither was ever filled in, so both read 0% on a flow that was failing every
// message into a full sink.
func TestGetStatusReportsErrorRateFromFailuresAndDeadLetters(t *testing.T) {
	e := NewEngine(nil, nil, nil)
	for range 6 {
		e.statusTracker.IncProcessed()
	}
	e.NodeStats("map").Observe(time.Millisecond, true)
	e.NodeStats("map").Observe(time.Millisecond, true)
	e.statusTracker.IncDeadLetter()

	// 8 messages reached the pipeline (6 processed, 2 failed in a node); 3 of
	// them went wrong (2 node failures, 1 parked in the dead-letter sink).
	got := e.GetStatus().ErrorRate
	if want := 3.0 / 8.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("ErrorRate = %v, want %v", got, want)
	}
}

func TestGetStatusReportsNoErrorRateBeforeAnyMessage(t *testing.T) {
	if got := NewEngine(nil, nil, nil).GetStatus().ErrorRate; got != 0 {
		t.Errorf("ErrorRate = %v on an engine that has seen nothing, want 0", got)
	}
}

// Backpressure is the fullest sink buffer: one jammed sink is the reading that
// matters, however idle the others are.
func TestGetStatusReportsBackpressureAsTheFullestSinkBuffer(t *testing.T) {
	e := NewEngine(nil, nil, nil)
	idle := &sinkWriter{sinkID: "idle", ch: make(chan *pendingMessage, 10)}
	busy := &sinkWriter{sinkID: "busy", ch: make(chan *pendingMessage, 4)}
	for range 3 {
		busy.ch <- &pendingMessage{}
	}
	e.sinkWriters = []*sinkWriter{idle, busy}

	if got := e.GetStatus().Backpressure; math.Abs(got-0.75) > 1e-9 {
		t.Errorf("Backpressure = %v, want 0.75", got)
	}
}

// The average was only kept when adaptive throughput was switched on, which it
// is not by default, so the Tuning tab's latency read "—" on an ordinary flow.
func TestGetStatusReportsAverageLatencyWithAdaptiveThroughputOff(t *testing.T) {
	e := NewEngine(nil, nil, nil)
	if e.config.AdaptiveThroughput {
		t.Fatal("test assumes adaptive throughput is off by default")
	}
	e.adaptiveThrottle(t.Context(), 40*time.Millisecond)

	if got := e.GetStatus().AvgLatency; got != 40*time.Millisecond {
		t.Errorf("AvgLatency = %v, want 40ms", got)
	}
}
