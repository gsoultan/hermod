package registry

import (
	"context"
	"fmt"
	"time"

	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/comm/message"
)

func (r *Registry) startReconciliationLoop(ctx context.Context) {
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
	msgs, err := r.storage.ListSuspendedMessages(ctx, "", time.Now())
	if err != nil {
		return
	}

	for _, sm := range msgs {
		r.resumeSuspendedMessage(ctx, sm)
	}
}

func (r *Registry) resumeSuspendedMessage(ctx context.Context, sm storage.SuspendedMessage) {
	r.mu.Lock()
	ae, ok := r.engines[sm.WorkflowID]
	r.mu.Unlock()

	if !ok {
		// Workflow engine not running on this worker, skip or handle re-assignment
		return
	}

	m := message.AcquireMessage()
	m.SetID(sm.ID)
	m.SetAfter(sm.Payload)
	for k, v := range sm.Metadata {
		m.SetMetadata(k, v)
	}
	for k, v := range sm.Data {
		m.SetData(k, v)
	}

	r.BroadcastLog(sm.WorkflowID, "INFO", fmt.Sprintf("Resuming suspended message at node %s", sm.NodeID), m.ID())

	// AE has the needed maps
	r.resumeFromNode(sm.WorkflowID, sm.NodeID, m, ae.workflow, ae.nodeMap, ae.adj, ae.sinks, ae.sinkNodeToIndex, "")
	_ = r.storage.DeleteSuspendedMessage(ctx, sm.ID)
	message.ReleaseMessage(m)
}
