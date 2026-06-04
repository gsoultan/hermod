package telemetry

import (
	"sync"
	"time"
)

type StatusTracker struct {
	mu                sync.RWMutex
	sourceStatus      string
	sinkStatuses      map[string]string
	engineStatus      string
	lastMsgTime       time.Time
	processedMessages uint64
	deadLetterCount   uint64
	nodeMetrics       map[string]uint64
	nodeErrorMetrics  map[string]uint64
	nodeSamples       map[string]any
	edgeMetrics       map[string]uint64
	latencyAvg        time.Duration
}

func NewStatusTracker() *StatusTracker {
	return &StatusTracker{
		sinkStatuses:     make(map[string]string),
		nodeMetrics:      make(map[string]uint64),
		nodeErrorMetrics: make(map[string]uint64),
		nodeSamples:      make(map[string]any),
		edgeMetrics:      make(map[string]uint64),
	}
}

func (s *StatusTracker) SetSourceStatus(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sourceStatus = status
}

func (s *StatusTracker) SetSinkStatus(sinkID, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sinkStatuses[sinkID] = status
}

func (s *StatusTracker) SetEngineStatus(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.engineStatus = status
}

func (s *StatusTracker) IncProcessed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.processedMessages++
	s.lastMsgTime = time.Now()
}

func (s *StatusTracker) IncDeadLetter() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deadLetterCount++
}

func (s *StatusTracker) UpdateNodeMetric(nodeID string, count uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodeMetrics[nodeID] += count
}

func (s *StatusTracker) UpdateNodeErrorMetric(nodeID string, count uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodeErrorMetrics[nodeID] += count
}

func (s *StatusTracker) UpdateEdgeMetric(sourceNodeID, targetNodeID string, count uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sourceNodeID + "->" + targetNodeID
	s.edgeMetrics[key] += count
}

func (s *StatusTracker) UpdateLatency(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latencyAvg == 0 {
		s.latencyAvg = d
	} else {
		s.latencyAvg = (s.latencyAvg*9 + d) / 10
	}
}

func (s *StatusTracker) GetAvgLatency() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latencyAvg
}

func (s *StatusTracker) GetLastMsgTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastMsgTime
}

func (s *StatusTracker) GetStatus() (sourceStatus string, sinkStatuses map[string]string, engineStatus string, lastMsgTime time.Time, processed uint64, dlq uint64, latency time.Duration) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Copy sink statuses
	snk := make(map[string]string, len(s.sinkStatuses))
	for k, v := range s.sinkStatuses {
		snk[k] = v
	}

	return s.sourceStatus, snk, s.engineStatus, s.lastMsgTime, s.processedMessages, s.deadLetterCount, s.latencyAvg
}

func (s *StatusTracker) GetNodeMetrics() map[string]uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make(map[string]uint64, len(s.nodeMetrics))
	for k, v := range s.nodeMetrics {
		res[k] = v
	}
	return res
}

func (s *StatusTracker) GetNodeErrorMetrics() map[string]uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make(map[string]uint64, len(s.nodeErrorMetrics))
	for k, v := range s.nodeErrorMetrics {
		res[k] = v
	}
	return res
}

func (s *StatusTracker) GetNodeSamples() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make(map[string]any, len(s.nodeSamples))
	for k, v := range s.nodeSamples {
		res[k] = v
	}
	return res
}

func (s *StatusTracker) GetEdgeMetrics() map[string]uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make(map[string]uint64, len(s.edgeMetrics))
	for k, v := range s.edgeMetrics {
		res[k] = v
	}
	return res
}

func (s *StatusTracker) UpdateNodeSample(nodeID string, sample any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodeSamples[nodeID] = sample
}
