package engine

import (
	"context"
	"errors"
	"fmt"
	"hash"
	"hash/fnv"
	"math/rand"
	"runtime/debug"
	"slices"
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
	// spillCancel stops the spill-buffer consumer goroutine, and spillWg waits
	// for it to fully return. The consumer feeds messages back into w.ch, so it
	// must be stopped before w.ch is closed to avoid a send-on-closed-channel
	// race/panic during shutdown.
	spillCancel context.CancelFunc
	spillWg     sync.WaitGroup

	// Circuit Breaker state
	cbMu          sync.RWMutex
	cbFailCount   int
	cbLastFailure time.Time
	cbOpenUntil   time.Time
	cbStatus      string // "closed", "open", "half-open"

	// Adaptive Batching. currentBatchSize is exported observability state that
	// may be read concurrently (status snapshots, tests) while a writer goroutine
	// adjusts it, so it is an atomic. The actual hot-loop batch sizing uses a
	// goroutine-local copy (see runOn) to keep sharded writers race-free.
	currentBatchSize atomic.Int64
	batchTimeout     time.Duration
	updateMu         sync.RWMutex
}

type pendingMessage struct {
	msg      hermod.Message
	done     chan error
	refCount atomic.Int32
}

var pendingMessagePool = sync.Pool{
	New: func() any {
		return &pendingMessage{
			done:     make(chan error, 1),
			refCount: atomic.Int32{},
		}
	},
}

func acquirePendingMessage(msg hermod.Message) *pendingMessage {
	pm := pendingMessagePool.Get().(*pendingMessage)
	pm.msg = msg
	pm.refCount.Store(2) // One for the producer/runner, one for the writer/backpressure
	return pm
}

func releasePendingMessage(pm *pendingMessage) {
	// A pendingMessage is shared between a producer (the runner) and a consumer
	// (the sinkWriter or backpressure strategy). It must only be returned to the
	// pool when BOTH have finished their work.
	if pm.refCount.Add(-1) > 0 {
		return
	}

	if pm.msg != nil {
		pm.msg.Release()
		pm.msg = nil
	}

	// Reset the done channel by reading if it has anything (should be empty though)
	select {
	case <-pm.done:
	default:
	}
	pendingMessagePool.Put(pm)
}

var fnvPool = sync.Pool{
	New: func() any {
		return fnv.New32a()
	},
}

func (e *Engine) prepareDLQMessage(m hermod.Message, sinkID string, errStr string) {
	if m == nil {
		return
	}
	if sinkID != "" {
		m.SetMetadata("_hermod_failed_sink", sinkID)
	}
	if errStr != "" {
		m.SetMetadata("_hermod_last_error", errStr)
	}
	m.SetMetadata("_hermod_failed_at", time.Now().Format(time.RFC3339))
	e.statusTracker.IncDeadLetter()
	telemetry.DeadLetterCount.WithLabelValues(e.workflowID, sinkID).Inc()
}

func (e *Engine) writeToDLQ(ctx context.Context, sinkID string, msgs ...hermod.Message) {
	if e.deadLetterSink == nil || len(msgs) == 0 {
		return
	}

	// If the DLQ sink supports batching, use it
	if bsnk, ok := e.deadLetterSink.(hermod.BatchSink); ok && len(msgs) > 1 {
		for _, m := range msgs {
			e.prepareDLQMessage(m, sinkID, "")
		}
		if err := bsnk.WriteBatch(ctx, msgs); err != nil {
			e.logger.Error("Failed to write batch to Dead Letter Sink", "workflow_id", e.workflowID, "error", err)
			telemetry.DeadLetterErrors.WithLabelValues(e.workflowID, sinkID).Inc()
		}
		return
	}

	// Fallback to single writes
	for _, m := range msgs {
		e.prepareDLQMessage(m, sinkID, "")
		if err := e.deadLetterSink.Write(ctx, m); err != nil {
			e.logger.Error("Failed to write to Dead Letter Sink", "workflow_id", e.workflowID, "error", err)
			telemetry.DeadLetterErrors.WithLabelValues(e.workflowID, sinkID).Inc()
		}
	}
}

// writeToSink writes a single message to the sink with retry/reconnect.
// Optional onAttemptError observers are invoked on every individual sink write
// failure (including transient failures that a later retry recovers from). This
// lets callers (e.g. the circuit breaker) account for the underlying sink
// health even when retries ultimately succeed.
func (e *Engine) writeToSink(ctx context.Context, snk hermod.Sink, msg hermod.Message, sinkID string, i int, onAttemptError ...func()) error {
	if msg == nil {
		return nil
	}
	// Trace single write
	var span trace.Span
	ctx, span = tracer.Start(ctx, "sink.write", trace.WithAttributes(
		attribute.String("workflow_id", e.workflowID),
		attribute.String("sink_id", sinkID),
		attribute.String("message_id", msg.ID()),
	))
	defer span.End()

	if e.isFailing() {
		return errors.New("simulated engine failure")
	}

	if e.IsSafeMode() && e.deadLetterSink != nil {
		e.logger.Warn("Safe Mode Active: diverting message to Dead Letter Sink", "workflow_id", e.workflowID, "sink_id", sinkID, "message_id", msg.ID())
		msg.SetMetadata("_hermod_safe_mode", "true")
		msg.SetMetadata("_hermod_original_sink", sinkID)
		e.writeToDLQ(ctx, sinkID, msg)
		return nil
	}

	// Pre-write validation
	if vs, ok := snk.(hermod.ValidatingSink); ok {
		if err := vs.Validate(ctx, msg); err != nil {
			e.logger.Error("Sink pre-write validation failed", "workflow_id", e.workflowID, "sink_id", sinkID, "message_id", msg.ID(), "error", err)
			if e.deadLetterSink != nil {
				e.logger.Info("Sending invalid message to Dead Letter Sink", "workflow_id", e.workflowID, "sink_id", sinkID, "message_id", msg.ID())
				msg.SetMetadata("_hermod_validation_failed", "true")
				e.writeToDLQ(ctx, sinkID, msg)
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

	for j := range maxRetries {
		start := time.Now()
		var before map[string]any
		if e.traceRecorder != nil && e.config.TraceSampleRate > 0 {
			before = msg.ToMap()
		}

		idempStart := time.Now()
		err := snk.Write(ctx, msg)
		if err != nil {
			lastErr = err
			for _, observe := range onAttemptError {
				if observe != nil {
					observe()
				}
			}
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

		// Record trace step for successful delivery (or failed final attempt recorded later)
		e.RecordTraceStep(ctx, msg, sinkID, start, before, nil)

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
			e.writeToDLQ(ctx, sinkID, msg)
			return nil // Message preserved in DLQ
		}
		return fmt.Errorf("sink write error: %w", lastErr)
	}
	return nil
}

func (e *Engine) writeBatchToSink(ctx context.Context, snk hermod.BatchSink, msgs []hermod.Message, sinkID string, i int) error {
	// Filter nil messages using modern slice tools
	msgs = slices.DeleteFunc(msgs, func(m hermod.Message) bool { return m == nil })

	if len(msgs) == 0 {
		return nil
	}

	// Trace batch write
	var span trace.Span
	ctx, span = tracer.Start(ctx, "sink.write_batch", trace.WithAttributes(
		attribute.String("workflow_id", e.workflowID),
		attribute.String("sink_id", sinkID),
		attribute.Int("batch_size", len(msgs)),
	))
	defer span.End()

	// Pre-write validation
	if vs, ok := snk.(hermod.ValidatingSink); ok {
		validMsgs := make([]hermod.Message, 0, len(msgs))
		invalidMsgs := make([]hermod.Message, 0)
		for _, m := range msgs {
			if err := vs.Validate(ctx, m); err != nil {
				e.logger.Error("Sink pre-write validation failed for message in batch", "workflow_id", e.workflowID, "sink_id", sinkID, "message_id", m.ID(), "error", err)
				if e.deadLetterSink != nil {
					m.SetMetadata("_hermod_validation_failed", "true")
					m.SetMetadata("_hermod_last_error", err.Error())
					invalidMsgs = append(invalidMsgs, m)
				}
				continue
			}
			validMsgs = append(validMsgs, m)
		}
		if len(invalidMsgs) > 0 {
			e.writeToDLQ(ctx, sinkID, invalidMsgs...)
		}
		msgs = validMsgs
	}

	if len(msgs) == 0 {
		return nil
	}

	if e.IsSafeMode() && e.deadLetterSink != nil {
		e.logger.Warn("Safe Mode Active: diverting batch to Dead Letter Sink", "workflow_id", e.workflowID, "sink_id", sinkID, "batch_size", len(msgs))
		for _, m := range msgs {
			if m != nil {
				m.SetMetadata("_hermod_safe_mode", "true")
				m.SetMetadata("_hermod_original_sink", sinkID)
			}
		}
		e.writeToDLQ(ctx, sinkID, msgs...)
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

	for j := range maxRetries {
		start := time.Now()
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

		// Record trace step for each message in the batch.
		// Sampling is handled internally by RecordTraceStep.
		for _, m := range msgs {
			if m != nil {
				e.RecordTraceStep(ctx, m, sinkID, start, nil, nil)
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

// circuitState returns the current circuit breaker status in a thread-safe way.
func (sw *sinkWriter) circuitState() string {
	sw.cbMu.Lock()
	defer sw.cbMu.Unlock()
	return sw.cbStatus
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

// shutdownSpill stops the spill-buffer consumer (if any) and waits for it to
// fully return. It must be called before w.ch is closed so the consumer can
// never send on a closed channel. It is safe to call when no spill consumer is
// running.
func (w *sinkWriter) shutdownSpill() {
	w.updateMu.RLock()
	cancel := w.spillCancel
	w.updateMu.RUnlock()
	if cancel != nil {
		cancel()
	}
	w.spillWg.Wait()
}

// setupSpillBuffer eagerly creates the spill-to-disk buffer. It is called
// synchronously by the runner during sinkWriter construction, before any
// producer or writer goroutine starts, so that w.spillBuffer can be read by the
// producer path (enqueueWithStrategy) without a data race.
func (w *sinkWriter) setupSpillBuffer() {
	if w.config.BackpressureStrategy != config.BPSpillToDisk {
		return
	}
	path := w.config.SpillPath
	if path == "" {
		path = ".hermod-spill-" + w.sinkID
	}
	maxSize := w.config.SpillMaxSize
	if maxSize <= 0 {
		maxSize = 100 * 1024 * 1024 // 100MB default
	}
	spill, err := buffer.NewFileBuffer(path, maxSize)
	if err != nil {
		if w.engine != nil && w.engine.logger != nil {
			w.engine.logger.Error("Failed to initialize spill buffer", "sink_id", w.sinkID, "path", path, "error", err)
		}
		return
	}
	w.spillBuffer = spill
}

// startSpillConsumer starts the spill-buffer consumer (once) under a dedicated
// child context tracked by spillWg, so it can be stopped and waited for before
// w.ch is closed. The consumer re-enqueues spilled messages into w.ch; a late
// send on a closed channel would otherwise race/panic.
func (w *sinkWriter) startSpillConsumer(ctx context.Context) {
	consumer, ok := w.spillBuffer.(hermod.Consumer)
	if !ok {
		return
	}
	spillCtx, cancel := context.WithCancel(ctx)
	w.updateMu.Lock()
	w.spillCancel = cancel
	w.updateMu.Unlock()
	w.spillWg.Go(func() {
		defer func() {
			if p := recover(); p != nil {
				if w.engine != nil && w.engine.logger != nil {
					w.engine.logger.Error("Panic in spill buffer consumer", "sink_id", w.sinkID, "panic", p)
				}
			}
		}()
		_ = consumer.Consume(spillCtx, func(ctx context.Context, msg hermod.Message) error {
			// Try to put back into the main channel. Since we are spilling, we
			// want to prioritize messages in sw.ch but also drain the spill
			// buffer when there is room.
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
	})
}

func (w *sinkWriter) run(ctx context.Context) {
	// Start the spill consumer once (it feeds back into the single w.ch).
	w.startSpillConsumer(ctx)

	if w.useShards && w.shardCount > 1 && len(w.shards) == w.shardCount {
		// Spawn a run loop per shard channel
		for i := range w.shardCount {
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
	// Batch sizing state is kept goroutine-local.
	batchSize := w.config.BatchSize
	if batchSize < 1 {
		batchSize = 1
	}
	w.currentBatchSize.Store(int64(batchSize))
	batchTimeout := w.config.BatchTimeout
	if batchTimeout == 0 {
		batchTimeout = 100 * time.Millisecond
	}

	// Reuse slices to minimize allocations in the hot loop.
	batch := make([]*pendingMessage, 0, batchSize)
	msgsReuse := make([]hermod.Message, 0, batchSize)
	var batchBytes int
	ticker := time.NewTicker(batchTimeout)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}

		start := time.Now()
		if err := w.checkCircuitBreaker(); err != nil {
			for _, pm := range batch {
				pm.done <- err
				releasePendingMessage(pm)
			}
			batch = batch[:0]
			batchBytes = 0
			return
		}

		// Reuse the messages slice
		msgsReuse = msgsReuse[:0]
		for _, pm := range batch {
			msgsReuse = append(msgsReuse, pm.msg)
		}

		var err error
		transientFailure := false
		observeAttemptErr := func() { transientFailure = true }
		var perMsgErr []error
		isBatch := false
		if bs, ok := w.sink.(hermod.BatchSink); ok && len(msgsReuse) > 1 {
			isBatch = true
			err = w.engine.writeBatchToSink(ctx, bs, msgsReuse, w.sinkID, w.index)
		} else {
			perMsgErr = make([]error, len(msgsReuse))
			for i, m := range msgsReuse {
				e := w.engine.writeToSink(ctx, w.sink, m, w.sinkID, w.index, observeAttemptErr)
				perMsgErr[i] = e
				if e != nil {
					err = e
				}
			}
		}

		if err != nil || transientFailure {
			w.recordFailure()
		} else {
			w.recordSuccess()
		}

		if isBatch {
			for _, pm := range batch {
				pm.done <- err
				releasePendingMessage(pm)
			}
		} else {
			for i := range batch {
				batch[i].done <- perMsgErr[i]
				releasePendingMessage(batch[i])
			}
		}

		if w.config.AdaptiveBatching {
			duration := time.Since(start)
			if err == nil {
				if duration < batchTimeout/2 && len(input) > 0 {
					increment := int(float64(batchSize) * 0.05)
					if increment < 1 {
						increment = 1
					}
					batchSize += increment
					if batchSize > 5000 {
						batchSize = 5000
					}
				} else if duration > time.Duration(float64(batchTimeout)*0.8) {
					batchSize = int(float64(batchSize) * 0.9)
					if batchSize < 1 {
						batchSize = 1
					}
				}
			} else {
				batchSize = int(float64(batchSize) * 0.5)
				if batchSize < 1 {
					batchSize = 1
				}
			}
			w.currentBatchSize.Store(int64(batchSize))
		}

		batch = batch[:0]
		batchBytes = 0
	}

	for {
		// Resilience: Recover from panics in the processing loop and restart.
		exit := func() bool {
			defer func() {
				if r := recover(); r != nil {
					w.engine.logger.Error("Panic in sinkWriter.runOn, restarting shard loop", "sink_id", w.sinkID, "error", r, "stack", string(debug.Stack()))
					time.Sleep(1 * time.Second)
				}
			}()

			for {
				select {
				case pm, ok := <-input:
					if !ok {
						flush()
						return true // input closed
					}
					// Take a local reference to the message to avoid data races if
					// the owner releases the pendingMessage concurrently.
					msg := pm.msg
					if msg != nil {
						batch = append(batch, pm)
						if payload := msg.Payload(); payload != nil {
							batchBytes += len(payload)
						}
						if len(batch) >= batchSize || (w.config.BatchBytes > 0 && batchBytes >= w.config.BatchBytes) {
							flush()
						}
					} else {
						// Message already released by owner (e.g. timeout)
						pm.done <- errors.New("message released before processing")
						releasePendingMessage(pm)
					}
				case <-ticker.C:
					flush()
				case <-ctx.Done():
					flush()
					return true
				}
			}
		}()
		if exit {
			return
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
					// Signal the eviction to the owning goroutine. We also call
					// releasePendingMessage here to ensure the pooled object is
					// returned even if the owner has already timed out. The
					// atomic released flag in releasePendingMessage safely
					// prevents double-release.
					old.done <- errors.New("dropped due to backpressure (drop_oldest)")
					releasePendingMessage(old)
				}
				telemetry.BackpressureDropTotal.WithLabelValues(w.engine.workflowID, w.sinkID, string(config.BPDropOldest)).Inc()
			default:
			}
			select {
			case target <- pm:
			default:
				pm.done <- errors.New("dropped due to backpressure (drop_oldest - overflow)")
				releasePendingMessage(pm)
				telemetry.BackpressureDropTotal.WithLabelValues(w.engine.workflowID, w.sinkID, string(config.BPDropOldest)).Inc()
			}
		}
	case config.BPDropNewest:
		select {
		case target <- pm:
		default:
			pm.done <- errors.New("dropped due to backpressure (drop_newest)")
			releasePendingMessage(pm)
			telemetry.BackpressureDropTotal.WithLabelValues(w.engine.workflowID, w.sinkID, string(config.BPDropNewest)).Inc()
		}
	case config.BPSampling:
		rate := w.config.SamplingRate
		if rate <= 0 {
			rate = 0.5
		}
		if rand.Float64() > rate {
			pm.done <- errors.New("dropped due to sampling")
			releasePendingMessage(pm)
			telemetry.BackpressureDropTotal.WithLabelValues(w.engine.workflowID, w.sinkID, string(config.BPSampling)).Inc()
		} else {
			select {
			case target <- pm:
			case <-ctx.Done():
				pm.done <- ctx.Err()
				releasePendingMessage(pm)
			}
		}
	case config.BPSpillToDisk:
		select {
		case target <- pm:
			// enqueued in memory
		default:
			if w.spillBuffer != nil {
				// Spill the raw message so we can reload later. Produce takes
				// ownership of the message and releases it back to the pool, so
				// detach it from pm first; otherwise releasePendingMessage (called
				// by the owning goroutine after pm.done) would release it a second
				// time, recycling the message while it is still being read
				// elsewhere (use-after-free / data race).
				err := w.spillBuffer.Produce(ctx, pm.msg)
				pm.msg = nil
				if err != nil {
					pm.done <- fmt.Errorf("spill to disk failed: %w", err)
				} else {
					pm.done <- nil
				}
				releasePendingMessage(pm)
				telemetry.BackpressureSpillTotal.WithLabelValues(w.engine.workflowID, w.sinkID).Inc()
			} else {
				// Fallback: block like BPBlock
				select {
				case target <- pm:
				case <-ctx.Done():
					pm.done <- ctx.Err()
					releasePendingMessage(pm)
				}
			}
		}
	default: // BPBlock
		select {
		case target <- pm:
		case <-ctx.Done():
			pm.done <- ctx.Err()
			releasePendingMessage(pm)
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
	// FNV-1a hash (using pool to avoid allocations)
	h := fnvPool.Get().(hash.Hash32)
	h.Reset()
	_, _ = h.Write([]byte(key))
	idx := int(h.Sum32() % uint32(w.shardCount))
	fnvPool.Put(h)
	return w.shards[idx]
}
