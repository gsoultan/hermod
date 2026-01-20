package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// DefaultLogger is a simple logger that uses zerolog for zero-allocation structured logging.
type DefaultLogger struct {
	logger zerolog.Logger
}

func NewDefaultLogger() *DefaultLogger {
	return &DefaultLogger{
		logger: zerolog.New(os.Stderr).With().Timestamp().Logger(),
	}
}

func (l *DefaultLogger) log(event *zerolog.Event, msg string, keysAndValues ...interface{}) {
	for i := 0; i < len(keysAndValues); i += 2 {
		key := fmt.Sprintf("%v", keysAndValues[i])
		if i+1 < len(keysAndValues) {
			event.Interface(key, keysAndValues[i+1])
		} else {
			event.Interface(key, nil)
		}
	}
	event.Msg(msg)
}

func (l *DefaultLogger) Debug(msg string, keysAndValues ...interface{}) {
	l.log(l.logger.Debug(), msg, keysAndValues...)
}

func (l *DefaultLogger) Info(msg string, keysAndValues ...interface{}) {
	l.log(l.logger.Info(), msg, keysAndValues...)
}

func (l *DefaultLogger) Warn(msg string, keysAndValues ...interface{}) {
	l.log(l.logger.Warn(), msg, keysAndValues...)
}

func (l *DefaultLogger) Error(msg string, keysAndValues ...interface{}) {
	l.log(l.logger.Error(), msg, keysAndValues...)
}

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
	nodeMetrics       map[string]uint64
	nodeSamples       map[string]interface{}
	nodeMetricsMu     sync.RWMutex

	// Async Sink Writers
	sinkWriters []*sinkWriter
}

type sinkWriter struct {
	engine *Engine
	sink   hermod.Sink
	sinkID string
	index  int
	config SinkConfig

	ch chan *pendingMessage
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
	WorkflowID     string                 `json:"workflow_id,omitempty"`
	EngineStatus   string                 `json:"engine_status,omitempty"`
	SourceStatus   string                 `json:"source_status,omitempty"`
	SourceID       string                 `json:"source_id,omitempty"`
	SinkStatuses   map[string]string      `json:"sink_statuses,omitempty"`
	SinkID         string                 `json:"sink_id,omitempty"`
	SinkStatus     string                 `json:"sink_status,omitempty"`
	ProcessedCount uint64                 `json:"processed_count"`
	NodeMetrics    map[string]uint64      `json:"node_metrics,omitempty"`
	NodeSamples    map[string]interface{} `json:"node_samples,omitempty"`
}

// Config holds configuration for the Engine.
type Config struct {
	MaxRetries        int           `json:"max_retries"`
	RetryInterval     time.Duration `json:"retry_interval"`
	ReconnectInterval time.Duration `json:"reconnect_interval"`
	StatusInterval    time.Duration `json:"status_interval"`
}

type SinkConfig struct {
	MaxRetries     int             `json:"max_retries"`
	RetryInterval  time.Duration   `json:"retry_interval"`
	RetryIntervals []time.Duration `json:"retry_intervals"`
	BatchSize      int             `json:"batch_size"`
	BatchTimeout   time.Duration   `json:"batch_timeout"`
}

type SourceConfig struct {
	ReconnectIntervals []time.Duration `json:"reconnect_intervals"`
}

// DefaultConfig returns the default configuration for the Engine.
func DefaultConfig() Config {
	return Config{
		MaxRetries:        3,
		RetryInterval:     100 * time.Millisecond,
		ReconnectInterval: 30 * time.Second,
		StatusInterval:    5 * time.Second,
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
	}
	e.SetLogger(e.logger)
	return e
}

// SetConfig sets the configuration for the engine.
func (e *Engine) SetConfig(config Config) {
	e.config = config
}

// SetSinkConfigs sets the per-sink configurations for the engine.
func (e *Engine) SetSinkConfigs(configs []SinkConfig) {
	e.sinkConfigs = configs
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
func (e *Engine) SetRouter(router RouterFunc) {
	e.router = router
}

// UpdateNodeMetric updates the processed count for a specific workflow node.
func (e *Engine) UpdateNodeMetric(nodeID string, count uint64) {
	e.nodeMetricsMu.Lock()
	if e.nodeMetrics == nil {
		e.nodeMetrics = make(map[string]uint64)
	}
	e.nodeMetrics[nodeID] += count
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

func (e *Engine) writeToSink(ctx context.Context, snk hermod.Sink, msg hermod.Message, sinkID string, i int) error {
	if msg == nil {
		return nil
	}
	// Retry mechanism for Sink Write
	var lastErr error

	maxRetries := e.config.MaxRetries
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
		e.logger.Info("Message written to sink",
			"workflow_id", e.workflowID,
			"sink_id", sinkID,
			"action", "write",
			"message_id", msg.ID(),
			"payload", string(msg.Payload()),
			"before", string(msg.Before()),
			"after", string(msg.After()),
		)
		lastErr = nil
		break
	}
	if lastErr != nil {
		e.logger.Error("Sink write failed after retries", "workflow_id", e.workflowID, "sink_id", sinkID, "error", lastErr)
		if e.deadLetterSink != nil {
			e.logger.Info("Sending message to Dead Letter Sink", "workflow_id", e.workflowID, "sink_id", sinkID, "message_id", msg.ID())
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

// GetStatus returns the current status of the engine.
func (e *Engine) GetStatus() StatusUpdate {
	e.statusMu.RLock()
	defer e.statusMu.RUnlock()

	update := StatusUpdate{
		WorkflowID:     e.workflowID,
		EngineStatus:   e.engineStatus,
		SourceStatus:   e.sourceStatus,
		SourceID:       e.sourceID,
		SinkStatuses:   make(map[string]string),
		ProcessedCount: e.processedMessages,
	}
	for k, v := range e.sinkStatuses {
		update.SinkStatuses[k] = v
	}

	e.nodeMetricsMu.RLock()
	if len(e.nodeMetrics) > 0 {
		update.NodeMetrics = make(map[string]uint64)
		for k, v := range e.nodeMetrics {
			update.NodeMetrics[k] = v
		}
	}
	if len(e.nodeSamples) > 0 {
		update.NodeSamples = make(map[string]interface{})
		for k, v := range e.nodeSamples {
			update.NodeSamples[k] = v
		}
	}
	e.nodeMetricsMu.RUnlock()

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
	if len(msgs) == 1 {
		return e.writeToSink(ctx, snk, msgs[0], sinkID, i)
	}

	// Retry mechanism for Sink WriteBatch
	var lastErr error

	maxRetries := e.config.MaxRetries
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

func (w *sinkWriter) run(ctx context.Context) {
	batchSize := w.config.BatchSize
	if batchSize < 1 {
		batchSize = 1
	}

	batchTimeout := w.config.BatchTimeout
	if batchTimeout == 0 {
		batchTimeout = 100 * time.Millisecond
	}

	batch := make([]*pendingMessage, 0, batchSize)
	ticker := time.NewTicker(batchTimeout)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
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
				}
			}
		}

		for _, pm := range batch {
			pm.done <- err
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case pm, ok := <-w.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, pm)
			if len(batch) >= batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// Start begins the data transfer process.
func (e *Engine) Start(ctx context.Context) error {
	// Initialize Sink Writers
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

		sw := &sinkWriter{
			engine: e,
			sink:   snk,
			sinkID: sinkID,
			index:  i,
			config: cfg,
			ch:     make(chan *pendingMessage, 1000),
		}
		e.sinkWriters[i] = sw
		go sw.run(ctx)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	e.logger.Info("Starting Hermod Engine", "workflow_id", e.workflowID)
	e.setStatus("connecting")
	ActiveEngines.Inc()
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
		for attempt := 1; attempt <= 3; attempt++ {
			if err := snk.Ping(ctx); err != nil {
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
				msg, err := e.source.Read(ctx)
				if err != nil {
					if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
						e.logger.Error("Source read error, attempting to reconnect", "workflow_id", e.workflowID, "error", err)
						e.setSourceStatus("reconnecting")
						e.setStatus("reconnecting:source")
						reconnectAttempts++
						continue // Go back to Ping loop
					}
					return
				}

				e.recordSourceActivity()

				if msg == nil {
					continue
				}

				e.logger.Info("Message received from source",
					"workflow_id", e.workflowID,
					"source_id", e.sourceID,
					"action", "read",
					"message_id", msg.ID(),
					"payload_len", len(msg.Payload()),
				)

				if err := e.buffer.Produce(ctx, msg); err != nil {
					if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
						e.logger.Error("Buffer produce error", "workflow_id", e.workflowID, "error", err)
						errCh <- fmt.Errorf("buffer produce error: %w", err)
					}
					return
				}
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

		// Semaphore to limit in-flight messages (backpressure)
		inFlightSem := make(chan struct{}, 1000)
		var inFlightWg sync.WaitGroup

		// Use a separate context for draining to ensure we don't get stuck if sink is slow,
		// but also allow it to finish even if main context is cancelled.
		drainCtx := context.Background()

		err := consumer.Consume(drainCtx, func(drainCtx context.Context, msg hermod.Message) error {
			inFlightSem <- struct{}{}
			inFlightWg.Add(1)

			go func(m hermod.Message) {
				defer func() {
					<-inFlightSem
					inFlightWg.Done()
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
					ProcessingLatency.WithLabelValues(e.workflowID).Observe(time.Since(start).Seconds())
				}()

				var routed []RoutedMessage
				if e.router != nil {
					var err error
					routed, err = e.router(drainCtx, m)
					if err != nil {
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

							select {
							case sw.ch <- pm:
								select {
								case err := <-pm.done:
									if err != nil {
										serrCh <- err
									}
									releasePendingMessage(pm)
								case <-ctx.Done():
									serrCh <- ctx.Err()
								}
							case <-ctx.Done():
								releasePendingMessage(pm)
								serrCh <- ctx.Err()
							}
						}(rm.SinkIndex, rm.Message)
					}
					swg.Wait()
					close(serrCh)
					for err := range serrCh {
						if err != nil {
							MessageErrors.WithLabelValues(e.workflowID, e.sourceID, "sink").Inc()
							e.logger.Error("Sink write error", "workflow_id", e.workflowID, "error", err)
							return
						}
					}
				}

				// Acknowledge the message to the source after all successful sink writes
				if err := e.source.Ack(drainCtx, m); err != nil {
					e.logger.Error("Source acknowledgement failed", "workflow_id", e.workflowID, "error", err)
					MessageErrors.WithLabelValues(e.workflowID, e.sourceID, "ack").Inc()
					return
				}

				MessagesProcessed.WithLabelValues(e.workflowID, e.sourceID).Inc()

				e.statusMu.Lock()
				e.processedMessages++
				e.statusMu.Unlock()
			}(msg)

			return nil
		})
		inFlightWg.Wait()

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
	close(errCh)

	var lastErr error
	for err := range errCh {
		if err != nil {
			lastErr = err
		}
	}

	if lastErr != nil {
		e.logger.Error("Hermod Engine stopped with error", "workflow_id", e.workflowID, "error", lastErr)
		return lastErr
	}

	e.logger.Info("Hermod Engine stopped gracefully", "workflow_id", e.workflowID)
	return nil
}
