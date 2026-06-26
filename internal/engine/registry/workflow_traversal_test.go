package registry

import (
	"context"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/comm/message"
	pkgengine "github.com/user/hermod/pkg/engine"
)

// stubBranchExecutor is a deterministic branching node executor used to drive
// the traversal down a single, known branch in tests.
type stubBranchExecutor struct {
	branch string
}

func (e *stubBranchExecutor) Execute(ctx context.Context, nctx NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	return []hermod.Message{msg}, e.branch, nil
}

// TestWorkflowTraversal_ConditionalJoinReached reproduces the join-after-branch
// stall: a switch node selects a single branch, but the join node downstream has
// an in-degree of 2. The traversal must still reach (and route) the join node by
// pruning the untaken branch instead of waiting forever for a message that will
// never arrive.
//
// Graph:
//
//	S(source) -> SW(switch)
//	SW -"yes"-> A -> J(sink)
//	SW -"no"--> B -> J(sink)
//
// Only the "yes" branch is taken; J has in-degree 2.
func TestWorkflowTraversal_ConditionalJoinReached(t *testing.T) {
	// "switch" is not exercised by other tests in this package, so registering a
	// stub executor here does not affect them.
	RegisterNodeExecutor("switch", &stubBranchExecutor{branch: "yes"})

	r := NewRegistry(nil)
	eng := pkgengine.NewEngine(nil, nil, nil)

	nodeMap := map[string]*storage.WorkflowNode{
		"S":  {ID: "S", Type: "source"},
		"SW": {ID: "SW", Type: "switch"},
		"A":  {ID: "A", Type: "passthrough"},
		"B":  {ID: "B", Type: "passthrough"},
		"J":  {ID: "J", Type: "sink"},
	}
	adj := map[string][]string{
		"S":  {"SW"},
		"SW": {"A", "B"},
		"A":  {"J"},
		"B":  {"J"},
	}
	edgeLabels := map[string]string{
		"SW:A": "yes",
		"SW:B": "no",
	}
	inDegree := map[string]int{
		"SW": 1,
		"A":  1,
		"B":  1,
		"J":  2,
	}
	sinkNodeToIndex := map[string]int{"J": 0}

	srcMsg := message.AcquireMessage()
	srcMsg.SetID("m1")

	tr := &workflowTraversal{
		registry:        r,
		eng:             eng,
		workflowID:      "wf-join",
		nodeMap:         nodeMap,
		adj:             adj,
		edgeLabels:      edgeLabels,
		edgeBreakpoints: map[string]bool{},
		inDegree:        inDegree,
		sinkNodeToIndex: sinkNodeToIndex,
		currentMessages: map[string]hermod.Message{"S": srcMsg},
		receivedCount:   map[string]int{},
		resolvedCount:   map[string]int{},
		fired:           map[string]bool{},
	}

	tr.wg.Add(1)
	tr.processNode(t.Context(), "S")
	tr.wg.Wait()

	if len(tr.routed) != 1 {
		t.Fatalf("join sink J was not reached: expected exactly 1 routed message, got %d", len(tr.routed))
	}
	if tr.routed[0].SinkIndex != 0 {
		t.Fatalf("expected routed sink index 0 (node J), got %d", tr.routed[0].SinkIndex)
	}
}

// panicExecutor is a node executor that always panics, used to simulate a
// malformed node/message (e.g. a bad type assertion deep in a transformer)
// crashing during live workflow execution.
type panicExecutor struct{}

func (e *panicExecutor) Execute(ctx context.Context, nctx NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	panic("boom: simulated node failure")
}

// TestWorkflowTraversal_NodePanicContained is the regression test for the
// production 502: a node executed inside a `go t.processNode(...)` goroutine
// panicked, and because the goroutine had no recover() the panic propagated to
// the goroutine's top and crashed the whole process (which also hosts the API
// server), surfacing to in-flight requests as a 502. With the fix the panic is
// contained: traversal completes, no message is routed past the failed node,
// and the process keeps running.
//
// Graph: S(source) -> P(panic) -> J(sink)
func TestWorkflowTraversal_NodePanicContained(t *testing.T) {
	RegisterNodeExecutor("panic", &panicExecutor{})

	r := NewRegistry(nil)
	eng := pkgengine.NewEngine(nil, nil, nil)

	nodeMap := map[string]*storage.WorkflowNode{
		"S": {ID: "S", Type: "source"},
		"P": {ID: "P", Type: "panic"},
		"J": {ID: "J", Type: "sink"},
	}
	adj := map[string][]string{
		"S": {"P"},
		"P": {"J"},
	}
	inDegree := map[string]int{
		"P": 1,
		"J": 1,
	}
	sinkNodeToIndex := map[string]int{"J": 0}

	srcMsg := message.AcquireMessage()
	srcMsg.SetID("m1")

	tr := &workflowTraversal{
		registry:        r,
		eng:             eng,
		workflowID:      "wf-panic",
		nodeMap:         nodeMap,
		adj:             adj,
		edgeLabels:      map[string]string{},
		edgeBreakpoints: map[string]bool{},
		inDegree:        inDegree,
		sinkNodeToIndex: sinkNodeToIndex,
		currentMessages: map[string]hermod.Message{"S": srcMsg},
		receivedCount:   map[string]int{},
		resolvedCount:   map[string]int{},
		fired:           map[string]bool{},
	}

	// Without the recover() in processNode, the panic in node "P" runs on its
	// own goroutine and aborts the entire test binary here. With the fix this
	// returns normally.
	tr.wg.Add(1)
	tr.processNode(t.Context(), "S")
	tr.wg.Wait()

	if len(tr.routed) != 0 {
		t.Fatalf("expected no messages routed past the panicking node, got %d", len(tr.routed))
	}
}

// TestWorkflowTraversal_SkippedNodePropagates verifies that when a branch is not
// taken, the prune is propagated through intermediate nodes so a deeper join can
// still resolve, and that a node reachable only through the untaken branch is not
// executed.
//
// Graph:
//
//	S(source) -> SW(switch)
//	SW -"yes"-> A -> J(sink)
//	SW -"no"--> B -> C -> J(sink)
//
// Only "yes" is taken; B and C must be skipped, J (in-degree 2) must still fire.
func TestWorkflowTraversal_SkippedNodePropagates(t *testing.T) {
	RegisterNodeExecutor("switch", &stubBranchExecutor{branch: "yes"})

	r := NewRegistry(nil)
	eng := pkgengine.NewEngine(nil, nil, nil)

	nodeMap := map[string]*storage.WorkflowNode{
		"S":  {ID: "S", Type: "source"},
		"SW": {ID: "SW", Type: "switch"},
		"A":  {ID: "A", Type: "passthrough"},
		"B":  {ID: "B", Type: "passthrough"},
		"C":  {ID: "C", Type: "passthrough"},
		"J":  {ID: "J", Type: "sink"},
	}
	adj := map[string][]string{
		"S":  {"SW"},
		"SW": {"A", "B"},
		"A":  {"J"},
		"B":  {"C"},
		"C":  {"J"},
	}
	edgeLabels := map[string]string{
		"SW:A": "yes",
		"SW:B": "no",
	}
	inDegree := map[string]int{
		"SW": 1,
		"A":  1,
		"B":  1,
		"C":  1,
		"J":  2,
	}
	sinkNodeToIndex := map[string]int{"J": 0}

	srcMsg := message.AcquireMessage()
	srcMsg.SetID("m1")

	tr := &workflowTraversal{
		registry:        r,
		eng:             eng,
		workflowID:      "wf-skip",
		nodeMap:         nodeMap,
		adj:             adj,
		edgeLabels:      edgeLabels,
		edgeBreakpoints: map[string]bool{},
		inDegree:        inDegree,
		sinkNodeToIndex: sinkNodeToIndex,
		currentMessages: map[string]hermod.Message{"S": srcMsg},
		receivedCount:   map[string]int{},
		resolvedCount:   map[string]int{},
		fired:           map[string]bool{},
	}

	tr.wg.Add(1)
	tr.processNode(t.Context(), "S")
	tr.wg.Wait()

	if len(tr.routed) != 1 {
		t.Fatalf("join sink J was not reached through skip-propagation: expected 1 routed message, got %d", len(tr.routed))
	}

	// Node C is only reachable via the untaken "no" branch and must be skipped
	// (never delivered a message).
	if _, ok := tr.currentMessages["C"]; ok {
		t.Fatalf("node C should have been skipped but received a message")
	}
}
