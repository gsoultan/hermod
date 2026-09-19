package registry

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// statusWriteGate remembers what a workflow's status rows already hold, so a
// value that has not moved is not written again.
//
// The engine notifies on every status *write*, not on every status *change*:
// checkHealth pings each sink once a second and calls setSinkStatus for each,
// and SetEngineStatusUnless publishes "running" over "running". Those
// notifications have to keep flowing — they are the only thing that pushes
// per-workflow status to the UI, and unlike the dashboard there is no periodic
// floor behind them — so the redundancy is absorbed here, at the storage
// boundary, rather than by firing less often.
//
// The callback's own comment says the statuses "change rarely". This is what
// makes that true.
type statusWriteGate struct {
	mu   sync.Mutex
	last map[string]string
}

func newStatusWriteGate() *statusWriteGate {
	return &statusWriteGate{last: make(map[string]string)}
}

// changed reports whether value differs from the last one recorded for key,
// recording it when it does. An unseen key always counts as changed: storage
// may hold anything from a previous run, so the first write of a workflow's
// life establishes the value rather than assuming it.
func (g *statusWriteGate) changed(key, value string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if prev, seen := g.last[key]; seen && prev == value {
		return false
	}
	g.last[key] = value
	return true
}

// forget drops a recorded value so the next pass writes it again.
//
// Called when a write fails. Leaving the value recorded would mean storage had
// refused it and nothing ever tried again: the UI would show the previous
// status for the life of the workflow, which is the opposite of what a status
// row is for.
func (g *statusWriteGate) forget(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.last, key)
}

// applyStatusUpdate persists a status update and raises whatever it implies.
//
// Writes are synchronous because they are few — which is only true because of
// the gate. Without it a workflow with N sinks wrote (N+1) x (N+2) rows a
// second, for ever, almost always re-storing the value already there.
func (r *Registry) applyStatusUpdate(
	ctx context.Context,
	id string,
	gate *statusWriteGate,
	update telemetry.StatusUpdate,
	dlqAlerted *atomic.Bool,
) {
	store := r.store()
	if store == nil {
		return
	}

	if gate.changed("workflow", update.EngineStatus) {
		if err := store.UpdateWorkflowStatus(ctx, id, update.EngineStatus); err != nil {
			gate.forget("workflow")
		}
	}
	if update.SourceID != "" {
		key := "source:" + update.SourceID
		if gate.changed(key, update.SourceStatus) {
			if err := store.UpdateSourceStatus(ctx, update.SourceID, update.SourceStatus); err != nil {
				gate.forget(key)
			}
		}
	}
	for sinkID, status := range update.SinkStatuses {
		key := "sink:" + sinkID
		if gate.changed(key, status) {
			if err := store.UpdateSinkStatus(ctx, sinkID, status); err != nil {
				gate.forget(key)
			}
		}
	}

	r.notifyOnStatusChange(ctx, id, update, dlqAlerted)
}
