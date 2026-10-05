package traversal_test

import (
	"errors"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/traversal"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// Telling a record that was dropped on purpose from one nothing could deliver.
//
// Both end a walk with nothing routed. The engine cannot tell them apart, so it
// treats both as undeliverable: parked in the dead-letter sink, or left
// unacknowledged. For a record a sink could not take that is what keeps it from
// being lost. For a record a filter was asked to drop it is a false failure, on
// every record the filter drops.
//
// The traversal is what knows why a walk ended. Filtered says it ended on
// purpose; Unaccounted says some walk ended for a reason that is not one. A
// message is only "filtered" when the first is set and the second is not —
// these pin both, and above all that anything unexplained wins.

type walk struct {
	nodes  map[string]*storage.WorkflowNode
	adj    map[string][]string
	labels map[string]string
	sinks  map[string]int
	run    func(node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error)
}

// pass forwards the message unchanged.
func pass(msg hermod.Message) ([]hermod.Message, string, error) {
	msg.Retain()
	return []hermod.Message{msg}, "", nil
}

func (w walk) do(t *testing.T) (filtered, unaccounted bool, routed int) {
	t.Helper()
	reg := &mockRegistry{}
	reg.RunWorkflowNodeFn = func(_ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
		return w.run(node, msg)
	}
	index := make(map[string]int, len(w.nodes))
	for id := range w.nodes {
		index[id] = len(index)
	}
	msg := message.AcquireMessage()
	msg.SetID("m1")

	tr := traversal.Acquire(reg, pkgengine.NewEngine(nil, nil, nil), "wf", w.nodes, w.adj, index, w.labels, nil,
		traversal.ReachableInDegree(w.adj, "S"), w.sinks)
	msg.Retain()
	tr.CurrentMessages[index["S"]] = msg
	tr.Traverse(t.Context(), "S")

	filtered, unaccounted, routed = tr.Filtered.Load(), tr.Unaccounted.Load(), len(tr.Routed)
	for _, r := range tr.Routed {
		r.Message.Release()
	}
	traversal.Release(tr)
	msg.Release()
	return filtered, unaccounted, routed
}

// source → N → sink, with N of the given type doing what run says.
func through(nodeType string, run func(msg hermod.Message) ([]hermod.Message, string, error)) walk {
	return walk{
		nodes: map[string]*storage.WorkflowNode{
			"S": {ID: "S", Type: "source"},
			"N": {ID: "N", Type: nodeType},
			"K": {ID: "K", Type: "sink"},
		},
		adj:   map[string][]string{"S": {"N"}, "N": {"K"}},
		sinks: map[string]int{"K": 0},
		run: func(node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
			if node.ID == "N" {
				return run(msg)
			}
			return pass(msg)
		},
	}
}

func nothing(hermod.Message) ([]hermod.Message, string, error) { return nil, "", nil }

func TestANodeThatDropsOnPurposeMarksTheMessageFiltered(t *testing.T) {
	for _, nodeType := range []string{"transformation", "validator", "deduplicate"} {
		t.Run(nodeType, func(t *testing.T) {
			filtered, unaccounted, routed := through(nodeType, nothing).do(t)
			if !filtered || unaccounted || routed != 0 {
				t.Errorf("filtered=%v unaccounted=%v routed=%d; want a deliberate drop and nothing else", filtered, unaccounted, routed)
			}
		})
	}
}

// A node that emits nothing because it is holding the message — an approval
// waiting for a person, a wait that suspended it, a collect that absorbed it —
// has not dropped it. What the engine does with those is unchanged.
func TestANodeThatHoldsAMessageHasNotFilteredIt(t *testing.T) {
	for _, nodeType := range []string{"approval", "wait", "collect", "foreach"} {
		t.Run(nodeType, func(t *testing.T) {
			_, unaccounted, _ := through(nodeType, nothing).do(t)
			if !unaccounted {
				t.Errorf("a %s node that emitted nothing was not recorded as unaccounted for", nodeType)
			}
		})
	}
}

func TestANodeThatFailsHasNotFilteredTheMessage(t *testing.T) {
	failing := func(hermod.Message) ([]hermod.Message, string, error) {
		return nil, "", errors.New("could not parse the payload")
	}
	_, unaccounted, _ := through("transformation", failing).do(t)
	if !unaccounted {
		t.Error("a failed node was not recorded as unaccounted for; its message would be acknowledged as filtered")
	}
}

// A condition whose outcome has no edge wired for it is a filter by another
// name: the workflow's author gave that outcome nowhere to go.
func TestAnOutcomeWithNoEdgeMarksTheMessageFiltered(t *testing.T) {
	w := through("condition", func(msg hermod.Message) ([]hermod.Message, string, error) {
		msg.Retain()
		return []hermod.Message{msg}, "false", nil
	})
	w.labels = map[string]string{"N:K": "true"}

	filtered, unaccounted, routed := w.do(t)
	if !filtered || unaccounted || routed != 0 {
		t.Errorf("filtered=%v unaccounted=%v routed=%d; want a deliberate drop and nothing else", filtered, unaccounted, routed)
	}
}

// The case all of this must never get wrong. The walk reaches a sink node the
// engine has no sink for. Nothing is routed, exactly as with a filter — and
// this message must be kept, not acknowledged.
func TestASinkThatCannotBeResolvedIsNotAFilter(t *testing.T) {
	w := through("passthrough", pass)
	w.sinks = map[string]int{} // the sink node resolves to nothing

	filtered, unaccounted, routed := w.do(t)
	if routed != 0 {
		t.Fatalf("routed=%d for a sink that does not resolve", routed)
	}
	if !unaccounted {
		t.Errorf("filtered=%v unaccounted=%v: a message whose sink could not be resolved "+
			"would be acknowledged as though a filter had dropped it", filtered, unaccounted)
	}
}

// One branch filters and the other cannot deliver. The deliberate drop on one
// side does not make the message safe to acknowledge.
func TestAFilterOnOneBranchDoesNotExcuseTheOther(t *testing.T) {
	w := walk{
		nodes: map[string]*storage.WorkflowNode{
			"S": {ID: "S", Type: "source"},
			"F": {ID: "F", Type: "transformation"},
			"B": {ID: "B", Type: "passthrough"},
			"K": {ID: "K", Type: "sink"},
		},
		adj:   map[string][]string{"S": {"F", "B"}, "B": {"K"}},
		sinks: map[string]int{},
		run: func(node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
			if node.ID == "F" {
				return nil, "", nil
			}
			return pass(msg)
		},
	}
	filtered, unaccounted, _ := w.do(t)
	if !filtered || !unaccounted {
		t.Errorf("filtered=%v unaccounted=%v; want both, so that the unresolved branch wins", filtered, unaccounted)
	}
}

func TestADeliveredMessageIsNeitherFilteredNorUnaccounted(t *testing.T) {
	filtered, unaccounted, routed := through("passthrough", pass).do(t)
	if filtered || unaccounted || routed != 1 {
		t.Errorf("filtered=%v unaccounted=%v routed=%d; want a plain delivery", filtered, unaccounted, routed)
	}
}
