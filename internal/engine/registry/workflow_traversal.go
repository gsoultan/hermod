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
	receivedCount   map[string]int
	countMu         sync.Mutex
	routed          []pkgengine.RoutedMessage
	routedMu        sync.Mutex
	wg              sync.WaitGroup
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

	for _, m := range msgs {
		t.routeMessage(ctx, node, m, branch)
	}
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
	t.eng.UpdateNodeSample(node.ID, t.registry.getConsistentData(msgs[0]))
}

func (t *workflowTraversal) routeMessage(ctx context.Context, node *storage.WorkflowNode, msg hermod.Message, branch string) {
	if node.Type == "sink" {
		isSeq, _ := node.Config["sequential"].(bool)
		if !isSeq {
			if idx, ok := t.sinkNodeToIndex[node.ID]; ok {
				t.routedMu.Lock()
				t.routed = append(t.routed, pkgengine.RoutedMessage{SinkIndex: idx, Message: msg})
				t.routedMu.Unlock()
			}
		}
	}

	for _, targetID := range t.adj[node.ID] {
		if t.shouldFollowEdge(node, targetID, branch) {
			// Record per-edge metric using correct source/target
			t.eng.UpdateEdgeMetric(node.ID, targetID, 1)
			t.followEdge(ctx, targetID, msg)
		}
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

func (t *workflowTraversal) followEdge(ctx context.Context, targetID string, msg hermod.Message) {

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

	t.countMu.Lock()
	t.receivedCount[targetID]++
	if t.receivedCount[targetID] == t.inDegree[targetID] {
		t.wg.Add(1)
		go t.processNode(ctx, targetID)
	}
	t.countMu.Unlock()
}
