package worker

import (
	"context"
	"reflect"
	"sync"
	"time"

	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/engine/telemetry"
)

func (w *Worker) sync(ctx context.Context, initial bool) {
	workerID := w.getWorkerMetricID()
	defer w.trackSyncMetrics(time.Now(), workerID)

	workflows, _, err := w.storage.ListWorkflows(ctx, storage.CommonFilter{})
	if err != nil {
		w.logger.Error("Worker: failed to list workflows", "error", err)
		telemetry.WorkerSyncErrors.WithLabelValues(workerID).Inc()
		return
	}

	srcMap, snkMap := w.loadResourceMaps(ctx)
	w.updateActiveWorkflowMetrics(workflows, workerID, initial)

	sctx := SyncContext{SourceMap: srcMap, SinkMap: snkMap, WorkerID: workerID}
	w.syncAllWorkflows(ctx, workflows, sctx)

	if initial {
		w.logger.Info("Worker: initial sync complete")
	}
}

func (w *Worker) getWorkerMetricID() string {
	if w.workerGUID != "" {
		return w.workerGUID
	}
	return "default"
}

func (w *Worker) trackSyncMetrics(start time.Time, workerID string) {
	telemetry.WorkerSyncDuration.WithLabelValues(workerID).Observe(time.Since(start).Seconds())
	if r := recover(); r != nil {
		w.logger.Error("Worker: sync panicked", "panic", r)
		telemetry.WorkerSyncErrors.WithLabelValues(workerID).Inc()
	}
}

func (w *Worker) loadResourceMaps(ctx context.Context) (map[string]storage.Source, map[string]storage.Sink) {
	sources, _, _ := w.storage.ListSources(ctx, storage.CommonFilter{})
	sinks, _, _ := w.storage.ListSinks(ctx, storage.CommonFilter{})

	srcMap := make(map[string]storage.Source)
	for _, s := range sources {
		srcMap[s.ID] = s
	}

	snkMap := make(map[string]storage.Sink)
	for _, s := range sinks {
		snkMap[s.ID] = s
	}
	return srcMap, snkMap
}

func (w *Worker) updateActiveWorkflowMetrics(workflows []storage.Workflow, workerID string, initial bool) {
	count := 0
	owned := 0
	for _, wf := range workflows {
		if wf.Active && w.isWorkflowAssigned(wf) {
			count++
			if w.isLeaseOwned(wf) {
				owned++
			}
		}
	}
	telemetry.WorkerActiveWorkflows.WithLabelValues(workerID).Set(float64(count))
	telemetry.WorkerLeasesOwned.WithLabelValues(workerID).Set(float64(owned))
	if initial {
		w.logger.Info("Worker: found active workflows assigned to this worker", "count", count)
	}
}

func (w *Worker) syncAllWorkflows(ctx context.Context, workflows []storage.Workflow, sctx SyncContext) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)
	for i := range workflows {
		wf := workflows[i]
		wg.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					w.logger.Error("Worker: SyncWorkflow panicked", "workflow_id", wf.ID, "panic", r)
				}
			}()
			sem <- struct{}{}
			defer func() { <-sem }()
			w.SyncWorkflow(ctx, wf, sctx)
		})
	}
	wg.Wait()
}

func (w *Worker) SyncWorkflow(ctx context.Context, wf storage.Workflow, sctx SyncContext) {
	if !w.isWorkflowAssigned(wf) {
		w.handleUnassignedWorkflow(ctx, wf)
		return
	}

	if wf.Active && !w.registry.IsEngineRunning(wf.ID) {
		if w.currentCPU > 0.85 || w.currentMem > 0.85 {
			w.logger.Warn("Worker: admission control rejected new workflow", "workflow_id", wf.ID)
			return
		}
	}

	w.processWorkflowSync(ctx, wf, sctx)
}

func (w *Worker) isWorkflowAssigned(wf storage.Workflow) bool {
	if wf.WorkerID != "" {
		return w.workerGUID != "" && wf.WorkerID == w.workerGUID
	}
	return w.isAssigned(wf.ID, wf.OwnerID)
}

func (w *Worker) isLeaseOwned(wf storage.Workflow) bool {
	return w.workerGUID != "" && wf.OwnerID == w.workerGUID && wf.LeaseUntil != nil && time.Now().Before(*wf.LeaseUntil)
}

func (w *Worker) handleUnassignedWorkflow(ctx context.Context, wf storage.Workflow) {
	if w.registry.IsEngineRunning(wf.ID) {
		w.logger.Info("Worker: workflow handed off, stopping", "workflow_id", wf.ID)
		w.stopWorkflow(ctx, wf.ID)
	}
}

func (w *Worker) processWorkflowSync(ctx context.Context, wf storage.Workflow, sctx SyncContext) {
	isRunning := w.registry.IsEngineRunning(wf.ID)
	if w.workerGUID != "" {
		if !w.ensureLease(ctx, wf, isRunning, sctx.WorkerID) {
			return
		}
	}

	if isRunning {
		w.handleRunningWorkflow(ctx, wf, sctx)
	} else if wf.Active && wf.Status != "Parked" {
		w.startWorkflow(ctx, wf, sctx.WorkerID)
	}
}

func (w *Worker) ensureLease(ctx context.Context, wf storage.Workflow, isRunning bool, workerID string) bool {
	owned := w.isLeaseOwned(wf)
	if !owned {
		acquired, _ := w.storage.AcquireWorkflowLease(ctx, wf.ID, w.workerGUID, w.leaseTTLSeconds)
		if acquired {
			w.trackLeaseAcquisition(wf, workerID)
			owned = true
		}
	}

	if !owned && isRunning {
		w.logger.Warn("Worker: stopping workflow (lease lost)", "workflow_id", wf.ID)
		w.stopWorkflow(ctx, wf.ID)
		return false
	}
	return owned
}

func (w *Worker) trackLeaseAcquisition(wf storage.Workflow, workerID string) {
	if wf.OwnerID != "" && wf.OwnerID != w.workerGUID {
		telemetry.LeaseStealTotal.WithLabelValues(workerID).Inc()
	} else {
		telemetry.LeaseAcquireTotal.WithLabelValues(workerID).Inc()
	}
}

func (w *Worker) handleRunningWorkflow(ctx context.Context, wf storage.Workflow, sctx SyncContext) {
	if !wf.Active {
		w.logger.Info("Worker: stopping workflow (inactive)", "workflow_id", wf.ID)
		_ = w.registry.StopEngine(wf.ID)
		w.stopLeaseRenewal(wf.ID)
		if w.workerGUID != "" {
			_ = w.storage.ReleaseWorkflowLease(ctx, wf.ID, w.workerGUID)
		}
		return
	}

	if w.hasConfigChanged(wf, sctx) {
		w.logger.Info("Worker: config changed, restarting", "workflow_id", wf.ID)
		_ = w.storage.UpdateWorkflowStatus(ctx, wf.ID, "Restarting")
		_ = w.registry.StopEngineWithoutUpdate(wf.ID)
		w.stopLeaseRenewal(wf.ID)
		w.startWorkflow(ctx, wf, sctx.WorkerID)
	}
}

func (w *Worker) hasConfigChanged(wf storage.Workflow, sctx SyncContext) bool {
	curWf, ok := w.registry.GetWorkflowConfig(wf.ID)
	if !ok {
		return true
	}
	if !reflect.DeepEqual(curWf.Nodes, wf.Nodes) || !reflect.DeepEqual(curWf.Edges, wf.Edges) {
		return true
	}
	// Simplified check for other fields
	if curWf.Name != wf.Name || curWf.VHost != wf.VHost || curWf.DeadLetterSinkID != wf.DeadLetterSinkID {
		return true
	}
	return w.hasResourceConfigChanged(wf.ID, sctx.SourceMap, sctx.SinkMap)
}

func (w *Worker) startWorkflow(ctx context.Context, wf storage.Workflow, workerID string) {
	w.logger.Info("Worker: starting workflow", "workflow_id", wf.ID)
	err := w.registry.StartWorkflow(wf.ID, wf)
	if err != nil {
		w.logger.Error("Worker: failed to start", "workflow_id", wf.ID, "error", err)
		telemetry.WorkerSyncErrors.WithLabelValues(workerID).Inc()
	} else if w.workerGUID != "" {
		w.startLeaseRenewal(wf.ID)
	}
}

func (w *Worker) stopWorkflow(ctx context.Context, id string) {
	go func() {
		_ = w.registry.StopEngineWithoutUpdate(id)
		w.stopLeaseRenewal(id)
		if w.workerGUID != "" {
			_ = w.storage.ReleaseWorkflowLease(ctx, id, w.workerGUID)
		}
	}()
}

func (w *Worker) hasResourceConfigChanged(workflowID string, sourceMap map[string]storage.Source, sinkMap map[string]storage.Sink) bool {
	srcConfigs, ok := w.registry.GetSourceConfigs(workflowID)
	if ok {
		for _, sc := range srcConfigs {
			if dbSrc, exists := sourceMap[sc.ID]; exists {
				if !reflect.DeepEqual(dbSrc.Config, sc.Config) || dbSrc.Type != sc.Type {
					return true
				}
			}
		}
	}
	snkConfigs, ok := w.registry.GetSinkConfigs(workflowID)
	if ok {
		for _, sc := range snkConfigs {
			if dbSnk, exists := sinkMap[sc.ID]; exists {
				if !reflect.DeepEqual(dbSnk.Config, sc.Config) || dbSnk.Type != sc.Type {
					return true
				}
			}
		}
	}
	return false
}
