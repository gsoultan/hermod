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

// Before the editor moved a switch case's edges with it, renaming a case left
// its edge on the old name: hidden on the canvas, but saved, promoted and
// imported. A workflow built that way reached production with an edge
// labelled APPROVE out of a switch whose case is APPROVED. Save, import and
// promotion all validate here, so the edge is refused before it travels.

import (
	"context"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

func switchWorkflow(edges ...storage.WorkflowEdge) storage.Workflow {
	base := []storage.WorkflowEdge{{ID: "e0", SourceID: "src1", TargetID: "sw"}}
	return storage.Workflow{
		Name: "wf",
		Nodes: []storage.WorkflowNode{
			{ID: "src1", Type: "source", RefID: "s1"},
			{ID: "sw", Type: "switch", Config: map[string]any{
				"label": "Pipeline Category",
				"cases": []any{
					map[string]any{"label": "SUBMIT"},
					map[string]any{"label": "APPROVED"},
					map[string]any{"label": ""},
				},
			}},
			{ID: "snk1", Type: "sink", RefID: "k1"},
		},
		Edges: append(base, edges...),
	}
}

func branchEdge(id, handle string) storage.WorkflowEdge {
	return storage.WorkflowEdge{ID: id, SourceID: "sw", TargetID: "snk1", SourceHandle: handle,
		Config: map[string]any{"label": handle}}
}

func TestValidateWorkflowRefusesAnEdgeNamingNoCase(t *testing.T) {
	h := &WorkflowHandler{Handler: &handlers.Handler{}}

	cases := []struct {
		name    string
		edges   []storage.WorkflowEdge
		wantBad []string
	}{
		{"every edge names a case", []storage.WorkflowEdge{
			branchEdge("e1", "SUBMIT"), branchEdge("e2", "APPROVED"), branchEdge("e3", "case_2"), branchEdge("e4", "default"),
		}, nil},
		{"an unlabelled edge takes every branch", []storage.WorkflowEdge{
			{ID: "e1", SourceID: "sw", TargetID: "snk1"},
		}, nil},
		{"an edge left on a renamed case", []storage.WorkflowEdge{
			branchEdge("e1", "SUBMIT"), branchEdge("e2", "APPROVE"), branchEdge("e3", "APPROVED"),
		}, []string{"e2"}},
		{"the label wins over the handle, as in the engine", []storage.WorkflowEdge{
			{ID: "e1", SourceID: "sw", TargetID: "snk1", SourceHandle: "APPROVED", Config: map[string]any{"label": "APPROVE"}},
		}, []string{"e1"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var bad []string
			for _, issue := range h.ValidateWorkflow(context.Background(), switchWorkflow(tc.edges...)) {
				if issue.Severity == "error" && strings.Contains(issue.Message, "matches no case") {
					if issue.NodeID != "sw" {
						t.Errorf("issue names node %q, want sw", issue.NodeID)
					}
					for _, e := range tc.edges {
						if strings.Contains(issue.Message, "'"+e.ID+"'") {
							bad = append(bad, e.ID)
						}
					}
				}
			}
			if strings.Join(bad, ",") != strings.Join(tc.wantBad, ",") {
				t.Fatalf("refused edges %v, want %v", bad, tc.wantBad)
			}
		})
	}
}

func TestValidateWorkflowRefusesAnEdgeNamingNoRouterRule(t *testing.T) {
	h := &WorkflowHandler{Handler: &handlers.Handler{}}
	wf := storage.Workflow{
		Name: "wf",
		Nodes: []storage.WorkflowNode{
			{ID: "src1", Type: "source", RefID: "s1"},
			{ID: "rt", Type: "router", Config: map[string]any{"rules": `[{"label":"europe"}]`}},
			{ID: "snk1", Type: "sink", RefID: "k1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e0", SourceID: "src1", TargetID: "rt"},
			{ID: "e1", SourceID: "rt", TargetID: "snk1", SourceHandle: "eu", Config: map[string]any{"label": "eu"}},
		},
	}
	for _, issue := range h.ValidateWorkflow(context.Background(), wf) {
		if issue.Severity == "error" && strings.Contains(issue.Message, "'e1'") && strings.Contains(issue.Message, "matches no rule") {
			return
		}
	}
	t.Fatal("an edge labelled eu out of a router whose only rule is europe was not refused")
}
