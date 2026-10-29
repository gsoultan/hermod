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
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/traversal"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// ranLog records the nodes a traversal ran, in order, from any goroutine.
type ranLog struct {
	mu  sync.Mutex
	ran []string
}

func (l *ranLog) add(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ran = append(l.ran, id)
}

func (l *ranLog) has(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Contains(l.ran, id)
}

// edgePause is one message held on an edge breakpoint.
type edgePause struct {
	edgeID, targetID string
	release          chan struct{}
}

// breakpointGraph is S -> A -> B and S -> C, with the edges named eSA, eAB and
// eSC; bp names the edges that carry a breakpoint.
func breakpointGraph(bp ...string) (map[string]*storage.WorkflowNode, map[string][]string, map[string][]string, map[string]int, map[string]int, map[string]bool) {
	nodeMap := map[string]*storage.WorkflowNode{
		"S": {ID: "S", Type: "source"},
		"A": {ID: "A", Type: "passthrough"},
		"B": {ID: "B", Type: "passthrough"},
		"C": {ID: "C", Type: "passthrough"},
	}
	adj := map[string][]string{"S": {"A", "C"}, "A": {"B"}}
	edgeIDs := map[string][]string{"S": {"eSA", "eSC"}, "A": {"eAB"}}
	nodeIndex := map[string]int{"S": 0, "A": 1, "B": 2, "C": 3}
	inDegree := map[string]int{"A": 1, "B": 1, "C": 1}
	breakpoints := map[string]bool{}
	for _, id := range bp {
		breakpoints[id] = true
	}
	return nodeMap, adj, edgeIDs, nodeIndex, inDegree, breakpoints
}

// traverseWithBreakpoints walks one message from S through breakpointGraph on
// its own goroutine, and returns a channel closed once the walk is done.
func traverseWithBreakpoints(reg *mockRegistry, bp ...string) <-chan struct{} {
	nodeMap, adj, edgeIDs, nodeIndex, inDegree, breakpoints := breakpointGraph(bp...)
	eng := pkgengine.NewEngine(nil, nil, nil)
	tr := traversal.Acquire(reg, eng, "wf-bp", nodeMap, adj, nodeIndex, nil, breakpoints, inDegree, nil)
	tr.EdgeIDs = edgeIDs
	msg := message.AcquireMessage()
	msg.SetID("m-1")
	msg.SetData("n", 1)
	tr.CurrentMessages[nodeIndex["S"]] = msg

	done := make(chan struct{})
	go func() {
		defer close(done)
		tr.Traverse(context.Background(), "S")
		traversal.Release(tr)
	}()
	return done
}

func newBreakpointRegistry(ran *ranLog, paused chan edgePause) *mockRegistry {
	return &mockRegistry{
		RunWorkflowNodeFn: func(_ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
			ran.add(node.ID)
			msg.Retain()
			return []hermod.Message{msg}, "", nil
		},
		EdgePauseFn: func(edgeID, targetID string, _ hermod.Message) {
			p := edgePause{edgeID: edgeID, targetID: targetID, release: make(chan struct{})}
			paused <- p
			<-p.release
		},
	}
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the traversal did not finish")
	}
}

// A message reaching an edge with a breakpoint waits there, before the node the
// edge leads to, until the debugger lets it go; then it carries on.
func TestEdgeBreakpointHoldsTheMessageBeforeItsTarget(t *testing.T) {
	ran := &ranLog{}
	paused := make(chan edgePause, 4)
	done := traverseWithBreakpoints(newBreakpointRegistry(ran, paused), "eAB")

	var p edgePause
	select {
	case p = <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("the message never stopped at the breakpoint on eAB")
	}
	if p.edgeID != "eAB" || p.targetID != "B" {
		t.Fatalf("paused on %s into %s, want eAB into B", p.edgeID, p.targetID)
	}
	if ran.has("B") {
		t.Fatal("B ran while the message was held on the edge into it")
	}

	close(p.release)
	waitDone(t, done)
	if !ran.has("B") {
		t.Error("B never ran after the breakpoint let the message go")
	}
}

// A breakpoint on one branch holds only that branch.
func TestEdgeBreakpointDoesNotHoldSiblingBranches(t *testing.T) {
	ran := &ranLog{}
	paused := make(chan edgePause, 4)
	done := traverseWithBreakpoints(newBreakpointRegistry(ran, paused), "eSA")

	var p edgePause
	select {
	case p = <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("the message never stopped at the breakpoint on eSA")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !ran.has("C") {
		if time.Now().After(deadline) {
			close(p.release)
			t.Fatal("C, on the other branch, waited for the breakpoint on eSA")
		}
		time.Sleep(time.Millisecond)
	}
	close(p.release)
	waitDone(t, done)
}

// With nobody attached to answer, a breakpoint holds nothing: an unattended
// breakpoint would stall a production workflow for the full pause limit on
// every message.
func TestEdgeBreakpointIsIgnoredWithNobodyAttached(t *testing.T) {
	ran := &ranLog{}
	reg := newBreakpointRegistry(ran, nil)
	reg.EdgePauseFn = nil
	waitDone(t, traverseWithBreakpoints(reg, "eAB"))
	if !ran.has("B") {
		t.Error("B did not run")
	}
}

// A breakpoint names one edge by its ID; an edge the message does not take is
// never paused on, and an edge without one is never paused on.
func TestEdgeBreakpointOnlyOnItsOwnEdge(t *testing.T) {
	ran := &ranLog{}
	paused := make(chan edgePause, 4)
	waitDone(t, traverseWithBreakpoints(newBreakpointRegistry(ran, paused), "eNotInGraph"))
	select {
	case p := <-paused:
		t.Errorf("paused on %s with no breakpoint on it", p.edgeID)
	default:
	}
}
