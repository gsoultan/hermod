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
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// store is a loader over a fixed set of workflows, each saved at one version.
type store map[string]storage.Workflow

func (s store) load(_ context.Context, id string, version int) (storage.Workflow, error) {
	wf, ok := s[id]
	if !ok {
		return storage.Workflow{}, errors.New("not found")
	}
	if version != 0 && version != 3 {
		return storage.Workflow{}, errors.New("no such version")
	}
	return wf, nil
}

func node(id, typ string, cfg map[string]any) storage.WorkflowNode {
	return storage.WorkflowNode{ID: id, Type: typ, Config: cfg}
}

func edge(id, from, to, label string) storage.WorkflowEdge {
	return storage.WorkflowEdge{ID: id, SourceID: from, TargetID: to, SourceHandle: label}
}

func call(id, subflowID string, extra map[string]any) storage.WorkflowNode {
	cfg := map[string]any{"subflow_id": subflowID, "version": float64(3)}
	for k, v := range extra {
		cfg[k] = v
	}
	return node(id, TypeCall, cfg)
}

// address is a sub-flow with one input field and two returns.
func address() storage.Workflow {
	return storage.Workflow{
		ID: "address",
		Nodes: []storage.WorkflowNode{
			node("in", TypeInput, map[string]any{"fields": []any{map[string]any{"name": "zip", "required": true}}}),
			node("geo", "transformation", nil),
			node("sw", "switch", nil),
			node("ok", TypeReturn, map[string]any{"name": "valid"}),
			node("bad", TypeReturn, map[string]any{"name": "review"}),
		},
		Edges: []storage.WorkflowEdge{
			edge("e1", "in", "geo", ""),
			edge("e2", "geo", "sw", ""),
			edge("e3", "sw", "ok", "high"),
			edge("e4", "sw", "bad", "low"),
		},
	}
}

func parent(callCfg map[string]any, edges ...storage.WorkflowEdge) storage.Workflow {
	return storage.Workflow{
		ID: "orders",
		Nodes: []storage.WorkflowNode{
			node("src", "source", nil),
			call("c", "address", callCfg),
			node("snk", "sink", nil),
			node("slack", "sink", nil),
			node("dlq", "sink", nil),
		},
		Edges: append([]storage.WorkflowEdge{edge("p1", "src", "c", "")}, edges...),
	}
}

var zipInput = map[string]any{"inputs": []any{map[string]any{"from": "customer.zip", "to": "zip"}}}

func ids(wf storage.Workflow) []string {
	out := make([]string, 0, len(wf.Nodes))
	for _, n := range wf.Nodes {
		out = append(out, n.ID+":"+n.Type)
	}
	slices.Sort(out)
	return out
}

func edgeSet(wf storage.Workflow) []string {
	out := make([]string, 0, len(wf.Edges))
	for _, e := range wf.Edges {
		out = append(out, e.SourceID+">"+e.TargetID+"["+edgeLabel(e)+"]")
	}
	slices.Sort(out)
	return out
}

func TestExpandSplicesTheSubflowInPlaceOfTheCall(t *testing.T) {
	wf := parent(zipInput,
		edge("p2", "c", "snk", "valid"),
		edge("p3", "c", "slack", "review"),
	)
	got, err := Expand(context.Background(), wf, store{"address": address()}.load)
	if err != nil {
		t.Fatal(err)
	}
	wantNodes := []string{
		"c:" + TypeEnter, "c~bad:" + TypeExit, "c~geo:transformation", "c~ok:" + TypeExit, "c~sw:switch",
		"dlq:sink", "slack:sink", "snk:sink", "src:source",
	}
	if g := ids(got); !slices.Equal(g, wantNodes) {
		t.Fatalf("nodes\n got %v\nwant %v", g, wantNodes)
	}
	wantEdges := []string{
		"c>c~geo[]", "c~bad>slack[]", "c~geo>c~sw[]", "c~ok>snk[]",
		"c~sw>c~bad[low]", "c~sw>c~ok[high]", "src>c[]",
	}
	if g := edgeSet(got); !slices.Equal(g, wantEdges) {
		t.Fatalf("edges\n got %v\nwant %v", g, wantEdges)
	}
	for _, n := range got.Nodes {
		if n.Type == TypeExit && n.Config["call_id"] != "c" {
			t.Fatalf("exit %s belongs to call %v, want c", n.ID, n.Config["call_id"])
		}
	}
	if len(wf.Nodes) != 5 {
		t.Fatal("Expand changed the workflow it was given")
	}
}

func TestExpandSendsAnUnlabelledEdgeFromEveryReturn(t *testing.T) {
	got, err := Expand(context.Background(), parent(zipInput, edge("p2", "c", "snk", "")), store{"address": address()}.load)
	if err != nil {
		t.Fatal(err)
	}
	g := edgeSet(got)
	for _, want := range []string{"c~ok>snk[]", "c~bad>snk[]"} {
		if !slices.Contains(g, want) {
			t.Fatalf("missing %s in %v", want, g)
		}
	}
}

func TestExpandRoutesUnhandledInnerFailuresToTheCallsErrorEdge(t *testing.T) {
	sub := address()
	sub.Nodes = append(sub.Nodes, node("fix", "transformation", nil))
	sub.Edges = append(sub.Edges, edge("e5", "geo", "fix", "error"), edge("e6", "fix", "ok", ""))
	got, err := Expand(context.Background(), parent(zipInput,
		edge("p2", "c", "snk", ""),
		edge("p3", "c", "dlq", "error"),
	), store{"address": sub}.load)
	if err != nil {
		t.Fatal(err)
	}
	g := edgeSet(got)
	for _, want := range []string{
		"c>dlq[error]",         // the call itself failing: the parent's message is untouched
		"c~sw>c~error[error]",  // no error edge of its own
		"c~fix>c~error[error]", // no error edge of its own
		"c~error>dlq[]",        // the restored parent message continues
		"c~geo>c~fix[error]",   // handled inside the sub-flow
	} {
		if !slices.Contains(g, want) {
			t.Fatalf("missing %s in %v", want, g)
		}
	}
	if slices.Contains(g, "c~geo>c~error[error]") {
		t.Fatalf("a node that handles its own errors was given a second error edge: %v", g)
	}
	var restore storage.WorkflowNode
	for _, n := range got.Nodes {
		if n.ID == "c~error" {
			restore = n
		}
	}
	if restore.Type != TypeExit || restore.Config["on_error"] != true || restore.Config["call_id"] != "c" {
		t.Fatalf("error exit = %+v", restore)
	}
}

func TestExpandNestsPrefixes(t *testing.T) {
	inner := storage.Workflow{ID: "geo", Nodes: []storage.WorkflowNode{
		node("in", TypeInput, nil), node("look", "transformation", nil), node("r", TypeReturn, nil),
	}, Edges: []storage.WorkflowEdge{edge("a", "in", "look", ""), edge("b", "look", "r", "")}}
	outer := storage.Workflow{ID: "address", Nodes: []storage.WorkflowNode{
		node("in", TypeInput, nil), call("g", "geo", nil), node("r", TypeReturn, nil),
	}, Edges: []storage.WorkflowEdge{edge("a", "in", "g", ""), edge("b", "g", "r", "")}}
	got, err := Expand(context.Background(), parent(nil, edge("p2", "c", "snk", "")), store{"address": outer, "geo": inner}.load)
	if err != nil {
		t.Fatal(err)
	}
	g := ids(got)
	for _, want := range []string{"c~g:" + TypeEnter, "c~g~look:transformation", "c~g~r:" + TypeExit} {
		if !slices.Contains(g, want) {
			t.Fatalf("missing %s in %v", want, g)
		}
	}
	for _, n := range got.Nodes {
		if n.ID == "c~g~r" && n.Config["call_id"] != "c~g" {
			t.Fatalf("nested exit belongs to %v, want c~g", n.Config["call_id"])
		}
	}
}

func TestExpandRefuses(t *testing.T) {
	self := address()
	self.Nodes = append(self.Nodes, call("loop", "address", nil))
	self.Edges = append(self.Edges, edge("e9", "geo", "loop", ""))

	notSub := storage.Workflow{ID: "address", Nodes: []storage.WorkflowNode{node("s", "source", nil)}}

	withSource := address()
	withSource.Nodes = append(withSource.Nodes, node("s", "source", nil))

	deep := map[string]storage.Workflow{}
	for i := range MaxDepth + 1 {
		id := "d" + string(rune('a'+i))
		next := "d" + string(rune('a'+i+1))
		deep[id] = storage.Workflow{ID: id, Nodes: []storage.WorkflowNode{
			node("in", TypeInput, nil), call("x", next, nil), node("r", TypeReturn, nil),
		}, Edges: []storage.WorkflowEdge{edge("a", "in", "x", ""), edge("b", "x", "r", "")}}
	}
	deepParent := parent(nil)
	deepParent.Nodes[1] = call("c", "da", nil)

	for _, tc := range []struct {
		name string
		wf   storage.Workflow
		st   store
		want string
	}{
		{"calls itself", parent(zipInput), store{"address": self}, "calls itself"},
		{"not a sub-flow", parent(zipInput), store{"address": notSub}, "not a sub-flow"},
		{"sub-flow with a source", parent(zipInput), store{"address": withSource}, "source"},
		{"missing", parent(zipInput), store{}, "not found"},
		{"required input unmapped", parent(nil), store{"address": address()}, `"zip"`},
		{"no version", parent(map[string]any{"version": nil, "inputs": zipInput["inputs"]}), store{"address": address()}, "version"},
		{"no sub-flow chosen", parent(map[string]any{"subflow_id": ""}), store{}, "choose"},
		{"unknown return", parent(zipInput, edge("p2", "c", "snk", "nope")), store{"address": address()}, `"nope"`},
		{"too deep", deepParent, store(deep), "deeper than"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Expand(context.Background(), tc.wf, tc.st.load)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %s", err, tc.want)
			}
		})
	}
}

func TestExpandLeavesAWorkflowWithoutCallsAlone(t *testing.T) {
	wf := address()
	got, err := Expand(context.Background(), wf, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(got), ids(wf)) || !slices.Equal(edgeSet(got), edgeSet(wf)) {
		t.Fatal("a workflow without calls changed")
	}
}

func TestIsSubflow(t *testing.T) {
	if !IsSubflow(address()) {
		t.Fatal("a workflow with an input node is a sub-flow")
	}
	if IsSubflow(parent(nil)) {
		t.Fatal("a workflow with a source is not a sub-flow")
	}
}

func TestWithoutFramesLeavesTheStepItWasGiven(t *testing.T) {
	step := map[string]any{"a": 1, "metadata": map[string]string{FramePrefix + "c": "{}", "idempotency_key": "k"}}
	got := WithoutFrames(step)
	md, _ := got["metadata"].(map[string]string)
	if _, ok := md[FramePrefix+"c"]; ok || md["idempotency_key"] != "k" || got["a"] != 1 {
		t.Fatalf("WithoutFrames = %v", got)
	}
	if _, ok := step["metadata"].(map[string]string)[FramePrefix+"c"]; !ok {
		t.Fatal("WithoutFrames changed the step it was given")
	}
	plain := map[string]any{"metadata": map[string]any{"x": "y"}}
	if g := WithoutFrames(plain); g["metadata"].(map[string]any)["x"] != "y" {
		t.Fatal("a step with nothing held changed")
	}
}
