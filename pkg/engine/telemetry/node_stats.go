package telemetry

import (
	"sync"
	"sync/atomic"
	"time"
)

// NodeStats is one node's live counters.
//
// The workflow router resolves a handle per node once, when it is built, and
// updates it on every message. The counters are the same ones GetNodeMetrics,
// GetNodeErrorMetrics and GetNodeLatencies report, so the per-message path is
// a few atomic operations: no map lookup, no key built, no allocation.
type NodeStats struct {
	processed *atomic.Uint64
	errors    *atomic.Uint64
	latency   *atomic.Int64 // moving average, ns; 0 = never observed

	// nextSample is the UnixNano before which no further payload sample may
	// be taken for this node; see ClaimSample.
	nextSample atomic.Int64
}

// NodeStats returns the handle for nodeID, creating it on first use. Every
// call for the same node returns the same handle.
func (s *StatusTracker) NodeStats(nodeID string) *NodeStats {
	if n, ok := loadTyped[NodeStats](&s.nodeStats, nodeID); ok {
		return n
	}
	return loadOrStoreTyped(&s.nodeStats, nodeID, &NodeStats{
		processed: s.getOrCreateAtomic(&s.nodeMetrics, nodeID),
		errors:    s.getOrCreateAtomic(&s.nodeErrorMetrics, nodeID),
		latency:   s.getOrCreateLatency(nodeID),
	})
}

func (s *StatusTracker) getOrCreateLatency(nodeID string) *atomic.Int64 {
	if v, ok := loadTyped[atomic.Int64](&s.nodeLatency, nodeID); ok {
		return v
	}
	return loadOrStoreTyped(&s.nodeLatency, nodeID, new(atomic.Int64))
}

// loadTyped loads key from m as a *T.
func loadTyped[T any](m *sync.Map, key string) (*T, bool) {
	v, ok := m.Load(key)
	if !ok {
		return nil, false
	}
	t, ok := v.(*T)
	return t, ok
}

// loadOrStoreTyped stores fresh under key unless a value is already there, and
// returns whichever value the map holds. Only this package writes these maps,
// so a value of another type cannot be present; if it ever were, fresh is
// returned rather than panicking.
func loadOrStoreTyped[T any](m *sync.Map, key string, fresh *T) *T {
	v, _ := m.LoadOrStore(key, fresh)
	if t, ok := v.(*T); ok {
		return t
	}
	return fresh
}

// Count records one message through the node without a duration — for a node
// that does no work of its own, such as a source.
func (n *NodeStats) Count() {
	n.processed.Add(1)
}

// Observe records one run of the node: it counts the message, counts it as an
// error when failed, and folds d into the node's moving-average latency.
func (n *NodeStats) Observe(d time.Duration, failed bool) {
	n.processed.Add(1)
	if failed {
		n.errors.Add(1)
	}
	// 0 means "never observed", so a run too fast to measure counts as 1ns.
	sample := max(int64(d), 1)
	for {
		old := n.latency.Load()
		next := sample
		if old != 0 {
			// EMA, alpha = 0.1: the same smoothing as the workflow average.
			next = (old*9 + sample) / 10
		}
		if n.latency.CompareAndSwap(old, next) {
			return
		}
	}
}

// ClaimSample reports whether the caller may take a payload sample for this
// node now. At most one caller is granted per interval, however many workers
// race for it: a sample is a full payload copy, and each new one re-fires the
// editor's previews of everything downstream.
func (n *NodeStats) ClaimSample(nowNanos int64, every time.Duration) bool {
	next := n.nextSample.Load()
	if nowNanos < next {
		return false
	}
	return n.nextSample.CompareAndSwap(next, nowNanos+int64(every))
}

// EdgeCounter returns the message counter for the edge source -> target, the
// same counter UpdateEdgeMetric adds to and GetEdgeMetrics reports, so a
// caller can resolve it once and add to it without building the key.
func (s *StatusTracker) EdgeCounter(sourceNodeID, targetNodeID string) *atomic.Uint64 {
	return s.getOrCreateAtomic(&s.edgeMetrics, sourceNodeID+"->"+targetNodeID)
}

// GetNodeLatencies returns each node's moving-average run time. Nodes that
// have not run are absent rather than reported as instantaneous.
func (s *StatusTracker) GetNodeLatencies() map[string]time.Duration {
	res := make(map[string]time.Duration)
	s.nodeLatency.Range(func(key, value any) bool {
		id, okKey := key.(string)
		v, okVal := value.(*atomic.Int64)
		if okKey && okVal {
			if ns := v.Load(); ns > 0 {
				res[id] = time.Duration(ns)
			}
		}
		return true
	})
	return res
}
