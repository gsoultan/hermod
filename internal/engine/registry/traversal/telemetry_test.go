// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
	tel := traversal.NewTelemetry(eng, g.nodeIndex, g.adj, nil, nil)

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
	tel := traversal.NewTelemetry(eng, g.nodeIndex, g.adj, nil, nil)

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
	tel := traversal.NewTelemetry(eng, g.nodeIndex, g.adj, nil, &watchers)

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

// Each edge is also counted under its canvas ID, position for position with
// the adjacency, so two edges between the same two nodes keep separate counts
// and the editor can draw each with its own rate.
func TestTraversalCountsEachEdgeUnderItsOwnID(t *testing.T) {
	reg := &mockRegistry{}
	eng := pkgengine.NewEngine(nil, nil, nil)
	g := newTelemetryGraph()
	// T reaches U along two edges, as two switch cases into one sink would.
	g.adj["T"] = []string{"U", "U"}
	g.inDegree["U"] = 2
	edgeIDs := map[string][]string{"S": {"e-st"}, "T": {"e-tu-1", "e-tu-2"}}
	tel := traversal.NewTelemetry(eng, g.nodeIndex, g.adj, edgeIDs, nil)

	for i := range 4 {
		g.run(t, reg, eng, tel, i)
	}

	counts := eng.GetStatus().EdgeCounts
	for id, want := range map[string]uint64{"e-st": 4, "e-tu-1": 4, "e-tu-2": 4} {
		if got := counts[id]; got != want {
			t.Errorf("EdgeCounts[%s] = %d, want %d", id, got, want)
		}
	}
}

// While a node is running a message it is reported busy, and once it returns it
// is not: this is what lets the editor flag a node that has hung.
func TestTraversalReportsANodeBusyWhileItRuns(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	reg := &mockRegistry{
		RunWorkflowNodeFn: func(_ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
			if node.ID == "T" {
				close(entered)
				<-release
			}
			msg.Retain()
			return []hermod.Message{msg}, "", nil
		},
	}
	eng := pkgengine.NewEngine(nil, nil, nil)
	g := newTelemetryGraph()
	tel := traversal.NewTelemetry(eng, g.nodeIndex, g.adj, nil, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		g.run(t, reg, eng, tel, 0)
	}()
	<-entered

	if got := eng.GetStatus().NodeBusy["T"]; got.InFlight != 1 {
		t.Errorf("NodeBusy[T] = %+v while T runs, want 1 in flight", got)
	}
	close(release)
	<-done
	if busy := eng.GetStatus().NodeBusy; len(busy) != 0 {
		t.Errorf("NodeBusy = %+v after the traversal finished, want none", busy)
	}
}

// A node that panics is recovered by the traversal. It must not be left
// counted as in flight, or the editor would report it hung for good.
func TestTraversalDoesNotLeaveAPanickedNodeBusy(t *testing.T) {
	reg := &mockRegistry{
		RunWorkflowNodeFn: func(_ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
			if node.ID == "T" {
				panic("boom")
			}
			msg.Retain()
			return []hermod.Message{msg}, "", nil
		},
	}
	eng := pkgengine.NewEngine(nil, nil, nil)
	g := newTelemetryGraph()
	tel := traversal.NewTelemetry(eng, g.nodeIndex, g.adj, nil, nil)

	g.run(t, reg, eng, tel, 0)

	if busy := eng.GetStatus().NodeBusy; len(busy) != 0 {
		t.Errorf("NodeBusy = %+v after T panicked, want none", busy)
	}
}

// A message held by the debugger before a node is reported, so the editor can
// say the node is waiting on the debugger rather than leaving it to look stuck.
func TestTraversalReportsAMessageHeldByTheDebugger(t *testing.T) {
	held := make(chan struct{})
	release := make(chan struct{})
	reg := &mockRegistry{
		PauseFn: func(nodeID string) {
			if nodeID == "U" {
				close(held)
				<-release
			}
		},
	}
	eng := pkgengine.NewEngine(nil, nil, nil)
	g := newTelemetryGraph()
	tel := traversal.NewTelemetry(eng, g.nodeIndex, g.adj, nil, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		g.run(t, reg, eng, tel, 0)
	}()
	<-held

	if got := eng.GetStatus().NodeHeld["U"]; got != 1 {
		t.Errorf("NodeHeld[U] = %d while the debugger holds a message there, want 1", got)
	}
	close(release)
	<-done
	if held := eng.GetStatus().NodeHeld; len(held) != 0 {
		t.Errorf("NodeHeld = %v after the debugger let go, want none", held)
	}
}
