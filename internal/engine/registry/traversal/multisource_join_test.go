package traversal_test

import (
	"sync/atomic"
	"testing"

	"github.com/gsoultan/Hermod/internal/engine/registry/traversal"
	"github.com/gsoultan/Hermod/internal/storage"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/Hermod/pkg/engine"
)

// TestWorkflowTraversal_MultipleSourcesConvergingOnOneNode covers the topology
// the workflow editor makes it trivial to draw:
//
//	src-a ┐
//	src-b ┼─> transform ──> sink
//	src-c ┘
//
// A message enters at exactly one source, so only one of the transform node's
// in-edges is ever resolved in a given traversal — the other two belong to
// source nodes this traversal never visits. The fire condition
// (ResolvedCount >= ReceivedCount, where ReceivedCount is the raw in-degree)
// therefore cannot be met, the transform never runs and the sink is never
// reached. The engine has already acknowledged the message to the source by
// then, so the data is silently lost.
//
// Node in-degree is the right barrier only for edges that fan out *inside* one
// traversal (a switch's branches, which pruneBranch accounts for). Edges from
// sibling source nodes are alternative entry points, not co-requisites, and
// must not be counted.
func TestWorkflowTraversal_MultipleSourcesConvergingOnOneNode(t *testing.T) {
	reg := &mockRegistry{}
	eng := pkgengine.NewEngine(nil, nil, nil)

	nodeMap := map[string]*storage.WorkflowNode{
		"src-a": {ID: "src-a", Type: "source"},
		"src-b": {ID: "src-b", Type: "source"},
		"src-c": {ID: "src-c", Type: "source"},
		"tx":    {ID: "tx", Type: "passthrough"},
		"snk":   {ID: "snk", Type: "sink"},
	}
	adj := map[string][]string{
		"src-a": {"tx"},
		"src-b": {"tx"},
		"src-c": {"tx"},
		"tx":    {"snk"},
	}
	nodeIndex := map[string]int{"src-a": 0, "src-b": 1, "src-c": 2, "tx": 3, "snk": 4}
	sinkNodeToIndex := map[string]int{"snk": 0}

	// The raw in-degree from the edge list is tx=3; restricted to what a single
	// entry can reach it must be 1, or the node can never fire.
	byEntry := traversal.ReachableInDegreeByEntry(adj, []string{"src-a", "src-b", "src-c"})
	for _, entry := range []string{"src-a", "src-b", "src-c"} {
		if got := byEntry[entry]["tx"]; got != 1 {
			t.Errorf("ReachableInDegree from %s: tx=%d, want 1 (sibling source edges must not count)", entry, got)
		}
	}

	// Every source must independently be able to drive a message to the sink.
	for _, entry := range []string{"src-a", "src-b", "src-c"} {
		t.Run("entry="+entry, func(t *testing.T) {
			msg := message.AcquireMessage()
			msg.SetID("m-" + entry)

			tr := traversal.Acquire(reg, eng, "wf-multisrc", nodeMap, adj, nodeIndex, nil, nil, byEntry[entry], sinkNodeToIndex)
			msg.Retain()
			tr.CurrentMessages[nodeIndex[entry]] = msg

			tr.Traverse(t.Context(), entry)

			if atomic.LoadInt32(&tr.Fired[nodeIndex["tx"]]) == 0 {
				t.Errorf("transform node never fired for a message entering at %s; "+
					"sibling source edges are being treated as a join barrier", entry)
			}
			if len(tr.Routed) == 0 {
				t.Errorf("message entering at %s reached no sink; it would be acknowledged and dropped", entry)
			}

			traversal.Release(tr)
			msg.Release()
		})
	}
}

// TestWorkflowTraversal_SwitchJoinStillBarriers guards the fix from
// overcorrecting: a join fed by branches of the same switch must still wait for
// its in-edges to resolve or prune, exactly as before.
func TestWorkflowTraversal_SwitchJoinStillBarriers(t *testing.T) {
	reg := &mockRegistry{}
	eng := pkgengine.NewEngine(nil, nil, nil)

	nodeMap := map[string]*storage.WorkflowNode{
		"S": {ID: "S", Type: "source"},
		"A": {ID: "A", Type: "passthrough"},
		"B": {ID: "B", Type: "passthrough"},
		"J": {ID: "J", Type: "sink"},
	}
	adj := map[string][]string{
		"S": {"A", "B"},
		"A": {"J"},
		"B": {"J"},
	}
	nodeIndex := map[string]int{"S": 0, "A": 1, "B": 2, "J": 3}
	sinkNodeToIndex := map[string]int{"J": 0}

	// Both branches are reachable from S, so J's barrier must stay at 2.
	inDegree := traversal.ReachableInDegree(adj, "S")
	if got := inDegree["J"]; got != 2 {
		t.Fatalf("ReachableInDegree from S: J=%d, want 2 (in-traversal fan-out must still barrier)", got)
	}

	msg := message.AcquireMessage()
	msg.SetID("m1")

	tr := traversal.Acquire(reg, eng, "wf-fanout-join", nodeMap, adj, nodeIndex, nil, nil, inDegree, sinkNodeToIndex)
	msg.Retain()
	tr.CurrentMessages[nodeIndex["S"]] = msg

	tr.Traverse(t.Context(), "S")

	if atomic.LoadInt32(&tr.Fired[nodeIndex["J"]]) == 0 {
		t.Error("join fed by both branches of an in-traversal fan-out did not fire")
	}
	// Both branches carry a copy, so the join must fire exactly once, not twice.
	if got := atomic.LoadInt32(&tr.Fired[nodeIndex["J"]]); got != 1 {
		t.Errorf("join fired %d times, want exactly 1", got)
	}

	traversal.Release(tr)
	msg.Release()
}
