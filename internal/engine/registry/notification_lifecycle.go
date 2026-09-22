package registry

import (
	"context"
	"fmt"

	"github.com/gsoultan/hermod/internal/notification"
	"github.com/gsoultan/hermod/internal/storage"
)

// Lifecycle alerting.
//
// A workflow that stops delivering does not always look like an error. The
// engine reports "Stopped" when an operator stops it and the process reports
// nothing at all when a worker goes down, so the two events an operator most
// wants pushed to them were the two the alerting predicates could not match:
// notifyOnStatusChange keys off EngineStatus containing "error", and neither
// of these does.

// SetWorkerID records which worker this registry belongs to, so a worker-level
// alert can say which one went down. A deployment runs several and "a worker is
// shutting down" is not actionable without the name.
func (r *Registry) SetWorkerID(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.workerID = id
}

func (r *Registry) getWorkerID() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.workerID
}

// notify sends one alert at ERROR if a notification service is configured.
// Every lifecycle call site goes through here or notifyAt, so none of them has
// to nil-check.
func (r *Registry) notify(ctx context.Context, title, message string, wf storage.Workflow) {
	r.notifyAt(ctx, notification.LevelError, title, message, wf)
}

// notifyAt sends one alert at an explicit severity.
//
// A workflow an operator stopped and a worker draining for a deploy are routine
// and must not be filed as errors: they would show up in the UI's error view
// and add ERROR rows — which are never sampled — to the log table.
func (r *Registry) notifyAt(ctx context.Context, level, title, message string, wf storage.Workflow) {
	if r.notificationService == nil {
		return
	}
	r.notificationService.NotifyLevel(ctx, level, title, message, wf)
}

// notifyWorkflowStopped reports a workflow an operator stopped.
//
// The workflow is re-read so the alert can name it. A stop for which the
// workflow can no longer be read is still worth reporting, so a failed read
// falls back to the ID rather than dropping the alert.
func (r *Registry) notifyWorkflowStopped(ctx context.Context, id string) {
	if r.notificationService == nil {
		return
	}

	wf := storage.Workflow{ID: id, Name: id}
	if store := r.store(); store != nil {
		if found, err := store.GetWorkflow(ctx, id); err == nil {
			wf = found
		}
	}

	r.notifyAt(ctx, notification.LevelInfo, "Workflow Stopped",
		fmt.Sprintf("Workflow '%s' (ID: %s) was stopped and is no longer processing messages",
			wf.Name, wf.ID), wf)
}

// NotifyWorkerShutdown reports that this worker is going down and taking its
// running workflows with it.
//
// One alert for the worker rather than one per workflow: a worker running forty
// workflows would otherwise send forty messages at the moment its operator is
// least able to read them.
func (r *Registry) NotifyWorkerShutdown(ctx context.Context, activeWorkflows int) {
	if r.notificationService == nil {
		return
	}
	// StopAll is reached from the signal handler, from the worker's own drain
	// and from the edge binary, and a shutdown may run more than one of them.
	// The alert describes the worker, not a workflow, so the per-workflow
	// dedupe in the notification service does not cover it.
	if !r.workerShutdownAlerted.CompareAndSwap(false, true) {
		return
	}

	worker := r.getWorkerID()
	if worker == "" {
		worker = "unnamed worker"
	}

	// The synthetic workflow carries no ID on purpose: this is not a workflow
	// event, and giving it one would file it against a real workflow in the UI.
	r.notifyAt(ctx, notification.LevelInfo, "Worker Shutting Down",
		fmt.Sprintf("Worker %s is shutting down; %d running workflow(s) are stopping with it and will be picked up by another worker if one is available",
			worker, activeWorkflows),
		storage.Workflow{Name: worker})
}
