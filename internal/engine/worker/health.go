package worker

import (
	"context"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/user/hermod/internal/factory"
	"github.com/user/hermod/internal/storage"
)

func (w *Worker) checkHealth(ctx context.Context) {
	if time.Since(w.lastHealthCheck) < 30*time.Second {
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

func (w *Worker) checkResourcesHealth(ctx context.Context) {
	sources, _, _ := w.storage.ListSources(ctx, storage.CommonFilter{})
	for _, src := range sources {
		if w.isResourceAssigned(src.ID, src.WorkerID) && !w.registry.IsResourceInUse(ctx, src.ID, "", true) {
			w.checkSourceHealth(ctx, src)
		}
	}
	sinks, _, _ := w.storage.ListSinks(ctx, storage.CommonFilter{})
	for _, snk := range sinks {
		if w.isResourceAssigned(snk.ID, snk.WorkerID) && !w.registry.IsResourceInUse(ctx, snk.ID, "", false) {
			w.checkSinkHealth(ctx, snk)
		}
	}
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
