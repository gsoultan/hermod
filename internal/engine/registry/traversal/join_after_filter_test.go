package traversal_test

import (
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/traversal"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// A node on a taken branch can emit nothing: a filter transformation drops the
// message and returns (nil, "", nil). That edge has then been walked as surely
// as a pruned one — nothing is ever going to arrive along it — but
// handleResults only pruned edges the branch did not take, and delivered one
// message per output, so an empty output did neither. A join downstream kept
// waiting for an edge that would never resolve, and the message the other
// branch carried to it was never written.
//
//	S ─> F (filter, drops) ─┐
//	S ─> B ─────────────────┴─> J (join) ─> K (sink)
//
// The editor's simulation already counts such an edge as walked
// (simulation.forward), so a preview showed the join firing while the live
// workflow dropped the message.
func TestWorkflowTraversal_JoinFiresWhenABranchEmitsNothing(t *testing.T) {
	cases := []struct {
		name      string
		drops     map[string]bool
		wantFired bool
	}{
		{name: "one branch filtered", drops: map[string]bool{"F": true}, wantFired: true},
		{name: "every branch filtered", drops: map[string]bool{"F": true, "B": true}, wantFired: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			message.ResetOverReleaseCount()

			reg := &mockRegistry{}
			reg.RunWorkflowNodeFn = func(_ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
				if tc.drops[node.ID] {
					return nil, "", nil
				}
				msg.Retain()
				return []hermod.Message{msg}, "", nil
			}
			eng := pkgengine.NewEngine(nil, nil, nil)

			nodeMap := map[string]*storage.WorkflowNode{
				"S": {ID: "S", Type: "source"},
				"F": {ID: "F", Type: "transformation"},
				"B": {ID: "B", Type: "passthrough"},
				"J": {ID: "J", Type: "join"},
				"K": {ID: "K", Type: "sink"},
			}
			adj := map[string][]string{
				"S": {"F", "B"},
				"F": {"J"},
				"B": {"J"},
				"J": {"K"},
			}
			nodeIndex := map[string]int{"S": 0, "F": 1, "B": 2, "J": 3, "K": 4}
			sinkNodeToIndex := map[string]int{"K": 0}
			inDegree := traversal.ReachableInDegree(adj, "S")

			msg := message.AcquireMessage()
			msg.SetID("m1")

			tr := traversal.Acquire(reg, eng, "wf-join-filter", nodeMap, adj, nodeIndex, nil, nil, inDegree, sinkNodeToIndex)
			msg.Retain()
			tr.CurrentMessages[nodeIndex["S"]] = msg

			tr.Traverse(t.Context(), "S")

			// Every in-edge of the join is accounted for, delivered or not.
			if got, want := atomic.LoadInt32(&tr.ResolvedCount[nodeIndex["J"]]), int32(inDegree["J"]); got != want {
				t.Errorf("join resolved %d of %d in-edges; an edge whose node emitted nothing was left open", got, want)
			}

			routed := len(tr.Routed)
			for _, r := range tr.Routed {
				r.Message.Release()
			}
			if tc.wantFired && routed != 1 {
				t.Errorf("sink behind the join received %d messages, want 1: the join never fired", routed)
			}
			if !tc.wantFired && routed != 0 {
				t.Errorf("sink behind the join received %d messages, want 0: nothing reached the join", routed)
			}

			traversal.Release(tr)
			msg.Release()

			if n := message.OverReleaseCount(); n != 0 {
				t.Errorf("%d message(s) released more often than retained", n)
			}
		})
	}
}

// The other half of the same barrier: a pruned edge can be the last one to
// resolve on a join another edge has already delivered to. pruneBranch then
// treated the join as reached only by pruned branches and pruned it, discarding
// the message waiting there.
//
//	S ─> X (branch "yes") ──yes──────────────┐
//	          └──no──> B ─────────────────────┴─> J (join) ─> K (sink)
//
// X delivers straight to J and then prunes B, so the prune arrives second on
// every run — the order is fixed by X's edge order, not by scheduling.
func TestWorkflowTraversal_JoinFiresWhenAPruneResolvesItLast(t *testing.T) {
	message.ResetOverReleaseCount()

	reg := &mockRegistry{}
	reg.RunWorkflowNodeFn = func(_ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
		msg.Retain()
		if node.ID == "X" {
			return []hermod.Message{msg}, "yes", nil
		}
		return []hermod.Message{msg}, "", nil
	}
	eng := pkgengine.NewEngine(nil, nil, nil)

	nodeMap := map[string]*storage.WorkflowNode{
		"S": {ID: "S", Type: "source"},
		"X": {ID: "X", Type: "switch"},
		"B": {ID: "B", Type: "passthrough"},
		"J": {ID: "J", Type: "join"},
		"K": {ID: "K", Type: "sink"},
	}
	adj := map[string][]string{
		"S": {"X"},
		"X": {"J", "B"},
		"B": {"J"},
		"J": {"K"},
	}
	edgeLabels := map[string]string{"X:J": "yes", "X:B": "no"}
	nodeIndex := map[string]int{"S": 0, "X": 1, "B": 2, "J": 3, "K": 4}
	sinkNodeToIndex := map[string]int{"K": 0}
	inDegree := traversal.ReachableInDegree(adj, "S")

	msg := message.AcquireMessage()
	msg.SetID("m1")

	tr := traversal.Acquire(reg, eng, "wf-join-prune-last", nodeMap, adj, nodeIndex, edgeLabels, nil, inDegree, sinkNodeToIndex)
	msg.Retain()
	tr.CurrentMessages[nodeIndex["S"]] = msg

	tr.Traverse(t.Context(), "S")

	routed := len(tr.Routed)
	for _, r := range tr.Routed {
		r.Message.Release()
	}
	if routed != 1 {
		t.Errorf("sink behind the join received %d messages, want 1: the prune discarded the join's waiting message", routed)
	}

	traversal.Release(tr)
	msg.Release()

	if n := message.OverReleaseCount(); n != 0 {
		t.Errorf("%d message(s) released more often than retained", n)
	}
}
