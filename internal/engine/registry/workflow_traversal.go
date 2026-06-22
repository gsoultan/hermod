package registry

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	pkgengine "github.com/user/hermod/pkg/engine"
	"github.com/user/hermod/pkg/engine/telemetry"
)

type workflowTraversal struct {
	registry        *Registry
	eng             *pkgengine.Engine
	workflowID      string
	nodeMap         map[string]*storage.WorkflowNode
	adj             map[string][]string
	edgeLabels      map[string]string
	edgeBreakpoints map[string]bool
	inDegree        map[string]int
	sinkNodeToIndex map[string]int
	currentMessages map[string]hermod.Message
	msgMu           sync.Mutex
	// receivedCount counts incoming edges that actually delivered a message.
	receivedCount map[string]int
	// resolvedCount counts incoming edges that have been resolved, whether they
	// delivered a message or were pruned (e.g. the edge belongs to a branch that
	// was not taken). A node becomes ready once every incoming edge is resolved.
	resolvedCount map[string]int
	// fired guards against a node being scheduled (or skipped) more than once.
	fired    map[string]bool
	countMu  sync.Mutex
	routed   []pkgengine.RoutedMessage
	routedMu sync.Mutex
	wg       sync.WaitGroup
}

func (t *workflowTraversal) processNode(ctx context.Context, currID string) {
	defer t.wg.Done()

	if err := t.eng.AcquireNode(ctx, currID); err != nil {
		t.registry.logger.Error("Failed to acquire node semaphore", "workflow_id", t.workflowID, "node_id", currID, "error", err)
		return
	}
	defer t.eng.ReleaseNode(currID)

	t.msgMu.Lock()
	currMsg := t.currentMessages[currID]
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
	for _, targetID := range t.adj[node.ID] {
		if t.shouldFollowEdge(node, targetID, branch) && len(msgs) > 0 {
			t.eng.UpdateEdgeMetric(node.ID, targetID, uint64(len(msgs)))
			for _, m := range msgs {
				t.deliverToTarget(targetID, m)
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
	telemetry.WorkflowNodeErrors.WithLabelValues(t.workflowID, node.ID, node.Type).Inc()
	t.eng.UpdateNodeErrorMetric(node.ID, 1)

	msgID := ""
	if len(msgs) > 0 {
		msgID = msgs[0].ID()
	}
	t.registry.BroadcastLog(t.workflowID, "ERROR", fmt.Sprintf("Node %s error: %v", node.ID, err), msgID)
}

func (t *workflowTraversal) recordSuccess(node *storage.WorkflowNode, msgs []hermod.Message) {
	telemetry.WorkflowNodeProcessed.WithLabelValues(t.workflowID, node.ID, node.Type).Inc()
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
func (t *workflowTraversal) deliverToTarget(targetID string, msg hermod.Message) {
	t.msgMu.Lock()
	if t.currentMessages[targetID] == nil {
		t.currentMessages[targetID] = msg.Clone()
	} else {
		strategy := ""
		if targetNode := t.nodeMap[targetID]; targetNode != nil {
			strategy, _ = targetNode.Config["strategy"].(string)
		}
		t.registry.mergeData(t.currentMessages[targetID].Data(), msg.Data(), strategy)
	}
	t.msgMu.Unlock()
}

// resolveEdge marks one incoming edge of targetID as resolved. delivered is true
// when the edge carried a message, false when it was pruned. When all incoming
// edges are resolved the node is scheduled (if it received at least one message)
// or skipped (propagating the prune to its own descendants).
func (t *workflowTraversal) resolveEdge(ctx context.Context, targetID string, delivered bool) {
	t.countMu.Lock()
	if delivered {
		t.receivedCount[targetID]++
	}
	t.resolvedCount[targetID]++

	fire, skip := false, false
	if t.resolvedCount[targetID] >= t.inDegree[targetID] && !t.fired[targetID] {
		t.fired[targetID] = true
		if t.receivedCount[targetID] > 0 {
			fire = true
		} else {
			skip = true
		}
	}
	t.countMu.Unlock()

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
