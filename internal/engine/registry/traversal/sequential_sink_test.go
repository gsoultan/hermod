package traversal_test

import (
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/traversal"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// ---------------------------------------------------------------------------
// A sequential sink must be written once, not twice.
//
// The flag selects between two models. Off, the node is a pass-through and the
// engine's sink writers do the write. On, the node writes inline and reports
// success or failure as a branch the workflow can act on.
//
// The traversal routes a sink node's *output* to the writer regardless of which
// model ran — so in sequential mode the message is written inline and then
// handed to the writer as well. Two writes per message, deterministically, for
// the whole life of the workflow.
//
// This asserts on what reaches the writer, since that is the second write.
// ---------------------------------------------------------------------------

func TestASequentialSinkIsNotAlsoRoutedToTheWriter(t *testing.T) {
	reg := &mockRegistry{
		RunWorkflowNodeFn: func(_ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
			// The sequential executor writes inline and returns the message so
			// the success branch has something to route.
			if node.Type == "sink" {
				return []hermod.Message{msg}, "success", nil
			}
			return []hermod.Message{msg}, "", nil
		},
	}
	eng := pkgengine.NewEngine(nil, nil, nil)

	nodeMap := map[string]*storage.WorkflowNode{
		"S": {ID: "S", Type: "source"},
		"K": {ID: "K", Type: "sink", Config: map[string]any{"sequential": true}},
	}
	adj := map[string][]string{"S": {"K"}}
	inDegree := map[string]int{"K": 1}
	sinkNodeToIndex := map[string]int{"K": 0}
	nodeIndex := map[string]int{"S": 0, "K": 1}

	srcMsg := message.AcquireMessage()
	srcMsg.SetID("m1")

	tr := traversal.Acquire(reg, eng, "wf-seq", nodeMap, adj, nodeIndex, nil, nil, inDegree, sinkNodeToIndex)
	srcMsg.Retain()
	tr.CurrentMessages[nodeIndex["S"]] = srcMsg

	tr.Traverse(t.Context(), "S")

	if got := len(tr.Routed); got != 0 {
		t.Errorf("a sequential sink handed %d message(s) to the async writer as well as "+
			"writing them inline; every message is delivered twice", got)
	}
}

// TestAPassThroughSinkIsStillRouted is the other half. The routing is how a
// non-sequential sink gets written at all — removing it for everything would
// stop every ordinary workflow from delivering anything.
func TestAPassThroughSinkIsStillRouted(t *testing.T) {
	reg := &mockRegistry{}
	eng := pkgengine.NewEngine(nil, nil, nil)

	nodeMap := map[string]*storage.WorkflowNode{
		"S": {ID: "S", Type: "source"},
		"K": {ID: "K", Type: "sink"}, // no sequential flag
	}
	adj := map[string][]string{"S": {"K"}}
	inDegree := map[string]int{"K": 1}
	sinkNodeToIndex := map[string]int{"K": 0}
	nodeIndex := map[string]int{"S": 0, "K": 1}

	srcMsg := message.AcquireMessage()
	srcMsg.SetID("m1")

	tr := traversal.Acquire(reg, eng, "wf-pass", nodeMap, adj, nodeIndex, nil, nil, inDegree, sinkNodeToIndex)
	srcMsg.Retain()
	tr.CurrentMessages[nodeIndex["S"]] = srcMsg

	tr.Traverse(t.Context(), "S")

	if got := len(tr.Routed); got != 1 {
		t.Errorf("a pass-through sink routed %d message(s) to the writer, want 1; "+
			"that routing is the only thing that writes it", got)
	}
}
