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
	"context"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/traversal"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// A node cut off by the workflow stopping is not a failure of the node: the
// traversal must record the walk as interrupted, so the router refuses to
// deliver the branches that did finish, and must not log it as an error. A
// restart under load logged "Node api_score failed: context canceled" for every
// request in flight, and a merge node behind it "failed" on the half-message
// its other branch brought, both at ERROR, for a stop that lost nothing.
//
//	S ─┬─> A (sink)
//	   └─> B (cut off by the stop) ─┐
//	   └─> C ───────────────────────┴─> M (merge, needs B's field) ─> K (sink)
func TestWorkflowTraversal_StopIsAnInterruptionNotAFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	reg := &mockRegistry{}
	reg.RunWorkflowNodeFn = func(_ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
		switch node.ID {
		case "B":
			cancel()
			return nil, "", context.Canceled
		case "M":
			return nil, "", errMissingField
		}
		msg.Retain()
		return []hermod.Message{msg}, "", nil
	}
	eng := pkgengine.NewEngine(nil, nil, nil)

	nodeMap := map[string]*storage.WorkflowNode{
		"S": {ID: "S", Type: "source"},
		"A": {ID: "A", Type: "sink"},
		"B": {ID: "B", Type: "transformation"},
		"C": {ID: "C", Type: "transformation"},
		"M": {ID: "M", Type: "transformation"},
		"K": {ID: "K", Type: "sink"},
	}
	adj := map[string][]string{"S": {"A", "B", "C"}, "B": {"M"}, "C": {"M"}, "M": {"K"}}
	nodeIndex := map[string]int{"S": 0, "A": 1, "B": 2, "C": 3, "M": 4, "K": 5}
	sinkNodeToIndex := map[string]int{"A": 0, "K": 1}
	inDegree := traversal.ReachableInDegree(adj, "S")

	msg := message.AcquireMessage()
	msg.SetID("m1")
	defer msg.Release()
	tr := traversal.Acquire(reg, eng, "wf-stop", nodeMap, adj, nodeIndex, nil, nil, inDegree, sinkNodeToIndex)
	defer traversal.Release(tr)
	msg.Retain()
	tr.CurrentMessages[nodeIndex["S"]] = msg

	tr.Traverse(ctx, "S")

	if !tr.Interrupted.Load() {
		t.Error("the walk the stop cut short is not recorded as interrupted; the router would " +
			"deliver A and acknowledge a message K never got")
	}
	for _, l := range reg.Logs {
		if strings.Contains(l, "failed") {
			t.Errorf("a stop was logged as a node failure: %q", l)
		}
	}
	for _, r := range tr.Routed {
		r.Message.Release()
	}
}

var errMissingField = &missingFieldErr{}

type missingFieldErr struct{}

func (*missingFieldErr) Error() string { return `no value for key path "b.field"` }
