// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package sql

import (
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// A workflow keeps the messages it cannot deliver on the Dead Letters page
// when DeadLetters is on. That replaced a dead-letter sink the workflow
// named; a workflow saved with one by an earlier release reads as on.

func TestWorkflowDeadLettersRoundTrips(t *testing.T) {
	s, _ := workflowStore(t)
	mustCreateWorkflow(t, s, storage.Workflow{ID: "wf-on", Name: "on", DeadLetters: true})
	mustCreateWorkflow(t, s, storage.Workflow{ID: "wf-off", Name: "off"})

	for id, want := range map[string]bool{"wf-on": true, "wf-off": false} {
		got, err := s.GetWorkflow(t.Context(), id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if got.DeadLetters != want {
			t.Errorf("%s: DeadLetters = %v, want %v", id, got.DeadLetters, want)
		}
	}
	list, _, err := s.ListWorkflows(t.Context(), storage.CommonFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, wf := range list {
		if wf.DeadLetters != (wf.ID == "wf-on") {
			t.Errorf("listed %s: DeadLetters = %v", wf.ID, wf.DeadLetters)
		}
	}
}

func TestAWorkflowWithALegacyDeadLetterSinkKeepsDeadLetters(t *testing.T) {
	s, db := workflowStore(t)
	mustCreateWorkflow(t, s, storage.Workflow{ID: "wf-legacy", Name: "legacy"})
	if _, err := db.ExecContext(t.Context(), `UPDATE workflows SET dead_letter_sink_id = 'snk-rabbit' WHERE id = 'wf-legacy'`); err != nil {
		t.Fatalf("seed legacy sink: %v", err)
	}

	got, err := s.GetWorkflow(t.Context(), "wf-legacy")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.DeadLetters {
		t.Fatal("a workflow that had a dead-letter sink lost its dead letters")
	}

	// Switching it off has to stick: the legacy column must not turn it back on.
	got.DeadLetters = false
	if err := s.UpdateWorkflow(t.Context(), got); err != nil {
		t.Fatalf("update: %v", err)
	}
	again, err := s.GetWorkflow(t.Context(), "wf-legacy")
	if err != nil {
		t.Fatalf("get again: %v", err)
	}
	if again.DeadLetters {
		t.Fatal("switching dead letters off did not stick")
	}
}
