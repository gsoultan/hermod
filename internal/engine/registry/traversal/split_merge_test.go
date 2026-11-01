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
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/traversal"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// A line split in two and drawn back together:
//
//	S ─> A (adds a) ─┐
//	S ─> B (adds b) ─┴─> J (sink)
//
// Both branches deliver, J fires once, and what it runs on has to carry what
// both branches did. resolveEdge kept only the first message to arrive and
// merged the second into it for a Stateful Join node alone, so a plain node
// where two live branches met ran on one branch's data and the other's was
// dropped with nothing to say so. The editor's simulation merges them
// (simulation.deliver), so the preview showed both and the workflow wrote one.
//
// Both delivery orders are walked, since the order decided which branch was
// lost.
func TestWorkflowTraversal_BranchesThatBothDeliverMergeWhereTheyMeet(t *testing.T) {
	for _, out := range [][]string{{"A", "B"}, {"B", "A"}} {
		t.Run(out[0]+" first", func(t *testing.T) {
			message.ResetOverReleaseCount()

			var mu sync.Mutex
			ran := map[string]int{}
			var atJ map[string]any
			reg := &mockRegistry{}
			reg.RunWorkflowNodeFn = func(_ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
				mu.Lock()
				defer mu.Unlock()
				ran[node.ID]++
				switch node.ID {
				case "A":
					msg.SetData("a", 1)
					msg.SetData("shared", map[string]any{"from_a": true})
				case "B":
					msg.SetData("b", 2)
					msg.SetData("shared", map[string]any{"from_b": true})
				case "J":
					atJ = msg.Data()
				}
				msg.Retain()
				return []hermod.Message{msg}, "", nil
			}
			eng := pkgengine.NewEngine(nil, nil, nil)

			nodeMap := map[string]*storage.WorkflowNode{
				"S": {ID: "S", Type: "source"},
				"A": {ID: "A", Type: "passthrough"},
				"B": {ID: "B", Type: "passthrough"},
				"J": {ID: "J", Type: "sink"},
			}
			adj := map[string][]string{"S": out, "A": {"J"}, "B": {"J"}}
			nodeIndex := map[string]int{"S": 0, "A": 1, "B": 2, "J": 3}

			msg := message.AcquireMessage()
			msg.SetID("m1")
			msg.SetData("id", "m1")
			tr := traversal.Acquire(reg, eng, "wf-split-merge", nodeMap, adj, nodeIndex, nil, nil,
				traversal.ReachableInDegree(adj, "S"), map[string]int{"J": 0})
			msg.Retain()
			tr.CurrentMessages[nodeIndex["S"]] = msg

			tr.Traverse(t.Context(), "S")

			mu.Lock()
			ranJ := ran["J"]
			got := atJ
			mu.Unlock()
			routed := len(tr.Routed)
			for _, rm := range tr.Routed {
				rm.Message.Release()
			}
			traversal.Release(tr)
			msg.Release()

			if ranJ != 1 {
				t.Fatalf("the node both branches lead to ran %d time(s), want 1", ranJ)
			}
			if routed != 1 {
				t.Errorf("the sink was handed %d message(s), want 1", routed)
			}
			if got["a"] != 1 || got["b"] != 2 {
				t.Errorf("the merged message carried a=%v b=%v, want a=1 b=2: one branch's work was dropped", got["a"], got["b"])
			}
			if got["id"] != "m1" {
				t.Errorf("the merged message lost the field both branches carried: id=%v", got["id"])
			}
			shared, _ := got["shared"].(map[string]any)
			if shared["from_a"] != true || shared["from_b"] != true {
				t.Errorf("shared = %v, want both branches' keys: a nested object is merged, not replaced", got["shared"])
			}
			if n := message.OverReleaseCount(); n != 0 {
				t.Errorf("the traversal over-released %d reference(s)", n)
			}
		})
	}
}
