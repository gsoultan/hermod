package worker

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/user/hermod/internal/factory"
	"github.com/user/hermod/internal/storage"
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
		cpuUsage, memUsage := w.getMetrics()
		w.currentCPU, w.currentMem = cpuUsage, memUsage
		_ = w.storage.UpdateWorkerHeartbeat(ctx, w.workerGUID, cpuUsage, memUsage)
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

func (w *Worker) getMetrics() (float64, float64) {
	v, _ := mem.VirtualMemory()
	memUsage := v.UsedPercent / 100.0
	c, _ := cpu.Percent(100*time.Millisecond, false)
	cpuUsage := 0.0
	if len(c) > 0 {
		cpuUsage = c[0] / 100.0
	} else {
		cpuUsage = float64(runtime.NumGoroutine()) / (float64(runtime.NumCPU()) * 100.0)
	}
	return min(1.0, cpuUsage), min(1.0, memUsage)
}

// maxConcurrentHealthChecks bounds how many resource health probes run in
// parallel, preventing a goroutine/connection storm when many sources/sinks
// are assigned to a single worker.
const maxConcurrentHealthChecks = 8

func (w *Worker) checkResourcesHealth(ctx context.Context) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentHealthChecks)

	// Optimization: fetch only sources and sinks assigned to this worker.
	sources, _, _ := w.storage.ListSources(ctx, storage.CommonFilter{WorkerID: w.workerGUID})
	for _, src := range sources {
		if !w.registry.IsResourceInUse(ctx, src.ID, "", true) {
			s := src
			sem <- struct{}{}
			wg.Go(func() {
				defer func() {
					<-sem
					if r := recover(); r != nil {
						w.logger.Error("Worker: checkSourceHealth panicked", "source_id", s.ID, "panic", r)
					}
				}()
				w.checkSourceHealth(ctx, s)
			})
		}
	}

	sinks, _, _ := w.storage.ListSinks(ctx, storage.CommonFilter{WorkerID: w.workerGUID})
	for _, snk := range sinks {
		if !w.registry.IsResourceInUse(ctx, snk.ID, "", false) {
			s := snk
			sem <- struct{}{}
			wg.Go(func() {
				defer func() {
					<-sem
					if r := recover(); r != nil {
						w.logger.Error("Worker: checkSinkHealth panicked", "sink_id", s.ID, "panic", r)
					}
				}()
				w.checkSinkHealth(ctx, s)
			})
		}
	}
	wg.Wait()
}

func (w *Worker) isResourceAssigned(id, workerID string) bool {
	if workerID != "" {
		return w.workerGUID != "" && workerID == w.workerGUID
	}
	return w.isAssigned(id, workerID)
}

func (w *Worker) checkSourceHealth(ctx context.Context, src storage.Source) {
	status := "running"
	s, err := factory.CreateSource(factory.SourceConfig{Type: src.Type, Config: src.Config})
	if err != nil || s.Ping(ctx) != nil {
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
	status := "running"
	s, err := factory.CreateSink(factory.SinkConfig{Type: snk.Type, Config: snk.Config})
	if err != nil || s.Ping(ctx) != nil {
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
