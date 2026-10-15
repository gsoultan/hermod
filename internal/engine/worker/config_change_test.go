// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package worker

import (
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// Every field here is one the registry reads when it builds an engine
// (registry_workflow.go). If a change to one of them does not force a restart,
// the engine keeps running on the value it started with and the setting looks
// inert: saving "Dry-Run Mode" showed the badge in the editor while the engine
// carried on writing to production sinks.
func TestWorkflowRuntimeConfigDiffersOnEveryEngineField(t *testing.T) {
	base := storage.Workflow{
		ID:                "wf-1",
		Name:              "orders",
		VHost:             "/",
		DeadLetters:       true,
		PrioritizeDLQ:     false,
		DryRun:            false,
		DLQThreshold:      10,
		MaxRetries:        3,
		RetryInterval:     "5s",
		ReconnectInterval: "30s",
		TraceSampleRate:   0.1,
	}

	tests := []struct {
		field  string
		mutate func(*storage.Workflow)
	}{
		{"Name", func(w *storage.Workflow) { w.Name = "invoices" }},
		{"VHost", func(w *storage.Workflow) { w.VHost = "/other" }},
		{"DeadLetters", func(w *storage.Workflow) { w.DeadLetters = false }},
		{"PrioritizeDLQ", func(w *storage.Workflow) { w.PrioritizeDLQ = true }},
		{"DryRun", func(w *storage.Workflow) { w.DryRun = true }},
		{"DLQThreshold", func(w *storage.Workflow) { w.DLQThreshold = 50 }},
		{"MaxRetries", func(w *storage.Workflow) { w.MaxRetries = 7 }},
		{"RetryInterval", func(w *storage.Workflow) { w.RetryInterval = "1s" }},
		{"ReconnectInterval", func(w *storage.Workflow) { w.ReconnectInterval = "10s" }},
		{"TraceSampleRate", func(w *storage.Workflow) { w.TraceSampleRate = 1.0 }},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			next := base
			tt.mutate(&next)
			if !workflowRuntimeConfigDiffers(base, next) {
				t.Errorf("changing %s did not require a restart; the running engine "+
					"would keep the old value indefinitely", tt.field)
			}
		})
	}
}

// The comparison runs on every sync tick for every workflow. Reporting a
// difference that is not there would restart healthy engines in a loop.
func TestWorkflowRuntimeConfigIdenticalIsNoChange(t *testing.T) {
	base := storage.Workflow{
		ID:                "wf-1",
		Name:              "orders",
		VHost:             "/",
		DeadLetters:       true,
		DLQThreshold:      10,
		MaxRetries:        3,
		RetryInterval:     "5s",
		ReconnectInterval: "30s",
		TraceSampleRate:   0.1,
	}
	if workflowRuntimeConfigDiffers(base, base) {
		t.Error("identical workflows reported as changed; engines would restart every sync tick")
	}

	// Fields the engine does not read must not force a restart either.
	next := base
	next.Status = "Restarting"
	next.WorkerID = "worker-2"
	next.TotalProcessed = 99
	if workflowRuntimeConfigDiffers(base, next) {
		t.Error("a status/ownership/counter change forced an engine restart")
	}
}
