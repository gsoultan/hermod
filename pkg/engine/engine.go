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

// Engine orchestrates the data flow from Source to Sinks.
type Engine struct {
	source               hermod.Source
	sinks                []hermod.Sink
	buffer               hermod.Producer // Using Producer as a buffer
	logger               hermod.Logger
	config               Config
	sinkConfigs          []SinkConfig
	sourceConfig         SourceConfig
	transforms           hermod.Transformer
	transformationGroups []TransformationGroup
	groupsMu             sync.RWMutex
	deadLetterSink       hermod.Sink

	connectionID string
	sourceID     string
	sinkIDs      []string
	sinkTypes    []string

	onStatusChange func(StatusUpdate)

	// Internal state tracking
	statusMu          sync.RWMutex
	sourceStatus      string
	sinkStatuses      map[string]string
	engineStatus      string
	lastMsgTime       time.Time
	processedMessages uint64
}

type StatusUpdate struct {
	ConnectionID   string            `json:"connection_id,omitempty"`
	EngineStatus   string            `json:"engine_status,omitempty"`
	SourceStatus   string            `json:"source_status,omitempty"`
	SourceID       string            `json:"source_id,omitempty"`
	SinkStatuses   map[string]string `json:"sink_statuses,omitempty"`
	SinkID         string            `json:"sink_id,omitempty"`
	SinkStatus     string            `json:"sink_status,omitempty"`
	ProcessedCount uint64            `json:"processed_count"`
}

type TransformationGroup struct {
	Transformer hermod.Transformer
	Sinks       []hermod.Sink
	SinkIDs     []string
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
}

type SourceConfig struct {
	ReconnectInterval  time.Duration   `json:"reconnect_interval"`
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
	return &Engine{
		source:       source,
		sinks:        sinks,
		buffer:       buffer,
		logger:       NewDefaultLogger(),
		config:       DefaultConfig(),
		sinkStatuses: make(map[string]string),
	}
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
}

// SetTransformations sets the transformations to be applied to messages.
func (e *Engine) SetTransformations(t hermod.Transformer) {
	e.transforms = t
}

// SetTransformationGroups sets the transformation groups for the engine.
func (e *Engine) SetTransformationGroups(groups []TransformationGroup) {
	e.groupsMu.Lock()
	defer e.groupsMu.Unlock()
	e.transformationGroups = groups
}

// SetDeadLetterSink sets the dead letter sink for the engine.
func (e *Engine) SetDeadLetterSink(sink hermod.Sink) {
	e.deadLetterSink = sink
}

// SetIDs sets the IDs for connection, source and sinks.
func (e *Engine) SetIDs(connectionID string, sourceID string, sinkIDs []string) {
	e.connectionID = connectionID
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
			SinkWriteErrors.WithLabelValues(e.connectionID, sinkID).Inc()
			e.setSinkStatus(sinkID, "reconnecting")
			e.setStatus("reconnecting:sink:" + sinkID)
			e.logger.Warn("Sink write error, retrying", "connection_id", e.connectionID, "attempt", j+1, "sink_id", sinkID, "error", err)

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
			e.logger.Info("Sink reconnected successfully", "connection_id", e.connectionID, "sink_id", sinkID, "action", "reconnect")
		}
		SinkWriteCount.WithLabelValues(e.connectionID, sinkID).Inc()
		e.logger.Info("Message written to sink",
			"connection_id", e.connectionID,
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
		e.logger.Error("Sink write failed after retries", "connection_id", e.connectionID, "sink_id", sinkID, "error", lastErr)
		if e.deadLetterSink != nil {
			e.logger.Info("Sending message to Dead Letter Sink", "connection_id", e.connectionID, "sink_id", sinkID, "message_id", msg.ID())
			DeadLetterCount.WithLabelValues(e.connectionID, sinkID).Inc()
			if err := e.deadLetterSink.Write(ctx, msg); err != nil {
				e.logger.Error("Failed to write to Dead Letter Sink", "connection_id", e.connectionID, "error", err)
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
		ConnectionID:   e.connectionID,
		EngineStatus:   e.engineStatus,
		SourceStatus:   e.sourceStatus,
		SourceID:       e.sourceID,
		SinkStatuses:   make(map[string]string),
		ProcessedCount: e.processedMessages,
	}
	for k, v := range e.sinkStatuses {
		update.SinkStatuses[k] = v
	}
	return update
}

// Start begins the data transfer process.
func (e *Engine) Start(ctx context.Context) error {
	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	e.logger.Info("Starting Hermod Engine", "connection_id", e.connectionID)
	e.setStatus("connecting")
	ActiveEngines.Inc()
	defer ActiveEngines.Dec()

	// Status Checker
	go func() {
		interval := e.config.StatusInterval
		if interval == 0 {
			interval = 5 * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := e.source.Ping(ctx); err != nil {
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
						e.logger.Info("System reconnected successfully", "connection_id", e.connectionID, "action", "reconnect")
					}
					e.setStatus("running")
				}
			}
		}
	}()

	// Pre-flight checks for Sinks (Sink failure turns off connection)
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
				e.logger.Warn("Sink ping failed during startup", "connection_id", e.connectionID, "sink_id", sinkID, "attempt", attempt, "error", err)
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
				if err := e.source.Ping(ctx); err != nil {
					e.setSourceStatus("reconnecting")
					e.setStatus("reconnecting:source")
					e.logger.Warn("Source ping failed, entering reconnecting state", "connection_id", e.connectionID, "error", err)

					var interval time.Duration
					if len(e.sourceConfig.ReconnectIntervals) > 0 {
						if reconnectAttempts < len(e.sourceConfig.ReconnectIntervals) {
							interval = e.sourceConfig.ReconnectIntervals[reconnectAttempts]
						} else {
							interval = e.sourceConfig.ReconnectIntervals[len(e.sourceConfig.ReconnectIntervals)-1]
						}
					} else if e.sourceConfig.ReconnectInterval > 0 {
						interval = e.sourceConfig.ReconnectInterval
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
					e.logger.Info("Source reconnected successfully", "connection_id", e.connectionID, "source_id", e.sourceID, "action", "reconnect")
				}
				e.setStatus("running")
			}

			select {
			case <-ctx.Done():
				e.logger.Info("Source-to-Buffer worker stopping due to context cancellation", "connection_id", e.connectionID)
				return
			default:
				msg, err := e.source.Read(ctx)
				if err != nil {
					if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
						e.logger.Error("Source read error, attempting to reconnect", "connection_id", e.connectionID, "error", err)
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
					"connection_id", e.connectionID,
					"source_id", e.sourceID,
					"action", "read",
					"message_id", msg.ID(),
					"table", msg.Table(),
					"operation", msg.Operation(),
					"payload", string(msg.Payload()),
					"before", string(msg.Before()),
					"after", string(msg.After()),
				)

				originalID := msg.ID()
				if e.transforms != nil {
					msg, err = e.transforms.Transform(ctx, msg)
					if err != nil {
						e.logger.Error("Transformation error", "connection_id", e.connectionID, "error", err)
						MessageErrors.WithLabelValues(e.connectionID, e.sourceID, "transform").Inc()
						// Continue or stop? Let's stop for safety if transformation fails
						errCh <- fmt.Errorf("transformation error: %w", err)
						return
					}
					if msg == nil {
						MessagesFiltered.WithLabelValues(e.connectionID, e.sourceID).Inc()
						e.logger.Info("Message filtered out by transformation",
							"connection_id", e.connectionID,
							"source_id", e.sourceID,
							"action", "filter",
							"message_id", originalID,
						)
						continue
					}
					e.logger.Info("Message transformed",
						"connection_id", e.connectionID,
						"source_id", e.sourceID,
						"action", "transform",
						"message_id", msg.ID(),
						"payload", string(msg.Payload()),
						"before", string(msg.Before()),
						"after", string(msg.After()),
					)
				}
				if err := e.buffer.Produce(ctx, msg); err != nil {
					if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
						e.logger.Error("Buffer produce error", "connection_id", e.connectionID, "error", err)
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
				e.logger.Error("Error closing buffer", "connection_id", e.connectionID, "error", err)
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
			e.logger.Error("Buffer does not implement Consumer interface", "connection_id", e.connectionID)
			errCh <- fmt.Errorf("buffer does not implement Consumer interface")
			return
		}

		// Use a separate context for draining to ensure we don't get stuck if sink is slow,
		// but also allow it to finish even if main context is cancelled.
		drainCtx := context.Background()

		err := consumer.Consume(drainCtx, func(drainCtx context.Context, msg hermod.Message) error {
			start := time.Now()
			defer func() {
				ProcessingLatency.WithLabelValues(e.connectionID).Observe(time.Since(start).Seconds())
			}()

			e.groupsMu.RLock()
			groups := e.transformationGroups
			e.groupsMu.RUnlock()

			// Map for tracking sink index for configs
			sinkIndexMap := make(map[string]int)
			for i, id := range e.sinkIDs {
				sinkIndexMap[id] = i
			}

			// If no groups defined, use default behavior (all sinks receive the message)
			if len(groups) == 0 {
				var swg sync.WaitGroup
				serrCh := make(chan error, len(e.sinks))
				for i, snk := range e.sinks {
					sinkID := ""
					if i < len(e.sinkIDs) {
						sinkID = e.sinkIDs[i]
					}
					swg.Add(1)
					go func(snk hermod.Sink, sinkID string, i int) {
						defer swg.Done()
						if err := e.writeToSink(drainCtx, snk, msg, sinkID, i); err != nil {
							serrCh <- err
						}
					}(snk, sinkID, i)
				}
				swg.Wait()
				close(serrCh)
				for err := range serrCh {
					if err != nil {
						MessageErrors.WithLabelValues(e.connectionID, e.sourceID, "sink").Inc()
						return err
					}
				}
			} else {
				var swg sync.WaitGroup
				serrCh := make(chan error, len(groups))
				for _, group := range groups {
					swg.Add(1)
					go func(group TransformationGroup) {
						defer swg.Done()
						groupMsg := msg
						if group.Transformer != nil {
							var err error
							groupMsg = msg.Clone()
							groupMsg, err = group.Transformer.Transform(drainCtx, groupMsg)
							if err != nil {
								e.logger.Error("Group transformation error", "connection_id", e.connectionID, "error", err)
								MessageErrors.WithLabelValues(e.connectionID, e.sourceID, "group_transform").Inc()
								serrCh <- fmt.Errorf("group transformation error: %w", err)
								return
							}
							if groupMsg == nil {
								MessagesFiltered.WithLabelValues(e.connectionID, e.sourceID).Inc()
								return
							}
						}

						var sswg sync.WaitGroup
						sserrCh := make(chan error, len(group.Sinks))
						for i, snk := range group.Sinks {
							sinkID := group.SinkIDs[i]
							sinkIdx := sinkIndexMap[sinkID]
							sswg.Add(1)
							go func(snk hermod.Sink, sinkID string, sinkIdx int) {
								defer sswg.Done()
								if err := e.writeToSink(drainCtx, snk, groupMsg, sinkID, sinkIdx); err != nil {
									sserrCh <- err
								}
							}(snk, sinkID, sinkIdx)
						}
						sswg.Wait()
						close(sserrCh)
						for err := range sserrCh {
							if err != nil {
								serrCh <- err
								return
							}
						}
					}(group)
				}
				swg.Wait()
				close(serrCh)
				for err := range serrCh {
					if err != nil {
						MessageErrors.WithLabelValues(e.connectionID, e.sourceID, "sink").Inc()
						return err
					}
				}
			}

			// Acknowledge the message to the source after all successful sink writes
			if err := e.source.Ack(drainCtx, msg); err != nil {
				e.logger.Error("Source acknowledgement failed", "connection_id", e.connectionID, "error", err)
				MessageErrors.WithLabelValues(e.connectionID, e.sourceID, "ack").Inc()
				return fmt.Errorf("source acknowledgement failed: %w", err)
			}

			MessagesProcessed.WithLabelValues(e.connectionID, e.sourceID).Inc()

			e.statusMu.Lock()
			e.processedMessages++
			e.statusMu.Unlock()
			e.notifyStatusChange()

			return nil
		})
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			e.logger.Error("Buffer-to-Sink worker error", "connection_id", e.connectionID, "error", err)
			errCh <- err
		} else {
			e.logger.Info("Buffer-to-Sink worker stopping", "connection_id", e.connectionID)
		}
	}()

	sinkWg.Wait()
	close(errCh)

	var lastErr error
	for err := range errCh {
		if err != nil {
			lastErr = err
		}
	}

	if lastErr != nil {
		e.logger.Error("Hermod Engine stopped with error", "connection_id", e.connectionID, "error", lastErr)
		return lastErr
	}

	e.logger.Info("Hermod Engine stopped gracefully", "connection_id", e.connectionID)
	return nil
}
