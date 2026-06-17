package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/engine/config"
	"github.com/user/hermod/pkg/engine/idempotency"
	"github.com/user/hermod/pkg/engine/source"
	"github.com/user/hermod/pkg/engine/telemetry"
)

type Runner struct {
	engine *Engine
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
	errCh  chan error
}

func NewRunner(e *Engine) *Runner {
	return &Runner{
		engine: e,
		errCh:  make(chan error, 2),
	}
}

func (r *Runner) Start(ctx context.Context) (err error) {
	r.ctx, r.cancel = context.WithCancel(ctx)

	// Isolate the workflow: a panic in any synchronous part of the engine
	// (e.g. the source ingestion loop) must never crash the worker process or
	// affect other workflows. Recover here, convert the panic into an error,
	// and cancel the engine context so background goroutines unwind cleanly.
	defer func() {
		if rec := recover(); rec != nil {
			r.engine.logger.Error("Panic in engine runner",
				"workflow_id", r.engine.workflowID,
				"panic", rec,
				"stack", string(debug.Stack()))
			r.engine.setStatus(fmt.Sprintf("Error: panic: %v", rec))
			if r.cancel != nil {
				r.cancel()
			}
			err = fmt.Errorf("engine panic: %v", rec)
		}
	}()

	// Initialize Priority Source if enabled
	if r.engine.config.PrioritizeDLQ && r.engine.deadLetterSink != nil {
		if dlqSource, ok := r.engine.deadLetterSink.(hermod.Source); ok {
			r.engine.logger.Info("DLQ Priority enabled: wrapping source with PriorityMultiplexer", "workflow_id", r.engine.workflowID)
			r.engine.source = source.NewPrioritySource(dlqSource, r.engine.source, r.engine.logger)
		}
	}

	// Initialize Sink Writers
	var writersWg sync.WaitGroup
	r.engine.sinkWriters = make([]*sinkWriter, len(r.engine.sinks))
	for i, snk := range r.engine.sinks {
		sinkID := ""
		if i < len(r.engine.sinkIDs) {
			sinkID = r.engine.sinkIDs[i]
		}

		cfg := config.SinkConfig{}
		if i < len(r.engine.sinkConfigs) {
			cfg = r.engine.sinkConfigs[i]
		}

		bufferCap := cfg.BackpressureBuffer
		if bufferCap <= 0 {
			bufferCap = 1000
		}

		sw := &sinkWriter{
			engine:           r.engine,
			sink:             snk,
			sinkID:           sinkID,
			index:            i,
			config:           cfg,
			ch:               make(chan *pendingMessage, bufferCap),
			currentBatchSize: cfg.BatchSize,
		}
		// Initialize sharding if configured
		if cfg.ShardCount > 1 {
			sw.useShards = true
			sw.shardCount = cfg.ShardCount
			sw.shardKeyMeta = cfg.ShardKeyMeta
			sw.shards = make([]chan *pendingMessage, cfg.ShardCount)
			for si := 0; si < cfg.ShardCount; si++ {
				sw.shards[si] = make(chan *pendingMessage, bufferCap)
			}
		}
		r.engine.sinkWriters[i] = sw
		writersWg.Go(func() {
			sw.run(r.ctx)
		})
	}

	r.engine.logger.Info("Starting Hermod Engine", "workflow_id", r.engine.workflowID)
	r.engine.setStatus("connecting")
	telemetry.ActiveEngines.Inc()

	// Start Outbox Relay if enabled
	if r.engine.outboxStore != nil {
		r.wg.Go(func() {
			defer func() {
				if err := recover(); err != nil {
					r.engine.logger.Error("Panic in Outbox Relay", "error", err, "stack", string(debug.Stack()))
				}
			}()
			r.engine.runOutboxRelay(r.ctx)
		})
	}

	defer telemetry.ActiveEngines.Dec()

	// Status Checker
	r.wg.Go(func() {
		defer func() {
			if err := recover(); err != nil {
				r.engine.logger.Error("Panic in Status Checker", "error", err, "stack", string(debug.Stack()))
			}
		}()
		interval := r.engine.config.StatusInterval
		if interval == 0 {
			interval = 1 * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-r.ctx.Done():
				return
			case <-ticker.C:
				r.checkHealth(interval)
			}
		}
	})

	// Periodic Checkpointing
	if r.engine.config.CheckpointInterval > 0 {
		r.wg.Go(func() {
			defer func() {
				if err := recover(); err != nil {
					r.engine.logger.Error("Panic in Checkpointing", "error", err, "stack", string(debug.Stack()))
				}
			}()
			ticker := time.NewTicker(r.engine.config.CheckpointInterval)
			defer ticker.Stop()
			for {
				select {
				case <-r.ctx.Done():
					return
				case <-ticker.C:
					checkpointCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					_ = r.engine.Checkpoint(checkpointCtx)
					cancel()
				}
			}
		})
	}

	// Main Loops: Ingestion and Processing
	var sinkWg sync.WaitGroup
	sinkWg.Go(func() {
		r.runBufferToSink(r.ctx, &sinkWg)
	})

	r.runSourceToBuffer(r.ctx)

	sinkWg.Wait()
	for _, sw := range r.engine.sinkWriters {
		if sw != nil {
			if sw.useShards {
				for _, ch := range sw.shards {
					if ch != nil {
						close(ch)
					}
				}
			} else if sw.ch != nil {
				close(sw.ch)
			}
		}
	}

	// Drain sink writers
	if r.engine.config.DrainTimeout > 0 {
		done := make(chan struct{})
		go func() {
			writersWg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(r.engine.config.DrainTimeout):
			r.engine.logger.Warn("Sink writers draining exceeded drain_timeout; still waiting", "workflow_id", r.engine.workflowID, "timeout", r.engine.config.DrainTimeout.String())
			<-done
		}
	} else {
		writersWg.Wait()
	}
	close(r.errCh)

	// Final checkpoint
	if r.engine.checkpointHandler != nil {
		checkpointCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = r.engine.Checkpoint(checkpointCtx)
		cancel()
	}

	var lastErr error
	for err := range r.errCh {
		if err != nil {
			lastErr = err
		}
	}

	if lastErr != nil {
		r.engine.logger.Error("Hermod Engine stopped with error", "workflow_id", r.engine.workflowID, "error", lastErr)
		r.engine.setStatus("Error: " + lastErr.Error())
		return lastErr
	}

	r.engine.logger.Info("Hermod Engine stopped gracefully", "workflow_id", r.engine.workflowID)
	r.engine.setSourceStatus("")
	for _, id := range r.engine.sinkIDs {
		r.engine.setSinkStatus(id, "")
	}
	r.engine.setStatus("Stopped")
	return nil
}

func (r *Runner) checkHealth(interval time.Duration) {
	var err error
	if readyChecker, ok := r.engine.source.(hermod.ReadyChecker); ok {
		err = readyChecker.IsReady(r.ctx)
	} else {
		err = r.engine.source.Ping(r.ctx)
	}

	if err != nil {
		r.engine.logger.Error("Background source health check failed", "workflow_id", r.engine.workflowID, "error", err.Error())
		lastMsgTime := r.engine.statusTracker.GetLastMsgTime()
		recentActivity := !lastMsgTime.IsZero() && time.Since(lastMsgTime) < interval*2

		if !recentActivity {
			r.engine.setSourceStatus("reconnecting")
			r.engine.setStatus("reconnecting:source")
		}
	} else {
		r.engine.setSourceStatus("running")
	}

	allSinksOk := true
	for i, snk := range r.engine.sinks {
		sinkID := ""
		if i < len(r.engine.sinkIDs) {
			sinkID = r.engine.sinkIDs[i]
		}
		if err := snk.Ping(r.ctx); err != nil {
			r.engine.logger.Error("Background sink health check failed", "workflow_id", r.engine.workflowID, "sink_id", sinkID, "error", err.Error())
			r.engine.setSinkStatus(sinkID, "reconnecting")
			if allSinksOk {
				r.engine.setStatus("reconnecting:sink:" + sinkID)
			}
			allSinksOk = false
		} else {
			r.engine.setSinkStatus(sinkID, "running")
		}
	}

	srcStatus, _, engStatus, _, _, _, _ := r.engine.statusTracker.GetStatus()
	if allSinksOk && srcStatus == "running" {
		if engStatus != "running" && strings.HasPrefix(engStatus, "reconnecting") {
			r.engine.logger.Info("System reconnected successfully", "workflow_id", r.engine.workflowID, "action", "reconnect")
		}
		r.engine.setStatus("running")
	}
}

func (r *Runner) runSourceToBuffer(ctx context.Context) {
	reconnectAttempts := 0
	for {
		// Check source connection
		r.engine.mu.RLock()
		interval := r.engine.config.StatusInterval
		if interval == 0 {
			interval = 5 * time.Second
		}
		lastMsgTime := r.engine.statusTracker.GetLastMsgTime()
		needsPing := reconnectAttempts > 0 || lastMsgTime.IsZero() || time.Since(lastMsgTime) > interval
		r.engine.mu.RUnlock()

		if needsPing {
			var err error
			if readyChecker, ok := r.engine.source.(hermod.ReadyChecker); ok {
				err = readyChecker.IsReady(ctx)
			} else {
				err = r.engine.source.Ping(ctx)
			}

			if err != nil {
				r.engine.setSourceStatus("reconnecting")
				if reconnectAttempts == 0 {
					r.engine.logger.Warn("Source disconnected, entering reconnect loop", "workflow_id", r.engine.workflowID)
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
		r.engine.setSourceStatus("running")
		_, _, engStatus, _, _, _, _ := r.engine.statusTracker.GetStatus()
		if engStatus == "reconnecting:source" || engStatus == "connecting" {
			if engStatus == "reconnecting:source" {
				r.engine.logger.Info("Source reconnected successfully", "workflow_id", r.engine.workflowID, "source_id", r.engine.sourceID, "action", "reconnect")
			}
			r.engine.setStatus("running")
		}

		select {
		case <-ctx.Done():
			return
		default:
			r.engine.checkpointMu.Lock()
			for r.engine.inCheckpoint {
				r.engine.checkpointMu.Unlock()
				time.Sleep(10 * time.Millisecond)
				r.engine.checkpointMu.Lock()
			}
			r.engine.checkpointMu.Unlock()

			m, err := r.engine.source.Read(ctx)
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					r.engine.logger.Error("Source read error", "workflow_id", r.engine.workflowID, "error", err)
					r.engine.setSourceStatus("error")
				}
				continue
			}

			if m == nil {
				continue
			}

			r.engine.recordSourceActivity()

			if err := r.engine.buffer.Produce(ctx, m); err != nil {
				r.engine.logger.Error("Failed to write message to buffer", "workflow_id", r.engine.workflowID, "error", err)
			}
		}
	}
}

func (r *Runner) runBufferToSink(ctx context.Context, sinkWg *sync.WaitGroup) {
	if consumer, ok := r.engine.buffer.(hermod.Consumer); ok {
		err := consumer.Consume(ctx, func(drainCtx context.Context, m hermod.Message) error {
			// Acquire inflight slot
			select {
			case r.engine.inFlightSem <- struct{}{}:
			case <-drainCtx.Done():
				return drainCtx.Err()
			}

			r.engine.inFlightWg.Add(1)
			go func() {
				defer func() {
					if p := recover(); p != nil {
						r.engine.logger.Error("Panic in message processing loop", "workflow_id", r.engine.workflowID, "panic", p, "stack", string(debug.Stack()))
					}
					<-r.engine.inFlightSem
					r.engine.inFlightWg.Done()
				}()

				start := time.Now()
				defer func() {
					duration := time.Since(start)
					telemetry.ProcessingLatency.WithLabelValues(r.engine.workflowID).Observe(duration.Seconds())
					r.engine.adaptiveThrottle(drainCtx, duration)
					if r.engine.DetectAnomaly(duration) {
						r.engine.logger.Warn("Anomaly detected in message processing", "workflow_id", r.engine.workflowID, "message_id", m.ID(), "duration", duration.String())
						m.SetMetadata("anomaly", "true")
						m.SetMetadata("anomaly_reason", "latency_spike")
					}
				}()

				// Ensure message has an idempotency key/ID set before routing to sinks
				if key, _ := idempotency.EnsureIdempotencyID(m); key == "" {
					telemetry.IdempotencyMissingTotal.WithLabelValues(r.engine.workflowID).Inc()
				}

				// Global workflow tracing
				if r.engine.traceRecorder != nil {
					r.engine.RecordTraceStep(drainCtx, m, "workflow_start", start, nil, nil)
				}

				// Data validation
				if r.engine.validator != nil {
					vstart := time.Now()
					if err := r.engine.validator.Validate(drainCtx, m.Data()); err != nil {
						r.engine.logger.Error("Message validation failed", "workflow_id", r.engine.workflowID, "message_id", m.ID(), "error", err)
						r.engine.UpdateNodeErrorMetric("validator", 1)
						r.engine.RecordTraceStep(drainCtx, m, "validator", vstart, nil, err)

						if r.engine.deadLetterSink != nil {
							m.SetMetadata("_hermod_validation_failed", "true")
							m.SetMetadata("_hermod_last_error", err.Error())
							_ = r.engine.deadLetterSink.Write(drainCtx, m)
							r.engine.statusTracker.IncDeadLetter()
						}
						return
					}
					r.engine.UpdateNodeMetric("validator", 1)
					r.engine.RecordTraceStep(drainCtx, m, "validator", vstart, nil, nil)
				}

				// Routing
				var targets []RoutedMessage
				if r.engine.router != nil {
					rstart := time.Now()
					t, err := r.engine.router(drainCtx, m)
					if err != nil {
						r.engine.logger.Error("Routing failed", "workflow_id", r.engine.workflowID, "message_id", m.ID(), "error", err)
						r.engine.RecordTraceStep(drainCtx, m, "router", rstart, nil, err)
						return
					}
					targets = t
					r.engine.RecordTraceStep(drainCtx, m, "router", rstart, nil, nil)
				} else {
					// Default: route to all sinks
					targets = make([]RoutedMessage, len(r.engine.sinks))
					for i := range r.engine.sinks {
						targets[i] = RoutedMessage{SinkIndex: i, Message: m}
					}
				}

				if len(targets) == 0 {
					// Even if filtered, we must acknowledge to prevent re-reading
					if outboxID, exists := m.Metadata()["_outbox_id"]; exists && r.engine.outboxStore != nil {
						_ = r.engine.outboxStore.DeleteOutboxItem(drainCtx, outboxID)
					} else {
						_ = r.engine.source.Ack(drainCtx, m)
					}
					r.engine.statusTracker.IncProcessed()
					return
				}

				// Concurrent writes to multiple sinks
				var swg sync.WaitGroup
				serrCh := make(chan error, len(targets))

				for _, target := range targets {
					if target.SinkIndex < 0 || target.SinkIndex >= len(r.engine.sinkWriters) {
						continue
					}

					sw := r.engine.sinkWriters[target.SinkIndex]
					swg.Add(1)
					target.Message.Retain()
					pm := acquirePendingMessage(target.Message)
					go func(sw *sinkWriter, pm *pendingMessage) {
						defer func() {
							if p := recover(); p != nil {
								r.engine.logger.Error("Panic in concurrent sink write", "workflow_id", r.engine.workflowID, "sink_id", sw.sinkID, "panic", p)
							}
							swg.Done()
						}()
						sw.enqueueWithStrategy(drainCtx, pm, sw.config.BackpressureStrategy)
						select {
						case err := <-pm.done:
							if err != nil {
								serrCh <- err
							}
							releasePendingMessage(pm)
						case <-drainCtx.Done():
							// NOTE: do not release pm here. On cancellation the
							// sink writer may still own this pending message (it
							// can be sitting in the sink channel or an in-flight
							// batch), so releasing it would return the pooled
							// object while still in use. The owning sink writer
							// drains and signals pm.done during shutdown.
							serrCh <- drainCtx.Err()
						}
					}(sw, pm)
				}
				swg.Wait()
				close(serrCh)
				m.Release()
				for err := range serrCh {
					if err != nil {
						r.engine.logger.Error("Sink write error", "workflow_id", r.engine.workflowID, "error", err)
						return
					}
				}

				// Acknowledge the message to the source after all successful sink writes
				if outboxID, exists := m.Metadata()["_outbox_id"]; exists && r.engine.outboxStore != nil {
					if err := r.engine.outboxStore.DeleteOutboxItem(drainCtx, outboxID); err != nil {
						r.engine.logger.Error("Failed to delete outbox item", "workflow_id", r.engine.workflowID, "id", outboxID, "error", err)
					}
				} else if err := r.engine.source.Ack(drainCtx, m); err != nil {
					r.engine.logger.Error("Source acknowledgement failed", "workflow_id", r.engine.workflowID, "error", err)
					return
				}

				telemetry.MessagesProcessed.WithLabelValues(r.engine.workflowID, r.engine.sourceID).Inc()
				r.engine.statusTracker.IncProcessed()
			}()
			return nil
		})
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			r.engine.logger.Error("Buffer-to-Sink worker error", "workflow_id", r.engine.workflowID, "error", err)
			r.errCh <- err
		}
	}
}
