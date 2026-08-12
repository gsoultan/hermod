package traversal_test

import (
	"errors"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry/traversal"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/comm/message"
	pkgengine "github.com/user/hermod/pkg/engine"
)

// ---------------------------------------------------------------------------
// Making the circuit breaker count.
//
// The node reads a failure count and opens once it passes a threshold, and
// nothing incremented that count — so a control offered as "Stop flow on
// failure threshold" always returned success, however broken the downstream.
//
// A breaker protects what it feeds, so a failure counts against the breakers
// that are its immediate upstream. The lookup only happens when something
// fails, which is why the count ages out rather than resetting on success:
// resetting would put a reverse-edge lookup on every successful message to
// serve a case that is rare by definition.
// ---------------------------------------------------------------------------

func runBreakerWorkflow(t *testing.T, failDownstream bool) *mockRegistry {
	t.Helper()

	reg := &mockRegistry{
		RunWorkflowNodeFn: func(_ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
			if node.ID == "K" && failDownstream {
				return nil, "", errors.New("the sink is refusing writes")
			}
			return []hermod.Message{msg}, "", nil
		},
	}
	eng := pkgengine.NewEngine(nil, nil, nil)

	nodeMap := map[string]*storage.WorkflowNode{
		"S":  {ID: "S", Type: "source"},
		"CB": {ID: "CB", Type: "circuit_breaker", Config: map[string]any{"failure_threshold": float64(2)}},
		"K":  {ID: "K", Type: "sink", Config: map[string]any{"sequential": true}},
	}
	adj := map[string][]string{"S": {"CB"}, "CB": {"K"}}
	inDegree := map[string]int{"CB": 1, "K": 1}
	nodeIndex := map[string]int{"S": 0, "CB": 1, "K": 2}

	for range 3 {
		srcMsg := message.AcquireMessage()
		srcMsg.SetID("m")
		tr := traversal.Acquire(reg, eng, "wf-cb", nodeMap, adj, nodeIndex, nil, nil, inDegree, nil)
		srcMsg.Retain()
		tr.CurrentMessages[nodeIndex["S"]] = srcMsg
		tr.Traverse(t.Context(), "S")
		traversal.Release(tr)
	}
	return reg
}

// TestAFailureDownstreamCountsAgainstTheBreaker is the wiring in one
// assertion: without it the count stays at zero and the breaker never opens.
func TestAFailureDownstreamCountsAgainstTheBreaker(t *testing.T) {
	reg := runBreakerWorkflow(t, true)

	charged := reg.chargedBreakers()
	if len(charged) != 3 {
		t.Fatalf("three failing messages charged the breaker %d time(s); it reads a count "+
			"that nothing increments, so it can never open however broken the downstream is",
			len(charged))
	}
	for _, id := range charged {
		if id != "CB" {
			t.Errorf("charged %q, which does not feed the failing node", id)
		}
	}
}

// TestASucceedingWorkflowLeavesTheBreakerAlone. A breaker that counts healthy
// traffic would open on a working system.
func TestASucceedingWorkflowLeavesTheBreakerAlone(t *testing.T) {
	reg := runBreakerWorkflow(t, false)

	if charged := reg.chargedBreakers(); len(charged) != 0 {
		t.Errorf("a healthy workflow charged the breaker %d time(s); it would open on a "+
			"working system", len(charged))
	}
}
