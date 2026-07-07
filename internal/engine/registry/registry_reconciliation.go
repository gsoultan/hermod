package registry

import (
	"context"
	"time"

	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/comm/message"
)

func (r *Registry) startReconciliationLoop(ctx context.Context) {
	defer func() {
		if p := recover(); p != nil {
			r.logger.Error("Registry: reconciliation loop panicked", "panic", p)
		}
	}()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.reconcileSuspendedMessages(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (r *Registry) reconcileSuspendedMessages(ctx context.Context) {
	if r.storage == nil {
		return
	}
	msgs, err := r.storage.ListSuspendedMessages(ctx, "", time.Now())
	if err != nil {
		return
	}

	for _, sm := range msgs {
		r.resumeSuspendedMessage(ctx, sm)
	}
}

// claimSuspendedMessage marks a suspended message as in-flight. It returns false
// if the message is already being resumed by another reconciliation pass.
func (r *Registry) claimSuspendedMessage(id string) bool {
	r.reconcilingMu.Lock()
	defer r.reconcilingMu.Unlock()
	if _, inFlight := r.reconciling[id]; inFlight {
		return false
	}
	r.reconciling[id] = struct{}{}
	return true
}

// releaseSuspendedMessage clears the in-flight marker for a suspended message.
func (r *Registry) releaseSuspendedMessage(id string) {
	r.reconcilingMu.Lock()
	defer r.reconcilingMu.Unlock()
	delete(r.reconciling, id)
}

func (r *Registry) resumeSuspendedMessage(ctx context.Context, sm storage.SuspendedMessage) {
	r.mu.RLock()
	ae, ok := r.engines[sm.WorkflowID]
	r.mu.RUnlock()

	if !ok {
		// Workflow engine not running on this worker, skip or handle re-assignment
		return
	}

	// Claim the message so overlapping reconciliation ticks don't resume it twice.
	if !r.claimSuspendedMessage(sm.ID) {
		return
	}
	defer r.releaseSuspendedMessage(sm.ID)

	m := message.AcquireMessage()
	m.SetID(sm.ID)
	m.SetAfter(sm.Payload)
	for k, v := range sm.Metadata {
		m.SetMetadata(k, v)
	}
	for k, v := range sm.Data {
		m.SetData(k, v)
	}

	r.BroadcastLog(sm.WorkflowID, "INFO", "Resuming suspended message at node "+sm.NodeID, m.ID())

	// AE has the needed maps
	r.resumeFromNode(sm.WorkflowID, sm.NodeID, m, ae.workflow, ae.nodeMap, ae.adj, ae.sinks, ae.sinkNodeToIndex, "")
	if r.storage != nil {
		_ = r.storage.DeleteSuspendedMessage(ctx, sm.ID)
	}
	message.ReleaseMessage(m)
}
