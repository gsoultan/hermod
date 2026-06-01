package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/governance"
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
	config         Config
	sinkConfigs    []SinkConfig
	sourceConfig   SourceConfig
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

	onStatusChange func(StatusUpdate)

	// Internal state tracking (Facade components)
	statusTracker *StatusTracker
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
}

func NewEngine(source hermod.Source, sinks []hermod.Sink, buffer hermod.Producer) *Engine {
	e := &Engine{
		source:        source,
		sinks:         sinks,
		buffer:        buffer,
		logger:        NewDefaultLogger(),
		config:        DefaultConfig(),
		statusTracker: NewStatusTracker(),
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
func (e *Engine) SetConfig(config Config) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.config = config

	// Resize inflight semaphore if empty
	if e.inFlightSem != nil && len(e.inFlightSem) == 0 {
		capSize := e.config.MaxInflight
		if capSize <= 0 {
			capSize = 128
		}
		e.inFlightSem = make(chan struct{}, capSize)
	}
}

func (e *Engine) SetSinkConfigs(configs []SinkConfig) {
	e.mu.Lock()
	e.sinkConfigs = configs
	e.mu.Unlock()

	// Update active sink writers
	for i, sw := range e.sinkWriters {
		if i < len(configs) {
			sw.updateMu.Lock()
			sw.config = configs[i]
			sw.currentBatchSize = configs[i].BatchSize
			if sw.currentBatchSize < 1 {
				sw.currentBatchSize = 1
			}
			sw.batchTimeout = configs[i].BatchTimeout
			if sw.batchTimeout == 0 {
				sw.batchTimeout = 100 * time.Millisecond
			}
			sw.updateMu.Unlock()
		}
	}
}

func (e *Engine) GetSinkConfigs() []SinkConfig {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.sinkConfigs
}

func (e *Engine) UpdateSinkConfig(sinkID string, update func(*SinkConfig)) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for i := range e.sinkConfigs {
		if e.sinkIDs[i] == sinkID {
			update(&e.sinkConfigs[i])

			// Also update the writer if it exists
			for _, sw := range e.sinkWriters {
				if sw.sinkID == sinkID {
					sw.updateMu.Lock()
					sw.config = e.sinkConfigs[i]
					sw.currentBatchSize = e.sinkConfigs[i].BatchSize
					if sw.currentBatchSize < 1 {
						sw.currentBatchSize = 1
					}
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

func (e *Engine) SetSourceConfig(cfg SourceConfig) {
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

func (e *Engine) SetOnStatusChange(fn func(StatusUpdate)) {
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
	if _, ok := e.source.(*PrioritySource); ok {
		e.logger.Info("DLQ Drain requested: Source already prioritized", "workflow_id", e.workflowID)
		return nil
	}

	e.source = NewPrioritySource(dlqSource, e.source, e.logger)
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
	e.statusTracker.mu.Lock()
	e.statusTracker.lastMsgTime = time.Now()
	e.statusTracker.mu.Unlock()
	e.setSourceStatus("running")
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
func (e *Engine) GetStatus() StatusUpdate {
	sourceStatus, sinkStatuses, engineStatus, _, processed, dlq, latency := e.statusTracker.GetStatus()

	update := StatusUpdate{
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
	for _, sw := range e.sinkWriters {
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

	e.statusTracker.mu.RLock()
	if len(e.statusTracker.nodeMetrics) > 0 {
		update.NodeMetrics = make(map[string]uint64)
		for k, v := range e.statusTracker.nodeMetrics {
			update.NodeMetrics[k] = v
		}
	}
	if len(e.statusTracker.nodeErrorMetrics) > 0 {
		update.NodeErrorMetrics = make(map[string]uint64)
		for k, v := range e.statusTracker.nodeErrorMetrics {
			update.NodeErrorMetrics[k] = v
		}
	}
	if len(e.statusTracker.nodeSamples) > 0 {
		update.NodeSamples = make(map[string]any)
		for k, v := range e.statusTracker.nodeSamples {
			update.NodeSamples[k] = v
		}
	}
	if len(e.statusTracker.edgeMetrics) > 0 {
		update.EdgeMetrics = make(map[string]uint64)
		for k, v := range e.statusTracker.edgeMetrics {
			update.EdgeMetrics[k] = v
		}
	}
	e.statusTracker.mu.RUnlock()

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
