package registry

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

const (
	// logWriteQueueSize bounds the log lines waiting for storage. Past it,
	// lines are dropped and counted rather than held: a node failing every
	// message against a slow log table would otherwise grow memory with the
	// message rate.
	logWriteQueueSize = 4096
	// logWriteWorkers is the fixed number of concurrent CreateLog calls.
	logWriteWorkers = 4
	// defaultLogWriteTimeout bounds one CreateLog, so a stalled insert gives
	// its worker back instead of stopping log persistence for good.
	defaultLogWriteTimeout = 5 * time.Second
	// logDropReportInterval throttles the process-log report of dropped lines.
	logDropReportInterval = time.Minute
)

// logWriter persists BroadcastLog lines off the pipeline's path with bounded
// memory and concurrency: a fixed queue drained by a fixed pool of workers.
//
// It used to be one goroutine per line, each doing an untimed insert, which
// under a failing node and slow storage was an unbounded number of goroutines.
//
// The zero value is ready: the pool starts on the first line, so a Registry
// built as a literal works too, and stops with the registry's context.
type logWriter struct {
	once    sync.Once
	queue   chan storage.Log
	wg      sync.WaitGroup
	dropped atomic.Uint64
	// lastDropReport is the UnixNano of the last dropped-lines report.
	lastDropReport atomic.Int64
}

// LogWritesDropped reports log lines discarded because log storage could not
// keep up.
func (r *Registry) LogWritesDropped() uint64 { return r.logWriter.dropped.Load() }

// enqueueLog hands a line to the writer pool without ever blocking.
func (r *Registry) enqueueLog(l storage.Log) {
	w := &r.logWriter
	w.once.Do(r.startLogWriters)

	select {
	case w.queue <- l:
	default:
		// Full (or stopped, where the queue is nil): drop and count. The
		// pipeline must not wait on log storage.
		n := w.dropped.Add(1)
		r.reportDroppedLogs(n)
	}
}

func (r *Registry) startLogWriters() {
	ctx := r.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		// Already shut down: leave the queue nil so every line is dropped.
		return
	}
	w := &r.logWriter
	w.queue = make(chan storage.Log, logWriteQueueSize)
	timeout := r.logWriteTimeout
	if timeout <= 0 {
		timeout = defaultLogWriteTimeout
	}
	for range logWriteWorkers {
		w.wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case l := <-w.queue:
					wctx, cancel := context.WithTimeout(ctx, timeout)
					_ = r.persistLog(wctx, l)
					cancel()
				}
			}
		})
	}
}

// stopLogWriters waits, up to limit, for the writer pool to exit after the
// registry's context is cancelled. It also stops a pool that has not started
// from starting later.
func (r *Registry) stopLogWriters(limit time.Duration) {
	w := &r.logWriter
	w.once.Do(func() {})
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		// A storage call that ignores its context; do not hold shutdown on it.
	}
}

func (r *Registry) reportDroppedLogs(total uint64) {
	now := time.Now().UnixNano()
	last := r.logWriter.lastDropReport.Load()
	if last != 0 && time.Duration(now-last) < logDropReportInterval {
		return
	}
	if !r.logWriter.lastDropReport.CompareAndSwap(last, now) {
		return
	}
	if r.logger != nil {
		r.logger.Warn("Log storage is not keeping up; workflow log lines were dropped",
			"dropped_total", total,
			"queue_size", logWriteQueueSize,
			"throttled_for", logDropReportInterval.String())
	}
}
