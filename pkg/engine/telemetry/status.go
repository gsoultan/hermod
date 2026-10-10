package telemetry

import (
	"maps"
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
	lag               atomic.Uint64

	nodeMetrics      sync.Map // string -> *atomic.Uint64
	nodeErrorMetrics sync.Map // string -> *atomic.Uint64
	nodeSamples      sync.Map // string -> any
	edgeMetrics      sync.Map // string -> *atomic.Uint64
	nodeLatency      sync.Map // string -> *atomic.Int64 (moving average, ns)
	nodeStats        sync.Map // string -> *NodeStats

	latencyAvg atomic.Int64 // Duration in ns

	// mps is the last throughput sample: the processed count at a moment and
	// the rate over the window that ended there. GetMPS derives the rate from
	// the monotonically increasing processedMessages, so reading never
	// consumes anything; see GetMPS.
	mps atomic.Pointer[mpsSample]

	// now is the clock GetMPS reads. nil means time.Now; tests set it.
	now func() time.Time
}

func (s *StatusTracker) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
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
	s.lastMsgTime.Store(time.Now().UnixNano())
}

// IncDeadLetter records one dead-lettered message and returns the new total.
// The caller needs the count to decide whether this message is the one that
// crossed the alert threshold, and reading it back separately would race
// another dead-letter and either double-report or miss the crossing.
func (s *StatusTracker) IncDeadLetter() uint64 {
	return s.deadLetterCount.Add(1)
}

func (s *StatusTracker) SetLag(count uint64) {
	s.lag.Store(count)
}

func (s *StatusTracker) GetLag() uint64 {
	return s.lag.Load()
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

func (s *StatusTracker) GetStatus() (sourceStatus string, sinkStatuses map[string]string, engineStatus string, lastMsgTime time.Time, processed uint64, dlq uint64, latency time.Duration, lag uint64) {
	s.mu.RLock()
	sourceStatus = s.sourceStatus
	engineStatus = s.engineStatus
	// Copy sink statuses
	sinkStatuses = make(map[string]string, len(s.sinkStatuses))
	maps.Copy(sinkStatuses, s.sinkStatuses)
	s.mu.RUnlock()

	lastMsgTime = s.GetLastMsgTime()
	processed = s.processedMessages.Load()
	dlq = s.deadLetterCount.Load()
	latency = s.GetAvgLatency()
	lag = s.lag.Load()

	return
}

// mpsSample is one throughput observation. It is immutable once published.
type mpsSample struct {
	at    int64   // UnixNano the sample was taken
	count uint64  // processedMessages at that moment
	rate  float64 // messages/second over the window that ended at `at`
}

// mpsWindow is the shortest window a rate is computed over.
const mpsWindow = time.Second

// GetMPS reports messages per second over the most recently completed window
// of at least one second.
//
// It is read from several goroutines (the status listener, the flusher, the
// dashboard sampler, the optimizer), so it must not consume what it reads.
// The rate is the difference of the monotonic processed count between two
// samples. Whichever reader first finds the current sample a window old
// publishes the next one with a compare-and-swap; every other reader — in the
// same window or racing for the same boundary — returns the rate already
// published. A gap longer than a window is averaged over, not reported as 0.
func (s *StatusTracker) GetMPS() float64 {
	now := s.clock().UnixNano()
	count := s.processedMessages.Load()

	prev := s.mps.Load()
	if prev == nil {
		// First observation: nothing to measure a window against yet.
		if s.mps.CompareAndSwap(nil, &mpsSample{at: now, count: count}) {
			return 0
		}
		return s.mps.Load().rate
	}

	elapsed := now - prev.at
	if elapsed < int64(mpsWindow) {
		return prev.rate
	}

	var delta uint64
	if count > prev.count {
		delta = count - prev.count
	}
	next := &mpsSample{
		at:    now,
		count: count,
		rate:  float64(delta) / (float64(elapsed) / float64(time.Second)),
	}
	if s.mps.CompareAndSwap(prev, next) {
		return next.rate
	}
	// Another reader published the sample for this boundary first.
	return s.mps.Load().rate
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

// SetEngineStatusUnless publishes status unless the engine is already in one of
// the excluded states, and reports whether it wrote. The decision and the write
// happen under one lock, which is the entire point: callers that read the
// status, decide, and then write it back hold no lock across the gap, and
// anything that writes in between is silently overwritten.
//
// The background health check is exactly that caller (see runner.go). A wedged
// sink still answers Ping, so the check concludes the pipeline is fine and
// publishes "running" -- and if the stall watchdog set "stalled" after the
// check read the status but before it wrote, the stall the supervisor had
// already been told about disappeared from the status the UI reads. Narrow
// enough that only a loaded machine hit it, which is how it reached CI as an
// occasional failure of stall_watchdog_test.go rather than a reproducible one.
func (s *StatusTracker) SetEngineStatusUnless(status string, unless ...string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range unless {
		if s.engineStatus == u {
			return false
		}
	}
	s.engineStatus = status
	return true
}
