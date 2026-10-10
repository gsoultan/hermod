package traversal_test

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/traversal"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// telemetryGraph is S -> T -> U, plus F -> U2 reachable from T as a fan-out
// target in the fan-out case.
type telemetryGraph struct {
	nodeMap   map[string]*storage.WorkflowNode
	adj       map[string][]string
	inDegree  map[string]int
	nodeIndex map[string]int
}

func newTelemetryGraph() telemetryGraph {
	return telemetryGraph{
		nodeMap: map[string]*storage.WorkflowNode{
			"S": {ID: "S", Type: "source"},
			"T": {ID: "T", Type: "transformation"},
			"U": {ID: "U", Type: "transformation"},
		},
		adj:       map[string][]string{"S": {"T"}, "T": {"U"}},
		inDegree:  map[string]int{"T": 1, "U": 1},
		nodeIndex: map[string]int{"S": 0, "T": 1, "U": 2},
	}
}

func (g telemetryGraph) run(t *testing.T, reg *mockRegistry, eng *pkgengine.Engine, tel *traversal.Telemetry, seq int) {
	t.Helper()
	msg := message.AcquireMessage()
	msg.SetID("m")
	msg.SetData("seq", seq)
	tr := traversal.Acquire(reg, eng, "wf-tel", g.nodeMap, g.adj, g.nodeIndex, nil, nil, g.inDegree, nil)
	tr.Telemetry = tel
	tr.CurrentMessages[g.nodeIndex["S"]] = msg
	tr.Traverse(t.Context(), "S")
	traversal.Release(tr)
}

// Per-node counts, errors and latency, and per-edge counts, are produced by the
// traversal that runs a live workflow. Nothing produced them before: the
// editor's node counters, edge throughput and the optimizer's error-rate gate
// all read empty maps.
func TestTraversalRecordsNodeAndEdgeTelemetry(t *testing.T) {
	reg := &mockRegistry{
		RunWorkflowNodeFn: func(_ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
			if node.ID == "T" {
				time.Sleep(2 * time.Millisecond)
				if v, _ := msg.Data()["seq"].(int); v == 3 {
					return nil, "", errors.New("boom")
				}
			}
			msg.Retain()
			return []hermod.Message{msg}, "", nil
		},
	}
	eng := pkgengine.NewEngine(nil, nil, nil)
	g := newTelemetryGraph()
	tel := traversal.NewTelemetry(eng, g.nodeIndex, g.adj, nil)

	for i := range 5 {
		g.run(t, reg, eng, tel, i)
	}

	st := eng.GetStatus()
	for node, want := range map[string]uint64{"S": 5, "T": 5, "U": 4} {
		if got := st.NodeMetrics[node]; got != want {
			t.Errorf("NodeMetrics[%s] = %d, want %d", node, got, want)
		}
	}
	if got := st.NodeErrorMetrics["T"]; got != 1 {
		t.Errorf("NodeErrorMetrics[T] = %d, want 1", got)
	}
	if got := st.NodeErrorMetrics["U"]; got != 0 {
		t.Errorf("NodeErrorMetrics[U] = %d, want 0", got)
	}
	for edge, want := range map[string]uint64{"S->T": 5, "T->U": 4} {
		if got := st.EdgeMetrics[edge]; got != want {
			t.Errorf("EdgeMetrics[%s] = %d, want %d", edge, got, want)
		}
	}
	if got := st.NodeLatencies["T"]; got < 2*time.Millisecond {
		t.Errorf("NodeLatencies[T] = %v; T takes at least 2ms a message", got)
	}
}

// A fan-out sends one message per item along the edge; the edge counts each.
func TestTraversalCountsEveryFanOutMessageOnTheEdge(t *testing.T) {
	reg := &mockRegistry{
		RunWorkflowNodeFn: func(_ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
			if node.ID == "T" {
				out := make([]hermod.Message, 3)
				for i := range out {
					out[i] = msg.Clone()
				}
				return out, "", nil
			}
			msg.Retain()
			return []hermod.Message{msg}, "", nil
		},
	}
	eng := pkgengine.NewEngine(nil, nil, nil)
	g := newTelemetryGraph()
	tel := traversal.NewTelemetry(eng, g.nodeIndex, g.adj, nil)

	g.run(t, reg, eng, tel, 0)

	st := eng.GetStatus()
	if got := st.EdgeMetrics["T->U"]; got != 3 {
		t.Errorf("EdgeMetrics[T->U] = %d, want 3", got)
	}
	if got := st.NodeMetrics["U"]; got != 3 {
		t.Errorf("NodeMetrics[U] = %d, want 3", got)
	}
}

// A payload sample is a full ToMap per node, and each new one re-fires the
// editor's previews. It is taken only while someone watches the workflow, and
// at most once a second per node however fast messages arrive.
func TestTraversalSamplesOnlyWhileWatchedAndAtMostOncePerInterval(t *testing.T) {
	reg := &mockRegistry{}
	eng := pkgengine.NewEngine(nil, nil, nil)
	g := newTelemetryGraph()
	var watchers atomic.Int32
	tel := traversal.NewTelemetry(eng, g.nodeIndex, g.adj, &watchers)

	for i := range 20 {
		g.run(t, reg, eng, tel, i)
	}
	if got := eng.GetStatus().NodeSamples; len(got) != 0 {
		t.Fatalf("samples taken with nobody watching: %v", got)
	}

	watchers.Store(1)
	for i := 100; i < 200; i++ {
		g.run(t, reg, eng, tel, i)
	}
	samples := eng.GetStatus().NodeSamples
	for _, node := range []string{"S", "T", "U"} {
		s, ok := samples[node].(map[string]any)
		if !ok {
			t.Fatalf("no sample for node %s while watched; samples: %v", node, samples)
		}
		// Throttled: the sample is still the first watched message, not the
		// latest of the hundred that followed within the same second.
		if seq := sampleSeq(s); seq != 100 {
			t.Errorf("sample for %s carries seq %v; want 100 — sampling is not throttled", node, seq)
		}
	}
}

func sampleSeq(sample map[string]any) any {
	if v, ok := sample["seq"]; ok {
		return v
	}
	if after, ok := sample["after"].(map[string]any); ok {
		return after["seq"]
	}
	if data, ok := sample["data"].(map[string]any); ok {
		return data["seq"]
	}
	return nil
}
