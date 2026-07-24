package worker

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"time"

	"github.com/user/hermod/internal/factory"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/engine/telemetry"
)

func (w *Worker) sync(ctx context.Context, initial bool) {
	workerID := w.getWorkerMetricID()
	defer w.trackSyncMetrics(time.Now(), workerID)

	// Optimization: fetch only relevant workflows to reduce storage load and improve scalability.
	// We need:
	// 1. Workflows assigned to us (WorkerID == GUID)
	// 2. Workflows we currently own (OwnerID == GUID)
	// 3. Unassigned active workflows (WorkerID == "" AND Active == true)
	// 4. Workflows that were active but are now inactive (to stop them) - handled by fetching all our assigned ones.

	active := true

	// Fetch assigned workflows (including inactive ones so we can stop them if needed)
	assigned, _, err := w.storage.ListWorkflows(ctx, storage.CommonFilter{WorkerID: w.workerGUID})
	if err != nil {
		w.logger.Error("Worker: failed to list assigned workflows", "error", err)
	}

	// Fetch owned workflows
	owned, _, err := w.storage.ListWorkflows(ctx, storage.CommonFilter{OwnerID: w.workerGUID})
	if err != nil {
		w.logger.Error("Worker: failed to list owned workflows", "error", err)
	}

	// Fetch unassigned active workflows
	unassigned, _, err := w.storage.ListWorkflows(ctx, storage.CommonFilter{WorkerID: "", Active: &active})
	if err != nil {
		w.logger.Error("Worker: failed to list unassigned workflows", "error", err)
	}

	// Combine and deduplicate
	wfMap := make(map[string]storage.Workflow)
	for _, wf := range assigned {
		wfMap[wf.ID] = wf
	}
	for _, wf := range owned {
		wfMap[wf.ID] = wf
	}
	for _, wf := range unassigned {
		wfMap[wf.ID] = wf
	}

	// Also fetch active ones that might have changed their workerID to empty or something else
	// (though unassigned already covers WorkerID=="")
	// For robustness, if total workflows is small, we could just fetch all active.
	// But for large scale, we rely on the above.

	workflows := make([]storage.Workflow, 0, len(wfMap))
	for _, wf := range wfMap {
		workflows = append(workflows, wf)
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
		cpu, mem := w.GetMetrics()
		if cpu > 0.85 || mem > 0.85 {
			w.logger.Warn("Worker: admission control rejected new workflow", "workflow_id", wf.ID, "cpu", cpu, "mem", mem)
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
		w.reportWorkflowHealth(ctx, wf.ID)
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
		_ = w.registry.StopEngine(ctx, wf.ID)
		w.stopLeaseRenewal(wf.ID)
		if w.workerGUID != "" {
			_ = w.storage.ReleaseWorkflowLease(ctx, wf.ID, w.workerGUID)
		}
		return
	}

	if w.hasConfigChanged(wf, sctx) {
		w.logger.Info("Worker: config changed, restarting", "workflow_id", wf.ID)
		_ = w.storage.UpdateWorkflowStatus(ctx, wf.ID, "Restarting")
		_ = w.registry.StopEngineWithoutUpdate(ctx, wf.ID)
		w.stopLeaseRenewal(wf.ID)
		w.startWorkflow(ctx, wf, sctx.WorkerID)
	}
}

func (w *Worker) hasConfigChanged(wf storage.Workflow, sctx SyncContext) bool {
	curWf, ok := w.registry.GetWorkflowConfig(wf.ID)
	if !ok {
		return true
	}
	if !jsonEqual(curWf.Nodes, wf.Nodes) {
		return true
	}
	if !jsonEqual(curWf.Edges, wf.Edges) {
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

func (w *Worker) reportWorkflowHealth(ctx context.Context, id string) {
	health, ok := w.registry.GetWorkflowHealth(id)
	if !ok {
		return
	}

	// Update storage with real-time stats from the engine
	_ = w.storage.UpdateWorkflowStats(ctx, id, health.Processed, health.Errors, health.Lag)

	// If the workflow is degraded or in error, log the issues for observability
	if health.Status != "healthy" && len(health.Issues) > 0 {
		w.logger.Warn("Worker: workflow health issue detected", "workflow_id", id, "status", health.Status, "issues", health.Issues)
	}
}

func (w *Worker) stopWorkflow(ctx context.Context, id string) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				w.logger.Error("Worker: stopWorkflow panicked", "workflow_id", id, "panic", r)
			}
		}()
		// Use context.Background for the actual stop to ensure it completes even if
		// the triggering sync context is canceled, but with a reasonable timeout.
		stopCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		_ = w.registry.StopEngineWithoutUpdate(stopCtx, id)
		w.stopLeaseRenewal(id)
		if w.workerGUID != "" {
			_ = w.storage.ReleaseWorkflowLease(context.Background(), id, w.workerGUID)
		}
	}()
}

func (w *Worker) hasResourceConfigChanged(workflowID string, sourceMap map[string]storage.Source, sinkMap map[string]storage.Sink) bool {
	if srcConfigs, ok := w.registry.GetSourceConfigs(workflowID); ok && sourceConfigsChanged(srcConfigs, sourceMap) {
		return true
	}
	if snkConfigs, ok := w.registry.GetSinkConfigs(workflowID); ok && sinkConfigsChanged(snkConfigs, sinkMap) {
		return true
	}
	return false
}

// sourceConfigsChanged reports whether any running source config differs from
// its stored counterpart.
func sourceConfigsChanged(running []factory.SourceConfig, stored map[string]storage.Source) bool {
	for _, sc := range running {
		dbSrc, exists := stored[sc.ID]
		if !exists {
			continue
		}
		if !jsonEqual(dbSrc.Config, sc.Config) || dbSrc.Type != sc.Type {
			return true
		}
	}
	return false
}

// sinkConfigsChanged reports whether any running sink config differs from its
// stored counterpart.
func sinkConfigsChanged(running []factory.SinkConfig, stored map[string]storage.Sink) bool {
	for _, sc := range running {
		dbSnk, exists := stored[sc.ID]
		if !exists {
			continue
		}
		if !jsonEqual(dbSnk.Config, sc.Config) || dbSnk.Type != sc.Type {
			return true
		}
	}
	return false
}

// jsonEqual reports whether a and b are equal after canonical JSON encoding.
// It is more robust than reflect.DeepEqual for configs that survive a DB/JSON
// round-trip: JSON encoding canonicalizes map key ordering and collapses
// numerically-equal values (e.g. int 1 vs float64 1) that would otherwise make
// reflect.DeepEqual report a spurious change and trigger an endless
// restart loop. It falls back to reflect.DeepEqual if either value cannot be
// marshaled.
func jsonEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return reflect.DeepEqual(a, b)
	}

	var av, bv any
	if err := json.Unmarshal(ab, &av); err != nil {
		return reflect.DeepEqual(a, b)
	}
	if err := json.Unmarshal(bb, &bv); err != nil {
		return reflect.DeepEqual(a, b)
	}

	// Remove internal fields starting with underscore before comparison
	stripInternalFields(av)
	stripInternalFields(bv)

	equal := reflect.DeepEqual(av, bv)
	return equal
}

func stripInternalFields(v any) {
	switch m := v.(type) {
	case map[string]any:
		for k, val := range m {
			if len(k) > 0 && k[0] == '_' {
				delete(m, k)
				continue
			}
			stripInternalFields(val)
		}
	case []any:
		for _, item := range m {
			stripInternalFields(item)
		}
	}
}
