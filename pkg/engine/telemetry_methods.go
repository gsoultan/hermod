package engine

import (
	"context"
	"hash/fnv"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/gsoultan/hermod"
)

// WillTrace reports whether a step recorded for this message would be kept.
//
// Callers that have to build a payload snapshot *before* the work they are
// tracing need this: without it they pay a ToMap() per message on a workflow
// whose tracing is off, which is the common case.
func (e *Engine) WillTrace(msg hermod.Message) bool {
	if e.traceRecorder == nil || e.config.TraceSampleRate <= 0 || msg == nil {
		return false
	}
	if e.config.TraceSampleRate >= 1.0 {
		return true
	}
	// Deterministic sampling based on message ID: a sampled message is traced
	// at every step or at none, never half a trace.
	h := fnv.New32a()
	_, _ = h.Write([]byte(msg.ID()))
	return float64(h.Sum32())/float64(0xFFFFFFFF) <= e.config.TraceSampleRate
}

func (e *Engine) RecordTraceStep(ctx context.Context, msg hermod.Message, nodeID string, start time.Time, before map[string]any, err error) {
	e.recordTraceStep(ctx, msg, nodeID, start, before, nil, err)
}

// RecordTraceStepSnapshot records a step whose payload was captured earlier,
// for a caller whose work mutates the message it is tracing.
//
// The router is why this exists. For a node-graph workflow the engine's router
// *is* the traversal — setupWorkflowRouter walks the whole DAG inside it — so a
// snapshot taken when it returns is the pipeline's output, while the step's
// timestamp is when routing began. The viewer orders steps by timestamp and
// rebuilds each "before" from the previous "after", so the two together put the
// final payload between the message arriving and the first node running.
//
// Pass the snapshot taken before the work started and the halves agree. Guard
// the capture with WillTrace so an untraced workflow pays nothing.
func (e *Engine) RecordTraceStepSnapshot(ctx context.Context, msg hermod.Message, nodeID string, start time.Time, before, after map[string]any, err error) {
	e.recordTraceStep(ctx, msg, nodeID, start, before, after, err)
}

// RecordCompletedTraceStep records a step whose payload is the *result* of the
// work, stamped with the moment that work finished rather than the moment it
// began. Duration still spans the whole of it.
//
// This is for a caller whose work records trace steps of its own. A node step
// carries the node's output, but a `pipeline` node's steps each record again
// under their transType at their own, later timestamps — so a start-stamped
// parent sorted ahead of its own children while holding what they produced.
// Because GetMessageTrace rebuilds each "before" from the previous step's
// "after", the first child was then shown as having been handed a field it had
// not computed yet, and its own "after" read as having deleted it.
//
// The same disagreement in the router is what RecordTraceStepSnapshot fixes,
// from the other side: the router is not a transformation, so it moves its
// payload back to its timestamp. A node *is* one, so it moves its timestamp
// forward to its payload.
func (e *Engine) RecordCompletedTraceStep(ctx context.Context, msg hermod.Message, nodeID string, start, done time.Time, before map[string]any, err error) {
	e.recordTraceStepAt(ctx, msg, nodeID, start, done, before, nil, err)
}

func (e *Engine) recordTraceStep(ctx context.Context, msg hermod.Message, nodeID string, start time.Time, before, after map[string]any, err error) {
	e.recordTraceStepAt(ctx, msg, nodeID, start, start, before, after, err)
}

func (e *Engine) recordTraceStepAt(ctx context.Context, msg hermod.Message, nodeID string, start, at time.Time, before, after map[string]any, err error) {
	if !e.WillTrace(msg) {
		return
	}

	if after == nil {
		// Optimization: use cached snapshot from context if available, otherwise ToMap()
		if last, ok := ctx.Value(hermod.LastTraceSnapshotKey).(*map[string]any); ok && *last != nil {
			after = *last
		} else {
			after = msg.ToMap()
		}
	}

	// Lineage Tracking. Read the one key rather than cloning the whole
	// metadata map to index it once.
	lineage, _ := hermod.MetadataValue(msg, "_hermod_lineage")
	if lineage == "" {
		lineage = nodeID
	} else {
		lineage += " -> " + nodeID
	}
	msg.SetMetadata("_hermod_lineage", lineage)

	step := hermod.TraceStep{
		NodeID:    nodeID,
		Timestamp: at,
		Duration:  time.Since(start),
		Before:    before,
		After:     after,
		Lineage:   lineage,
	}
	if err != nil {
		step.Error = err.Error()
	}

	// One goroutine and one five-second timer, per node, per message, with
	// nothing bounding either. That is free at the default TraceSampleRate of
	// 0 because WillTrace returned above — but tracing gets switched on
	// exactly when a workflow is busy and someone is trying to understand it,
	// and a five-node graph at 50k msgs/s is 250k goroutines and 250k timers
	// outstanding against a recorder writing to PostgreSQL.
	//
	// So the recorder gets a fixed number of slots and a step that cannot get
	// one is dropped. Trace fidelity is the right thing to lose here: the
	// alternative is the process. internal/engine/registry made the same call
	// for its own trace recording, with the same shape.
	if !e.acquireTraceSlot() {
		traceStepsDropped.Add(1)
		return
	}

	msg.Retain()
	go func() {
		defer e.releaseTraceSlot()
		defer msg.Release()
		// Detached from the caller's context so recording still completes when
		// the message finishes processing, but bounded so a wedged recorder
		// cannot hold a slot for ever. Armed here rather than above so a
		// dropped step never creates a timer at all.
		recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		e.traceRecorder.RecordStep(recordCtx, e.workflowID, msg.ID(), step)
	}()
}

// maxConcurrentTraceRecords is how many trace steps may be in flight at once.
//
// Sized for a recorder that keeps up: steps are handed over in microseconds
// and a healthy recorder never fills this. It only binds when the recorder has
// stalled, which is the case it exists for.
const maxConcurrentTraceRecords = 256

// traceStepsDropped counts steps discarded because every slot was busy. A
// non-zero value means traces have holes and the recorder is the bottleneck —
// it is a capacity signal, not a bug in the pipeline being traced.
var traceStepsDropped atomic.Int64

// TraceStepsDroppedCount reports how many trace steps were discarded because
// the recorder could not keep up.
func TraceStepsDroppedCount() int64 { return traceStepsDropped.Load() }

// ResetTraceStepsDroppedCount zeroes the counter, for tests.
func ResetTraceStepsDroppedCount() { traceStepsDropped.Store(0) }

func (e *Engine) acquireTraceSlot() bool {
	e.traceSlotsOnce.Do(func() {
		e.traceSlots = make(chan struct{}, maxConcurrentTraceRecords)
	})
	select {
	case e.traceSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (e *Engine) releaseTraceSlot() { <-e.traceSlots }

func (e *Engine) UpdateNodeMetric(nodeID string, count uint64) {
	e.statusTracker.UpdateNodeMetric(nodeID, count)
}

func (e *Engine) UpdateNodeErrorMetric(nodeID string, count uint64) {
	e.statusTracker.UpdateNodeErrorMetric(nodeID, count)
}

// UpdateNodeSample stores the latest payload sample for a node. Callers must
// pass an independent map (e.g. produced by Registry.getConsistentData) that is
// not mutated afterwards; the value is stored as-is to avoid an extra
// full-payload JSON round-trip on every message.
func (e *Engine) UpdateNodeSample(nodeID string, data map[string]any) {
	e.statusTracker.UpdateNodeSample(nodeID, data)
}

func (e *Engine) UpdateEdgeMetric(sourceNodeID string, targetNodeID string, count uint64) {
	e.statusTracker.UpdateEdgeMetric(sourceNodeID, targetNodeID, count)
}

func (e *Engine) adaptiveThrottle(ctx context.Context, duration time.Duration) {
	if !e.config.AdaptiveThroughput {
		return
	}

	e.statusTracker.UpdateLatency(duration)

	// Adjust polling interval every 5s based on latency and memory
	if time.Since(e.lastPollAdjust) < 5*time.Second {
		return
	}
	e.lastPollAdjust = time.Now()

	// Check memory pressure
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	memoryPressure := e.config.MaxMemoryMB > 0 && mem.Alloc > e.config.MaxMemoryMB*1024*1024

	_, _, _, _, _, _, latencyAvg, _ := e.statusTracker.GetStatus()

	// If latency is high (>500ms) or memory is high, slow down polling
	if latencyAvg > 500*time.Millisecond || memoryPressure {
		e.mu.Lock()
		e.throttleDelay += 100 * time.Millisecond
		if e.throttleDelay > 10*time.Second {
			e.throttleDelay = 10 * time.Second
		}
		delay := e.throttleDelay
		e.mu.Unlock()

		reason := "high latency"
		if memoryPressure {
			reason = "memory pressure"
		}
		e.logger.Warn("Adaptive throughput: throttling ingestion",
			"reason", reason,
			"avg_latency", latencyAvg.String(),
			"mem_alloc_mb", mem.Alloc/1024/1024,
			"throttle_delay", delay.String(),
			"workflow_id", e.workflowID)
	} else if latencyAvg < 100*time.Millisecond {
		e.mu.Lock()
		if e.throttleDelay > 0 {
			e.throttleDelay -= 100 * time.Millisecond
			if e.throttleDelay < 0 {
				e.throttleDelay = 0
			}
		}
		e.mu.Unlock()
	}
}
