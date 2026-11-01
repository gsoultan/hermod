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
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// A run through a sub-flow lists the sub-flow's own steps, named as they are in
// the sub-flow, after the step that called it.
func TestExecutionsListTheStepsInsideASubflow(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	if err := e.store.CreateWorkflow(ctx, storage.Workflow{
		ID: "stamp", Name: "stamp", VHost: "team-a",
		Nodes: []storage.WorkflowNode{
			{ID: "in", Type: "subflow_input", Config: map[string]any{"fields": []any{map[string]any{"name": "n"}}}},
			{ID: "s", Type: "transformation", Config: map[string]any{"transType": "set", "column.stamped": "yes", "label": "Stamp it"}},
			{ID: "r", Type: "subflow_return"},
		},
		Edges: []storage.WorkflowEdge{{ID: "a", SourceID: "in", TargetID: "s"}, {ID: "b", SourceID: "s", TargetID: "r"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.CreateWorkflow(ctx, storage.Workflow{
		ID: "wf-call", Name: "wf-call", VHost: "team-a",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			{ID: "call", Type: "call_subflow", Config: map[string]any{
				"label": "Stamp", "subflow_id": "stamp", "version": "latest",
				"inputs":  []any{map[string]any{"from": "n", "to": "n"}},
				"outputs": []any{map[string]any{"from": "stamped", "to": "stamped"}},
			}},
			{ID: "out", Type: "sink", RefID: "snk-1"},
		},
		Edges: []storage.WorkflowEdge{{ID: "e1", SourceID: "src", TargetID: "call"}, {ID: "e2", SourceID: "call", TargetID: "out"}},
	}); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, http.MethodPost, "/api/workflows/wf-call/run", `{"message":{"n":1}}`, storage.RoleEditor, "team-a"); rec.Code != http.StatusOK {
		t.Fatalf("run: %d %s", rec.Code, rec.Body.String())
	}

	rec := e.do(t, http.MethodGet, "/api/workflows/wf-call/executions", "", storage.RoleViewer, "team-a")
	var resp struct {
		Executions []struct {
			Status string `json:"status"`
			Steps  []struct {
				NodeID   string `json:"node_id"`
				NodeType string `json:"node_type"`
				Label    string `json:"label"`
			} `json:"steps"`
		} `json:"executions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Executions) != 1 {
		t.Fatalf("list: %v %s", err, rec.Body.String())
	}
	ex := resp.Executions[0]
	if ex.Status != "succeeded" {
		t.Fatalf("run status %s: %+v", ex.Status, ex.Steps)
	}
	var inner bool
	for _, s := range ex.Steps {
		if s.NodeID == "call~s" {
			inner = s.Label == "Stamp it" && s.NodeType == "set"
		}
	}
	if !inner {
		t.Fatalf("no labelled step for the set inside the sub-flow: %+v", ex.Steps)
	}

	// The caller's message, held while the sub-flow runs, is not stored again
	// with every step inside it.
	runs := e.do(t, http.MethodGet, "/api/workflows/wf-call/executions", "", storage.RoleViewer, "team-a")
	if strings.Contains(runs.Body.String(), "_hermod_subflow") {
		t.Fatalf("a run lists the saved caller message: %s", runs.Body.String())
	}
	var list struct {
		Executions []struct {
			RunID string `json:"run_id"`
		} `json:"executions"`
	}
	_ = json.Unmarshal(runs.Body.Bytes(), &list)
	one := e.do(t, http.MethodGet, "/api/workflows/wf-call/executions/"+list.Executions[0].RunID, "", storage.RoleViewer, "team-a")
	if one.Code != http.StatusOK || strings.Contains(one.Body.String(), "_hermod_subflow") {
		t.Fatalf("a run's step outputs carry the saved caller message: %d %s", one.Code, one.Body.String())
	}
}
