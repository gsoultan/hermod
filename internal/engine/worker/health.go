package worker

import (
	"context"
	"sync"
	"time"

	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
)

func (w *Worker) checkHealth(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("Worker: health check panicked", "panic", r)
		}
	}()
	if time.Since(w.lastHealthCheck) < w.heartbeatInterval() {
		return
	}
	w.lastHealthCheck = time.Now()
	if w.workerGUID != "" {
		res := currentHostResources()
		w.SetResources(res)
		_ = w.storage.UpdateWorkerHeartbeat(ctx, w.workerGUID, res)
	}
	w.checkResourcesHealth(ctx)
}

// heartbeatInterval is how often the worker samples resource usage and refreshes
// its persisted heartbeat. It tracks the lease TTL (capped at 30s) so that with
// short TTLs the worker's liveness stays well within the online window, while
// keeping the historical 30s cadence for the default 30s TTL.
func (w *Worker) heartbeatInterval() time.Duration {
	secs := min(30, max(5, w.leaseTTLSeconds))
	return time.Duration(secs) * time.Second
}

// maxConcurrentHealthChecks bounds how many resource health probes run in
// parallel, preventing a goroutine/connection storm when many sources/sinks
// are assigned to a single worker.
const maxConcurrentHealthChecks = 8

func (w *Worker) checkResourcesHealth(ctx context.Context) {
	if !w.healthChecking.CompareAndSwap(false, true) {
		return
	}

	go func() {
		defer w.healthChecking.Store(false)

		var wg sync.WaitGroup
		sem := make(chan struct{}, maxConcurrentHealthChecks)

		// Optimization: fetch only sources and sinks assigned to this worker.
		// Use a background context since this goroutine might outlive the poll cycle.
		sources, _, _ := w.storage.ListSources(context.Background(), storage.CommonFilter{WorkerID: w.workerGUID})
		for _, src := range sources {
			if !w.registry.IsResourceInUse(context.Background(), src.ID, "", true) {
				s := src
				sem <- struct{}{}
				wg.Go(func() {
					defer func() {
						<-sem
						if r := recover(); r != nil {
							w.logger.Error("Worker: checkSourceHealth panicked", "source_id", s.ID, "panic", r)
						}
					}()
					w.checkSourceHealth(context.Background(), s)
				})
			}
		}

		sinks, _, _ := w.storage.ListSinks(context.Background(), storage.CommonFilter{WorkerID: w.workerGUID})
		for _, snk := range sinks {
			if !w.registry.IsResourceInUse(context.Background(), snk.ID, "", false) {
				s := snk
				sem <- struct{}{}
				wg.Go(func() {
					defer func() {
						<-sem
						if r := recover(); r != nil {
							w.logger.Error("Worker: checkSinkHealth panicked", "sink_id", s.ID, "panic", r)
						}
					}()
					w.checkSinkHealth(context.Background(), s)
				})
			}
		}
		wg.Wait()
	}()
}

func (w *Worker) checkSourceHealth(ctx context.Context, src storage.Source) {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	status := "running"
	s, err := factory.CreateSource(factory.SourceConfig{Type: src.Type, Config: src.Config})
	if err != nil || s.Ping(checkCtx) != nil {
		status = "error"
	}
	if s != nil {
		s.Close()
	}
	if src.Status != status {
		src.Status = status
		_ = w.storage.UpdateSource(ctx, src)
	}
}

func (w *Worker) checkSinkHealth(ctx context.Context, snk storage.Sink) {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	status := "running"
	s, err := factory.CreateSink(factory.SinkConfig{Type: snk.Type, Config: snk.Config})
	if err != nil || s.Ping(checkCtx) != nil {
		status = "error"
	}
	if s != nil {
		s.Close()
	}
	if snk.Status != status {
		snk.Status = status
		_ = w.storage.UpdateSink(ctx, snk)
	}
}
