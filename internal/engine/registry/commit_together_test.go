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
	"context"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// Sink nodes that commit together.
//
// A workflow whose branches build different rows for different databases
// cannot use the stand-alone Transaction Group node, which sends the same rows
// to every member. Instead each database is its own sink node and the nodes
// name the transaction they commit in. The registry turns the sink nodes that
// share a name into one two-phase group, so the engine drives them through one
// writer and one event's rows reach every database in one transaction.

func sinkNode(id, ref, transaction string) storage.WorkflowNode {
	n := storage.WorkflowNode{ID: id, Type: "sink", RefID: ref, Config: map[string]any{"label": "DB " + id}}
	if transaction != "" {
		n.Config["transaction"] = transaction
	}
	return n
}

func edges(pairs ...string) []storage.WorkflowEdge {
	out := make([]storage.WorkflowEdge, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, storage.WorkflowEdge{ID: pairs[i] + "-" + pairs[i+1], SourceID: pairs[i], TargetID: pairs[i+1]})
	}
	return out
}

func TestSinkNodesSharingATransactionNameBecomeOneSink(t *testing.T) {
	wf := storage.Workflow{
		ID: "wf",
		Nodes: []storage.WorkflowNode{
			{ID: "S", Type: "source", RefID: "src"},
			sinkNode("A", "pg", "t1"),
			sinkNode("C", "plain", ""),
			sinkNode("B", "my", "t1"),
		},
		Edges: edges("S", "A", "S", "C", "S", "B"),
	}

	plans, err := (&Registry{}).planWorkflowSinks(wf)
	if err != nil {
		t.Fatalf("planWorkflowSinks: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("got %d engine sinks for three sink nodes, want 2: the transaction and the plain node", len(plans))
	}
	group := plans[0]
	if group.transaction != "t1" || len(group.nodes) != 2 || group.nodes[0].ID != "A" || group.nodes[1].ID != "B" {
		t.Errorf("first sink = transaction %q over nodes %v, want t1 over A then B", group.transaction, nodeIDs(group.nodes))
	}
	if plain := plans[1]; plain.transaction != "" || len(plain.nodes) != 1 || plain.nodes[0].ID != "C" {
		t.Errorf("second sink = transaction %q over nodes %v, want the plain node C alone", plain.transaction, nodeIDs(plain.nodes))
	}
}

func nodeIDs(nodes []storage.WorkflowNode) []string {
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	return ids
}

// A single database already commits its own rows atomically, so a transaction
// with one member is a mistake in the workflow — most likely a node that was
// meant to join and did not — and it is refused by name rather than run as a
// group of one.
func TestATransactionWithOneMemberIsRefused(t *testing.T) {
	wf := storage.Workflow{
		ID: "wf",
		Nodes: []storage.WorkflowNode{
			{ID: "S", Type: "source", RefID: "src"},
			sinkNode("A", "pg", "orders"),
		},
		Edges: edges("S", "A"),
	}

	_, err := (&Registry{}).planWorkflowSinks(wf)
	if err == nil {
		t.Fatal("a transaction with one member was accepted")
	}
	for _, want := range []string{`"orders"`, "DB A (A)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should name %s, got: %v", want, err)
		}
	}
}

// A sink node nobody connected is not part of the workflow, and not part of the
// transaction either: the other member then commits alone, which is refused.
func TestAnUnconnectedSinkNodeIsNotAMember(t *testing.T) {
	wf := storage.Workflow{
		ID: "wf",
		Nodes: []storage.WorkflowNode{
			{ID: "S", Type: "source", RefID: "src"},
			sinkNode("A", "pg", "t1"),
			sinkNode("B", "my", "t1"),
		},
		Edges: edges("S", "A"),
	}

	if _, err := (&Registry{}).planWorkflowSinks(wf); err == nil {
		t.Fatal("a transaction whose only connected member is A was accepted")
	}
}

// The group is refused by the node that cannot take part, not by the sink
// record behind it: the node is what the person sees on the canvas.
func TestASinkThatCannotCommitTogetherIsRefusedByNode(t *testing.T) {
	r := registryWithSinks(map[string]storage.Sink{
		"pg":    postgresSink("pg"),
		"kafka": {ID: "kafka", Type: "kafka", Config: map[string]string{"brokers": "127.0.0.1:1"}},
	})
	wf := storage.Workflow{
		ID: "wf",
		Nodes: []storage.WorkflowNode{
			{ID: "S", Type: "source", RefID: "src"},
			sinkNode("A", "pg", "t1"),
			sinkNode("K", "kafka", "t1"),
		},
		Edges: edges("S", "A", "S", "K"),
	}

	_, _, _, err := r.discoverWorkflowSinks(context.Background(), wf, &multiSource{})
	if err == nil {
		t.Fatal("a Kafka sink node was accepted as a member of a transaction")
	}
	for _, want := range []string{"DB K (K)", "kafka", `"t1"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should name %s, got: %v", want, err)
		}
	}
}

// Saving catches what running would refuse, so the mistake is seen in the
// editor and not in the run log.
func TestSavingAWorkflowWithAOneMemberTransactionIsRefused(t *testing.T) {
	reg := newSimRegistry(t)
	wf := storage.Workflow{
		ID: "wf", Name: "one member",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			sinkNode("A", "snk-1", "t1"),
		},
		Edges: edges("src", "A"),
	}

	err := reg.ValidateWorkflow(t.Context(), wf)
	if err == nil {
		t.Fatal("a workflow with a one-member transaction was accepted")
	}
	if !strings.Contains(err.Error(), `"t1"`) {
		t.Errorf("the error should name the transaction, got: %v", err)
	}
}

func TestSavingAWorkflowWhoseMemberCannotCommitTogetherIsRefused(t *testing.T) {
	reg := newSimRegistry(t)
	// snk-1 is a SQLite sink, which has no two-phase commit.
	wf := storage.Workflow{
		ID: "wf", Name: "sqlite member",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			sinkNode("A", "snk-1", "t1"),
			sinkNode("B", "snk-1", "t1"),
		},
		Edges: edges("src", "A", "src", "B"),
	}

	err := reg.ValidateWorkflow(t.Context(), wf)
	if err == nil {
		t.Fatal("a workflow whose transaction members are SQLite sinks was accepted")
	}
	for _, want := range []string{"DB A (A)", "sqlite"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should name %s, got: %v", want, err)
		}
	}
}
