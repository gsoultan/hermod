package traversal_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/Hermod/internal/engine/registry/traversal"
	"github.com/gsoultan/Hermod/internal/storage"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/Hermod/pkg/engine"
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
