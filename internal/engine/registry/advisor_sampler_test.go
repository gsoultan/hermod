// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/advisor"
	"github.com/gsoultan/hermod/internal/storage"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

func TestAdvisorSamplerReadsEveryRunningWorkflow(t *testing.T) {
	r := newTestRegistry()
	r.advisor = advisor.New(advisor.DefaultCapacity)
	r.engines["wf-1"] = &activeEngine{engine: pkgengine.NewEngine(nil, nil, nil), isWorkflow: true, workflow: storage.Workflow{ID: "wf-1"}}
	r.engines["src-only"] = &activeEngine{engine: pkgengine.NewEngine(nil, nil, nil)}

	at := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	r.sampleAdvisor(at)
	r.sampleAdvisor(at.Add(advisor.SampleInterval))

	got := r.advisor.Samples("wf-1")
	if len(got) != 2 || !got[1].At.Equal(at.Add(advisor.SampleInterval)) {
		t.Fatalf("wf-1 samples = %+v", got)
	}
	if n := r.advisor.Flows(); n != 1 {
		t.Fatalf("advisor tracks %d flows; only workflows are read", n)
	}
}

func TestAdvisorSamplerForgetsAStoppedWorkflow(t *testing.T) {
	r := newTestRegistry()
	r.advisor = advisor.New(advisor.DefaultCapacity)
	r.advisor.Observe("gone", advisor.Sample{Processed: 5})
	r.engines["wf-1"] = &activeEngine{engine: pkgengine.NewEngine(nil, nil, nil), isWorkflow: true, workflow: storage.Workflow{ID: "wf-1"}}

	r.sampleAdvisor(time.Now())

	if got := r.advisor.Samples("gone"); len(got) != 0 {
		t.Fatalf("a stopped workflow kept %d samples", len(got))
	}
}

func TestWorkflowInsightsForAFlowThatIsNotRunning(t *testing.T) {
	r := newTestRegistry()
	r.advisor = advisor.New(advisor.DefaultCapacity)
	rep := r.WorkflowInsights(context.Background(), "wf-off")
	if rep.State != advisor.StateNotRunning || rep.WorkflowID != "wf-off" {
		t.Fatalf("report = %+v", rep)
	}
}
