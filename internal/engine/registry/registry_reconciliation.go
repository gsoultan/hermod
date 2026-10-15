// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package registry

import (
	"context"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
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
	if r.store() == nil {
		return
	}
	msgs, err := r.store().ListSuspendedMessages(ctx, "", time.Now())
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
	r.resumeFromNode(ctx, sm.WorkflowID, sm.NodeID, m, ae.engine, ae.workflow, ae.nodeMap, ae.adj, ae.sinks, ae.sinkNodeToIndex, "")
	// A walk cut short by shutdown keeps its suspended row, so the message is
	// resumed again rather than lost.
	if s := r.store(); s != nil && ctx.Err() == nil {
		_ = s.DeleteSuspendedMessage(ctx, sm.ID)
	}
	// Release, not ReleaseMessage: the latter resets and pools the message
	// unconditionally. If anything downstream still holds a reference, that hands a
	// live message to the next Acquire and the two owners then read each other's
	// data. Nothing retains it on this path today; the refcount is what keeps that
	// true if the path changes.
	m.Release()
}
