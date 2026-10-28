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
	"sort"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// Two cases of a switch that should do the same thing are drawn as two edges
// into the same node:
//
//	src ─> sw ─SUBMIT────────────> submit
//	       ├──REVISION──┐
//	       └──REJECTED──┴────────> close
//
// The live traversal looked an edge's label up by its source and target, one
// label per pair, so the second edge's label overwrote the first: the pair
// sw→close carried only REJECTED. A REVISION message matched its case, found
// no edge labelled REVISION, and stopped at the switch. The editor's
// simulation walks the stored edges one by one and showed it reaching close.
func TestSwitchCasesSharingATargetEachReachIt(t *testing.T) {
	const rows = 3 // seq 1 is SUBMIT, 2 is REVISION, 3 is REJECTED

	store := newPipeStorage()
	reg := NewRegistry(store)
	sinks := map[string]*pipeSink{
		"snk-submit": {name: "submit"},
		"snk-close":  {name: "close"},
	}

	wf := storage.Workflow{
		ID:   "wf-switch-shared-target",
		Name: "wf-switch-shared-target",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s-orders"},
			{ID: "sw", Type: "switch", Config: map[string]any{
				"label": "Switch", "type": "switch", "field": "seq",
				"cases": []any{
					map[string]any{"label": "SUBMIT", "operator": "=", "value": "1"},
					map[string]any{"label": "REVISION", "operator": "=", "value": "2"},
					map[string]any{"label": "REJECTED", "operator": "=", "value": "3"},
				},
			}},
			{ID: "submit", Type: "sink", RefID: "snk-submit"},
			{ID: "close", Type: "sink", RefID: "snk-close"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e-in", SourceID: "src", TargetID: "sw"},
			{ID: "e-submit", SourceID: "sw", TargetID: "submit", Config: map[string]any{"label": "SUBMIT"}},
			{ID: "e-revision", SourceID: "sw", TargetID: "close", Config: map[string]any{"label": "REVISION"}},
			{ID: "e-rejected", SourceID: "sw", TargetID: "close", Config: map[string]any{"label": "REJECTED"}},
		},
		MaxRetries:    5,
		RetryInterval: "10ms",
	}

	message.ResetOverReleaseCount()
	stop := startForeachPipeline(t, reg, wf, &pipeSource{name: "orders", count: rows}, sinks)
	defer stop()

	waitUntil(t, 10*time.Second, "every case at its sink", func() bool {
		return sinks["snk-submit"].distinct() >= 1 && sinks["snk-close"].distinct() >= 2
	})

	seqs := func(s *pipeSink) []string {
		seen := map[string]bool{}
		var out []string
		for _, row := range s.received() {
			seq := fmt.Sprint(row["seq"])
			if !seen[seq] {
				seen[seq] = true
				out = append(out, seq)
			}
		}
		sort.Strings(out)
		return out
	}
	if got := seqs(sinks["snk-submit"]); fmt.Sprint(got) != "[1]" {
		t.Errorf("SUBMIT sink received seq %v, want [1]", got)
	}
	if got := seqs(sinks["snk-close"]); fmt.Sprint(got) != "[2 3]" {
		t.Errorf("shared REVISION/REJECTED sink received seq %v, want [2 3] (REVISION is seq 2)", got)
	}
	if n := message.OverReleaseCount(); n != 0 {
		t.Errorf("the pipeline over-released %d message reference(s)", n)
	}
}
