// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package pebble

import (
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// A step that waited its turn for a connection keeps the wait, so the Runs
// tab can show how long and behind whom; a step that did not wait has none.
func TestTraceStepKeepsItsWait(t *testing.T) {
	s := newTracePebble(t)
	base := time.Now().UTC().Truncate(time.Millisecond)
	wait := &hermod.StepWait{For: 42 * time.Second, Reason: "no free connection",
		Holders: []hermod.Holder{{WorkflowID: "wf-report", NodeID: "n3", Kind: "SQL step", Running: 25 * time.Second}}}
	for i, step := range []hermod.TraceStep{
		{NodeID: "lookup", Timestamp: base, Duration: 43 * time.Second, After: map[string]any{"a": 1}, Wait: wait},
		{NodeID: "sink", Timestamp: base.Add(time.Millisecond), After: map[string]any{"a": 1}},
	} {
		if err := s.RecordTraceStep(t.Context(), "wf-totals", "m-1", step); err != nil {
			t.Fatalf("RecordTraceStep %d: %v", i, err)
		}
	}
	tr, err := s.GetMessageTrace(t.Context(), "wf-totals", "m-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Steps) != 2 {
		t.Fatalf("got %d steps, want 2", len(tr.Steps))
	}
	got := tr.Steps[0].Wait
	if got == nil || got.For != wait.For || got.Reason != wait.Reason || len(got.Holders) != 1 || got.Holders[0] != wait.Holders[0] {
		t.Errorf("Wait = %+v, want %+v", got, wait)
	}
	if tr.Steps[1].Wait != nil {
		t.Errorf("a step that did not wait has Wait %+v", tr.Steps[1].Wait)
	}
}
