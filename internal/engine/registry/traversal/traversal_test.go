package traversal_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/engine/registry/traversal"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// mockRegistry implements traversal.Registry
type mockRegistry struct {
	LogSvc            hermod.Logger
	RunWorkflowNodeFn func(workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error)
	Logs              []string

	breakerMu       sync.Mutex
	BreakerFailures []string
}

// RecordCircuitBreakerFailure records which breakers were charged for a failure.
func (m *mockRegistry) RecordCircuitBreakerFailure(_, breakerNodeID string) {
	m.breakerMu.Lock()
	defer m.breakerMu.Unlock()
	m.BreakerFailures = append(m.BreakerFailures, breakerNodeID)
}

// chargedBreakers returns the breakers charged so far.
func (m *mockRegistry) chargedBreakers() []string {
	m.breakerMu.Lock()
	defer m.breakerMu.Unlock()
	return append([]string(nil), m.BreakerFailures...)
}

func (m *mockRegistry) RunWorkflowNode(workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	if m.RunWorkflowNodeFn != nil {
		return m.RunWorkflowNodeFn(workflowID, node, msg)
	}
	// Returning the input: the caller releases it, so hand it a reference, the
	// way Registry.runWorkflowNode does. Without it every node released the
	// message once more than it held, the message went back to the pool while
	// still in use, and whichever test acquired it next failed instead — the
	// fan-out tests, one run in three.
	msg.Retain()
	return []hermod.Message{msg}, "", nil
}
func (m *mockRegistry) IsDebuggerAttached(workflowID string) bool                             { return false }
func (m *mockRegistry) PauseForDebugger(workflowID string, nodeID string, msg hermod.Message) {}
func (m *mockRegistry) BroadcastLog(workflowID, level, msg, details string) {
	m.Logs = append(m.Logs, msg)
}
func (m *mockRegistry) Logger() hermod.Logger { return m.LogSvc }

type stubBranchExecutor struct {
	branch string
}

func (e *stubBranchExecutor) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	return []hermod.Message{msg}, e.branch, nil
}

func TestWorkflowTraversal_ConditionalJoinReached(t *testing.T) {
	interfaces.RegisterNodeExecutor("switch", &stubBranchExecutor{branch: "yes"})

	reg := &mockRegistry{
		RunWorkflowNodeFn: func(workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
			// Both paths below return the input: the caller releases it, so hand
			// it a reference.
			msg.Retain()
			if node.Type == "switch" {
				if e, ok := interfaces.GetNodeExecutor("switch"); ok {
					return e.Execute(context.Background(), nil, workflowID, node, msg)
				}
			}
			return []hermod.Message{msg}, "", nil
		},
	}
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
	nodeIndex := map[string]int{"S": 0, "SW": 1, "A": 2, "B": 3, "J": 4}

	srcMsg := message.AcquireMessage()
	srcMsg.SetID("m1")

	tr := traversal.Acquire(reg, eng, "wf-join", nodeMap, adj, nodeIndex, edgeLabels, nil, inDegree, sinkNodeToIndex)
	// CurrentMessages owns a reference: resolveEdge retains before storing, and
	// processNode releases after consuming. Seeding the slot directly has to
	// honour the same invariant, or the source message is freed back to the
	// pool mid-traversal while downstream nodes still hold it.
	srcMsg.Retain()
	tr.CurrentMessages[nodeIndex["S"]] = srcMsg

	tr.Traverse(t.Context(), "S")

	firedJ := atomic.LoadInt32(&tr.Fired[nodeIndex["J"]])
	if firedJ == 0 {
		t.Errorf("Join node J should have fired even if one branch was skipped")
	}
}

// A condition's two branches often meet again: one branch reshapes the row and
// the other goes straight to the same sink.
//
//	S ─> C ─true──> A ─> J
//	     └──false────────┘
//
// J waits for both of its in-edges, and the edge the message did not take is
// resolved by pruning it. When that prune was the last of J's edges to arrive,
// J was marked done and pruned in turn, with the message from the taken branch
// still waiting in its slot. The message was never written; the engine saw a
// message that resolved no sink and could only report it as delivered nowhere.
// Which edge arrives last follows the order the edges were drawn in, so one
// workflow could lose every message on its false branch and none on its true
// one.
//
// TestWorkflowTraversal_ConditionalJoinReached could not see this: a pruned
// node is marked fired too, so it asserts something a dropped message also
// satisfies. This asserts the node ran.
func TestWorkflowTraversal_BranchesThatMeetAgainDeliverTheMessage(t *testing.T) {
	cases := []struct {
		name   string
		branch string
		// out is C's out-edges in the order the workflow lists them.
		out []string
	}{
		{"false branch, direct edge drawn first", "false", []string{"J", "A"}},
		{"false branch, direct edge drawn last", "false", []string{"A", "J"}},
		{"true branch, direct edge drawn first", "true", []string{"J", "A"}},
		{"true branch, direct edge drawn last", "true", []string{"A", "J"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			message.ResetOverReleaseCount()

			var mu sync.Mutex
			ran := map[string]int{}
			reg := &mockRegistry{}
			reg.RunWorkflowNodeFn = func(_ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
				mu.Lock()
				ran[node.ID]++
				mu.Unlock()
				// Returning the input: the caller releases it, so hand it a reference.
				msg.Retain()
				if node.ID == "C" {
					return []hermod.Message{msg}, tc.branch, nil
				}
				return []hermod.Message{msg}, "", nil
			}
			eng := pkgengine.NewEngine(nil, nil, nil)

			nodeMap := map[string]*storage.WorkflowNode{
				"S": {ID: "S", Type: "source"},
				"C": {ID: "C", Type: "condition"},
				"A": {ID: "A", Type: "passthrough"},
				"J": {ID: "J", Type: "sink"},
			}
			adj := map[string][]string{"S": {"C"}, "C": tc.out, "A": {"J"}}
			edgeLabels := map[string]string{"C:A": "true", "C:J": "false"}
			nodeIndex := map[string]int{"S": 0, "C": 1, "A": 2, "J": 3}

			msg := message.AcquireMessage()
			msg.SetID("m1")
			tr := traversal.Acquire(reg, eng, "wf-rejoin", nodeMap, adj, nodeIndex, edgeLabels, nil,
				traversal.ReachableInDegree(adj, "S"), map[string]int{"J": 0})
			msg.Retain()
			tr.CurrentMessages[nodeIndex["S"]] = msg

			tr.Traverse(t.Context(), "S")

			mu.Lock()
			ranJ := ran["J"]
			mu.Unlock()
			routed := len(tr.Routed)
			for _, rm := range tr.Routed {
				rm.Message.Release()
			}
			traversal.Release(tr)
			msg.Release()

			if ranJ != 1 {
				t.Errorf("the node both branches lead to ran %d time(s), want 1", ranJ)
			}
			if routed != 1 {
				t.Errorf("the sink both branches lead to was handed %d message(s), want 1", routed)
			}
			if n := message.OverReleaseCount(); n != 0 {
				t.Errorf("the traversal over-released %d reference(s)", n)
			}
		})
	}
}

// TakesEdge is the one rule for which edges a node's output travels along. The
// live traversal walks a message with it and the editor's simulation walks a
// sample with it, so a preview cannot route differently from the workflow it
// previews.
func TestTakesEdge(t *testing.T) {
	cases := []struct {
		branch, label string
		want          bool
	}{
		// A node that names no branch sends its output along every edge.
		{"", "", true},
		{"", "eu", true},
		// A node that names one sends it along the edges labelled for it...
		{"us", "us", true},
		{"default", "default", true},
		// ...and along unlabelled ones,
		{"us", "", true},
		// but not along an edge labelled for another branch.
		{"us", "eu", false},
		{"default", "eu", false},
	}
	for _, tc := range cases {
		if got := traversal.TakesEdge(tc.branch, tc.label); got != tc.want {
			t.Errorf("TakesEdge(branch %q, label %q) = %v, want %v", tc.branch, tc.label, got, tc.want)
		}
	}
}
