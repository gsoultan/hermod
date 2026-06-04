package engine

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"runtime"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
)

func (e *Engine) RecordTraceStep(ctx context.Context, msg hermod.Message, nodeID string, start time.Time, before map[string]any, err error) {
	if e.traceRecorder == nil || e.config.TraceSampleRate <= 0 {
		return
	}

	if msg == nil {
		return
	}

	// Use deterministic sampling based on Message ID
	if e.config.TraceSampleRate < 1.0 {
		h := fnv.New32a()
		_, _ = h.Write([]byte(msg.ID()))
		// Normalize to 0.0 - 1.0
		sampleValue := float64(h.Sum32()) / float64(0xFFFFFFFF)
		if sampleValue > e.config.TraceSampleRate {
			return
		}
	}

	// Prepare trace data. Use consistent representation from MarshalJSON for CDC and non-CDC.
	var after map[string]any
	if dm, ok := msg.(*message.DefaultMessage); ok {
		msgJSON, _ := dm.MarshalJSON()
		_ = json.Unmarshal(msgJSON, &after)
	} else {
		// Fallback for other message types
		baseData := msg.Data()
		if baseData != nil {
			after = make(map[string]any, len(baseData))
			for k, v := range baseData {
				after[k] = v
			}
		} else {
			after = make(map[string]any)
		}
	}

	// Lineage Tracking: Append nodeID to message metadata lineage
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

	e.traceRecorder.RecordStep(ctx, e.workflowID, msg.ID(), step)
}

func (e *Engine) UpdateNodeMetric(nodeID string, count uint64) {
	e.statusTracker.UpdateNodeMetric(nodeID, count)
}

func (e *Engine) UpdateNodeErrorMetric(nodeID string, count uint64) {
	e.statusTracker.UpdateNodeErrorMetric(nodeID, count)
}

func (e *Engine) UpdateNodeSample(nodeID string, data map[string]any) {
	// Deep copy sample to avoid mutation
	sampleJSON, _ := json.Marshal(data)
	var sampleCopy map[string]any
	_ = json.Unmarshal(sampleJSON, &sampleCopy)
	e.statusTracker.UpdateNodeSample(nodeID, sampleCopy)
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

	_, _, _, _, _, _, latencyAvg := e.statusTracker.GetStatus()

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
