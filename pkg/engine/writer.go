package engine

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/buffer"
	"github.com/user/hermod/pkg/engine/config"
	"github.com/user/hermod/pkg/engine/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type sinkWriter struct {
	engine *Engine
	sink   hermod.Sink
	sinkID string
	index  int
	config config.SinkConfig

	ch chan *pendingMessage
	// Optional sharding for per-key ordering with parallelism
	useShards    bool
	shardCount   int
	shardKeyMeta string
	shards       []chan *pendingMessage
	shardWg      sync.WaitGroup

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
	msg      hermod.Message
	done     chan error
	released atomic.Bool
}

var pendingMessagePool = sync.Pool{
	New: func() any {
		return &pendingMessage{
			done: make(chan error, 1),
		}
	},
}

func acquirePendingMessage(msg hermod.Message) *pendingMessage {
	pm := pendingMessagePool.Get().(*pendingMessage)
	pm.msg = msg
	pm.released.Store(false)
	return pm
}

func releasePendingMessage(pm *pendingMessage) {
	// Guard against double-release: a pendingMessage has a single owner (the
	// goroutine that selects on pm.done). Backpressure eviction must never
	// release a message owned by another goroutine, but we keep this guard so a
	// stray double-release can never corrupt the pool or drive the message
	// refcount below zero.
	if !pm.released.CompareAndSwap(false, true) {
		return
	}
	if pm.msg != nil {
		pm.msg.Release()
	}
	pm.msg = nil
	// Reset the done channel by reading if it has anything (should be empty though)
	select {
	case <-pm.done:
	default:
	}
	pendingMessagePool.Put(pm)
}

func (e *Engine) writeToSink(ctx context.Context, snk hermod.Sink, msg hermod.Message, sinkID string, i int) error {
	// Trace single write
	var span trace.Span
	ctx, span = tracer.Start(ctx, "sink.write", trace.WithAttributes(
		attribute.String("workflow_id", e.workflowID),
		attribute.String("sink_id", sinkID),
		attribute.String("message_id", func() string {
			if msg != nil {
				return msg.ID()
			}
			return ""
		}()),
	))
	defer span.End()
	if msg == nil {
		return nil
	}

	if e.isFailing() {
		return fmt.Errorf("simulated engine failure")
	}

	if e.IsSafeMode() && e.deadLetterSink != nil {
		e.logger.Warn("Safe Mode Active: diverting message to Dead Letter Sink", "workflow_id", e.workflowID, "sink_id", sinkID, "message_id", msg.ID())
		msg.SetMetadata("_hermod_safe_mode", "true")
		msg.SetMetadata("_hermod_original_sink", sinkID)
		e.statusTracker.IncDeadLetter()
		telemetry.DeadLetterCount.WithLabelValues(e.workflowID, sinkID).Inc()
		if err := e.deadLetterSink.Write(ctx, msg); err != nil {
			e.logger.Error("Failed to write to Dead Letter Sink in Safe Mode", "workflow_id", e.workflowID, "error", err)
			return fmt.Errorf("safe mode diversion failed: %w", err)
		}
		return nil
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
			telemetry.SinkWriteErrors.WithLabelValues(e.workflowID, sinkID).Inc()
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
				span.RecordError(ctx.Err())
				span.SetStatus(codes.Error, ctx.Err().Error())
				return ctx.Err()
			}
		}
		if j > 0 {
			e.logger.Info("Sink reconnected successfully", "workflow_id", e.workflowID, "sink_id", sinkID, "action", "reconnect")
		}
		telemetry.SinkWriteCount.WithLabelValues(e.workflowID, sinkID).Inc()
		// Record observed latency for the sink write path (captures idempotency checks when present)
		telemetry.IdempotencyLatency.WithLabelValues(e.workflowID, sinkID).Observe(time.Since(idempStart).Seconds())
		// If sink reports idempotency effect, emit metrics
		if reporter, ok := snk.(hermod.IdempotencyReporter); ok {
			if dedup, conflict := reporter.LastWriteIdempotent(); dedup || conflict {
				if dedup {
					telemetry.IdempotencyDedupTotal.WithLabelValues(e.workflowID, sinkID).Inc()
				}
				if conflict {
					telemetry.IdempotencyConflictsTotal.WithLabelValues(e.workflowID, sinkID).Inc()
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
		span.RecordError(lastErr)
		span.SetStatus(codes.Error, lastErr.Error())
		if e.deadLetterSink != nil {
			e.logger.Info("Sending message to Dead Letter Sink", "workflow_id", e.workflowID, "sink_id", sinkID, "message_id", msg.ID())

			// Add failure context to metadata
			msg.SetMetadata("_hermod_failed_sink", sinkID)
			msg.SetMetadata("_hermod_last_error", lastErr.Error())
			msg.SetMetadata("_hermod_failed_at", time.Now().Format(time.RFC3339))

			e.statusTracker.IncDeadLetter()

			telemetry.DeadLetterCount.WithLabelValues(e.workflowID, sinkID).Inc()
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

func (e *Engine) writeBatchToSink(ctx context.Context, snk hermod.BatchSink, msgs []hermod.Message, sinkID string, i int) error {
	// Trace batch write
	var span trace.Span
	ctx, span = tracer.Start(ctx, "sink.write_batch", trace.WithAttributes(
		attribute.String("workflow_id", e.workflowID),
		attribute.String("sink_id", sinkID),
		attribute.Int("batch_size", len(msgs)),
	))
	defer span.End()
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

	if e.IsSafeMode() && e.deadLetterSink != nil {
		e.logger.Warn("Safe Mode Active: diverting batch to Dead Letter Sink", "workflow_id", e.workflowID, "sink_id", sinkID, "batch_size", len(msgs))
		for _, m := range msgs {
			if m == nil {
				continue
			}
			m.SetMetadata("_hermod_safe_mode", "true")
			m.SetMetadata("_hermod_original_sink", sinkID)
			e.statusTracker.IncDeadLetter()
			telemetry.DeadLetterCount.WithLabelValues(e.workflowID, sinkID).Inc()
			if err := e.deadLetterSink.Write(ctx, m); err != nil {
				e.logger.Error("Failed to write message from batch to Dead Letter Sink in Safe Mode", "workflow_id", e.workflowID, "error", err)
			}
		}
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
			telemetry.SinkWriteErrors.WithLabelValues(e.workflowID, sinkID).Add(float64(len(msgs)))
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
				span.RecordError(ctx.Err())
				span.SetStatus(codes.Error, ctx.Err().Error())
				return ctx.Err()
			}
		}
		if j > 0 {
			e.logger.Info("Sink reconnected successfully", "workflow_id", e.workflowID, "sink_id", sinkID, "action", "reconnect")
		}
		telemetry.SinkWriteCount.WithLabelValues(e.workflowID, sinkID).Add(float64(len(msgs)))
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
		span.RecordError(lastErr)
		span.SetStatus(codes.Error, lastErr.Error())

		if e.deadLetterSink != nil || len(msgs) > 1 {
			e.logger.Warn("Batch write failed, attempting individual writes to isolate errors", "workflow_id", e.workflowID, "sink_id", sinkID, "batch_size", len(msgs))

			allSucceeded := true
			for _, m := range msgs {
				if m == nil {
					continue
				}
				// Use writeToSink for individual processing (which already handles DLQ)
				if err := e.writeToSink(ctx, snk, m, sinkID, i); err != nil {
					allSucceeded = false
					// We continue with other messages in the batch instead of stopping
					// writeToSink already logged the error and handled DLQ if available
				}
			}

			if allSucceeded {
				return nil
			}

			// If some failed even after individual attempts, and we don't have a DLQ
			// for those individual failures, writeToSink would have returned an error.
			// But here we want to return nil if we managed to process the whole batch
			// (either by success or by DLQing the individual failures).
			// If writeToSink returned nil for all messages (either success or DLQ),
			// then allSucceeded will be true.
			// If it returned error for any message (meaning no DLQ or DLQ failed),
			// then we still have a problem.
			if !allSucceeded {
				return fmt.Errorf("sink batch write failed and some messages could not be diverted: %w", lastErr)
			}
			return nil
		}

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
	if time.Since(sw.cbLastFailure) > interval && sw.cbStatus == "closed" {
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
	if w.useShards && w.shardCount > 1 && len(w.shards) == w.shardCount {
		// Spawn a run loop per shard channel
		for i := 0; i < w.shardCount; i++ {
			ch := w.shards[i]
			w.shardWg.Go(func() {
				w.runOn(ctx, ch)
			})
		}
		w.shardWg.Wait()
		return
	}
	// Fallback: single channel
	w.runOn(ctx, w.ch)
}

func (w *sinkWriter) runOn(ctx context.Context, input <-chan *pendingMessage) {
	defer func() {
		if r := recover(); r != nil {
			w.engine.logger.Error("Panic in sinkWriter.runOn", "sink_id", w.sinkID, "error", r, "stack", string(debug.Stack()))
		}
	}()
	if w.config.BackpressureStrategy == config.BPSpillToDisk {
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
				defer func() {
					if p := recover(); p != nil {
						if w.engine != nil && w.engine.logger != nil {
							w.engine.logger.Error("Panic in spill buffer consumer", "sink_id", w.sinkID, "panic", p)
						}
					}
				}()
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
	var batchBytes int
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
			for _, pm := range batch {
				pm.done <- err
			}
		} else {
			for i, m := range msgs {
				e := w.engine.writeToSink(ctx, w.sink, m, w.sinkID, w.index)
				batch[i].done <- e
				if e != nil {
					err = e
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

		batch = batch[:0]
		batchBytes = 0
	}

	for {
		select {
		case pm, ok := <-input:
			if !ok {
				flush()
				return
			}
			batch = append(batch, pm)
			// accumulate payload size for byte-based flushing
			if pm != nil && pm.msg != nil && pm.msg.Payload() != nil {
				batchBytes += len(pm.msg.Payload())
			}
			// flush when reaching count or byte thresholds
			if len(batch) >= w.currentBatchSize || (w.config.BatchBytes > 0 && batchBytes >= w.config.BatchBytes) {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// enqueueWithStrategy sends the pending message into the appropriate channel (single or sharded)
// according to the configured backpressure strategy.
func (w *sinkWriter) enqueueWithStrategy(ctx context.Context, pm *pendingMessage, strategy config.BackpressureStrategy) {
	target := w.pickShard(pm.msg)
	if strategy == "" {
		strategy = config.BPBlock
	}
	switch strategy {
	case config.BPDropOldest:
		select {
		case target <- pm:
			// enqueued
		default:
			// drop one oldest from this shard, then try again
			select {
			case old := <-target:
				if old != nil {
					// Signal the eviction to the owning goroutine; that owner is
					// responsible for releasing the pending message. Releasing it
					// here would double-release the pooled object (use-after-free).
					old.done <- errors.New("dropped due to backpressure (drop_oldest)")
				}
				telemetry.BackpressureDropTotal.WithLabelValues(w.engine.workflowID, w.sinkID, string(config.BPDropOldest)).Inc()
			default:
			}
			select {
			case target <- pm:
			default:
				pm.done <- errors.New("dropped due to backpressure (drop_oldest - overflow)")
				telemetry.BackpressureDropTotal.WithLabelValues(w.engine.workflowID, w.sinkID, string(config.BPDropOldest)).Inc()
			}
		}
	case config.BPDropNewest:
		select {
		case target <- pm:
		default:
			pm.done <- errors.New("dropped due to backpressure (drop_newest)")
			telemetry.BackpressureDropTotal.WithLabelValues(w.engine.workflowID, w.sinkID, string(config.BPDropNewest)).Inc()
		}
	case config.BPSampling:
		rate := w.config.SamplingRate
		if rate <= 0 {
			rate = 0.5
		}
		if rand.Float64() > rate {
			pm.done <- errors.New("dropped due to sampling")
			telemetry.BackpressureDropTotal.WithLabelValues(w.engine.workflowID, w.sinkID, string(config.BPSampling)).Inc()
		} else {
			select {
			case target <- pm:
			case <-ctx.Done():
				pm.done <- ctx.Err()
			}
		}
	case config.BPSpillToDisk:
		select {
		case target <- pm:
			// enqueued in memory
		default:
			if w.spillBuffer != nil {
				// Spill the raw message so we can reload later
				if err := w.spillBuffer.Produce(ctx, pm.msg); err != nil {
					pm.done <- fmt.Errorf("spill to disk failed: %w", err)
				} else {
					pm.done <- nil
					telemetry.BackpressureSpillTotal.WithLabelValues(w.engine.workflowID, w.sinkID).Inc()
				}
			} else {
				// Fallback: block like BPBlock
				select {
				case target <- pm:
				case <-ctx.Done():
					pm.done <- ctx.Err()
				}
			}
		}
	default: // BPBlock
		select {
		case target <- pm:
		case <-ctx.Done():
			pm.done <- ctx.Err()
		}
	}
}

func (w *sinkWriter) pickShard(msg hermod.Message) chan *pendingMessage {
	if !w.useShards || w.shardCount <= 1 || len(w.shards) != w.shardCount {
		return w.ch
	}
	// Choose key from metadata or message ID
	var key string
	if w.shardKeyMeta != "" && msg != nil {
		if md := msg.Metadata(); md != nil {
			if v, ok := md[w.shardKeyMeta]; ok && v != "" {
				key = v
			}
		}
	}
	if key == "" && msg != nil {
		key = msg.ID()
	}
	if key == "" {
		// fallback to random shard
		return w.shards[rand.Intn(w.shardCount)]
	}
	// FNV-1a hash
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	idx := int(h.Sum32() % uint32(w.shardCount))
	return w.shards[idx]
}
