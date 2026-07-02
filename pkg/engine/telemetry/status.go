package telemetry

import (
	"sync"
	"sync/atomic"
	"time"
)

type StatusTracker struct {
	mu           sync.RWMutex
	sourceStatus string
	sinkStatuses map[string]string
	engineStatus string

	lastMsgTime       atomic.Int64 // UnixNano
	processedMessages atomic.Uint64
	deadLetterCount   atomic.Uint64

	nodeMetrics      sync.Map // string -> *atomic.Uint64
	nodeErrorMetrics sync.Map // string -> *atomic.Uint64
	nodeSamples      sync.Map // string -> any
	edgeMetrics      sync.Map // string -> *atomic.Uint64

	latencyAvg  atomic.Int64 // Duration in ns
	mpsCounter  atomic.Uint64
	lastMps     atomic.Uint64
	lastMpsTime atomic.Int64 // Unix
}

func NewStatusTracker() *StatusTracker {
	return &StatusTracker{
		sinkStatuses: make(map[string]string),
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
	s.processedMessages.Add(1)
	s.mpsCounter.Add(1)
	s.lastMsgTime.Store(time.Now().UnixNano())
}

func (s *StatusTracker) IncDeadLetter() {
	s.deadLetterCount.Add(1)
}

func (s *StatusTracker) getOrCreateAtomic(m *sync.Map, key string) *atomic.Uint64 {
	if val, ok := m.Load(key); ok {
		return val.(*atomic.Uint64)
	}
	newAtomic := new(atomic.Uint64)
	val, loaded := m.LoadOrStore(key, newAtomic)
	if loaded {
		return val.(*atomic.Uint64)
	}
	return newAtomic
}

func (s *StatusTracker) UpdateNodeMetric(nodeID string, count uint64) {
	s.getOrCreateAtomic(&s.nodeMetrics, nodeID).Add(count)
}

func (s *StatusTracker) UpdateNodeErrorMetric(nodeID string, count uint64) {
	s.getOrCreateAtomic(&s.nodeErrorMetrics, nodeID).Add(count)
}

func (s *StatusTracker) UpdateEdgeMetric(sourceNodeID, targetNodeID string, count uint64) {
	key := sourceNodeID + "->" + targetNodeID
	s.getOrCreateAtomic(&s.edgeMetrics, key).Add(count)
}

func (s *StatusTracker) UpdateLatency(d time.Duration) {
	for {
		old := s.latencyAvg.Load()
		var next int64
		if old == 0 {
			next = int64(d)
		} else {
			// EMA with alpha=0.1
			next = (old*9 + int64(d)) / 10
		}
		if s.latencyAvg.CompareAndSwap(old, next) {
			break
		}
	}
}

func (s *StatusTracker) GetAvgLatency() time.Duration {
	return time.Duration(s.latencyAvg.Load())
}

func (s *StatusTracker) GetLastMsgTime() time.Time {
	nanos := s.lastMsgTime.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

func (s *StatusTracker) GetStatus() (sourceStatus string, sinkStatuses map[string]string, engineStatus string, lastMsgTime time.Time, processed uint64, dlq uint64, latency time.Duration) {
	s.mu.RLock()
	sourceStatus = s.sourceStatus
	engineStatus = s.engineStatus
	// Copy sink statuses
	sinkStatuses = make(map[string]string, len(s.sinkStatuses))
	for k, v := range s.sinkStatuses {
		sinkStatuses[k] = v
	}
	s.mu.RUnlock()

	lastMsgTime = s.GetLastMsgTime()
	processed = s.processedMessages.Load()
	dlq = s.deadLetterCount.Load()
	latency = s.GetAvgLatency()

	return
}

func (s *StatusTracker) GetMPS() float64 {
	now := time.Now().Unix()
	lastTime := s.lastMpsTime.Load()

	if now > lastTime {
		// Time has advanced, rotate buckets
		count := s.mpsCounter.Swap(0)
		s.lastMps.Store(count)
		s.lastMpsTime.Store(now)

		// If more than 1 second passed since last check, the gap had 0 throughput
		if now > lastTime+1 && lastTime > 0 {
			s.lastMps.Store(0)
			return 0
		}
		return float64(count)
	}

	// Still within the same second, return last completed second's count
	return float64(s.lastMps.Load())
}

func (s *StatusTracker) GetNodeMetrics() map[string]uint64 {
	res := make(map[string]uint64)
	s.nodeMetrics.Range(func(key, value any) bool {
		res[key.(string)] = value.(*atomic.Uint64).Load()
		return true
	})
	return res
}

func (s *StatusTracker) GetNodeErrorMetrics() map[string]uint64 {
	res := make(map[string]uint64)
	s.nodeErrorMetrics.Range(func(key, value any) bool {
		res[key.(string)] = value.(*atomic.Uint64).Load()
		return true
	})
	return res
}

func (s *StatusTracker) GetNodeSamples() map[string]any {
	res := make(map[string]any)
	s.nodeSamples.Range(func(key, value any) bool {
		res[key.(string)] = value
		return true
	})
	return res
}

func (s *StatusTracker) GetEdgeMetrics() map[string]uint64 {
	res := make(map[string]uint64)
	s.edgeMetrics.Range(func(key, value any) bool {
		res[key.(string)] = value.(*atomic.Uint64).Load()
		return true
	})
	return res
}

func (s *StatusTracker) UpdateNodeSample(nodeID string, sample any) {
	s.nodeSamples.Store(nodeID, sample)
}
