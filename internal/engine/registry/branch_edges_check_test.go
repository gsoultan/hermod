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
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// A switch case with no edge drops every message that matches it, and the
// engine acknowledges each one as filtered without a word. The usual cause is
// renaming a case after its edge was drawn: the edge keeps the old name, which
// now matches no case. Both halves are checked when the workflow starts, so
// the workflow's log says which case goes nowhere instead of the messages
// quietly disappearing.
func TestUnroutedBranchesAreReported(t *testing.T) {
	sw := storage.WorkflowNode{ID: "sw", Type: "switch", Config: map[string]any{
		"label": "Switch",
		"cases": []any{
			map[string]any{"label": "SUBMIT"},
			map[string]any{"label": "REVISION"},
			map[string]any{"label": "REJECTED"},
		},
	}}
	edge := func(id, target, label string) storage.WorkflowEdge {
		return storage.WorkflowEdge{ID: id, SourceID: "sw", TargetID: target, Config: map[string]any{"label": label}}
	}
	nodes := []storage.WorkflowNode{sw, {ID: "a", Type: "sink"}, {ID: "b", Type: "sink"}}

	t.Run("every case has an edge", func(t *testing.T) {
		wf := storage.Workflow{Nodes: nodes, Edges: []storage.WorkflowEdge{
			edge("e1", "a", "SUBMIT"), edge("e2", "b", "REVISION"), edge("e3", "b", "REJECTED"), edge("e4", "a", "default"),
		}}
		if got := unroutedBranches(wf); len(got) != 0 {
			t.Errorf("reported %v for a switch whose every case has an edge", got)
		}
	})

	t.Run("a renamed case leaves its edge behind", func(t *testing.T) {
		wf := storage.Workflow{Nodes: nodes, Edges: []storage.WorkflowEdge{
			edge("e1", "a", "SUBMIT"), edge("e2", "b", "REVISON"), edge("e3", "b", "REJECTED"),
		}}
		got := strings.Join(unroutedBranches(wf), "\n")
		for _, want := range []string{`case "REVISION" has no edge`, `labelled "REVISON"`} {
			if !strings.Contains(got, want) {
				t.Errorf("report %q does not mention %q", got, want)
			}
		}
	})

	t.Run("an unlabelled edge carries every case", func(t *testing.T) {
		wf := storage.Workflow{Nodes: nodes, Edges: []storage.WorkflowEdge{edge("e1", "a", "")}}
		if got := unroutedBranches(wf); len(got) != 0 {
			t.Errorf("reported %v, but an unlabelled edge takes every branch", got)
		}
	})
}

// The check runs when the workflow starts and lands in the workflow's own log.
// It is logged after StartWorkflow releases the registry lock, which logging
// takes for reading: logged under it, starting the workflow would deadlock.
func TestStartingAWorkflowLogsAnUnroutedCase(t *testing.T) {
	store := newPipeStorage()
	reg := NewRegistry(store)
	sinks := map[string]*pipeSink{"snk-a": {name: "a"}}
	wf := storage.Workflow{
		ID:   "wf-unrouted-case",
		Name: "wf-unrouted-case",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s-orders"},
			{ID: "sw", Type: "switch", Config: map[string]any{
				"label": "Switch", "field": "seq",
				"cases": []any{
					map[string]any{"label": "SUBMIT", "operator": "=", "value": "1"},
					map[string]any{"label": "REVISION", "operator": "=", "value": "2"},
				},
			}},
			{ID: "a", Type: "sink", RefID: "snk-a"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e-in", SourceID: "src", TargetID: "sw"},
			{ID: "e-a", SourceID: "sw", TargetID: "a", Config: map[string]any{"label": "SUBMIT"}},
		},
	}
	logs := reg.SubscribeWorkflowLogs(wf.ID)
	stop := startForeachPipeline(t, reg, wf, &pipeSource{name: "orders", count: 0}, sinks)
	defer stop()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case l := <-logs:
			if l.Level == "WARN" && strings.Contains(l.Message, `case "REVISION" has no edge`) {
				return
			}
		case <-deadline:
			t.Fatal("starting the workflow logged no warning for the REVISION case that has no edge")
		}
	}
}
