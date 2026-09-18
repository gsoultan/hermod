package traversal_test

import (
	"sort"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	_ "github.com/gsoultan/hermod/internal/engine/registry/nodes/control"
	"github.com/gsoultan/hermod/internal/engine/registry/traversal"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// A fan-out node returns several messages from one. Every one of them has to
// walk the rest of the graph: that is the whole point of the node, and the only
// thing that makes a downstream sink write one row per array item.
//
// ForeachNode.Execute is unit-tested and returns the right slice. The traversal
// is where the slice has to become N walks, and it is the part no test covered.
func TestWorkflowTraversal_FanoutRunsDownstreamOncePerMessage(t *testing.T) {
	message.ResetOverReleaseCount()

	const items = 3

	var mu sync.Mutex
	var seen []any

	reg := &mockRegistry{}
	reg.RunWorkflowNodeFn = func(_ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
		switch node.Type {
		case "foreach":
			out := make([]hermod.Message, 0, items)
			for i := range items {
				c := msg.Clone()
				c.SetData("_item", i)
				out = append(out, c)
			}
			return out, "", nil
		case "passthrough":
			mu.Lock()
			seen = append(seen, msg.Data()["_item"])
			mu.Unlock()
		}
		// Returning the input: the caller releases it, so hand it a reference.
		msg.Retain()
		return []hermod.Message{msg}, "", nil
	}

	eng := pkgengine.NewEngine(nil, nil, nil)

	nodeMap := map[string]*storage.WorkflowNode{
		"S": {ID: "S", Type: "source"},
		"F": {ID: "F", Type: "foreach", Config: map[string]any{"arrayPath": "items"}},
		"T": {ID: "T", Type: "passthrough"},
		"K": {ID: "K", Type: "sink"},
	}
	adj := map[string][]string{
		"S": {"F"},
		"F": {"T"},
		"T": {"K"},
	}
	inDegree := map[string]int{"F": 1, "T": 1, "K": 1}
	nodeIndex := map[string]int{"S": 0, "F": 1, "T": 2, "K": 3}
	sinkNodeToIndex := map[string]int{"K": 0}

	srcMsg := message.AcquireMessage()
	srcMsg.SetID("m1")
	srcMsg.SetData("items", []any{"a", "b", "c"})

	tr := traversal.Acquire(reg, eng, "wf-fanout", nodeMap, adj, nodeIndex, nil, nil, inDegree, sinkNodeToIndex)
	srcMsg.Retain()
	tr.CurrentMessages[nodeIndex["S"]] = srcMsg

	tr.Traverse(t.Context(), "S")

	mu.Lock()
	got := append([]any(nil), seen...)
	mu.Unlock()

	if len(got) != items {
		t.Fatalf("downstream node ran %d time(s) for a %d-item fan-out; items %v reached it", len(got), items, got)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].(int) < got[j].(int) })
	for i := range items {
		if got[i] != i {
			t.Fatalf("expected each item index to reach the downstream node exactly once, got %v", got)
		}
	}

	routed := len(tr.Routed)
	for _, rm := range tr.Routed {
		rm.Message.Release()
	}
	traversal.Release(tr)
	srcMsg.Release()

	if routed != items {
		t.Fatalf("sink was handed %d message(s) for a %d-item fan-out", routed, items)
	}
	if n := message.OverReleaseCount(); n != 0 {
		t.Fatalf("fan-out traversal over-released %d reference(s)", n)
	}
}

// The same walk, driven by the registered ForeachNode rather than a stand-in.
//
// ForeachNode.Execute had full unit coverage and the traversal had its own
// tests; nothing ran the two together, which is exactly where the fan-out was
// being thrown away. This is the assembly.
func TestWorkflowTraversal_RealForeachNodeFansOut(t *testing.T) {
	message.ResetOverReleaseCount()

	exec, ok := interfaces.GetNodeExecutor("foreach")
	if !ok {
		t.Fatal("foreach node executor is not registered")
	}

	var mu sync.Mutex
	var seen []string

	reg := &mockRegistry{}
	reg.RunWorkflowNodeFn = func(wfID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
		if node.Type == "foreach" {
			return exec.Execute(t.Context(), nil, wfID, node, msg)
		}
		if node.Type == "sink" {
			mu.Lock()
			item, _ := msg.Data()["_item"].(map[string]any)
			seen = append(seen, item["sku"].(string))
			mu.Unlock()
		}
		msg.Retain()
		return []hermod.Message{msg}, "", nil
	}

	eng := pkgengine.NewEngine(nil, nil, nil)

	nodeMap := map[string]*storage.WorkflowNode{
		"S": {ID: "S", Type: "source"},
		"F": {ID: "F", Type: "foreach", Config: map[string]any{"arrayPath": "lines"}},
		"K": {ID: "K", Type: "sink"},
	}
	adj := map[string][]string{"S": {"F"}, "F": {"K"}}
	inDegree := map[string]int{"F": 1, "K": 1}
	nodeIndex := map[string]int{"S": 0, "F": 1, "K": 2}

	srcMsg := message.AcquireMessage()
	srcMsg.SetID("order-1")
	srcMsg.SetData("order_id", 42)
	srcMsg.SetData("lines", []any{
		map[string]any{"sku": "A-1"},
		map[string]any{"sku": "B-7"},
		map[string]any{"sku": "C-9"},
	})

	tr := traversal.Acquire(reg, eng, "wf-foreach", nodeMap, adj, nodeIndex, nil, nil, inDegree, map[string]int{"K": 0})
	srcMsg.Retain()
	tr.CurrentMessages[nodeIndex["S"]] = srcMsg

	tr.Traverse(t.Context(), "S")

	mu.Lock()
	got := append([]string(nil), seen...)
	mu.Unlock()
	sort.Strings(got)

	for _, rm := range tr.Routed {
		rm.Message.Release()
	}
	traversal.Release(tr)
	srcMsg.Release()

	want := []string{"A-1", "B-7", "C-9"}
	if len(got) != len(want) {
		t.Fatalf("sink saw %v, want one message per line item %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sink saw %v, want %v", got, want)
		}
	}
	if n := message.OverReleaseCount(); n != 0 {
		t.Fatalf("fan-out traversal over-released %d reference(s)", n)
	}
}
