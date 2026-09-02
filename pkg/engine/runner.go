package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/engine/config"
	"github.com/gsoultan/Hermod/pkg/engine/idempotency"
	"github.com/gsoultan/Hermod/pkg/engine/source"
	"github.com/gsoultan/Hermod/pkg/engine/telemetry"
	"github.com/gsoultan/Hermod/pkg/infra/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type Runner struct {
	engine *Engine
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
	errCh  chan error
	// lagState is only touched from the health-check path, which runs on a
	// single goroutine, so it needs no lock of its own.
	lagState lagState
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

	// Pre-flight checks: verify every sink is reachable before starting the
	// pipeline so we fail fast on a misconfigured/unreachable sink instead of
	// silently buffering messages that can never be delivered. Sources keep
	// their own runtime reconnect loop (see runSourceToBuffer), so they are not
	// part of this hard pre-flight.
	preflightStart := time.Now()
	if err := r.preflightSinks(r.ctx); err != nil {
		r.engine.setStatus("Error: " + err.Error())
		r.cancel()
		return err
	}
	if time.Since(preflightStart) > 5*time.Second {
		r.engine.logger.Warn("Sink pre-flight checks took longer than expected", "workflow_id", r.engine.workflowID, "duration", time.Since(preflightStart).Round(time.Millisecond))
	}

	// Initialize Sink Writers.
	var writersWg sync.WaitGroup
	r.engine.mu.RLock()
	numSinks := len(r.engine.sinks)
	sinkWriters := make([]*sinkWriter, numSinks)
	for i := range numSinks {
		snk := r.engine.sinks[i]
		sinkID := ""
		if i < len(r.engine.sinkIDs) {
			sinkID = r.engine.sinkIDs[i]
		}

		cfg := config.SinkConfig{}
		if i < len(r.engine.sinkConfigs) {
			cfg = r.engine.sinkConfigs[i]
		}
		r.engine.mu.RUnlock()

		bufferCap := cfg.BackpressureBuffer
		if bufferCap <= 0 {
			bufferCap = 1000
		}

		// A batch can only fill from messages that are in flight, so a batch
		// size above MaxInflight never completes on count and every flush waits
		// out BatchTimeout instead. Clamp to what is reachable and say so.
		if clamped := effectiveBatchSize(cfg.BatchSize, r.engine.config.MaxInflight); clamped != cfg.BatchSize {
			r.engine.logger.Warn("Sink batch_size exceeds engine max_inflight and was clamped; "+
				"raise max_inflight to use the configured batch size",
				"workflow_id", r.engine.workflowID,
				"sink_id", sinkID,
				"configured_batch_size", cfg.BatchSize,
				"max_inflight", r.engine.config.MaxInflight,
				"effective_batch_size", clamped)
			cfg.BatchSize = clamped
		}

		sw := &sinkWriter{
			engine: r.engine,
			sink:   snk,
			sinkID: sinkID,
			index:  i,
			config: cfg,
			ch:     make(chan *pendingMessage, bufferCap),
		}
		sw.currentBatchSize.Store(int64(cfg.BatchSize))
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
		// Eagerly initialize the spill-to-disk buffer (if configured) before any
		// producer or writer goroutine starts, so the producer path can read
		// sw.spillBuffer without a data race.
		sw.setupSpillBuffer()
		sinkWriters[i] = sw
		r.engine.mu.RLock()
	}
	r.engine.mu.RUnlock()
	r.engine.stopMu.Lock()
	r.engine.sinkWriters = sinkWriters
	r.engine.stopMu.Unlock()
	for _, sw := range sinkWriters {
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

	// A wedged pipeline used to be indistinguishable from an idle one: the
	// workflow stayed active=true and "running", nothing was logged at any
	// level, and replication lag grew until someone restarted it by hand.
	r.wg.Go(func() {
		r.watchForStalls(r.ctx)
	})

	// The watchdog above only sees work the engine has already accepted. A
	// source that stops delivering entirely is invisible to it, so sources that
	// can vouch for their own stream are watched separately.
	r.wg.Go(func() {
		r.watchForStreamSilence(r.ctx)
	})

	// Main Loops: Ingestion and Processing
	var sinkWg sync.WaitGroup
	sinkWg.Go(func() {
		r.runBufferToSink(r.ctx, &sinkWg)
	})

	r.wg.Go(func() {
		// The source ingestion loop runs on its own goroutine, so the recover in
		// Start does not cover it. Without this, a panic anywhere in a source
		// connector — a nil dereference in a CDC parser, say — takes down the
		// whole worker process and every sibling workflow running on it.
		defer func() {
			if rec := recover(); rec != nil {
				r.engine.logger.Error("Panic in source ingestion loop",
					"workflow_id", r.engine.workflowID,
					"panic", rec,
					"stack", string(debug.Stack()))
				r.engine.setStatus(fmt.Sprintf("Error: panic: %v", rec))
				// Non-blocking: errCh is buffered and Start drains it after
				// shutdown. Sending before cancel keeps this ordered ahead of
				// the close(r.errCh) that follows ctx.Done().
				select {
				case r.errCh <- fmt.Errorf("source ingestion panic: %v", rec):
				default:
				}
				r.cancel()
			}
		}()
		r.runSourceToBuffer(r.ctx)
	})

	// Wait for context to be done instead of blocking synchronously
	<-r.ctx.Done()

	// The source ingestion loop has exited because the engine is stopping, so
	// release the source's resources now (see closeSourceOnShutdown).
	r.closeSourceOnShutdown()

	sinkWg.Wait()
	// Wait for all in-flight per-message processing goroutines to finish before
	// closing the sink channels. Those goroutines (tracked by inFlightWg, not
	// sinkWg) are senders to sw.ch; closing the channel while they are still
	// sending would be a send-on-closed-channel race/panic.
	r.engine.inFlightWg.Wait()
	for _, sw := range r.engine.sinkWriters {
		if sw != nil {
			// Stop the spill-buffer consumer (which feeds back into sw.ch) and
			// wait for it to return before closing the channel, otherwise it can
			// send on a closed channel (race/panic). The source-to-buffer fan-out
			// has already finished (sinkWg.Wait above), so the spill consumer is
			// the only remaining sender to sw.ch.
			sw.shutdownSpill()
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
			// The budget has expired, and the writers' own detached write
			// contexts are cancelled at the same deadline, so they should be
			// unwinding now. Give them a grace period to finish and then stop
			// waiting: an unbounded <-done here meant one sink that never
			// returns held the whole process open, which is the failure mode
			// the timeout exists to escape.
			r.engine.logger.Warn("Sink writers draining exceeded drain_timeout",
				"workflow_id", r.engine.workflowID, "timeout", r.engine.config.DrainTimeout.String())
			select {
			case <-done:
			case <-time.After(drainAbandonGrace()):
				r.engine.logger.Error("Abandoning sink writer drain; a sink is not returning",
					"workflow_id", r.engine.workflowID, "grace", drainAbandonGrace().String())
			}
		}
	} else {
		writersWg.Wait()
	}
	r.closeSinksOnShutdown()
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

// closeSourceOnShutdown closes the engine's source on the graceful shutdown
// path. This is critical for CDC sources (e.g. Postgres): their replication
// stream runs on an independent background context and is only torn down by
// Close(). Previously Close() was invoked solely from HardStop(), so a normal
// stop/restart (config change, lease handoff, worker shutdown) leaked the
// streaming goroutine and left the replication slot active. The next source
// instance would then contend with the leaked one for the same slot, so CDC
// silently stopped delivering data even though the worker appeared online.
// Close is safe to call here and is idempotent with HardStop.
func (r *Runner) closeSourceOnShutdown() {
	if r.engine.source == nil {
		return
	}
	if err := r.engine.source.Close(); err != nil {
		r.engine.logger.Warn("Error closing source during shutdown", "workflow_id", r.engine.workflowID, "error", err)
	}
}

func (r *Runner) closeSinksOnShutdown() {
	r.engine.mu.RLock()
	defer r.engine.mu.RUnlock()
	for _, snk := range r.engine.sinks {
		if snk == nil {
			continue
		}
		if err := snk.Close(); err != nil {
			r.engine.logger.Warn("Error closing sink during shutdown", "workflow_id", r.engine.workflowID, "error", err)
		}
	}
	if r.engine.deadLetterSink != nil {
		_ = r.engine.deadLetterSink.Close()
	}
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
		// Update lag if supported
		if lagReporter, ok := r.engine.source.(hermod.LagReporter); ok {
			if lag, err := lagReporter.GetLag(r.ctx); err == nil {
				r.engine.statusTracker.SetLag(lag)

				// Retained WAL accumulates on the SOURCE database, so an
				// unnoticed stall fills someone else's primary rather than
				// degrading Hermod. Report the crossings.
				threshold := r.engine.config.LagWarnBytes
				if threshold == 0 {
					threshold = DefaultLagWarnBytes
				}
				breached, cleared := r.lagState.observe(lag, threshold)
				switch {
				case breached:
					r.engine.logger.Error("Source retention above threshold: the source database is holding WAL for this workflow",
						"workflow_id", r.engine.workflowID,
						"retained_bytes", lag,
						"threshold_bytes", threshold,
						"hint", "the pipeline is not acknowledging; check sink health and workflow status")
				case cleared:
					r.engine.logger.Info("Source retention back to normal",
						"workflow_id", r.engine.workflowID,
						"retained_bytes", lag)
				}
			}
		}
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

	srcStatus, _, engStatus, _, _, _, _, _ := r.engine.statusTracker.GetStatus()
	if allSinksOk && srcStatus == "running" && engStatus != "stalled" {
		// Not while stalled. The stall watchdog owns that status: it sets it
		// when nothing completes while work is outstanding, and clears it again
		// when it sees progress resume (see watchForStalls).
		//
		// A wedged sink is not an unreachable one — it accepts connections and
		// answers Ping, it just never finishes a write. So allSinksOk stays
		// true here, and this used to overwrite "stalled" with "running" on the
		// next tick. The stall was real, the supervisor had already been told,
		// and the status the UI reads said the workflow was fine.
		if engStatus != "running" && strings.HasPrefix(engStatus, "reconnecting") {
			r.engine.logger.Info("System reconnected successfully", "workflow_id", r.engine.workflowID, "action", "reconnect")
		}
		r.engine.setStatus("running")
	}
}

// preflightAttempts is the number of times a sink is pinged during startup
// pre-flight before the engine gives up and fails to start.
const preflightAttempts = 3

// preflightSinks pings every configured sink (up to preflightAttempts times each
// with a short backoff) before the pipeline starts. It returns an error as soon
// as any sink remains unreachable, so a misconfigured sink fails the engine fast
// instead of silently accumulating undeliverable messages.
func (r *Runner) preflightSinks(ctx context.Context) error {
	r.engine.logger.Info("Runner: preflighting sinks", "count", len(r.engine.sinks))
	for i, snk := range r.engine.sinks {
		if snk == nil {
			continue
		}
		sinkID := ""
		if i < len(r.engine.sinkIDs) {
			sinkID = r.engine.sinkIDs[i]
		}
		if err := r.pingWithRetry(ctx, snk.Ping); err != nil {
			r.engine.logger.Error("Sink pre-flight check failed",
				"workflow_id", r.engine.workflowID,
				"sink_id", sinkID,
				"attempts", preflightAttempts,
				"error", err)
			return fmt.Errorf("sink pre-flight checks failed after %d attempts: %w", preflightAttempts, err)
		}
	}
	return nil
}

// pingWithRetry invokes ping up to preflightAttempts times, returning nil on the
// first success. Between attempts it waits a short, bounded backoff and honors
// context cancellation so it can never block startup indefinitely.
func (r *Runner) pingWithRetry(ctx context.Context, ping func(context.Context) error) error {
	const backoff = 50 * time.Millisecond
	var err error
	for attempt := range preflightAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
		if err = ping(ctx); err == nil {
			return nil
		}
	}
	return err
}

// reconnectWait returns how long to wait before the next source reconnect
// attempt. It honors the configured SourceConfig.ReconnectIntervals (indexing by
// attempt and clamping to the last entry once exhausted) and only falls back to
// the supplied default interval when no reconnect intervals are configured.
func (r *Runner) reconnectWait(attempt int, fallback time.Duration) time.Duration {
	r.engine.mu.RLock()
	intervals := r.engine.sourceConfig.ReconnectIntervals
	r.engine.mu.RUnlock()
	if len(intervals) == 0 {
		return fallback
	}
	idx := attempt
	if idx >= len(intervals) {
		idx = len(intervals) - 1
	}
	return intervals[idx]
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
				if errors.Is(err, context.Canceled) {
					return
				}
				r.engine.setSourceStatus("reconnecting")
				if reconnectAttempts == 0 {
					r.engine.logger.Warn("Source disconnected, entering reconnect loop", "workflow_id", r.engine.workflowID, "error", err)
				}

				select {
				case <-ctx.Done():
					return
				case <-time.After(r.reconnectWait(reconnectAttempts, interval)):
					reconnectAttempts++
					continue
				}
			}
		}

		reconnectAttempts = 0
		r.engine.setSourceStatus("running")
		_, _, engStatus, _, _, _, _, _ := r.engine.statusTracker.GetStatus()
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
				if errors.Is(err, context.Canceled) {
					return
				}
				if errors.Is(err, context.DeadlineExceeded) {
					continue
				}
				r.engine.logger.Error("Source read error", "workflow_id", r.engine.workflowID, "error", err)
				// A read failure means the source is (temporarily) unable to
				// deliver data. Surface it as a recoverable reconnect rather than
				// a silent error so the status reflects "reconnecting:source" and
				// the loop backs off before retrying instead of hot-spinning.
				r.engine.setSourceStatus("reconnecting")
				r.engine.setStatus("reconnecting:source")
				select {
				case <-ctx.Done():
					return
				case <-time.After(r.reconnectWait(reconnectAttempts, interval)):
					reconnectAttempts++
				}
				continue
			}

			if m == nil {
				continue
			}

			r.engine.recordSourceActivity()

			// Where a trace begins — but only if nothing upstream already
			// started one. The registry's multiplexer stamps a record as it
			// takes it from a sub-source; stamping again here would overwrite
			// that context and orphan the span it belongs to. When the engine
			// is used directly as a library there is nothing above it, and this
			// is the entry point.
			//
			// The span is started and ended around the handover rather than
			// around the Read above, because Read blocks until something
			// arrives — a quiet source would otherwise report minutes of
			// "reading" that were minutes of waiting.
			//
			// The context it produces cannot be passed on: the sink writers run
			// on other goroutines fed by the buffer, so it is stamped onto the
			// message instead and picked up again at the write.
			if !trace.SpanContextFromContext(tracing.Extract(ctx, m)).IsValid() {
				readCtx, span := tracer.Start(ctx, "source.receive", trace.WithAttributes(
					attribute.String("workflow_id", r.engine.workflowID),
					attribute.String("message_id", m.ID()),
					attribute.String("table", m.Table()),
					attribute.String("operation", string(m.Operation())),
				))
				tracing.Inject(readCtx, m)
				span.End()
			}

			if err := r.engine.buffer.Produce(ctx, m); err != nil {
				r.engine.logger.Error("Failed to write message to buffer", "workflow_id", r.engine.workflowID, "error", err)
				m.Release()
			}
		}
	}
}

func (r *Runner) runBufferToSink(ctx context.Context, sinkWg *sync.WaitGroup) {
	defer func() {
		if rec := recover(); rec != nil {
			r.engine.logger.Error("Panic in runBufferToSink", "workflow_id", r.engine.workflowID, "panic", rec, "stack", string(debug.Stack()))
		}
	}()
	if consumer, ok := r.engine.buffer.(hermod.Consumer); ok {
		numWorkers := cap(r.engine.inFlightSem)
		if numWorkers <= 0 {
			numWorkers = 128
		}

		msgChan := make(chan hermod.Message, numWorkers)

		// Start persistent worker pool for message processing
		for range numWorkers {
			r.wg.Go(func() {
				for {
					select {
					case <-ctx.Done():
						return
					case m, ok := <-msgChan:
						if !ok {
							return
						}
						r.processMessage(ctx, m)
						// Slot released inside processMessage or by Done()
						r.engine.inFlightWg.Done()
						<-r.engine.inFlightSem
					}
				}
			})
		}

		err := consumer.Consume(ctx, func(drainCtx context.Context, m hermod.Message) error {
			// Acquire inflight slot to maintain backpressure from buffer to workers
			select {
			case r.engine.inFlightSem <- struct{}{}:
			case <-drainCtx.Done():
				return drainCtx.Err()
			}

			r.engine.inFlightWg.Add(1)
			select {
			case msgChan <- m:
				return nil
			case <-drainCtx.Done():
				r.engine.inFlightWg.Done()
				<-r.engine.inFlightSem
				return drainCtx.Err()
			}
		})
		close(msgChan)

		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			r.engine.logger.Error("Buffer-to-Sink consumer error", "workflow_id", r.engine.workflowID, "error", err)
			r.errCh <- err
		}
	}
}

func (r *Runner) processMessage(ctx context.Context, m hermod.Message) {
	if m == nil {
		return
	}
	defer m.Release()

	defer func() {
		if p := recover(); p != nil {
			r.engine.logger.Error("Panic in message processing", "workflow_id", r.engine.workflowID, "panic", p, "stack", string(debug.Stack()))
		}
	}()

	start := time.Now()
	defer func() {
		duration := time.Since(start)
		telemetry.ProcessingLatency.WithLabelValues(r.engine.workflowID).Observe(duration.Seconds())
		r.engine.adaptiveThrottle(ctx, duration)
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
		r.engine.RecordTraceStep(ctx, m, "workflow_start", start, nil, nil)
	}

	// Data validation
	if r.engine.validator != nil {
		vstart := time.Now()
		if err := r.engine.validator.Validate(ctx, m.Data()); err != nil {
			r.engine.logger.Error("Message validation failed", "workflow_id", r.engine.workflowID, "message_id", m.ID(), "error", err)
			r.engine.UpdateNodeErrorMetric("validator", 1)
			r.engine.RecordTraceStep(ctx, m, "validator", vstart, nil, err)

			if r.engine.deadLetterSink != nil {
				m.SetMetadata("_hermod_validation_failed", "true")
				m.SetMetadata("_hermod_last_error", err.Error())
				_ = r.engine.deadLetterSink.Write(ctx, m)
				r.engine.statusTracker.IncDeadLetter()
			}
			return
		}
		r.engine.UpdateNodeMetric("validator", 1)
		r.engine.RecordTraceStep(ctx, m, "validator", vstart, nil, nil)
	}

	// Routing
	var targets []RoutedMessage
	if r.engine.router != nil {
		rstart := time.Now()
		t, err := r.engine.router(ctx, m)
		if err != nil {
			r.engine.logger.Error("Routing failed", "workflow_id", r.engine.workflowID, "message_id", m.ID(), "error", err)
			r.engine.RecordTraceStep(ctx, m, "router", rstart, nil, err)
			return
		}
		targets = t
		r.engine.RecordTraceStep(ctx, m, "router", rstart, nil, nil)
	} else {
		// Default: route to all sinks
		targets = make([]RoutedMessage, len(r.engine.sinks))
		for i := range r.engine.sinks {
			m.Retain()
			targets[i] = RoutedMessage{SinkIndex: i, Message: m}
		}
	}

	defer func() {
		for _, target := range targets {
			if target.Message != nil {
				target.Message.Release()
			}
		}
	}()

	if len(targets) == 0 {
		ack := func() {
			// Even if filtered, we must acknowledge to prevent re-reading
			if outboxID, exists := m.Metadata()["_outbox_id"]; exists && r.engine.outboxStore != nil {
				_ = r.engine.outboxStore.DeleteOutboxItem(ctx, outboxID)
			} else {
				_ = r.engine.source.Ack(ctx, m)
			}
			r.engine.statusTracker.IncProcessed()
		}

		// A workflow with no sinks at all can never deliver anything, so there is
		// nothing to preserve: acknowledge and move on.
		if !r.engine.hasSinks() {
			ack()
			return
		}

		// The workflow HAS sinks and resolved none of them. This used to be
		// acknowledged and discarded exactly like a filtered message — the two
		// were indistinguishable — which during a sink outage acknowledged 1996
		// messages to the replication slot, advanced it past them, delivered
		// none, wrote none to a dead-letter queue and logged nothing. The data
		// was gone, and the "no data is lost, just restart it" behaviour seen
		// earlier only held while the slot happened not to have advanced yet.
		//
		// Undeliverable is not the same as unwanted, so it is no longer treated
		// as a successful delivery.
		telemetry.MessagesDroppedNoTarget.WithLabelValues(r.engine.workflowID).Inc()
		r.engine.reportUnroutable(m)

		// Preferred: park it in the dead-letter sink, which preserves the message
		// and lets the source advance — but only if the park actually worked.
		// This used to acknowledge unconditionally, so a dead-letter sink that
		// was unreachable turned every undeliverable message into a silent drop:
		// the one outcome the sink exists to prevent, and the exact opposite of
		// what the branch below does when there is no sink at all.
		if r.engine.deadLetterSink != nil {
			if err := r.engine.writeToDLQ(ctx, "", m); err == nil {
				ack()
				return
			}
			// The park failed, so the message is nowhere. That is the same
			// position as having no dead-letter sink, and it gets the same
			// answer: fall through and do not acknowledge.
			r.engine.logger.Error("Dead-letter sink refused an undeliverable message; "+
				"leaving it unacknowledged so it is redelivered rather than lost",
				"workflow_id", r.engine.workflowID, "message_id", m.ID())
		}

		// No dead-letter sink: do NOT acknowledge. Leaving the message
		// un-acknowledged keeps it on the source — for a replication slot that
		// means the WAL is retained and replays on the next run — which is the
		// same choice the routing-error path above already makes. Retention is
		// visible (the lag threshold reports it) and recoverable; a silent drop
		// is neither.
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
		target.Message.Retain()
		pm := acquirePendingMessage(target.Message)
		swg.Go(func() {
			sw.enqueueWithStrategy(ctx, pm, sw.snapshotConfig().BackpressureStrategy)
			select {
			case err := <-pm.done:
				if err != nil {
					serrCh <- err
				}
			case <-ctx.Done():
				// Shutdown. Abandoning the wait here reports a failure for a
				// message the writer is still draining and will deliver, so the
				// acknowledgement below is skipped: the message goes out *and*
				// replays on the next start. Wait out the drain budget for a
				// real answer instead — the same budget the writer honours, so
				// this cannot outlive it.
				drainCtx, cancelDrain := drainWriteContext(ctx, drainBudget(r.engine))
				select {
				case err := <-pm.done:
					if err != nil {
						serrCh <- err
					}
				case <-drainCtx.Done():
					serrCh <- drainCtx.Err()
				}
				cancelDrain()
			}
			releasePendingMessage(pm)
		})
	}
	swg.Wait()
	close(serrCh)
	for err := range serrCh {
		if err != nil {
			r.engine.logger.Error("Sink write error", "workflow_id", r.engine.workflowID, "error", err)
			return
		}
	}

	// Acknowledge the message to the source after all successful sink writes.
	//
	// For a CDC source this is what advances the replication slot, and it is a
	// network round trip to the upstream server. On the engine's own context it
	// is refused the moment shutdown begins, so every message the drain
	// successfully delivered would be replayed on the next start — a duplicate
	// per in-flight message per deploy. It gets the same detached, bounded
	// treatment as the write it confirms.
	ackCtx := ctx
	if ctx.Err() != nil {
		var cancelAck context.CancelFunc
		ackCtx, cancelAck = drainWriteContext(ctx, drainBudget(r.engine))
		defer cancelAck()
	}
	if outboxID, exists := m.Metadata()["_outbox_id"]; exists && r.engine.outboxStore != nil {
		if err := r.engine.outboxStore.DeleteOutboxItem(ackCtx, outboxID); err != nil {
			r.engine.logger.Error("Failed to delete outbox item", "workflow_id", r.engine.workflowID, "id", outboxID, "error", err)
		}
	} else if err := r.engine.source.Ack(ackCtx, m); err != nil {
		r.engine.logger.Error("Source acknowledgement failed", "workflow_id", r.engine.workflowID, "error", err)
		return
	}

	telemetry.MessagesProcessed.WithLabelValues(r.engine.workflowID, r.engine.sourceID).Inc()
	r.engine.statusTracker.IncProcessed()
}

// drainAbandonGrace is how long shutdown waits for sink writers *after* the
// drain budget has already expired. The writers' write contexts are cancelled
// at the budget, so this only covers unwinding; a sink that still has not
// returned is not going to, and holding the process open for it turns a slow
// destination into a stuck deploy. Derived from the shared budget so it cannot
// push the total past the orchestrator's grace period.
func drainAbandonGrace() time.Duration { return config.Shutdown().Grace }
