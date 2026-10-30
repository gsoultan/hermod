// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"context"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// Saving a workflow goes through this handler's validator, not the engine's, so
// what the engine refuses about sink nodes that commit together has to be
// refused here too, or the editor saves a workflow that cannot start.
func TestValidateWorkflowRefusesAOneMemberTransaction(t *testing.T) {
	h := &WorkflowHandler{Handler: &handlers.Handler{}}
	wf := storage.Workflow{
		Name: "alone",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			{ID: "pg", Type: "sink", RefID: "pg-1", Config: map[string]any{"label": "Orders DB", "transaction": "solo"}},
		},
		Edges: []storage.WorkflowEdge{{ID: "e1", SourceID: "src", TargetID: "pg"}},
	}

	var found *ValidationIssue
	for _, issue := range h.ValidateWorkflow(context.Background(), wf) {
		if strings.Contains(issue.Message, `"solo"`) {
			found = &issue
			break
		}
	}
	if found == nil {
		t.Fatal("a transaction with one member was not reported")
	}
	if found.Severity != "error" {
		t.Errorf("severity = %q, want error: the workflow could not start", found.Severity)
	}
	if !strings.Contains(found.Message, "Orders DB") {
		t.Errorf("the message should name the node, got: %s", found.Message)
	}
	if found.Recommendation == "" {
		t.Error("the issue gives no recommendation")
	}
}

func TestValidateWorkflowAcceptsTwoMembers(t *testing.T) {
	h := &WorkflowHandler{Handler: &handlers.Handler{}}
	wf := storage.Workflow{
		Name: "pair",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			{ID: "pg", Type: "sink", RefID: "pg-1", Config: map[string]any{"transaction": "orders"}},
			{ID: "my", Type: "sink", RefID: "my-1", Config: map[string]any{"transaction": "orders"}},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "pg"},
			{ID: "e2", SourceID: "src", TargetID: "my"},
		},
	}
	for _, issue := range h.ValidateWorkflow(context.Background(), wf) {
		if strings.Contains(issue.Message, "transaction") {
			t.Errorf("two members in one transaction were reported: %s", issue.Message)
		}
	}
}
