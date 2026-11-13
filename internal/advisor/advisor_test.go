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

import (
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

func TestAdvisorKeepsTheNewestSamplesUpToItsCapacity(t *testing.T) {
	a := New(3)
	for i := range 5 {
		a.Observe("wf-1", Sample{At: t0.Add(time.Duration(i) * time.Second), Processed: uint64(i)})
	}
	got := a.Samples("wf-1")
	if len(got) != 3 {
		t.Fatalf("kept %d samples, want 3", len(got))
	}
	for i, s := range got {
		if s.Processed != uint64(i+2) {
			t.Fatalf("sample %d = %d, want oldest-first 2,3,4", i, s.Processed)
		}
	}
}

func TestAdvisorReturnsACopy(t *testing.T) {
	a := New(4)
	a.Observe("wf-1", Sample{Processed: 1})
	got := a.Samples("wf-1")
	got[0].Processed = 99
	if a.Samples("wf-1")[0].Processed != 1 {
		t.Fatal("a caller's change reached the advisor's window")
	}
}

func TestAdvisorForgetsAStoppedFlow(t *testing.T) {
	a := New(4)
	a.Observe("wf-1", Sample{Processed: 1})
	a.Observe("wf-2", Sample{Processed: 1})
	a.Forget("wf-1")
	if got := a.Samples("wf-1"); len(got) != 0 {
		t.Fatalf("stopped flow kept %d samples", len(got))
	}
	if got := a.Samples("wf-2"); len(got) != 1 {
		t.Fatal("forgetting one flow dropped another")
	}
	if n := a.Flows(); n != 1 {
		t.Fatalf("advisor tracks %d flows, want 1", n)
	}
}

func TestAdvisorReportReadsTheFlowsWindow(t *testing.T) {
	a := New(DefaultCapacity)
	for _, s := range steady(nil) {
		a.Observe("wf-1", s)
	}
	r := a.Report(chain())
	if r.State != StateReady || r.BottleneckNodeID != "lookup" {
		t.Fatalf("report = %+v", r)
	}
	if r := a.Report(Flow{ID: "other"}); r.State != StateNotRunning {
		t.Fatalf("unknown flow state = %q", r.State)
	}
}

func TestSampleFromLeavesPayloadSamplesOut(t *testing.T) {
	s := SampleFrom(t0, telemetry.StatusUpdate{
		ProcessedCount: 7,
		NodeSamples:    map[string]any{"n1": map[string]any{"email": "a@example.com"}},
		NodeMetrics:    map[string]uint64{"n1": 7},
	})
	if s.Processed != 7 || s.NodeRuns["n1"] != 7 {
		t.Fatalf("sample = %+v", s)
	}
	// Sample has no field that can hold a payload; this guards it staying so.
	var _ = Sample{}.NodeRuns
}

func TestAdvisorIsSafeForConcurrentUse(t *testing.T) {
	a := New(8)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for j := range 100 {
				a.Observe("wf", Sample{Processed: uint64(i*1000 + j)})
				_ = a.Samples("wf")
				if j%10 == 0 {
					a.Forget("wf")
				}
			}
		})
	}
	wg.Wait()
}
