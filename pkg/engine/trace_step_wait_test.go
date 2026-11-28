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

	"github.com/gsoultan/hermod"
)

// A node's step carries what it waited for, read from the step's context, so
// the Runs tab can show that a lookup waited its turn and behind whom.
func TestRecordCompletedTraceStep_CarriesTheStepsWait(t *testing.T) {
	recorder := &mockTraceRecorder{}
	e := NewEngine(nil, nil, nil)
	e.traceRecorder = recorder
	e.workflowID = "wf-totals"
	e.config = DefaultConfig()
	e.config.TraceSampleRate = 1.0

	ctx := hermod.WithStepWait(t.Context())
	hermod.AddStepWait(ctx, 4*time.Second, &hermod.Busy{Holders: []hermod.Holder{{WorkflowID: "wf-report", Kind: "SQL step"}}})
	start := time.Now()
	e.RecordCompletedTraceStep(ctx, &mockMessage{id: "m"}, "n1", start, start, nil, nil)

	var steps []hermod.TraceStep
	for range 50 {
		if steps = recorder.GetSteps("m"); len(steps) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(steps) != 1 {
		t.Fatalf("recorded %d steps, want 1", len(steps))
	}
	w := steps[0].Wait
	if w == nil || w.For != 4*time.Second || len(w.Holders) != 1 || w.Holders[0].WorkflowID != "wf-report" {
		t.Errorf("Wait = %+v, want 4s behind wf-report", w)
	}
}
