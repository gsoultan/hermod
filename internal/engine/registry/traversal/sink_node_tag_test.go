// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package traversal_test

import (
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/traversal"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// A routed message says which sink node it reached.
//
// Two sink nodes can share one engine sink: the members of a transaction that
// commit together are one sink to the engine. The row that branch X built for
// database A and the row that branch Y built for database B then arrive at the
// same sink in the same batch, and the only thing that can tell them apart is
// the node each one came through. The traversal records it on the message.

// routed walks one message from S through the given graph and returns what
// reached each sink node, keyed by the node's ID as the message reports it.
func routed(t *testing.T, nodes map[string]*storage.WorkflowNode, adj map[string][]string, sinks map[string]int) map[string][]string {
	t.Helper()
	reg := &mockRegistry{}
	reg.RunWorkflowNodeFn = func(_ string, _ *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
		return pass(msg)
	}
	index := make(map[string]int, len(nodes))
	for id := range nodes {
		index[id] = len(index)
	}
	msg := message.AcquireMessage()
	msg.SetID("m1")

	tr := traversal.Acquire(reg, pkgengine.NewEngine(nil, nil, nil), "wf", nodes, adj, index, nil, nil,
		traversal.ReachableInDegree(adj, "S"), sinks)
	msg.Retain()
	tr.CurrentMessages[index["S"]] = msg
	tr.Traverse(t.Context(), "S")

	out := map[string][]string{}
	for _, r := range tr.Routed {
		node, _ := hermod.MetadataValue(r.Message, hermod.MetaSinkNode)
		out[node] = append(out[node], r.Message.ID())
		r.Message.Release()
	}
	traversal.Release(tr)
	msg.Release()
	return out
}

func TestARoutedMessageNamesTheSinkNodeItReached(t *testing.T) {
	// S → A and S → B, both sink nodes, both mapped to the same engine sink.
	got := routed(t,
		map[string]*storage.WorkflowNode{
			"S": {ID: "S", Type: "source"},
			"A": {ID: "A", Type: "sink"},
			"B": {ID: "B", Type: "sink"},
		},
		map[string][]string{"S": {"A", "B"}},
		map[string]int{"A": 0, "B": 0},
	)
	if len(got["A"]) != 1 || len(got["B"]) != 1 {
		t.Fatalf("routed messages by sink node = %v, want one through A and one through B", got)
	}
	if unnamed := got[""]; len(unnamed) != 0 {
		t.Errorf("%d routed message(s) do not say which sink node they reached", len(unnamed))
	}
}

func TestTheTagFollowsTheMessageThroughATransform(t *testing.T) {
	got := routed(t,
		map[string]*storage.WorkflowNode{
			"S": {ID: "S", Type: "source"},
			"T": {ID: "T", Type: "transformer"},
			"K": {ID: "K", Type: "sink"},
		},
		map[string][]string{"S": {"T"}, "T": {"K"}},
		map[string]int{"K": 0},
	)
	if len(got["K"]) != 1 {
		t.Fatalf("routed messages by sink node = %v, want one through K", got)
	}
}
