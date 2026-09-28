package traversal_test

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/traversal"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// A node on a taken branch can fail. Its message is dead-lettered where it
// failed, and nothing is ever going to arrive along its out-edges — but
// handleResults returned on the error before walking them, so they were neither
// delivered nor pruned. A join downstream kept waiting for an edge that would
// never resolve, and the message the other branch carried to it was never
// written.
//
//	S ─> F (fails) ─┐
//	S ─> B ─────────┴─> J (join) ─> K (sink)
//
// An errored branch now counts as finished for the join, the same as a branch
// whose node emitted nothing (join_after_filter_test.go): the join fires with
// what arrived, and the failed message still goes to the dead-letter sink.
func TestWorkflowTraversal_JoinFiresWhenABranchFails(t *testing.T) {
	cases := []struct {
		name      string
		fails     map[string]bool
		wantFired bool
		wantDLQ   int
	}{
		{name: "one branch fails", fails: map[string]bool{"F": true}, wantFired: true, wantDLQ: 1},
		{name: "every branch fails", fails: map[string]bool{"F": true, "B": true}, wantFired: false, wantDLQ: 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			message.ResetOverReleaseCount()

			reg := &mockRegistry{}
			reg.RunWorkflowNodeFn = func(_ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
				if tc.fails[node.ID] {
					return nil, "", errors.New("node " + node.ID + " could not parse the payload")
				}
				msg.Retain()
				return []hermod.Message{msg}, "", nil
			}
			dlq := &capturingSink{}
			eng := pkgengine.NewEngine(nil, nil, nil)
			eng.SetDeadLetterSink(dlq)

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

			tr := traversal.Acquire(reg, eng, "wf-join-error", nodeMap, adj, nodeIndex, nil, nil, inDegree, sinkNodeToIndex)
			msg.Retain()
			tr.CurrentMessages[nodeIndex["S"]] = msg

			tr.Traverse(t.Context(), "S")

			// Every in-edge of the join is accounted for, delivered or not.
			if got, want := atomic.LoadInt32(&tr.ResolvedCount[nodeIndex["J"]]), int32(inDegree["J"]); got != want {
				t.Errorf("join resolved %d of %d in-edges; an edge whose node failed was left open", got, want)
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

			// The failure is still preserved where it happened.
			if got := len(dlq.written()); got != tc.wantDLQ {
				t.Errorf("dead-letter sink received %d message(s), want %d", got, tc.wantDLQ)
			}
			if !tr.DeadLettered.Load() {
				t.Error("the traversal does not record that it dead-lettered a message")
			}

			traversal.Release(tr)
			msg.Release()

			if n := message.OverReleaseCount(); n != 0 {
				t.Errorf("%d message(s) released more often than retained", n)
			}
		})
	}
}
