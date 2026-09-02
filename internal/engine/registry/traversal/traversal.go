package traversal

import (
	"context"
	"fmt"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/storage"
	pkgengine "github.com/gsoultan/Hermod/pkg/engine"
)

type Registry interface {
	RunWorkflowNode(workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error)
	IsDebuggerAttached(workflowID string) bool
	PauseForDebugger(workflowID string, nodeID string, msg hermod.Message)
	BroadcastLog(workflowID, level, message, details string)
	Logger() hermod.Logger
	// RecordCircuitBreakerFailure counts a downstream failure against a breaker.
	RecordCircuitBreakerFailure(workflowID, breakerNodeID string)
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

// countAgainstBreakers records a failure against every circuit breaker that
// feeds the failed node.
//
// A breaker protects what it feeds, so its immediate downstream is what counts
// against it. Anything further along is deliberately not counted: a breaker
// three nodes upstream of a failure has no useful relationship to it, and
// guessing otherwise makes a control that trips for reasons nobody can trace.
//
// The reverse lookup runs only when something has failed. That is why the count
// ages out rather than resetting on success — resetting would put this on every
// successful message to serve a case that is rare by definition.
func (t *WorkflowTraversal) countAgainstBreakers(failedID string) {
	for parent, targets := range t.Adj {
		node := t.NodeMap[parent]
		if node == nil || node.Type != "circuit_breaker" {
			continue
		}
		if slices.Contains(targets, failedID) {
			t.Registry.RecordCircuitBreakerFailure(t.WorkflowID, parent)
		}
	}
}

// nodeWritesInline reports whether a sink node performs its own write, in which
// case the async writers must not write it again. It reads the same flag the
// sink executor reads (nodes/core/sink.go).
func nodeWritesInline(node *storage.WorkflowNode) bool {
	if node == nil {
		return false
	}
	seq, _ := node.Config["sequential"].(bool)
	return seq
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

	// A node that failed must not take the message with it.
	//
	// The dead-letter sink caught validation failures and sink write failures.
	// A node failing inside the workflow was logged and the message released, so
	// a workflow with a dead-letter sink configured still lost every message a
	// transformation, condition or wait rejected — and the log line looked
	// enough like handling that nobody would go looking.
	//
	// The message is dead-lettered here, while it is still alive: the deferred
	// release above frees it as soon as this function returns.
	if err != nil {
		t.countAgainstBreakers(currID)
	}

	if err != nil && t.Eng != nil {
		if !t.Eng.DeadLetterNodeFailure(ctx, currNode.ID, currMsg, err) {
			t.Registry.BroadcastLog(t.WorkflowID, "ERROR", fmt.Sprintf(
				"Node %s failed and there is no dead-letter sink, so the message is lost: %v",
				currNode.ID, err), currMsg.ID())
		}
	}

	// If the current node is a sink, route the results to the writer — unless it
	// already wrote them itself.
	//
	// The sequential flag picks between two models. Off, this node is a
	// pass-through and the engine's sink writers do the write, so this routing
	// is the only thing that delivers anything. On, the executor writes inline
	// and returns the message so the success and error branches have something
	// to carry.
	//
	// Routing regardless of which model ran meant a sequential sink wrote inline
	// and then handed the same message to the writer: two deliveries per
	// message, for the life of the workflow. Worse on the failing path, where
	// the executor returns the message with the error branch and the writer then
	// retried a write the workflow had already been told had failed.
	if currNode.Type == "sink" && !nodeWritesInline(currNode) {
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
		// Every message in the returned slice is owned by the caller, which
		// releases each one after handleResults — the same contract
		// Registry.RunWorkflowNode implements by retaining when it passes the
		// input straight through. Returning the input here without retaining it
		// made processNode release one reference more than it held: the message
		// went back to the pool while the runner and the routed sink references
		// were still using it, was re-acquired and refilled by the source, and
		// the stale owners then delivered the wrong payload. The symptom was
		// messages delivered twice while others were never delivered, with the
		// total conserved, and no error logged anywhere.
		msg.Retain()
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
