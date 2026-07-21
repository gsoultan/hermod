package traversal

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

type Registry interface {
	RunWorkflowNode(workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error)
	IsDebuggerAttached(workflowID string) bool
	PauseForDebugger(workflowID string, nodeID string, msg hermod.Message)
	BroadcastLog(workflowID, level, message, details string)
	Logger() hermod.Logger
}

type WorkflowTraversal struct {
	Registry        Registry
	Eng             *pkgengine.Engine
	WorkflowID      string
	NodeMap         map[string]*storage.WorkflowNode
	Adj             map[string][]string
	NodeIndex       map[string]int
	EdgeLabels      map[string]string
	EdgeBreakpoints map[string]bool
	InDegree        map[string]int
	SinkNodeToIndex map[string]int

	// Array-based state for ultra-fast traversal
	CurrentMessages []hermod.Message
	MsgMu           sync.Mutex
	ReceivedCount   []int32
	ResolvedCount   []int32
	Fired           []int32 // 0=not fired, 1=fired

	Routed   []pkgengine.RoutedMessage
	RoutedMu sync.Mutex
	Wg       sync.WaitGroup
}

var TraversalPool = sync.Pool{
	New: func() any {
		return &WorkflowTraversal{}
	},
}

func Acquire(
	reg Registry,
	eng *pkgengine.Engine,
	workflowID string,
	nodeMap map[string]*storage.WorkflowNode,
	adj map[string][]string,
	nodeIndex map[string]int,
	edgeLabels map[string]string,
	edgeBreakpoints map[string]bool,
	inDegree map[string]int,
	sinkNodeToIndex map[string]int,
) *WorkflowTraversal {
	t := TraversalPool.Get().(*WorkflowTraversal)
	t.Registry = reg
	t.Eng = eng
	t.WorkflowID = workflowID
	t.NodeMap = nodeMap
	t.Adj = adj
	t.NodeIndex = nodeIndex
	t.EdgeLabels = edgeLabels
	t.EdgeBreakpoints = edgeBreakpoints
	t.InDegree = inDegree
	t.SinkNodeToIndex = sinkNodeToIndex

	// Re-initialize slices for the specific workflow topology
	numNodes := len(nodeMap)
	if cap(t.CurrentMessages) < numNodes {
		t.CurrentMessages = make([]hermod.Message, numNodes)
		t.ReceivedCount = make([]int32, numNodes)
		t.ResolvedCount = make([]int32, numNodes)
		t.Fired = make([]int32, numNodes)
	} else {
		t.CurrentMessages = t.CurrentMessages[:numNodes]
		t.ReceivedCount = t.ReceivedCount[:numNodes]
		t.ResolvedCount = t.ResolvedCount[:numNodes]
		t.Fired = t.Fired[:numNodes]
		for i := range t.CurrentMessages {
			t.CurrentMessages[i] = nil
			t.ReceivedCount[i] = 0
			t.ResolvedCount[i] = 0
			t.Fired[i] = 0
		}
	}

	for id, count := range inDegree {
		t.ReceivedCount[nodeIndex[id]] = int32(count)
	}

	t.Routed = t.Routed[:0]
	return t
}

func Release(t *WorkflowTraversal) {
	t.MsgMu.Lock()
	for i := range t.CurrentMessages {
		if t.CurrentMessages[i] != nil {
			t.CurrentMessages[i].Release()
			t.CurrentMessages[i] = nil
		}
	}
	t.MsgMu.Unlock()
	t.Registry = nil
	t.Eng = nil
	TraversalPool.Put(t)
}

func (t *WorkflowTraversal) Traverse(ctx context.Context, startNodeID string) {
	t.Wg.Go(func() { t.processNode(ctx, startNodeID) })
	t.Wg.Wait()
}

func (t *WorkflowTraversal) processNode(ctx context.Context, currID string) {
	defer func() {
		if rec := recover(); rec != nil {
			if t.Registry != nil && t.Registry.Logger() != nil {
				t.Registry.Logger().Error("Workflow node panicked during traversal",
					"workflow_id", t.WorkflowID, "node_id", currID,
					"panic", rec, "stack", string(debug.Stack()))
			}
			if t.Registry != nil {
				nodeDisplayName := currID
				if node, ok := t.NodeMap[currID]; ok {
					if label, ok := node.Config["label"].(string); ok && label != "" {
						nodeDisplayName = label
					}
				}
				t.Registry.BroadcastLog(t.WorkflowID, "ERROR",
					fmt.Sprintf("Node %s panicked: %v", nodeDisplayName, rec), "")
			}
		}
	}()

	if err := t.Eng.AcquireNode(ctx, currID); err != nil {
		if t.Registry != nil && t.Registry.Logger() != nil {
			t.Registry.Logger().Error("Failed to acquire node semaphore", "workflow_id", t.WorkflowID, "node_id", currID, "error", err)
		}
		return
	}
	defer t.Eng.ReleaseNode(currID)

	t.MsgMu.Lock()
	idx := t.NodeIndex[currID]
	currMsg := t.CurrentMessages[idx]
	t.CurrentMessages[idx] = nil
	t.MsgMu.Unlock()

	currNode := t.NodeMap[currID]
	if currNode == nil || currMsg == nil {
		if currMsg != nil {
			currMsg.Release()
		}
		return
	}
	defer currMsg.Release()

	msgs, branch, err := t.runNode(ctx, currNode, currMsg)

	// If the current node is a sink, route the results to the writer.
	if currNode.Type == "sink" {
		t.RoutedMu.Lock()
		if sinkIdx, ok := t.SinkNodeToIndex[currID]; ok {
			for _, m := range msgs {
				m.Retain()
				t.Routed = append(t.Routed, pkgengine.RoutedMessage{
					SinkIndex: sinkIdx,
					Message:   m,
				})
			}
		}
		t.RoutedMu.Unlock()
	}

	t.handleResults(ctx, currNode, msgs, branch, err)

	// Release messages returned from runNode as we've either routed them
	// or they were passed to resolveEdge (which retains them).
	for _, m := range msgs {
		m.Release()
	}
}

func (t *WorkflowTraversal) runNode(ctx context.Context, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	if node.Type == "source" {
		return []hermod.Message{msg}, "", nil
	}

	if t.Registry.IsDebuggerAttached(t.WorkflowID) {
		t.Registry.PauseForDebugger(t.WorkflowID, node.ID, msg)
	}

	start := time.Now()
	msgs, branch, err := t.Registry.RunWorkflowNode(t.WorkflowID, node, msg)

	if len(msgs) > 0 {
		for _, m := range msgs {
			t.Eng.RecordTraceStep(ctx, m, node.ID, start, nil, err)
		}
	} else {
		t.Eng.RecordTraceStep(ctx, msg, node.ID, start, nil, err)
	}

	return msgs, branch, err
}

func (t *WorkflowTraversal) handleResults(ctx context.Context, node *storage.WorkflowNode, msgs []hermod.Message, branch string, err error) {
	if err != nil {
		t.Registry.BroadcastLog(t.WorkflowID, "ERROR", fmt.Sprintf("Node %s failed: %v", node.ID, err), "")
		return
	}

	targets := t.Adj[node.ID]
	for _, targetID := range targets {
		taken := true
		if branch != "" {
			if label := t.EdgeLabels[node.ID+":"+targetID]; label != "" && label != branch {
				taken = false
			}
		}

		if taken {
			for _, msg := range msgs {
				// Clone the message if it's going to multiple targets to avoid data races
				// when nodes modify the message concurrently.
				passMsg := msg
				if len(targets) > 1 {
					passMsg = msg.Clone()
				}

				t.resolveEdge(ctx, targetID, passMsg)

				// If we cloned, release the clone's initial reference count
				// as resolveEdge has already called Retain() if it stored it.
				if passMsg != msg {
					passMsg.Release()
				}
			}
		} else {
			t.pruneBranch(ctx, targetID)
		}
	}
}

func (t *WorkflowTraversal) pruneBranch(ctx context.Context, targetID string) {
	idx := t.NodeIndex[targetID]
	newCount := atomic.AddInt32(&t.ResolvedCount[idx], 1)
	if newCount >= t.ReceivedCount[idx] {
		// If the node hasn't fired yet, and it was reached only by pruned branches,
		// we must continue pruning its successors.
		if atomic.CompareAndSwapInt32(&t.Fired[idx], 0, 1) {
			targets := t.Adj[targetID]
			for _, nextID := range targets {
				t.pruneBranch(ctx, nextID)
			}
		}
	} else {
		// Even if not yet fully resolved, we should check if there's any other path
		// that could still reach it. The current logic handles this by incrementing
		// ResolvedCount.
	}
}

func (t *WorkflowTraversal) resolveEdge(ctx context.Context, targetID string, msg hermod.Message) {
	idx := t.NodeIndex[targetID]
	targetNode := t.NodeMap[targetID]

	t.MsgMu.Lock()
	if t.CurrentMessages[idx] == nil {
		msg.Retain()
		t.CurrentMessages[idx] = msg
	} else if targetNode != nil && targetNode.Type == "join" {
		// For join nodes, we must merge the data into the already-waiting message.
		dest := t.CurrentMessages[idx]
		for k, v := range msg.Data() {
			dest.SetData(k, v)
		}
		for k, v := range msg.Metadata() {
			dest.SetMetadata(k, v)
		}
	}
	t.MsgMu.Unlock()

	newCount := atomic.AddInt32(&t.ResolvedCount[idx], 1)
	if newCount >= t.ReceivedCount[idx] {
		if atomic.CompareAndSwapInt32(&t.Fired[idx], 0, 1) {
			t.Wg.Go(func() { t.processNode(ctx, targetID) })
		}
	}
}
