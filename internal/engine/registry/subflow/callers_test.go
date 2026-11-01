// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package subflow

import (
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

func callerOf(id, name string, calls ...storage.WorkflowNode) storage.Workflow {
	return storage.Workflow{ID: id, Name: name, Nodes: append([]storage.WorkflowNode{{ID: "src", Type: "source"}}, calls...)}
}

func callStep(id, label, subflowID string, version any) storage.WorkflowNode {
	return storage.WorkflowNode{ID: id, Type: TypeCall, Config: map[string]any{
		"label": label, "subflow_id": subflowID, "version": version,
	}}
}

func TestCallersListsEveryStepThatRunsTheSubflow(t *testing.T) {
	wfs := []storage.Workflow{
		callerOf("orders", "Orders", callStep("c1", "Clean address", "address", float64(3)), callStep("c2", "", "address", "latest")),
		callerOf("returns", "Returns", callStep("c3", "Check", "other", float64(1))),
		callerOf("refunds", "Refunds", callStep("c4", "Bad pin", "address", "soon")),
		// The sub-flow itself is never its own caller.
		{ID: "address", Nodes: []storage.WorkflowNode{{ID: "in", Type: TypeInput}}},
	}
	got := Callers(wfs, "address")
	want := []Caller{
		{WorkflowID: "orders", WorkflowName: "Orders", NodeID: "c1", Step: "Clean address", Version: 3},
		{WorkflowID: "orders", WorkflowName: "Orders", NodeID: "c2", Step: "c2", Latest: true},
		{WorkflowID: "refunds", WorkflowName: "Refunds", NodeID: "c4", Step: "Bad pin"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("caller %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCallersOfAnUnusedSubflowIsEmpty(t *testing.T) {
	if got := Callers([]storage.Workflow{callerOf("orders", "Orders")}, "address"); len(got) != 0 {
		t.Fatalf("got %+v, want none", got)
	}
}
