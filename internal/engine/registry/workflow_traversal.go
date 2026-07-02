package registry

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	pkgengine "github.com/user/hermod/pkg/engine"
)

type workflowTraversal struct {
	registry        *Registry
	eng             *pkgengine.Engine
	workflowID      string
	nodeMap         map[string]*storage.WorkflowNode
	adj             map[string][]string
	nodeIndex       map[string]int
	edgeLabels      map[string]string
	edgeBreakpoints map[string]bool
	inDegree        map[string]int
	sinkNodeToIndex map[string]int

	// Array-based state for ultra-fast traversal
	currentMessages []hermod.Message
	msgMu           sync.Mutex
	receivedCount   []int32
	resolvedCount   []int32
	fired           []int32 // 0=not fired, 1=fired

	routed   []pkgengine.RoutedMessage
	routedMu sync.Mutex
	wg       sync.WaitGroup
}

func (t *workflowTraversal) processNode(ctx context.Context, currID string) {
	defer t.wg.Done()

	// Each node runs in its own goroutine (see resolveEdge). A panic here would
	// otherwise propagate to the goroutine's top and crash the entire process,
	// taking the embedded API server down with it and surfacing to in-flight
	// requests (e.g. the workflow editor) as a 502. Contain the panic so a
	// single malformed node/message can never bring the worker down; the rest
	// of the traversal and other workflows keep running.
	defer func() {
		if rec := recover(); rec != nil {
			if t.registry != nil && t.registry.logger != nil {
				t.registry.logger.Error("Workflow node panicked during traversal",
					"workflow_id", t.workflowID, "node_id", currID,
					"panic", rec, "stack", string(debug.Stack()))
			}
			if t.registry != nil {
				t.registry.BroadcastLog(t.workflowID, "ERROR",
					fmt.Sprintf("Node %s panicked: %v", currID, rec), "")
			}
		}
	}()

	if err := t.eng.AcquireNode(ctx, currID); err != nil {
		t.registry.logger.Error("Failed to acquire node semaphore", "workflow_id", t.workflowID, "node_id", currID, "error", err)
		return
	}
	defer t.eng.ReleaseNode(currID)

	t.msgMu.Lock()
	currMsg := t.currentMessages[t.nodeIndex[currID]]
	t.msgMu.Unlock()

	currNode := t.nodeMap[currID]
	if currNode == nil || currMsg == nil {
		return
	}

	msgs, branch, err := t.runNode(ctx, currNode, currMsg)
	t.handleResults(ctx, currNode, msgs, branch, err)
}

func (t *workflowTraversal) runNode(ctx context.Context, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	if node.Type == "source" {
		return []hermod.Message{msg}, "", nil
	}

	if t.registry.isDebuggerAttached(t.workflowID) {
		t.registry.pauseForDebugger(t.workflowID, node.ID, msg)
	}

	start := time.Now()
	msgs, branch, err := t.registry.runWorkflowNode(t.workflowID, node, msg)

	// Record metrics and traces
	if len(msgs) > 0 {
		for _, m := range msgs {
			t.eng.RecordTraceStep(ctx, m, node.ID, start, nil, err)
		}
	} else {
		t.eng.RecordTraceStep(ctx, msg, node.ID, start, nil, err)
	}

	return msgs, branch, err
}

func (t *workflowTraversal) handleResults(ctx context.Context, node *storage.WorkflowNode, msgs []hermod.Message, branch string, err error) {
	if err != nil {
		t.recordError(node, msgs, err)
		branch = "error"
	}

	if len(msgs) > 0 {
		t.recordSuccess(node, msgs)
	}

	// Route produced messages to sink outputs (per message).
	t.routeToSink(node, msgs)

	// Resolve every outgoing edge exactly once for this node execution. Edges on
	// branches that were taken deliver the produced messages; all other edges
	// are pruned so that downstream join nodes can still reach their expected
	// in-degree instead of stalling forever.
	validTargets := make([]string, 0, len(t.adj[node.ID]))
	for _, targetID := range t.adj[node.ID] {
		if t.shouldFollowEdge(node, targetID, branch) {
			validTargets = append(validTargets, targetID)
		} else {
			t.resolveEdge(ctx, targetID, false)
		}
	}

	for i, targetID := range validTargets {
		if len(msgs) > 0 {
			t.eng.UpdateEdgeMetric(node.ID, targetID, uint64(len(msgs)))
			// If this is not the last target, we must clone the messages because
			// subsequent targets will also need them. If it is the last target,
			// we can pass ownership of the messages to that target.
			shouldClone := i < len(validTargets)-1
			for _, m := range msgs {
				t.deliverToTarget(targetID, m, shouldClone)
			}
			t.resolveEdge(ctx, targetID, true)
		} else {
			t.resolveEdge(ctx, targetID, false)
		}
	}
}

// routeToSink appends produced messages to the routed output when the node is a
// non-sequential sink node.
func (t *workflowTraversal) routeToSink(node *storage.WorkflowNode, msgs []hermod.Message) {
	if node.Type != "sink" {
		return
	}
	if isSeq, _ := node.Config["sequential"].(bool); isSeq {
		return
	}
	idx, ok := t.sinkNodeToIndex[node.ID]
	if !ok {
		return
	}
	t.routedMu.Lock()
	for _, m := range msgs {
		t.routed = append(t.routed, pkgengine.RoutedMessage{SinkIndex: idx, Message: m})
	}
	t.routedMu.Unlock()
}

func (t *workflowTraversal) recordError(node *storage.WorkflowNode, msgs []hermod.Message, err error) {
	t.eng.UpdateNodeErrorMetric(node.ID, 1)

	msgID := ""
	if len(msgs) > 0 {
		msgID = msgs[0].ID()
	}
	t.registry.BroadcastLog(t.workflowID, "ERROR", fmt.Sprintf("Node %s error: %v", node.ID, err), msgID)
}

func (t *workflowTraversal) recordSuccess(node *storage.WorkflowNode, msgs []hermod.Message) {
	t.eng.UpdateNodeMetric(node.ID, uint64(len(msgs)))
	// Capturing a node payload sample copies the whole message; only do it when
	// a client is actually watching the status/dashboard/live streams.
	if t.registry.hasStatusObservers() {
		t.eng.UpdateNodeSample(node.ID, t.registry.getConsistentData(msgs[0]))
	}
}

func (t *workflowTraversal) shouldFollowEdge(node *storage.WorkflowNode, targetID string, branch string) bool {
	edgeLabel := t.edgeLabels[node.ID+":"+targetID]
	if branch == "error" {
		return edgeLabel == "error"
	}
	if edgeLabel == "error" {
		return false
	}
	if node.Type == "condition" || node.Type == "switch" {
		return edgeLabel == "" || edgeLabel == branch
	}
	return true
}

// deliverToTarget stores (or merges) a message destined for targetID so that it
// becomes the input message for that node once it is scheduled.
func (t *workflowTraversal) deliverToTarget(targetID string, msg hermod.Message, shouldClone bool) {
	idx := t.nodeIndex[targetID]
	t.msgMu.Lock()
	defer t.msgMu.Unlock()

	if t.currentMessages[idx] == nil {
		if shouldClone {
			t.currentMessages[idx] = msg.Clone()
		} else {
			t.currentMessages[idx] = msg
		}
	} else {
		strategy := ""
		if targetNode := t.nodeMap[targetID]; targetNode != nil {
			strategy, _ = targetNode.Config["strategy"].(string)
		}
		t.registry.mergeData(t.currentMessages[idx].Data(), msg.Data(), strategy)
	}
}

// resolveEdge marks one incoming edge of targetID as resolved. delivered is true
// when the edge carried a message, false when it was pruned. When all incoming
// edges are resolved the node is scheduled (if it received at least one message)
// or skipped (propagating the prune to its own descendants).
func (t *workflowTraversal) resolveEdge(ctx context.Context, targetID string, delivered bool) {
	idx := t.nodeIndex[targetID]
	if delivered {
		atomic.AddInt32(&t.receivedCount[idx], 1)
	}
	resolved := atomic.AddInt32(&t.resolvedCount[idx], 1)

	fire, skip := false, false
	if resolved >= int32(t.inDegree[targetID]) {
		if atomic.CompareAndSwapInt32(&t.fired[idx], 0, 1) {
			if atomic.LoadInt32(&t.receivedCount[idx]) > 0 {
				fire = true
			} else {
				skip = true
			}
		}
	}

	switch {
	case fire:
		t.wg.Add(1)
		go t.processNode(ctx, targetID)
	case skip:
		// The node will never run; propagate the prune to its descendants so
		// that deeper join nodes can still reach their expected in-degree.
		for _, child := range t.adj[targetID] {
			t.resolveEdge(ctx, child, false)
		}
	}
}
