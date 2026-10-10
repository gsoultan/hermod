package traversal_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/traversal"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

type nodeCtxKey struct{}

// TestTraversalRunsNodesOnItsContext: the traversal is handed the workflow's
// lifetime context, and every node must run on it. Running nodes on a fresh
// background context meant a stopped workflow could not cancel a lookup or API
// call in flight.
func TestTraversalRunsNodesOnItsContext(t *testing.T) {
	var mu sync.Mutex
	var seen []any
	reg := &mockRegistry{OnNodeContext: func(ctx context.Context) {
		mu.Lock()
		seen = append(seen, ctx.Value(nodeCtxKey{}))
		mu.Unlock()
	}}
	eng := pkgengine.NewEngine(nil, nil, nil)

	nodeMap := map[string]*storage.WorkflowNode{
		"S": {ID: "S", Type: "source"},
		"T": {ID: "T", Type: "transformation"},
		"K": {ID: "K", Type: "sink"},
	}
	adj := map[string][]string{"S": {"T"}, "T": {"K"}}
	inDegree := map[string]int{"T": 1, "K": 1}
	nodeIndex := map[string]int{"S": 0, "T": 1, "K": 2}

	msg := message.AcquireMessage()
	msg.SetID("m1")
	tr := traversal.Acquire(reg, eng, "wf-ctx", nodeMap, adj, nodeIndex, nil, nil, inDegree, map[string]int{"K": 0})
	msg.Retain()
	tr.CurrentMessages[nodeIndex["S"]] = msg

	ctx := context.WithValue(t.Context(), nodeCtxKey{}, "workflow-lifetime")
	tr.Traverse(ctx, "S")
	for _, rm := range tr.Routed {
		rm.Message.Release()
	}
	msg.Release()

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("nodes run = %d, want 2 (T and K)", len(seen))
	}
	for i, v := range seen {
		if v != "workflow-lifetime" {
			t.Errorf("node %d ran on a context without the traversal's value (%v); "+
				"nodes are not bound to the workflow's context", i, v)
		}
	}
}

// TestANodeCancelledByShutdownIsNotDeadLettered: once nodes run on the
// workflow's context, stopping the workflow makes an in-flight node fail with
// the context's error. That is not the message's fault. Parking it in the
// dead-letter sink would turn every stop into dead-lettered data; it has to be
// left unacknowledged so the source redelivers it on the next run.
func TestANodeCancelledByShutdownIsNotDeadLettered(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	reg := &mockRegistry{
		RunWorkflowNodeFn: func(_ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
			if node.ID == "T" {
				cancel() // the workflow is stopped while T is waiting on the network
				return nil, "", fmt.Errorf("api_lookup: %w", ctx.Err())
			}
			msg.Retain()
			return []hermod.Message{msg}, "", nil
		},
	}
	dlq := &capturingSink{}
	eng := pkgengine.NewEngine(nil, nil, nil)
	eng.SetDeadLetterSink(dlq)

	nodeMap := map[string]*storage.WorkflowNode{
		"S": {ID: "S", Type: "source"},
		"T": {ID: "T", Type: "transformation"},
	}
	nodeIndex := map[string]int{"S": 0, "T": 1}

	msg := message.AcquireMessage()
	msg.SetID("m-stop")
	tr := traversal.Acquire(reg, eng, "wf-stop", nodeMap, map[string][]string{"S": {"T"}}, nodeIndex, nil, nil, map[string]int{"T": 1}, nil)
	msg.Retain()
	tr.CurrentMessages[nodeIndex["S"]] = msg
	tr.Traverse(ctx, "S")
	msg.Release()

	if got := dlq.written(); len(got) != 0 {
		t.Errorf("a node cancelled by the workflow stopping dead-lettered %v; it must be left for redelivery", got)
	}
	if tr.DeadLettered.Load() {
		t.Error("traversal reports the message dead-lettered after a shutdown cancellation")
	}
	if !tr.Unaccounted.Load() {
		t.Error("traversal does not mark the cancelled message unaccounted, so the engine could acknowledge it")
	}
}
