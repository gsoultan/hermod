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
	"fmt"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// A resumed or replayed message is walked node by node, with no in-degree to
// make a node shared by two edges wait for both and run once. So when more
// than one edge the branch takes leads to the same node, the node has to be
// listed once: listed twice it ran twice, and an execute_sql node behind it
// wrote its row twice.
func TestReplayTargetsListASharedTargetOnce(t *testing.T) {
	edge := func(id, target, label string) storage.WorkflowEdge {
		return storage.WorkflowEdge{ID: id, SourceID: "sw", TargetID: target, Config: map[string]any{"label": label}}
	}
	for _, tc := range []struct {
		name   string
		edges  []storage.WorkflowEdge
		branch string
		want   []string
	}{
		{"named branch plus an unlabelled edge into one node", []storage.WorkflowEdge{
			edge("e1", "shared", "REVISION"), edge("e2", "shared", ""), edge("e3", "other", "SUBMIT"),
		}, "REVISION", []string{"shared"}},
		{"no branch, two edges into one node", []storage.WorkflowEdge{
			edge("e1", "shared", ""), edge("e2", "shared", ""), edge("e3", "other", ""),
		}, "", []string{"shared", "other"}},
		{"cases sharing a node, one taken", []storage.WorkflowEdge{
			edge("e1", "shared", "REVISION"), edge("e2", "shared", "REJECTED"),
		}, "REJECTED", []string{"shared"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf := storage.Workflow{Edges: tc.edges}
			adj := map[string][]string{}
			for _, e := range wf.Edges {
				adj[e.SourceID] = append(adj[e.SourceID], e.TargetID)
			}
			if got := replayTargets(wf, adj, "sw", tc.branch); fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("replayTargets = %v, want %v", got, tc.want)
			}
		})
	}
}
