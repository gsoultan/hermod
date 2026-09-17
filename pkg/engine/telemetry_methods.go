package engine

import (
	"context"
	"hash/fnv"
	"runtime"
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

func (e *Engine) recordTraceStep(ctx context.Context, msg hermod.Message, nodeID string, start time.Time, before, after map[string]any, err error) {
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

	// Lineage Tracking
	lineage := msg.Metadata()["_hermod_lineage"]
	if lineage == "" {
		lineage = nodeID
	} else {
		lineage += " -> " + nodeID
	}
	msg.SetMetadata("_hermod_lineage", lineage)

	step := hermod.TraceStep{
		NodeID:    nodeID,
		Timestamp: start,
		Duration:  time.Since(start),
		Before:    before,
		After:     after,
		Lineage:   lineage,
	}
	if err != nil {
		step.Error = err.Error()
	}

	// Use a background context for recording to ensure it completes even if the
	// request context is cancelled (e.g. message finished processing).
	recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	msg.Retain()
	go func() {
		defer cancel()
		defer msg.Release()
		e.traceRecorder.RecordStep(recordCtx, e.workflowID, msg.ID(), step)
	}()
}

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
