package engine

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/governance"
	"github.com/user/hermod/pkg/buffer"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/schema"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("hermod-engine")

// DefaultLogger and related logging helpers moved to logger.go

type RoutedMessage struct {
	SinkIndex int
	Message   hermod.Message
}

type RouterFunc func(ctx context.Context, msg hermod.Message) ([]RoutedMessage, error)

// Engine orchestrates the data flow from Source to Sinks.
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

	// Internal state tracking
	statusMu          sync.RWMutex
	sourceStatus      string
	sinkStatuses      map[string]string
	engineStatus      string
	lastMsgTime       time.Time
	processedMessages uint64
	deadLetterCount   uint64
	nodeMetrics       map[string]uint64
	nodeErrorMetrics  map[string]uint64
	nodeSamples       map[string]interface{}
	nodeMetricsMu     sync.RWMutex

	// Async Sink Writers
	sinkWriters []*sinkWriter

	// Checkpointing
	checkpointHandler func(ctx context.Context, sourceState map[string]string) error
	checkpointMu      sync.Mutex
	inCheckpoint      bool

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

type sinkWriter struct {
	engine *Engine
	sink   hermod.Sink
	sinkID string
	index  int
	config SinkConfig

	ch chan *pendingMessage

	// Spill to Disk
	spillBuffer hermod.Producer

	// Circuit Breaker state
	cbMu          sync.RWMutex
	cbFailCount   int
	cbLastFailure time.Time
	cbOpenUntil   time.Time
	cbStatus      string // "closed", "open", "half-open"

	// Adaptive Batching
	currentBatchSize int
	batchTimeout     time.Duration
	updateMu         sync.RWMutex
}

type pendingMessage struct {
	msg  hermod.Message
	done chan error
}

var pendingMessagePool = sync.Pool{
	New: func() interface{} {
		return &pendingMessage{
			done: make(chan error, 1),
		}
	},
}

func acquirePendingMessage(msg hermod.Message) *pendingMessage {
	pm := pendingMessagePool.Get().(*pendingMessage)
	pm.msg = msg
	return pm
}

func releasePendingMessage(pm *pendingMessage) {
	pm.msg = nil
	// Reset the done channel by reading if it has anything (should be empty though)
	select {
	case <-pm.done:
	default:
	}
	pendingMessagePool.Put(pm)
}

type StatusUpdate struct {
	WorkflowID       string                 `json:"workflow_id,omitempty"`
	EngineStatus     string                 `json:"engine_status,omitempty"`
	SourceStatus     string                 `json:"source_status,omitempty"`
	SourceID         string                 `json:"source_id,omitempty"`
	SinkStatuses     map[string]string      `json:"sink_statuses,omitempty"`
	SinkID           string                 `json:"sink_id,omitempty"`
	SinkStatus       string                 `json:"sink_status,omitempty"`
	ProcessedCount   uint64                 `json:"processed_count"`
	DeadLetterCount  uint64                 `json:"dead_letter_count,omitempty"`
	NodeMetrics      map[string]uint64      `json:"node_metrics,omitempty"`
	NodeErrorMetrics map[string]uint64      `json:"node_error_metrics,omitempty"`
	NodeSamples      map[string]interface{} `json:"node_samples,omitempty"`
	SinkCBStatuses   map[string]string      `json:"sink_cb_statuses,omitempty"`
	SinkBufferFill   map[string]float64     `json:"sink_buffer_fill,omitempty"`
	AverageDQScore   float64                `json:"average_dq_score,omitempty"`
	AvgLatency       time.Duration          `json:"avg_latency,omitempty"`
}

// Config holds configuration for the Engine.
type Config struct {
	MaxRetries          int           `json:"max_retries"`
	RetryInterval       time.Duration `json:"retry_interval"`
	ReconnectInterval   time.Duration `json:"reconnect_interval"`
	StatusInterval      time.Duration `json:"status_interval"`
	PrioritizeDLQ       bool          `json:"prioritize_dlq"`
	DryRun              bool          `json:"dry_run"`
	CheckpointInterval  time.Duration `json:"checkpoint_interval"`
	TraceSampleRate     float64       `json:"trace_sample_rate"` // 0.0 to 1.0
	AdaptiveThroughput  bool          `json:"adaptive_throughput"`
	MaxMemoryMB         uint64        `json:"max_memory_mb"`
	OutboxRelayInterval time.Duration `json:"outbox_relay_interval"`
}

type BackpressureStrategy string

const (
	BPBlock       BackpressureStrategy = "block"
	BPDropOldest  BackpressureStrategy = "drop_oldest"
	BPDropNewest  BackpressureStrategy = "drop_newest"
	BPSampling    BackpressureStrategy = "sampling"
	BPSpillToDisk BackpressureStrategy = "spill_to_disk"
)

type SinkConfig struct {
	MaxRetries       int             `json:"max_retries"`
	RetryInterval    time.Duration   `json:"retry_interval"`
	RetryIntervals   []time.Duration `json:"retry_intervals"`
	BatchSize        int             `json:"batch_size"`
	BatchTimeout     time.Duration   `json:"batch_timeout"`
	AdaptiveBatching bool            `json:"adaptive_batching"`
	Concurrency      int             `json:"concurrency"`

	// Circuit Breaker settings
	CircuitBreakerThreshold int           `json:"cb_threshold"`
	CircuitBreakerInterval  time.Duration `json:"cb_interval"`
	CircuitBreakerCoolDown  time.Duration `json:"cb_cool_off"` // match internal/engine/registry.go key "circuit_cool_off"

	// Backpressure settings
	BackpressureStrategy BackpressureStrategy `json:"backpressure_strategy"`
	BackpressureBuffer   int                  `json:"backpressure_buffer"`
	SamplingRate         float64              `json:"sampling_rate"` // 0.0 to 1.0

	// Spill to Disk settings
	SpillPath    string `json:"spill_path"`
	SpillMaxSize int    `json:"spill_max_size"`
}

type SourceConfig struct {
	ReconnectIntervals []time.Duration `json:"reconnect_intervals"`
}

// DefaultConfig returns the default configuration for the Engine.
func DefaultConfig() Config {
	return Config{
		MaxRetries:          3,
		RetryInterval:       100 * time.Millisecond,
		ReconnectInterval:   30 * time.Second,
		StatusInterval:      5 * time.Second,
		CheckpointInterval:  1 * time.Minute,
		OutboxRelayInterval: 1 * time.Minute,
		TraceSampleRate:     1.0,
	}
}

func NewEngine(source hermod.Source, sinks []hermod.Sink, buffer hermod.Producer) *Engine {
	e := &Engine{
		source:       source,
		sinks:        sinks,
		buffer:       buffer,
		logger:       NewDefaultLogger(),
		config:       DefaultConfig(),
		sinkStatuses: make(map[string]string),
		inFlightSem:  make(chan struct{}, 1000),
		dqScorer:     governance.NewScorer(),
	}
	e.SetLogger(e.logger)
	return e
}

// SetConfig sets the configuration for the engine.
func (e *Engine) SetConfig(config Config) {
	e.config = config
}

func (e *Engine) SetSinkConfigs(configs []SinkConfig) {
	e.statusMu.Lock()
	e.sinkConfigs = configs
	e.statusMu.Unlock()

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
	e.statusMu.RLock()
	defer e.statusMu.RUnlock()
	return e.sinkConfigs
}

func (e *Engine) UpdateSinkConfig(sinkID string, update func(*SinkConfig)) {
	e.statusMu.Lock()
	defer e.statusMu.Unlock()

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

// SetSourceConfig sets the source configuration for the engine.
func (e *Engine) SetSourceConfig(config SourceConfig) {
	e.sourceConfig = config
}

// SetLogger sets the logger for the engine.
func (e *Engine) SetLogger(logger hermod.Logger) {
	e.logger = logger
	if l, ok := e.source.(hermod.Loggable); ok {
		l.SetLogger(logger)
	}
	for _, snk := range e.sinks {
		if l, ok := snk.(hermod.Loggable); ok {
			l.SetLogger(logger)
		}
	}
	if l, ok := e.deadLetterSink.(hermod.Loggable); ok {
		l.SetLogger(logger)
	}
}

// SetDeadLetterSink sets the dead letter sink for the engine.
func (e *Engine) SetDeadLetterSink(sink hermod.Sink) {
	e.deadLetterSink = sink
	if l, ok := sink.(hermod.Loggable); ok {
		l.SetLogger(e.logger)
	}
}

// SetIDs sets the IDs for workflow, source and sinks.
func (e *Engine) SetIDs(workflowID string, sourceID string, sinkIDs []string) {
	e.workflowID = workflowID
	e.sourceID = sourceID
	e.sinkIDs = sinkIDs
}

// SetSinkTypes sets the types for sinks.
func (e *Engine) SetSinkTypes(sinkTypes []string) {
	e.sinkTypes = sinkTypes
}

// SetOnStatusChange sets the callback for status changes.
func (e *Engine) SetOnStatusChange(f func(StatusUpdate)) {
	e.onStatusChange = f
}

// SetRouter sets the router function for the engine.
func (e *Engine) GetSource() hermod.Source {
	return e.source
}

func (e *Engine) GetSinks() []hermod.Sink {
	return e.sinks
}

func (e *Engine) SetRouter(router RouterFunc) {
	e.router = router
}

func (e *Engine) SetCheckpointHandler(f func(ctx context.Context, sourceState map[string]string) error) {
	e.checkpointHandler = f
}

func (e *Engine) SetValidator(validator schema.Validator) {
	e.validator = validator
}

func (e *Engine) SetTraceRecorder(recorder hermod.TraceRecorder) {
	e.traceRecorder = recorder
}

func (e *Engine) SimulateFailure(duration time.Duration) {
	e.failureSimMu.Lock()
	defer e.failureSimMu.Unlock()
	e.failUntil = time.Now().Add(duration)
	if e.logger != nil {
		e.logger.Warn("Engine: simulated failure injected", "duration", duration)
	}
}

func (e *Engine) isFailing() bool {
	e.failureSimMu.RLock()
	defer e.failureSimMu.RUnlock()
	return time.Now().Before(e.failUntil)
}

func (e *Engine) SetOutboxStorage(outbox hermod.OutboxStorage) {
	e.outboxStore = outbox
}

func (e *Engine) runOutboxRelay(ctx context.Context) {
	if e.outboxStore == nil {
		return
	}

	interval := e.config.OutboxRelayInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			items, err := e.outboxStore.ListOutboxItems(ctx, "pending", 100)
			if err != nil {
				e.logger.Error("Failed to fetch pending outbox items", "workflow_id", e.workflowID, "error", err)
				continue
			}

			for _, item := range items {
				if item.WorkflowID != e.workflowID {
					continue
				}

				// Reconstruct message
				msg := message.AcquireMessage()
				msg.SetPayload(item.Payload)
				for k, v := range item.Metadata {
					msg.SetMetadata(k, v)
				}
				// If we have a stored MessageID, we should probably restore it
				if mid, ok := item.Metadata["_message_id"]; ok {
					msg.SetID(mid)
				}

				// Mark that this message came from the outbox so we can delete it later
				msg.SetMetadata("_outbox_id", item.ID)

				// Try to push back to buffer
				if err := e.buffer.Produce(ctx, msg); err != nil {
					e.logger.Error("Failed to re-produce outbox item to buffer", "workflow_id", e.workflowID, "item_id", item.ID, "error", err)
					item.Attempts++
					item.LastError = err.Error()
					if item.Attempts > 10 {
						item.Status = "failed"
					}
					_ = e.outboxStore.UpdateOutboxItem(ctx, item)
					continue
				}

				// Do NOT delete here! It should be deleted only after successful processing by sinks.
				// We update it to 'processing' to avoid other relays picking it up (if we had distributed coordination on items)
				item.Status = "processing"
				_ = e.outboxStore.UpdateOutboxItem(ctx, item)
			}
		}
	}
}

func (e *Engine) RecordTraceStep(ctx context.Context, msg hermod.Message, nodeID string, start time.Time, err error) {
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

	step := hermod.TraceStep{
		NodeID:    nodeID,
		Timestamp: start,
		Duration:  time.Since(start),
		Data:      msg.Data(),
	}

	if err != nil {
		step.Error = err.Error()
	}

	e.traceRecorder.RecordStep(ctx, e.workflowID, msg.ID(), step)
}

func (e *Engine) UpdateNodeMetric(nodeID string, count uint64) {
	e.nodeMetricsMu.Lock()
	if e.nodeMetrics == nil {
		e.nodeMetrics = make(map[string]uint64)
	}
	e.nodeMetrics[nodeID] += count
	e.nodeMetricsMu.Unlock()
}

func (e *Engine) UpdateNodeErrorMetric(nodeID string, count uint64) {
	e.nodeMetricsMu.Lock()
	if e.nodeErrorMetrics == nil {
		e.nodeErrorMetrics = make(map[string]uint64)
	}
	e.nodeErrorMetrics[nodeID] += count
	e.nodeMetricsMu.Unlock()
}

// UpdateNodeSample updates the last data sample for a specific workflow node.
func (e *Engine) UpdateNodeSample(nodeID string, data map[string]interface{}) {
	if data == nil {
		return
	}
	e.nodeMetricsMu.Lock()
	if e.nodeSamples == nil {
		e.nodeSamples = make(map[string]interface{})
	}
	// Shallow copy of the map to avoid external modifications affecting the sample
	// or vice versa. Deep copy would be safer but more expensive.
	sample := make(map[string]interface{})
	for k, v := range data {
		sample[k] = v
	}
	e.nodeSamples[nodeID] = sample
	e.nodeMetricsMu.Unlock()
}

func (e *Engine) setStatus(status string) {
	e.statusMu.Lock()
	if e.engineStatus == status {
		e.statusMu.Unlock()
		return
	}
	e.engineStatus = status
	e.statusMu.Unlock()
	e.notifyStatusChange()
}

func (e *Engine) setSourceStatus(status string) {
	e.statusMu.Lock()
	if e.sourceStatus == status {
		e.statusMu.Unlock()
		return
	}
	e.sourceStatus = status
	e.statusMu.Unlock()
	e.notifyStatusChange()
}

func (e *Engine) setSinkStatus(sinkID string, status string) {
	e.statusMu.Lock()
	if e.sinkStatuses == nil {
		e.sinkStatuses = make(map[string]string)
	}
	if e.sinkStatuses[sinkID] == status {
		e.statusMu.Unlock()
		return
	}
	e.sinkStatuses[sinkID] = status
	e.statusMu.Unlock()
	e.notifyStatusChange()
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

	e.statusMu.Lock()
	defer e.statusMu.Unlock()

	// Check if already wrapped
	if _, ok := e.source.(*PrioritySource); ok {
		e.logger.Info("DLQ Drain requested: Source already prioritized", "workflow_id", e.workflowID)
		return nil
	}

	e.logger.Info("DLQ Drain requested: Wrapping source with PriorityMultiplexer", "workflow_id", e.workflowID)
	e.source = NewPrioritySource(dlqSource, e.source, e.logger)
	return nil
}

func (e *Engine) writeToSink(ctx context.Context, snk hermod.Sink, msg hermod.Message, sinkID string, i int) error {
	if msg == nil {
		return nil
	}

	if e.isFailing() {
		return fmt.Errorf("simulated engine failure")
	}

	// Pre-write validation
	if vs, ok := snk.(hermod.ValidatingSink); ok {
		if err := vs.Validate(ctx, msg); err != nil {
			e.logger.Error("Sink pre-write validation failed", "workflow_id", e.workflowID, "sink_id", sinkID, "message_id", msg.ID(), "error", err)
			if e.deadLetterSink != nil {
				e.logger.Info("Sending invalid message to Dead Letter Sink", "workflow_id", e.workflowID, "sink_id", sinkID, "message_id", msg.ID())
				msg.SetMetadata("_hermod_validation_failed", "true")
				msg.SetMetadata("_hermod_last_error", err.Error())
				if dlerr := e.deadLetterSink.Write(ctx, msg); dlerr != nil {
					e.logger.Error("Failed to write invalid message to Dead Letter Sink", "workflow_id", e.workflowID, "error", dlerr)
					return fmt.Errorf("validation error (and DLQ failed): %w", err)
				}
				return nil
			}
			return fmt.Errorf("validation error: %w", err)
		}
	}

	if e.config.DryRun {
		e.logger.Info("[DRY-RUN] Message would be written to sink",
			"workflow_id", e.workflowID,
			"sink_id", sinkID,
			"action", "write",
			"message_id", msg.ID(),
			"payload_len", len(msg.Payload()),
		)
		return nil
	}
	// Retry mechanism for Sink Write
	var lastErr error

	maxRetries := e.config.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 1
	}
	retryInterval := e.config.RetryInterval

	if i >= 0 && i < len(e.sinkConfigs) {
		if e.sinkConfigs[i].MaxRetries > 0 {
			maxRetries = e.sinkConfigs[i].MaxRetries
		}
		if e.sinkConfigs[i].RetryInterval > 0 {
			retryInterval = e.sinkConfigs[i].RetryInterval
		}
	}

	for j := 0; j < maxRetries; j++ {
		idempStart := time.Now()
		if err := snk.Write(ctx, msg); err != nil {
			lastErr = err
			SinkWriteErrors.WithLabelValues(e.workflowID, sinkID).Inc()
			e.setSinkStatus(sinkID, "reconnecting")
			e.setStatus("reconnecting:sink:" + sinkID)
			e.logger.Warn("Sink write error, retrying", "workflow_id", e.workflowID, "attempt", j+1, "sink_id", sinkID, "error", err)

			var interval time.Duration
			if i >= 0 && i < len(e.sinkConfigs) && len(e.sinkConfigs[i].RetryIntervals) > 0 {
				if j < len(e.sinkConfigs[i].RetryIntervals) {
					interval = e.sinkConfigs[i].RetryIntervals[j]
				} else {
					interval = e.sinkConfigs[i].RetryIntervals[len(e.sinkConfigs[i].RetryIntervals)-1]
				}
			} else {
				interval = time.Duration(j+1) * retryInterval
			}
			// Add jitter (±20%) to avoid thundering herd
			jitter := 0.8 + rand.Float64()*0.4
			interval = time.Duration(float64(interval) * jitter)

			select {
			case <-time.After(interval):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if j > 0 {
			e.logger.Info("Sink reconnected successfully", "workflow_id", e.workflowID, "sink_id", sinkID, "action", "reconnect")
		}
		SinkWriteCount.WithLabelValues(e.workflowID, sinkID).Inc()
		// Record observed latency for the sink write path (captures idempotency checks when present)
		IdempotencyLatency.WithLabelValues(e.workflowID, sinkID).Observe(time.Since(idempStart).Seconds())
		// If sink reports idempotency effect, emit metrics
		if reporter, ok := snk.(hermod.IdempotencyReporter); ok {
			if dedup, conflict := reporter.LastWriteIdempotent(); dedup || conflict {
				if dedup {
					IdempotencyDedupTotal.WithLabelValues(e.workflowID, sinkID).Inc()
				}
				if conflict {
					IdempotencyConflictsTotal.WithLabelValues(e.workflowID, sinkID).Inc()
				}
			}
		}
		e.logger.Info("Message written to sink",
			"workflow_id", e.workflowID,
			"sink_id", sinkID,
			"action", "write",
			"message_id", msg.ID(),
			"payload_len", len(msg.Payload()),
		)
		lastErr = nil
		break
	}
	if lastErr != nil {
		e.logger.Error("Sink write failed after retries", "workflow_id", e.workflowID, "sink_id", sinkID, "error", lastErr)
		if e.deadLetterSink != nil {
			e.logger.Info("Sending message to Dead Letter Sink", "workflow_id", e.workflowID, "sink_id", sinkID, "message_id", msg.ID())

			// Add failure context to metadata
			msg.SetMetadata("_hermod_failed_sink", sinkID)
			msg.SetMetadata("_hermod_last_error", lastErr.Error())
			msg.SetMetadata("_hermod_failed_at", time.Now().Format(time.RFC3339))

			e.statusMu.Lock()
			e.deadLetterCount++
			e.statusMu.Unlock()

			DeadLetterCount.WithLabelValues(e.workflowID, sinkID).Inc()
			if err := e.deadLetterSink.Write(ctx, msg); err != nil {
				e.logger.Error("Failed to write to Dead Letter Sink", "workflow_id", e.workflowID, "error", err)
				return fmt.Errorf("sink write error (and DLQ failed): %w", lastErr)
			}
			return nil // Message preserved in DLQ
		}
		return fmt.Errorf("sink write error: %w", lastErr)
	}
	return nil
}

func (e *Engine) notifyStatusChange() {
	if e.onStatusChange != nil {
		e.onStatusChange(e.GetStatus())
	}
}

func (e *Engine) recordSourceActivity() {
	e.statusMu.Lock()
	e.lastMsgTime = time.Now()
	e.statusMu.Unlock()
	e.setSourceStatus("running")
}

func (e *Engine) redactData(data map[string]interface{}) map[string]interface{} {
	if data == nil {
		return nil
	}
	redacted := make(map[string]interface{})
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
		} else if m, ok := v.(map[string]interface{}); ok {
			redacted[k] = e.redactData(m)
		} else {
			redacted[k] = v
		}
	}
	return redacted
}

// LastMsgTime returns the time of the last message received from the source.
func (e *Engine) LastMsgTime() time.Time {
	e.statusMu.RLock()
	defer e.statusMu.RUnlock()
	return e.lastMsgTime
}

// GetStatus returns the current status of the engine.
func (e *Engine) GetStatus() StatusUpdate {
	e.statusMu.RLock()
	defer e.statusMu.RUnlock()

	update := StatusUpdate{
		WorkflowID:      e.workflowID,
		EngineStatus:    e.engineStatus,
		SourceStatus:    e.sourceStatus,
		SourceID:        e.sourceID,
		SinkStatuses:    make(map[string]string),
		ProcessedCount:  e.processedMessages,
		DeadLetterCount: e.deadLetterCount,
	}
	for k, v := range e.sinkStatuses {
		update.SinkStatuses[k] = v
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

	e.nodeMetricsMu.RLock()
	if len(e.nodeMetrics) > 0 {
		update.NodeMetrics = make(map[string]uint64)
		for k, v := range e.nodeMetrics {
			update.NodeMetrics[k] = v
		}
	}
	if len(e.nodeErrorMetrics) > 0 {
		update.NodeErrorMetrics = make(map[string]uint64)
		for k, v := range e.nodeErrorMetrics {
			update.NodeErrorMetrics[k] = v
		}
	}
	if len(e.nodeSamples) > 0 {
		update.NodeSamples = make(map[string]interface{})
		for k, v := range e.nodeSamples {
			update.NodeSamples[k] = v
		}
	}
	e.nodeMetricsMu.RUnlock()

	if e.dqScorer != nil {
		update.AverageDQScore = e.dqScorer.GetAverageScore(e.workflowID) * 100
	}

	return update
}

func (e *Engine) writeBatchToSink(ctx context.Context, snk hermod.BatchSink, msgs []hermod.Message, sinkID string, i int) error {
	// Filter nil messages
	filtered := make([]hermod.Message, 0, len(msgs))
	for _, m := range msgs {
		if m != nil {
			filtered = append(filtered, m)
		}
	}
	msgs = filtered

	if len(msgs) == 0 {
		return nil
	}

	// Pre-write validation
	if vs, ok := snk.(hermod.ValidatingSink); ok {
		validMsgs := make([]hermod.Message, 0, len(msgs))
		for _, m := range msgs {
			if err := vs.Validate(ctx, m); err != nil {
				e.logger.Error("Sink pre-write validation failed for message in batch", "workflow_id", e.workflowID, "sink_id", sinkID, "message_id", m.ID(), "error", err)
				if e.deadLetterSink != nil {
					e.logger.Info("Sending invalid message from batch to Dead Letter Sink", "workflow_id", e.workflowID, "sink_id", sinkID, "message_id", m.ID())
					m.SetMetadata("_hermod_validation_failed", "true")
					m.SetMetadata("_hermod_last_error", err.Error())
					_ = e.deadLetterSink.Write(ctx, m)
				}
				continue
			}
			validMsgs = append(validMsgs, m)
		}
		msgs = validMsgs
	}

	if len(msgs) == 0 {
		return nil
	}

	if len(msgs) == 1 {
		return e.writeToSink(ctx, snk, msgs[0], sinkID, i)
	}

	if e.config.DryRun {
		e.logger.Info("[DRY-RUN] Batch would be written to sink",
			"workflow_id", e.workflowID,
			"sink_id", sinkID,
			"action", "write_batch",
			"batch_size", len(msgs),
		)
		return nil
	}

	// Retry mechanism for Sink WriteBatch
	var lastErr error

	maxRetries := e.config.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 1
	}
	retryInterval := e.config.RetryInterval

	if i >= 0 && i < len(e.sinkConfigs) {
		if e.sinkConfigs[i].MaxRetries > 0 {
			maxRetries = e.sinkConfigs[i].MaxRetries
		}
		if e.sinkConfigs[i].RetryInterval > 0 {
			retryInterval = e.sinkConfigs[i].RetryInterval
		}
	}

	for j := 0; j < maxRetries; j++ {
		if err := snk.WriteBatch(ctx, msgs); err != nil {
			lastErr = err
			SinkWriteErrors.WithLabelValues(e.workflowID, sinkID).Add(float64(len(msgs)))
			e.setSinkStatus(sinkID, "reconnecting")
			e.setStatus("reconnecting:sink:" + sinkID)
			e.logger.Warn("Sink batch write error, retrying", "workflow_id", e.workflowID, "attempt", j+1, "sink_id", sinkID, "batch_size", len(msgs), "error", err)

			var interval time.Duration
			if i >= 0 && i < len(e.sinkConfigs) && len(e.sinkConfigs[i].RetryIntervals) > 0 {
				if j < len(e.sinkConfigs[i].RetryIntervals) {
					interval = e.sinkConfigs[i].RetryIntervals[j]
				} else {
					interval = e.sinkConfigs[i].RetryIntervals[len(e.sinkConfigs[i].RetryIntervals)-1]
				}
			} else {
				interval = time.Duration(j+1) * retryInterval
			}

			select {
			case <-time.After(interval):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if j > 0 {
			e.logger.Info("Sink reconnected successfully", "workflow_id", e.workflowID, "sink_id", sinkID, "action", "reconnect")
		}
		SinkWriteCount.WithLabelValues(e.workflowID, sinkID).Add(float64(len(msgs)))
		e.logger.Info("Batch written to sink",
			"workflow_id", e.workflowID,
			"sink_id", sinkID,
			"action", "write_batch",
			"batch_size", len(msgs),
		)
		lastErr = nil
		break
	}

	if lastErr != nil {
		e.logger.Error("Sink batch write failed after retries", "workflow_id", e.workflowID, "sink_id", sinkID, "error", lastErr)
		return fmt.Errorf("sink batch write error: %w", lastErr)
	}
	return nil
}

func (sw *sinkWriter) checkCircuitBreaker() error {
	sw.cbMu.Lock()
	defer sw.cbMu.Unlock()

	if sw.cbStatus == "" {
		sw.cbStatus = "closed"
	}

	if sw.cbStatus == "open" {
		if time.Now().After(sw.cbOpenUntil) {
			sw.cbStatus = "half-open"
			if sw.engine != nil && sw.engine.logger != nil {
				sw.engine.logger.Info("Circuit breaker half-open", "workflow_id", sw.engine.workflowID, "sink_id", sw.sinkID)
			}
			return nil
		}
		return fmt.Errorf("circuit breaker is open for sink %s", sw.sinkID)
	}

	return nil
}

func (sw *sinkWriter) recordSuccess() {
	sw.cbMu.Lock()
	defer sw.cbMu.Unlock()

	if sw.cbStatus == "half-open" {
		sw.cbStatus = "closed"
		sw.cbFailCount = 0
		if sw.engine != nil {
			sw.engine.setSinkStatus(sw.sinkID, "active")
			if sw.engine.logger != nil {
				sw.engine.logger.Info("Circuit breaker closed after success", "workflow_id", sw.engine.workflowID, "sink_id", sw.sinkID)
			}
		}
	} else {
		sw.cbFailCount = 0
	}
}

func (sw *sinkWriter) recordFailure() {
	sw.cbMu.Lock()
	defer sw.cbMu.Unlock()

	threshold := sw.config.CircuitBreakerThreshold
	if threshold <= 0 {
		threshold = 5 // Default threshold
	}

	interval := sw.config.CircuitBreakerInterval
	if interval <= 0 {
		interval = 1 * time.Minute
	}

	coolDown := sw.config.CircuitBreakerCoolDown
	if coolDown <= 0 {
		coolDown = 30 * time.Second
	}

	now := time.Now()
	if now.Sub(sw.cbLastFailure) > interval && sw.cbStatus == "closed" {
		sw.cbFailCount = 1
	} else {
		sw.cbFailCount++
	}

	sw.cbLastFailure = now

	if sw.cbFailCount >= threshold || sw.cbStatus == "half-open" {
		sw.cbStatus = "open"
		sw.cbOpenUntil = now.Add(coolDown)
		if sw.engine != nil {
			sw.engine.setSinkStatus(sw.sinkID, "error:circuit_breaker_open")
			if sw.engine.logger != nil {
				sw.engine.logger.Error("Circuit breaker opened", "workflow_id", sw.engine.workflowID, "sink_id", sw.sinkID, "fail_count", sw.cbFailCount, "open_until", sw.cbOpenUntil)
			}
		}
	}
}

func (w *sinkWriter) run(ctx context.Context) {
	if w.config.BackpressureStrategy == BPSpillToDisk {
		path := w.config.SpillPath
		if path == "" {
			path = ".hermod-spill-" + w.sinkID
		}
		maxSize := w.config.SpillMaxSize
		if maxSize <= 0 {
			maxSize = 100 * 1024 * 1024 // 100MB default
		}
		var err error
		w.spillBuffer, err = buffer.NewFileBuffer(path, maxSize)
		if err != nil {
			if w.engine != nil && w.engine.logger != nil {
				w.engine.logger.Error("Failed to initialize spill buffer", "sink_id", w.sinkID, "path", path, "error", err)
			}
		} else {
			// Start a consumer for the spill buffer
			go func() {
				if consumer, ok := w.spillBuffer.(hermod.Consumer); ok {
					_ = consumer.Consume(ctx, func(ctx context.Context, msg hermod.Message) error {
						// Try to put back into the main channel
						// Since we are spilling, we want to prioritize messages in sw.ch
						// but also drain the spill buffer when there is room.
						pm := acquirePendingMessage(msg)
						select {
						case w.ch <- pm:
							// Successfully re-enqueued
							return nil
						case <-ctx.Done():
							releasePendingMessage(pm)
							return ctx.Err()
						}
					})
				}
			}()
		}
	}
	w.currentBatchSize = w.config.BatchSize
	if w.currentBatchSize < 1 {
		w.currentBatchSize = 1
	}
	w.batchTimeout = w.config.BatchTimeout
	if w.batchTimeout == 0 {
		w.batchTimeout = 100 * time.Millisecond
	}

	batch := make([]*pendingMessage, 0, w.currentBatchSize)
	w.updateMu.RLock()
	ticker := time.NewTicker(w.batchTimeout)
	w.updateMu.RUnlock()
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}

		start := time.Now()
		if err := w.checkCircuitBreaker(); err != nil {
			for _, pm := range batch {
				pm.done <- err
			}
			batch = batch[:0]
			return
		}

		msgs := make([]hermod.Message, len(batch))
		for i, pm := range batch {
			msgs[i] = pm.msg
		}

		var err error
		if bs, ok := w.sink.(hermod.BatchSink); ok && len(msgs) > 1 {
			err = w.engine.writeBatchToSink(ctx, bs, msgs, w.sinkID, w.index)
		} else {
			for _, m := range msgs {
				if e := w.engine.writeToSink(ctx, w.sink, m, w.sinkID, w.index); e != nil {
					err = e
					break // Stop processing this batch if one message fails after retries
				}
			}
		}

		if err != nil {
			w.recordFailure()
		} else {
			w.recordSuccess()
		}

		// Adaptive Batching logic
		if w.config.AdaptiveBatching {
			duration := time.Since(start)
			if err == nil {
				// If we are fast and have more messages waiting, increase batch size
				w.updateMu.Lock()
				if duration < w.batchTimeout/2 && len(w.ch) > w.currentBatchSize/2 {
					w.currentBatchSize = int(float64(w.currentBatchSize) * 1.1)
					if w.currentBatchSize > 5000 {
						w.currentBatchSize = 5000
					}
				}
				w.updateMu.Unlock()
			} else {
				// If we had error or were slow, decrease batch size
				w.updateMu.Lock()
				w.currentBatchSize = int(float64(w.currentBatchSize) * 0.7)
				if w.currentBatchSize < 1 {
					w.currentBatchSize = 1
				}
				w.updateMu.Unlock()
			}
		}

		for _, pm := range batch {
			pm.done <- err
		}
		batch = batch[:0]
	}

	for {
		select {
		case pm, ok := <-w.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, pm)
			if len(batch) >= w.currentBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (e *Engine) adaptiveThrottle(ctx context.Context, duration time.Duration) {
	if !e.config.AdaptiveThroughput {
		return
	}

	e.statusMu.Lock()
	defer e.statusMu.Unlock()

	// Update running average
	if e.latencyAvg == 0 {
		e.latencyAvg = duration
	} else {
		e.latencyAvg = (e.latencyAvg*9 + duration) / 10
	}

	// Adjust polling interval every 5s based on latency and memory
	if time.Since(e.lastPollAdjust) < 5*time.Second {
		return
	}
	e.lastPollAdjust = time.Now()

	// Check memory pressure
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	memoryPressure := e.config.MaxMemoryMB > 0 && m.Alloc > e.config.MaxMemoryMB*1024*1024

	// If latency is high (>500ms) or memory is high, slow down polling
	if e.latencyAvg > 500*time.Millisecond || memoryPressure {
		e.throttleDelay += 100 * time.Millisecond
		if e.throttleDelay > 10*time.Second {
			e.throttleDelay = 10 * time.Second
		}
		reason := "high latency"
		if memoryPressure {
			reason = "memory pressure"
		}
		e.logger.Warn("Adaptive throughput: throttling ingestion",
			"reason", reason,
			"avg_latency", e.latencyAvg.String(),
			"mem_alloc_mb", m.Alloc/1024/1024,
			"throttle_delay", e.throttleDelay.String(),
			"workflow_id", e.workflowID)
	} else if e.latencyAvg < 100*time.Millisecond && e.throttleDelay > 0 {
		e.throttleDelay -= 100 * time.Millisecond
		if e.throttleDelay < 0 {
			e.throttleDelay = 0
		}
	}
}

// Start begins the data transfer process.
func (e *Engine) Start(ctx context.Context) error {
	// Initialize Priority Source if enabled
	if e.config.PrioritizeDLQ && e.deadLetterSink != nil {
		if dlqSource, ok := e.deadLetterSink.(hermod.Source); ok {
			e.logger.Info("DLQ Priority enabled: wrapping source with PriorityMultiplexer", "workflow_id", e.workflowID)
			e.source = NewPrioritySource(dlqSource, e.source, e.logger)
		}
	}

	// Initialize Sink Writers
	var writersWg sync.WaitGroup
	e.sinkWriters = make([]*sinkWriter, len(e.sinks))
	for i, snk := range e.sinks {
		sinkID := ""
		if i < len(e.sinkIDs) {
			sinkID = e.sinkIDs[i]
		}

		cfg := SinkConfig{}
		if i < len(e.sinkConfigs) {
			cfg = e.sinkConfigs[i]
		}

		bufferCap := cfg.BackpressureBuffer
		if bufferCap <= 0 {
			bufferCap = 1000
		}

		sw := &sinkWriter{
			engine:           e,
			sink:             snk,
			sinkID:           sinkID,
			index:            i,
			config:           cfg,
			ch:               make(chan *pendingMessage, bufferCap),
			currentBatchSize: cfg.BatchSize,
		}
		e.sinkWriters[i] = sw
		writersWg.Add(1)
		go func(w *sinkWriter) {
			defer writersWg.Done()
			w.run(ctx)
		}(sw)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	e.logger.Info("Starting Hermod Engine", "workflow_id", e.workflowID)
	e.setStatus("connecting")
	ActiveEngines.Inc()

	// Start Outbox Relay if enabled
	if e.outboxStore != nil {
		go e.runOutboxRelay(ctx)
	}

	defer ActiveEngines.Dec()

	// Status Checker
	go func() {
		interval := e.config.StatusInterval
		if interval == 0 {
			interval = 1 * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var err error
				if readyChecker, ok := e.source.(hermod.ReadyChecker); ok {
					err = readyChecker.IsReady(ctx)
				} else {
					err = e.source.Ping(ctx)
				}

				if err != nil {
					e.logger.Error("Background source health check failed", "workflow_id", e.workflowID, "error", err.Error())
					e.statusMu.RLock()
					recentActivity := !e.lastMsgTime.IsZero() && time.Since(e.lastMsgTime) < interval*2
					e.statusMu.RUnlock()

					if !recentActivity {
						e.setSourceStatus("reconnecting")
						e.setStatus("reconnecting:source")
					}
				} else {
					e.setSourceStatus("running")
				}

				allSinksOk := true
				for i, snk := range e.sinks {
					sinkID := ""
					if i < len(e.sinkIDs) {
						sinkID = e.sinkIDs[i]
					}

					sinkType := ""
					if i < len(e.sinkTypes) {
						sinkType = e.sinkTypes[i]
					}

					if sinkType == "stdout" {
						e.setSinkStatus(sinkID, "running")
						continue
					}

					if err := snk.Ping(ctx); err != nil {
						e.logger.Error("Background sink health check failed", "workflow_id", e.workflowID, "sink_id", sinkID, "error", err.Error())
						e.setSinkStatus(sinkID, "reconnecting")
						if allSinksOk {
							e.setStatus("reconnecting:sink:" + sinkID)
						}
						allSinksOk = false
					} else {
						e.setSinkStatus(sinkID, "running")
					}
				}
				if allSinksOk && e.sourceStatus == "running" {
					if e.engineStatus != "running" && strings.HasPrefix(e.engineStatus, "reconnecting") {
						e.logger.Info("System reconnected successfully", "workflow_id", e.workflowID, "action", "reconnect")
					}
					e.setStatus("running")
				}
				e.notifyStatusChange()
			}
		}
	}()

	// Periodic Checkpointing
	if e.config.CheckpointInterval > 0 {
		go func() {
			ticker := time.NewTicker(e.config.CheckpointInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					_ = e.Checkpoint(ctx)
				}
			}
		}()
	}

	// Pre-flight checks for Sinks (Sink failure turns off workflow)
	for i, snk := range e.sinks {
		sinkID := ""
		if i < len(e.sinkIDs) {
			sinkID = e.sinkIDs[i]
		}

		sinkType := ""
		if i < len(e.sinkTypes) {
			sinkType = e.sinkTypes[i]
		}

		if sinkType == "stdout" {
			e.setSinkStatus(sinkID, "running")
			continue
		}

		success := false
		var lastErr error
		for attempt := 1; attempt <= 3; attempt++ {
			if err := snk.Ping(ctx); err != nil {
				lastErr = err
				e.setSinkStatus(sinkID, "reconnecting")
				e.logger.Warn("Sink ping failed during startup", "workflow_id", e.workflowID, "sink_id", sinkID, "attempt", attempt, "error", err)
				if attempt < 3 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(100 * time.Millisecond):
					}
				}
			} else {
				e.setSinkStatus(sinkID, "running")
				success = true
				break
			}
		}

		if !success {
			e.logger.Error("Sink pre-flight checks failed after 3 attempts", "workflow_id", e.workflowID, "sink_id", sinkID, "error", lastErr)
			return fmt.Errorf("sink pre-flight checks failed after 3 attempts")
		}
	}

	// Source to Buffer
	wg.Add(1)
	go func() {
		defer wg.Done()
		reconnectAttempts := 0
		for {
			// Check source connection only if we are in a failed state or haven't had activity for a while
			e.statusMu.RLock()
			interval := e.config.StatusInterval
			if interval == 0 {
				interval = 5 * time.Second
			}
			// Ping if:
			// 1. We are already in a reconnecting state
			// 2. We haven't had any activity yet
			// 3. It's been longer than the status interval since the last message
			needsPing := reconnectAttempts > 0 || e.lastMsgTime.IsZero() || time.Since(e.lastMsgTime) > interval
			e.statusMu.RUnlock()

			if needsPing {
				var err error
				if readyChecker, ok := e.source.(hermod.ReadyChecker); ok {
					err = readyChecker.IsReady(ctx)
				} else {
					err = e.source.Ping(ctx)
				}

				if err != nil {
					e.setSourceStatus("reconnecting")
					e.setStatus("reconnecting:source")
					e.logger.Warn("Source health check failed, entering reconnecting state", "workflow_id", e.workflowID, "error", err)

					var interval time.Duration
					if len(e.sourceConfig.ReconnectIntervals) > 0 {
						if reconnectAttempts < len(e.sourceConfig.ReconnectIntervals) {
							interval = e.sourceConfig.ReconnectIntervals[reconnectAttempts]
						} else {
							interval = e.sourceConfig.ReconnectIntervals[len(e.sourceConfig.ReconnectIntervals)-1]
						}
					} else {
						interval = e.config.ReconnectInterval
					}
					// Add jitter (±20%) to avoid stampedes when many workers reconnect
					jitter := 0.8 + rand.Float64()*0.4
					interval = time.Duration(float64(interval) * jitter)

					select {
					case <-ctx.Done():
						return
					case <-time.After(interval):
						reconnectAttempts++
						continue
					}
				}
			}

			reconnectAttempts = 0
			e.setSourceStatus("running")
			// If we were reconnecting, clear the status
			e.statusMu.RLock()
			currentStatus := e.engineStatus
			e.statusMu.RUnlock()
			if currentStatus == "reconnecting:source" || currentStatus == "connecting" {
				if currentStatus == "reconnecting:source" {
					e.logger.Info("Source reconnected successfully", "workflow_id", e.workflowID, "source_id", e.sourceID, "action", "reconnect")
				}
				e.setStatus("running")
			}

			select {
			case <-ctx.Done():
				e.logger.Info("Source-to-Buffer worker stopping due to context cancellation", "workflow_id", e.workflowID)
				return
			default:
				e.checkpointMu.Lock()
				// Wait if we are in checkpointing process
				for e.inCheckpoint {
					e.checkpointMu.Unlock()
					time.Sleep(10 * time.Millisecond)
					e.checkpointMu.Lock()
				}
				e.checkpointMu.Unlock()

				// Adaptive Throttle Delay
				e.statusMu.RLock()
				delay := e.throttleDelay
				e.statusMu.RUnlock()
				if delay > 0 {
					select {
					case <-ctx.Done():
						return
					case <-time.After(delay):
					}
				}

				ctx, span := tracer.Start(ctx, "SourceRead", trace.WithAttributes(
					attribute.String("workflow_id", e.workflowID),
					attribute.String("source_id", e.sourceID),
				))

				msg, err := e.source.Read(ctx)
				if err != nil {
					span.RecordError(err)
					span.SetStatus(codes.Error, err.Error())
					span.End()

					if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
						e.logger.Error("Source read error, attempting to reconnect", "workflow_id", e.workflowID, "error", err)
						e.setSourceStatus("reconnecting")
						e.setStatus("reconnecting:source")
						reconnectAttempts++
						continue // Go back to Ping loop
					}
					return
				}

				if msg == nil {
					span.End()
					continue
				}

				// Schema Validation
				if e.validator != nil {
					if err := e.validator.Validate(ctx, msg.Data()); err != nil {
						e.logger.Error("Schema validation failed", "workflow_id", e.workflowID, "error", err, "message_id", msg.ID())
						MessageErrors.WithLabelValues(e.workflowID, e.sourceID, "schema_validation").Inc()
						msg.SetMetadata("schema_validated", "false")
						msg.SetMetadata("schema_validation_error", err.Error())

						// Move to DLQ if enabled
						if e.deadLetterSink != nil {
							e.logger.Info("Moving failed schema validation message to DLQ", "workflow_id", e.workflowID, "message_id", msg.ID())
							if err := e.deadLetterSink.Write(ctx, msg); err != nil {
								e.logger.Error("Failed to write to DLQ", "workflow_id", e.workflowID, "error", err)
							}
						}

						// Release message as we won't process it further
						if dm, ok := msg.(*message.DefaultMessage); ok {
							message.ReleaseMessage(dm)
						}
						span.End()
						continue
					} else {
						msg.SetMetadata("schema_validated", "true")
					}
				}

				// Data Quality Scoring
				if e.dqScorer != nil {
					e.dqScorer.Score(ctx, e.workflowID, msg)
				}

				span.SetAttributes(
					attribute.String("message_id", msg.ID()),
					attribute.Int("payload_len", len(msg.Payload())),
				)

				e.recordSourceActivity()

				// Redact data for logging
				redactedData := e.redactData(msg.Data())

				e.logger.Info("Message received from source",
					"workflow_id", e.workflowID,
					"source_id", e.sourceID,
					"action", "read",
					"message_id", msg.ID(),
					"payload_len", len(msg.Payload()),
					"data", redactedData,
				)

				// Save to Outbox if enabled for 100% consistency
				if e.outboxStore != nil {
					outItem := hermod.OutboxItem{
						WorkflowID: e.workflowID,
						Payload:    msg.Payload(),
						Metadata:   msg.Metadata(),
						Status:     "pending",
						CreatedAt:  time.Now(),
					}
					// Add internal message ID to metadata for restoration
					if outItem.Metadata == nil {
						outItem.Metadata = make(map[string]string)
					}
					outItem.Metadata["_message_id"] = msg.ID()

					if err := e.outboxStore.CreateOutboxItem(ctx, outItem); err != nil {
						e.logger.Error("Failed to create outbox item", "workflow_id", e.workflowID, "message_id", msg.ID(), "error", err)
						// If we can't save to outbox, we continue with normal buffer produce,
						// but it's less reliable.
					} else {
						// If saved to outbox, we can Ack the source now if it supports it
						if err := e.source.Ack(ctx, msg); err != nil {
							e.logger.Warn("Failed to Ack source after outbox save", "workflow_id", e.workflowID, "message_id", msg.ID(), "error", err)
						}
					}
				}

				if err := e.buffer.Produce(ctx, msg); err != nil {
					span.RecordError(err)
					span.SetStatus(codes.Error, err.Error())
					span.End()

					if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
						e.logger.Error("Buffer produce error", "workflow_id", e.workflowID, "error", err)
						errCh <- fmt.Errorf("buffer produce error: %w", err)
					}
					return
				}
				span.End()
			}
		}
	}()

	// Wait for Source worker to finish then close buffer
	go func() {
		wg.Wait()
		// If buffer implements Closer, close it to signal Consumer that no more messages are coming
		if closer, ok := e.buffer.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				e.logger.Error("Error closing buffer", "workflow_id", e.workflowID, "error", err)
			}
		}
	}()

	// Buffer to Sink
	var sinkWg sync.WaitGroup
	sinkWg.Add(1)
	go func() {
		defer sinkWg.Done()
		consumer, ok := e.buffer.(hermod.Consumer)
		if !ok {
			e.logger.Error("Buffer does not implement Consumer interface", "workflow_id", e.workflowID)
			errCh <- fmt.Errorf("buffer does not implement Consumer interface")
			return
		}

		// Use a separate context for draining to ensure we don't get stuck if sink is slow,
		// but also allow it to finish even if main context is cancelled.
		drainCtx := context.Background()

		err := consumer.Consume(drainCtx, func(drainCtx context.Context, msg hermod.Message) error {
			e.checkpointMu.Lock()
			// Wait if we are in checkpointing process
			for e.inCheckpoint {
				e.checkpointMu.Unlock()
				time.Sleep(10 * time.Millisecond)
				e.checkpointMu.Lock()
			}
			e.checkpointMu.Unlock()

			e.inFlightSem <- struct{}{}
			e.inFlightWg.Add(1)

			go func(m hermod.Message) {
				ctx, span := tracer.Start(drainCtx, "ProcessMessage", trace.WithAttributes(
					attribute.String("workflow_id", e.workflowID),
					attribute.String("message_id", m.ID()),
				))
				defer span.End()

				defer func() {
					<-e.inFlightSem
					e.inFlightWg.Done()
					// Release the message back to the pool
					if dm, ok := m.(*message.DefaultMessage); ok {
						message.ReleaseMessage(dm)
					}
				}()

				if m == nil {
					return
				}

				start := time.Now()
				defer func() {
					duration := time.Since(start)
					ProcessingLatency.WithLabelValues(e.workflowID).Observe(duration.Seconds())
					e.adaptiveThrottle(drainCtx, duration)
				}()

				// Ensure message has an idempotency key/ID set before routing to sinks
				if key, _ := EnsureIdempotencyID(m); key == "" {
					IdempotencyMissingTotal.WithLabelValues(e.workflowID).Inc()
					if IdempotencyRequired() {
						e.logger.Warn("Missing idempotency key", "workflow_id", e.workflowID, "message_id", m.ID())
					}
				} else {
					IdempotencyKeysTotal.WithLabelValues(e.workflowID).Inc()
				}

				var routed []RoutedMessage
				if e.router != nil {
					var err error
					routed, err = e.router(ctx, m)
					if err != nil {
						span.RecordError(err)
						span.SetStatus(codes.Error, err.Error())
						e.logger.Error("Router error", "workflow_id", e.workflowID, "error", err)
						MessageErrors.WithLabelValues(e.workflowID, e.sourceID, "router").Inc()
						return
					}
				} else {
					// Default: send to all sinks
					routed = make([]RoutedMessage, len(e.sinks))
					for i := range e.sinks {
						routed[i] = RoutedMessage{SinkIndex: i, Message: m}
					}
				}

				if len(routed) == 0 {
					e.logger.Debug("Message filtered by router, skipping sinks", "workflow_id", e.workflowID, "message_id", m.ID())
				} else {
					var swg sync.WaitGroup
					serrCh := make(chan error, len(routed))
					for _, rm := range routed {
						if rm.SinkIndex < 0 || rm.SinkIndex >= len(e.sinkWriters) {
							continue
						}

						swg.Add(1)
						go func(i int, m hermod.Message) {
							defer swg.Done()
							sw := e.sinkWriters[i]
							pm := acquirePendingMessage(m)

							strategy := sw.config.BackpressureStrategy
							if strategy == "" {
								strategy = BPBlock
							}

							switch strategy {
							case BPDropOldest:
								select {
								case sw.ch <- pm:
									// Successfully queued
								default:
									// Channel is full, drop oldest
									select {
									case oldPm := <-sw.ch:
										if oldPm != nil {
											oldPm.done <- errors.New("dropped due to backpressure (drop_oldest)")
											releasePendingMessage(oldPm)
										}
										BackpressureDropTotal.WithLabelValues(e.workflowID, sw.sinkID, string(BPDropOldest)).Inc()
									default:
										// Should not happen as we just failed to send, but in case of race:
									}
									// Try sending again
									select {
									case sw.ch <- pm:
									default:
										// Still full? just drop this one then to avoid blocking
										pm.done <- errors.New("dropped due to backpressure (drop_oldest - overflow)")
										BackpressureDropTotal.WithLabelValues(e.workflowID, sw.sinkID, string(BPDropOldest)).Inc()
									}
								}
							case BPDropNewest:
								select {
								case sw.ch <- pm:
								default:
									pm.done <- errors.New("dropped due to backpressure (drop_newest)")
									BackpressureDropTotal.WithLabelValues(e.workflowID, sw.sinkID, string(BPDropNewest)).Inc()
								}
							case BPSampling:
								rate := sw.config.SamplingRate
								if rate <= 0 {
									rate = 0.5 // Default 50%
								}
								if rand.Float64() > rate {
									pm.done <- errors.New("dropped due to sampling")
									BackpressureDropTotal.WithLabelValues(e.workflowID, sw.sinkID, string(BPSampling)).Inc()
								} else {
									// Try to send, block if full
									select {
									case sw.ch <- pm:
									case <-ctx.Done():
										pm.done <- ctx.Err()
										return
									}
								}
							case BPSpillToDisk:
								select {
								case sw.ch <- pm:
									// Successfully queued in memory
								default:
									// Channel full, spill to disk
									if sw.spillBuffer != nil {
										if err := sw.spillBuffer.Produce(ctx, m); err != nil {
											pm.done <- fmt.Errorf("spill to disk failed: %w", err)
										} else {
											pm.done <- nil // Consider it successfully "handled" by spilling
											BackpressureSpillTotal.WithLabelValues(e.workflowID, sw.sinkID).Inc()
										}
									} else {
										// Fallback to block if spill buffer not available
										select {
										case sw.ch <- pm:
										case <-ctx.Done():
											pm.done <- ctx.Err()
											return
										}
									}
								}
							case BPBlock:
								fallthrough
							default:
								select {
								case sw.ch <- pm:
								case <-ctx.Done():
									pm.done <- ctx.Err()
									return
								}
							}

							select {
							case err := <-pm.done:
								if err != nil {
									serrCh <- err
								}
								releasePendingMessage(pm)
							case <-ctx.Done():
								serrCh <- ctx.Err()
								// We don't release pm here because it might still be in sw.ch and will be released by sw.run or after being pulled in BPDropOldest
							}
						}(rm.SinkIndex, rm.Message)
					}
					swg.Wait()
					close(serrCh)
					for err := range serrCh {
						if err != nil {
							span.RecordError(err)
							span.SetStatus(codes.Error, err.Error())
							MessageErrors.WithLabelValues(e.workflowID, e.sourceID, "sink").Inc()
							e.logger.Error("Sink write error", "workflow_id", e.workflowID, "error", err)
							return
						}
					}
				}

				// Acknowledge the message to the source after all successful sink writes
				if outboxID, exists := m.Metadata()["_outbox_id"]; exists && e.outboxStore != nil {
					if err := e.outboxStore.DeleteOutboxItem(ctx, outboxID); err != nil {
						e.logger.Error("Failed to delete outbox item after successful processing", "workflow_id", e.workflowID, "id", outboxID, "error", err)
					}
				} else if err := e.source.Ack(ctx, m); err != nil {
					span.RecordError(err)
					span.SetStatus(codes.Error, err.Error())
					e.logger.Error("Source acknowledgement failed", "workflow_id", e.workflowID, "error", err)
					MessageErrors.WithLabelValues(e.workflowID, e.sourceID, "ack").Inc()
					return
				}

				span.SetStatus(codes.Ok, "Processed")

				MessagesProcessed.WithLabelValues(e.workflowID, e.sourceID).Inc()

				e.statusMu.Lock()
				e.processedMessages++
				e.statusMu.Unlock()
			}(msg)

			return nil
		})
		e.inFlightWg.Wait()

		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			e.logger.Error("Buffer-to-Sink worker error", "workflow_id", e.workflowID, "error", err)
			errCh <- err
		} else {
			e.logger.Info("Buffer-to-Sink worker stopping", "workflow_id", e.workflowID)
		}
	}()

	sinkWg.Wait()
	for _, sw := range e.sinkWriters {
		if sw != nil && sw.ch != nil {
			close(sw.ch)
		}
	}
	writersWg.Wait()
	close(errCh)

	var lastErr error
	for err := range errCh {
		if err != nil {
			lastErr = err
		}
	}

	if lastErr != nil {
		e.logger.Error("Hermod Engine stopped with error", "workflow_id", e.workflowID, "error", lastErr)
		e.setStatus("Error: " + lastErr.Error())
		return lastErr
	}

	e.logger.Info("Hermod Engine stopped gracefully", "workflow_id", e.workflowID)
	e.setSourceStatus("")
	for _, id := range e.sinkIDs {
		e.setSinkStatus(id, "")
	}
	e.setStatus("Stopped")
	return nil
}

// Checkpoint performs a periodic state snapshot.
func (e *Engine) Checkpoint(ctx context.Context) error {
	if e.checkpointHandler == nil {
		return nil
	}

	e.checkpointMu.Lock()
	if e.inCheckpoint {
		e.checkpointMu.Unlock()
		return nil
	}
	e.inCheckpoint = true
	e.checkpointMu.Unlock()

	defer func() {
		e.checkpointMu.Lock()
		e.inCheckpoint = false
		e.checkpointMu.Unlock()
	}()

	e.logger.Info("Starting checkpoint", "workflow_id", e.workflowID)

	// Wait for all in-flight messages to be processed and acknowledged by sinks
	e.inFlightWg.Wait()

	// Collect source state if stateful
	var sourceState map[string]string
	if stateful, ok := e.source.(hermod.Stateful); ok {
		sourceState = stateful.GetState()
	}

	// Call the checkpoint handler (e.g. to save node states in Registry)
	if err := e.checkpointHandler(ctx, sourceState); err != nil {
		e.logger.Error("Checkpoint failed", "workflow_id", e.workflowID, "error", err)
		return err
	}

	e.logger.Info("Checkpoint completed successfully", "workflow_id", e.workflowID)
	return nil
}

// HardStop attempts to forcefully and quickly stop the engine by closing
// the source, sinks, and buffer. This is used as a last resort when the
// context cancellation path does not lead to timely shutdown.
func (e *Engine) HardStop() {
	e.stopMu.Lock()
	if e.stopped {
		e.stopMu.Unlock()
		return
	}
	e.stopped = true
	e.stopMu.Unlock()

	// Close buffer first to unblock consumers/producers
	if closer, ok := e.buffer.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
	// Close source to unblock any blocking Read
	if e.source != nil {
		_ = e.source.Close()
	}
	// Close sinks to unblock any pending writes
	for _, snk := range e.sinks {
		if snk != nil {
			_ = snk.Close()
		}
	}
}
