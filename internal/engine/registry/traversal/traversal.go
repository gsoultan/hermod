package traversal

import (
	"context"
	"fmt"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
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

	// InlineDelivered records that at least one sink node wrote this message
	// itself. Such a node routes nothing on purpose, so without this the engine
	// cannot tell a delivered message from an undeliverable one and refuses to
	// acknowledge data it has in fact written.
	//
	// InlineFailed records that one did not. With several inline sinks, one
	// succeeding does not make the message safe to acknowledge: the failed sink
	// still needs the redelivery that leaving it unacknowledged buys. Only
	// delivered-and-nothing-failed is an acknowledgement.
	InlineDelivered atomic.Bool
	InlineFailed    atomic.Bool

	// DeadLettered records that a failing node parked this message in the
	// dead-letter sink. A failure past a fan-out is parked as a *clone*, so the
	// marker the engine sets on the message it parked never reaches the original
	// the engine is about to make an acknowledge-or-keep decision about. Carrying
	// it on the traversal instead survives the clone, and the router stamps the
	// original once the walk is done.
	DeadLettered atomic.Bool
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
	t.InlineDelivered.Store(false)
	t.InlineFailed.Store(false)
	t.DeadLettered.Store(false)
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
	// The start node runs on the caller's goroutine. Spawning one and then
	// immediately waiting for it bought nothing: the caller has nothing else to
	// do until the walk finishes, and everything downstream still gets its own
	// goroutine through resolveEdge, which Wg.Wait below still covers.
	//
	// On a four-node graph that is one goroutine per message out of four, and
	// the whole traversal is only ~0.7% of engine CPU, so this is tidiness
	// rather than a fix — the measured cost of a workflow is allocation, not
	// scheduling.
	t.processNode(ctx, startNodeID)
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

	// Deferred, not trailing. This function recovers from panics in everything
	// below — which is deliberate, so one bad node cannot take the worker with
	// it — and a trailing release is exactly what that recovery skips. Every
	// message runNode produced would keep a reference it never gets back:
	// never pooled, holding its payload for the life of the process, and worst
	// under a fan-out, where one panic leaks one message per array item.
	defer func() {
		for _, m := range msgs {
			m.Release()
		}
	}()

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
		switch {
		case t.Eng.DeadLetterNodeFailure(ctx, currNode.ID, currMsg, err):
			t.DeadLettered.Store(true)
		case t.Eng.IsDryRun():
			// A dry run parks nothing, but it also acknowledges nothing, so the
			// message stays on the source and will be redelivered. Saying it was
			// lost here would be the opposite of what happened.
			t.Registry.BroadcastLog(t.WorkflowID, "WARN", fmt.Sprintf(
				"[DRY-RUN] Node %s failed; the message is left on the source rather than dead-lettered: %v",
				currNode.ID, err), currMsg.ID())
		default:
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
	if currNode.Type == "sink" && nodeWritesInline(currNode) {
		// Delivered, by this node, already. Nothing is routed — recording it is
		// the only thing that stops the engine treating the message as
		// undeliverable and leaving the source unacknowledged. A write that
		// failed records the opposite, and wins.
		if err == nil {
			t.InlineDelivered.Store(true)
		} else {
			t.InlineFailed.Store(true)
		}
	}

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

	// The messages are released by the deferred loop above, whether this
	// returns or panics. They have either been routed or handed to
	// resolveEdge, which retains what it keeps.
	t.handleResults(ctx, currNode, msgs, branch, err)
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

	// Stamped when the node finished, not when it started. The step's payload is
	// the node's output, and a `pipeline` node's own steps record under their
	// transType at timestamps in between — so a start-stamped parent sorted
	// ahead of its own children carrying their result, and the viewer, which
	// rebuilds each "before" from the previous "after", showed the first child
	// dropping a field it had not yet produced. One instant for every fan-out
	// sibling: they did all finish together.
	done := time.Now()

	if len(msgs) > 0 {
		for _, m := range msgs {
			t.Eng.RecordCompletedTraceStep(ctx, m, node.ID, start, done, nil, err)
		}
	} else {
		t.Eng.RecordCompletedTraceStep(ctx, msg, node.ID, start, done, nil, err)
	}

	return msgs, branch, err
}

func (t *WorkflowTraversal) handleResults(ctx context.Context, node *storage.WorkflowNode, msgs []hermod.Message, branch string, err error) {
	if err != nil {
		t.Registry.BroadcastLog(t.WorkflowID, "ERROR", fmt.Sprintf("Node %s failed: %v", node.ID, err), "")
		return
	}

	// A node that returns more than one message has fanned out, and each of those
	// messages is an independent walk of everything downstream — that is the only
	// thing that makes a foreach node write one row per array item.
	//
	// This traversal cannot carry them: it holds a single message slot per node
	// and fires each node exactly once (CurrentMessages / Fired). Handing it all N
	// delivered the first message and silently dropped the rest — the fan-out node
	// itself was correct and unit-tested, the loss happened here. So the first
	// message stays in this traversal and every other one is walked by a traversal
	// of its own over the same graph, with its results merged back.
	if len(msgs) > 1 {
		t.forkFanout(ctx, node, msgs[1:], branch)
		msgs = msgs[:1]
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

// forkFanout walks the graph below node once for each extra fan-out message.
//
// It runs on its own goroutine so the fan-out node's concurrency slot is not
// held for the length of the walks below it — holding it would deadlock any
// graph whose downstream loops back. The extras are walked one at a time rather
// than all at once: an array field is attacker- or upstream-controlled, and a
// goroutine and a pooled traversal per element turns a 100k-row array into a
// memory incident. The first message is already walking in parallel with these.
func (t *WorkflowTraversal) forkFanout(ctx context.Context, node *storage.WorkflowNode, extras []hermod.Message, branch string) {
	// The caller releases msgs as soon as handleResults returns, so this
	// goroutine needs references of its own.
	owned := make([]hermod.Message, len(extras))
	for i, m := range extras {
		m.Retain()
		owned[i] = m
	}

	t.Wg.Go(func() {
		defer func() {
			for _, m := range owned {
				m.Release()
			}
		}()
		for _, m := range owned {
			t.walkFrom(ctx, node, m, branch)
		}
	})
}

// walkFrom runs one message through everything downstream of node in a
// traversal of its own, then folds that traversal's outcome into this one.
func (t *WorkflowTraversal) walkFrom(ctx context.Context, node *storage.WorkflowNode, msg hermod.Message, branch string) {
	child := Acquire(t.Registry, t.Eng, t.WorkflowID, t.NodeMap, t.Adj, t.NodeIndex,
		t.EdgeLabels, t.EdgeBreakpoints, t.InDegree, t.SinkNodeToIndex)

	child.handleResults(ctx, node, []hermod.Message{msg}, branch, nil)
	child.Wg.Wait()

	// Routed messages carry a reference each; moving them hands that reference to
	// this traversal, whose caller releases them after the writers have run.
	t.RoutedMu.Lock()
	child.RoutedMu.Lock()
	t.Routed = append(t.Routed, child.Routed...)
	child.Routed = child.Routed[:0]
	child.RoutedMu.Unlock()
	t.RoutedMu.Unlock()

	// Acknowledgement is decided on the parent traversal, so an item delivered,
	// failed or dead-lettered down here has to be visible there.
	if child.InlineDelivered.Load() {
		t.InlineDelivered.Store(true)
	}
	if child.InlineFailed.Load() {
		t.InlineFailed.Store(true)
	}
	if child.DeadLettered.Load() {
		t.DeadLettered.Store(true)
	}

	Release(child)
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
