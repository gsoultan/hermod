package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/governance"
	"github.com/user/hermod/pkg/engine/config"
	"github.com/user/hermod/pkg/engine/source"
	"github.com/user/hermod/pkg/engine/telemetry"
	"github.com/user/hermod/pkg/infra/schema"
	"go.opentelemetry.io/otel"
)

var tracer = otel.Tracer("hermod-engine")

// Engine orchestrates the data flow from Source to Sinks.
// It acts as a Facade, delegating complex tasks to internal components.
type Engine struct {
	source         hermod.Source
	sinks          []hermod.Sink
	buffer         hermod.Producer // Using Producer as a buffer
	logger         hermod.Logger
	config         config.Config
	sinkConfigs    []config.SinkConfig
	sourceConfig   config.SourceConfig
	deadLetterSink hermod.Sink
	router         RouterFunc
	validator      schema.Validator
	traceRecorder  hermod.TraceRecorder
	outboxStore    hermod.OutboxStorage
	dqScorer       *governance.Scorer

	workflowID string
	sourceID   string
	sinkIDs    []string
	sinkTypes  []string

	onStatusChange func(telemetry.StatusUpdate)

	// Internal state tracking (Facade components)
	statusTracker *telemetry.StatusTracker
	mu            sync.RWMutex
	runner        *Runner

	// Async Sink Writers
	sinkWriters []*sinkWriter

	// Checkpointing
	checkpointHandler func(ctx context.Context, sourceState map[string]string) error
	checkpointMu      sync.Mutex
	inCheckpoint      bool
	checkpoint        *CheckpointManager

	// In-flight message tracking for draining
	inFlightSem chan struct{}
	inFlightWg  sync.WaitGroup

	// Adaptive Throughput
	latencyAvg       time.Duration
	throughputTarget int
	lastPollAdjust   time.Time
	throttleDelay    time.Duration

	// stopMu protects hard-stop sequences
	stopMu  sync.Mutex
	stopped bool

	// Failure Simulation
	failureSimMu sync.RWMutex
	failUntil    time.Time

	// Safe Mode (divert to DLQ)
	safeModeMu sync.RWMutex
	safeMode   bool

	// Node-level backpressure
	nodeSemsMu sync.RWMutex
	nodeSems   map[string]chan struct{}
}

func NewEngine(source hermod.Source, sinks []hermod.Sink, buffer hermod.Producer) *Engine {
	e := &Engine{
		source:        source,
		sinks:         sinks,
		buffer:        buffer,
		logger:        telemetry.NewDefaultLogger(),
		config:        config.DefaultConfig(),
		statusTracker: telemetry.NewStatusTracker(),
		dqScorer:      governance.NewScorer(),
	}
	// initialize inflight semaphore using configured cap
	capSize := e.config.MaxInflight
	if capSize <= 0 {
		capSize = 128
	}
	e.inFlightSem = make(chan struct{}, capSize)
	e.runner = NewRunner(e)
	e.checkpoint = NewCheckpointManager(e, nil)
	return e
}

// SetConfig sets the configuration for the engine.
func (e *Engine) SetConfig(cfg config.Config) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.config = cfg

	// Resize inflight semaphore if empty
	if e.inFlightSem != nil && len(e.inFlightSem) == 0 {
		capSize := e.config.MaxInflight
		if capSize <= 0 {
			capSize = 128
		}
		e.inFlightSem = make(chan struct{}, capSize)
	}
}

// snapshotSinkWriters returns the current sink-writer slice header under stopMu.
// The slice is published once during startup (runner.Start); reading it under
// the lifecycle lock guarantees callers observe the fully-initialized slice and
// never race with that publish.
func (e *Engine) snapshotSinkWriters() []*sinkWriter {
	e.stopMu.Lock()
	defer e.stopMu.Unlock()
	return e.sinkWriters
}

func (e *Engine) SetSinkConfigs(configs []config.SinkConfig) {
	e.mu.Lock()
	e.sinkConfigs = configs
	e.mu.Unlock()

	// Update active sink writers
	for i, sw := range e.snapshotSinkWriters() {
		if i < len(configs) {
			sw.updateMu.Lock()
			sw.config = configs[i]
			batchSize := configs[i].BatchSize
			if batchSize < 1 {
				batchSize = 1
			}
			sw.currentBatchSize.Store(int64(batchSize))
			sw.batchTimeout = configs[i].BatchTimeout
			if sw.batchTimeout == 0 {
				sw.batchTimeout = 100 * time.Millisecond
			}
			sw.updateMu.Unlock()
		}
	}
}

func (e *Engine) GetSinkConfigs() []config.SinkConfig {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.sinkConfigs
}

func (e *Engine) UpdateSinkConfig(sinkID string, update func(*config.SinkConfig)) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for i := range e.sinkConfigs {
		if i >= len(e.sinkIDs) {
			break
		}
		if e.sinkIDs[i] == sinkID {
			update(&e.sinkConfigs[i])

			// Also update the writer if it exists
			for _, sw := range e.snapshotSinkWriters() {
				if sw.sinkID == sinkID {
					sw.updateMu.Lock()
					sw.config = e.sinkConfigs[i]
					batchSize := e.sinkConfigs[i].BatchSize
					if batchSize < 1 {
						batchSize = 1
					}
					sw.currentBatchSize.Store(int64(batchSize))
					sw.batchTimeout = e.sinkConfigs[i].BatchTimeout
					if sw.batchTimeout == 0 {
						sw.batchTimeout = 100 * time.Millisecond
					}
					sw.updateMu.Unlock()
				}
			}
			return
		}
	}
}

func (e *Engine) SetSourceConfig(cfg config.SourceConfig) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sourceConfig = cfg
}

func (e *Engine) SetLogger(l hermod.Logger) {
	e.logger = l
	if l != nil {
		if s, ok := e.source.(hermod.Loggable); ok {
			s.SetLogger(l)
		}
		for _, snk := range e.sinks {
			if sl, ok := snk.(hermod.Loggable); ok {
				sl.SetLogger(l)
			}
		}
		if dl, ok := e.deadLetterSink.(hermod.Loggable); ok {
			dl.SetLogger(l)
		}
	}
}

func (e *Engine) SetRouter(r RouterFunc) {
	e.router = r
}

func (e *Engine) SetValidator(v schema.Validator) {
	e.validator = v
}

func (e *Engine) SetTraceRecorder(tr hermod.TraceRecorder) {
	e.traceRecorder = tr
}

func (e *Engine) SetOnStatusChange(fn func(telemetry.StatusUpdate)) {
	e.onStatusChange = fn
}

func (e *Engine) SetCheckpointHandler(fn func(context.Context, map[string]string) error) {
	e.checkpointHandler = fn
	if e.checkpoint != nil {
		e.checkpoint.handler = fn
	}
}

func (e *Engine) SetWorkflowID(id string) {
	e.workflowID = id
}

func (e *Engine) SetSourceID(id string) {
	e.sourceID = id
}

func (e *Engine) SetSinkIDs(ids []string) {
	e.sinkIDs = ids
}

func (e *Engine) SetIDs(workflowID string, sourceID string, sinkIDs []string) {
	e.workflowID = workflowID
	e.sourceID = sourceID
	e.sinkIDs = sinkIDs
}

func (e *Engine) SetSinkTypes(types []string) {
	e.sinkTypes = types
}

func (e *Engine) SetDeadLetterSink(snk hermod.Sink) {
	e.deadLetterSink = snk
}

// GetSource returns the source configured for the engine.
func (e *Engine) GetSource() hermod.Source {
	return e.source
}

// GetSinks returns the sinks configured for the engine.
func (e *Engine) GetSinks() []hermod.Sink {
	return e.sinks
}

// DrainDLQ attempts to wrap the current source with a PrioritySource to drain DLQ messages.
func (e *Engine) DrainDLQ(ctx context.Context) error {
	if e.deadLetterSink == nil {
		return fmt.Errorf("no dead letter sink configured for this workflow")
	}

	dlqSource, ok := e.deadLetterSink.(hermod.Source)
	if !ok {
		return fmt.Errorf("dead letter sink does not support reading")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Check if already wrapped
	if _, ok := e.source.(*source.PrioritySource); ok {
		e.logger.Info("DLQ Drain requested: Source already prioritized", "workflow_id", e.workflowID)
		return nil
	}

	e.source = source.NewPrioritySource(dlqSource, e.source, e.logger)
	e.logger.Info("DLQ Drain initiated: PrioritySource active", "workflow_id", e.workflowID)
	return nil
}

func (e *Engine) setStatus(status string) {
	e.statusTracker.SetEngineStatus(status)
	e.notifyStatusChange()
}

func (e *Engine) setSourceStatus(status string) {
	e.statusTracker.SetSourceStatus(status)
	e.notifyStatusChange()
}

func (e *Engine) setSinkStatus(sinkID string, status string) {
	e.statusTracker.SetSinkStatus(sinkID, status)
	e.notifyStatusChange()
}

func (e *Engine) notifyStatusChange() {
	if e.onStatusChange != nil {
		e.onStatusChange(e.GetStatus())
	}
}

func (e *Engine) recordSourceActivity() {
	e.statusTracker.SetSourceStatus("running")
}

func (e *Engine) redactData(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	redacted := make(map[string]any)
	sensitiveFields := []string{"password", "secret", "token", "key", "email", "phone", "address"}
	for k, v := range data {
		isSensitive := false
		lowerK := strings.ToLower(k)
		for _, sf := range sensitiveFields {
			if strings.Contains(lowerK, sf) {
				isSensitive = true
				break
			}
		}

		if isSensitive {
			redacted[k] = "[REDACTED]"
		} else if m, ok := v.(map[string]any); ok {
			redacted[k] = e.redactData(m)
		} else {
			redacted[k] = v
		}
	}
	return redacted
}

// LastMsgTime returns the time of the last message received from the source.
func (e *Engine) LastMsgTime() time.Time {
	_, _, _, lastMsgTime, _, _, _ := e.statusTracker.GetStatus()
	return lastMsgTime
}

// GetStatus returns the current status of the engine.
func (e *Engine) GetStatus() telemetry.StatusUpdate {
	sourceStatus, sinkStatuses, engineStatus, _, processed, dlq, latency := e.statusTracker.GetStatus()

	update := telemetry.StatusUpdate{
		WorkflowID:      e.workflowID,
		EngineStatus:    engineStatus,
		SourceStatus:    sourceStatus,
		SourceID:        e.sourceID,
		SinkStatuses:    sinkStatuses,
		ProcessedCount:  processed,
		DeadLetterCount: dlq,
		AvgLatency:      latency,
	}

	update.SinkCBStatuses = make(map[string]string)
	update.SinkBufferFill = make(map[string]float64)
	for _, sw := range e.snapshotSinkWriters() {
		sw.cbMu.Lock()
		update.SinkCBStatuses[sw.sinkID] = sw.cbStatus
		sw.cbMu.Unlock()

		if sw.ch != nil {
			capacity := cap(sw.ch)
			if capacity > 0 {
				update.SinkBufferFill[sw.sinkID] = float64(len(sw.ch)) / float64(capacity)
			}
		}
	}

	nodeMetrics := e.statusTracker.GetNodeMetrics()
	if len(nodeMetrics) > 0 {
		update.NodeMetrics = nodeMetrics
	}
	nodeErrorMetrics := e.statusTracker.GetNodeErrorMetrics()
	if len(nodeErrorMetrics) > 0 {
		update.NodeErrorMetrics = nodeErrorMetrics
	}
	nodeSamples := e.statusTracker.GetNodeSamples()
	if len(nodeSamples) > 0 {
		update.NodeSamples = nodeSamples
	}
	edgeMetrics := e.statusTracker.GetEdgeMetrics()
	if len(edgeMetrics) > 0 {
		update.EdgeMetrics = edgeMetrics
	}

	if e.dqScorer != nil {
		update.AverageDQScore = e.dqScorer.GetAverageScore(e.workflowID) * 100
	}

	return update
}

// Start begins the data transfer process.
func (e *Engine) Start(ctx context.Context) error {
	return e.runner.Start(ctx)
}

// HardStop attempts to forcefully and quickly stop the engine.
func (e *Engine) HardStop() {
	e.stopMu.Lock()
	if e.stopped {
		e.stopMu.Unlock()
		return
	}
	e.stopped = true
	e.stopMu.Unlock()

	if e.runner != nil && e.runner.cancel != nil {
		e.runner.cancel()
	}

	// Close components
	if e.buffer != nil {
		if closer, ok := e.buffer.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}
	if e.source != nil {
		_ = e.source.Close()
	}
	for _, snk := range e.sinks {
		if snk != nil {
			_ = snk.Close()
		}
	}
}

func (e *Engine) isFailing() bool {
	e.failureSimMu.RLock()
	defer e.failureSimMu.RUnlock()
	return time.Now().Before(e.failUntil)
}

func (e *Engine) SetSafeMode(enabled bool) {
	e.safeModeMu.Lock()
	defer e.safeModeMu.Unlock()
	e.safeMode = enabled
	if enabled {
		e.logger.Warn("Engine ENTERED SAFE MODE: all traffic diverted to DLQ", "workflow_id", e.workflowID)
	} else {
		e.logger.Info("Engine EXITED SAFE MODE", "workflow_id", e.workflowID)
	}
}

func (e *Engine) IsSafeMode() bool {
	e.safeModeMu.RLock()
	defer e.safeModeMu.RUnlock()
	return e.safeMode
}

func (e *Engine) DetectAnomaly(duration time.Duration) bool {
	avg := e.statusTracker.GetAvgLatency()
	if avg == 0 {
		return false
	}
	// Anomaly: 5x the average AND at least 500ms (to avoid noise on very fast pipelines)
	return duration > avg*5 && duration > 500*time.Millisecond
}

// AcquireNode attempts to acquire a concurrency slot for a specific node.
// This implements node-level backpressure.
func (e *Engine) AcquireNode(ctx context.Context, nodeID string) error {
	e.nodeSemsMu.RLock()
	sem, ok := e.nodeSems[nodeID]
	e.nodeSemsMu.RUnlock()

	if !ok {
		e.nodeSemsMu.Lock()
		if e.nodeSems == nil {
			e.nodeSems = make(map[string]chan struct{})
		}
		sem, ok = e.nodeSems[nodeID]
		if !ok {
			// Default node capacity from config or fallback
			limit := e.config.MaxInflight / 2
			if limit < 16 {
				limit = 16
			}
			sem = make(chan struct{}, limit)
			e.nodeSems[nodeID] = sem
		}
		e.nodeSemsMu.Unlock()
	}

	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ReleaseNode releases a concurrency slot for a specific node.
func (e *Engine) ReleaseNode(nodeID string) {
	e.nodeSemsMu.RLock()
	sem, ok := e.nodeSems[nodeID]
	e.nodeSemsMu.RUnlock()
	if ok {
		select {
		case <-sem:
		default:
			// Should not happen if AcquireNode was called
		}
	}
}

func (e *Engine) SimulateFailure(duration time.Duration) {
	e.failureSimMu.Lock()
	defer e.failureSimMu.Unlock()
	e.failUntil = time.Now().Add(duration)
	if e.logger != nil {
		e.logger.Warn("Engine: simulated failure injected", "duration", duration)
	}
}

func (e *Engine) Checkpoint(ctx context.Context) error {
	if e.checkpoint == nil {
		return nil
	}
	return e.checkpoint.Checkpoint(ctx)
}

func (e *Engine) GetConcurrency() int {
	return e.config.MaxInflight
}

func (e *Engine) UpdateConcurrency(n int) {
	if n < 1 {
		n = 1
	}
	e.config.MaxInflight = n
	e.logger.Info("Engine: concurrency updated", "workflow_id", e.workflowID, "new_concurrency", n)
}
