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

import "time"

// Fixes a finding can offer. The names are the proposal kinds in
// internal/selfheal, which owns what each one changes.
const (
	FixTraceSampling = "set_trace_sampling"
	FixLookupCache   = "enable_lookup_cache"
)

// Summary is how a flow ran over a window, in the three numbers a change is
// judged by.
type Summary struct {
	// Throughput is messages a second.
	Throughput float64 `json:"throughput"`
	// StepTimeMs is the time all steps together spend on one message.
	StepTimeMs float64 `json:"step_time_ms"`
	// ErrorRate is failed step runs and dead letters over messages, 0 to 1.
	ErrorRate     float64 `json:"error_rate"`
	Messages      uint64  `json:"messages"`
	WindowSeconds float64 `json:"window_seconds"`
}

func summarise(w window, flow Flow) Summary {
	s := Summary{Messages: w.last.Processed - w.first.Processed, WindowSeconds: w.span.Seconds()}
	if s.WindowSeconds > 0 {
		s.Throughput = float64(s.Messages) / s.WindowSeconds
	}
	if s.Messages == 0 {
		return s
	}
	var stepTime float64
	var failed uint64
	for _, n := range flow.Nodes {
		failed += w.errors(n.ID)
		if timedStep(n.Type) {
			stepTime += float64(w.latency(n.ID)) * float64(w.runs(n.ID))
		}
	}
	failed += w.last.DeadLetters - w.first.DeadLetters
	s.StepTimeMs = stepTime / float64(s.Messages) / float64(time.Millisecond)
	s.ErrorRate = min(float64(failed)/float64(s.Messages), 1)
	return s
}

// SummarySince summarises the samples taken at or after since, and reports
// false when they are too few to judge by.
func SummarySince(samples []Sample, flow Flow, since time.Time) (Summary, bool) {
	run := currentRun(samples)
	start := len(run)
	for i, s := range run {
		if !s.At.Before(since) {
			start = i
			break
		}
	}
	run = run[start:]
	if len(run) == 0 {
		return Summary{}, false
	}
	w := window{s: run, first: run[0], last: run[len(run)-1]}
	w.span = w.last.At.Sub(w.first.At)
	if !w.ready() {
		return Summary{}, false
	}
	return summarise(w, flow), true
}

// Verdicts on a change.
const (
	VerdictBetter = "better"
	VerdictWorse  = "worse"
	VerdictSame   = "same"
)

// How far a number must move before a change is credited or blamed for it.
const (
	stepTimeMoved   = 0.15 // of the earlier step time
	stepTimeMovedMs = 1.0  // and at least this much
	errorsRose      = 0.02 // absolute rise in the error rate
	errorsFell      = 0.005
)

// Effect is a change measured: how the flow ran before and after it.
type Effect struct {
	Verdict string  `json:"verdict"`
	Before  Summary `json:"before"`
	After   Summary `json:"after"`
}

// Compare judges a change by what it did to step time and failures.
//
// Throughput is reported but not judged: it follows how much the source
// sends, which a change to the flow does not control.
func Compare(before, after Summary) Effect {
	e := Effect{Verdict: VerdictSame, Before: before, After: after}
	stepDelta := after.StepTimeMs - before.StepTimeMs
	moved := max(before.StepTimeMs*stepTimeMoved, stepTimeMovedMs)
	errDelta := after.ErrorRate - before.ErrorRate
	switch {
	case errDelta >= errorsRose, stepDelta >= moved:
		e.Verdict = VerdictWorse
	case stepDelta <= -moved, errDelta <= -errorsFell && after.ErrorRate <= before.ErrorRate/2:
		e.Verdict = VerdictBetter
	}
	return e
}
