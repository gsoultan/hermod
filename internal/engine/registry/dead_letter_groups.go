// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// openDeadLetters is the set of records with a dead letter waiting at a sink,
// per workflow, and the id of each one's row. It is what lets a later
// delivery of the same record close the row without a store lookup per
// delivered message. The zero value is ready to use.
//
// It can be stale in one direction only: a row settled on the page stays here
// until a delivery tries to close it and the store says it already is. It is
// never missing a row this process parked; one parked by another process, or
// grouped by the retention sweep, is picked up the next time the workflow
// starts (loadOpenDeadLetters).
type openDeadLetters struct {
	mu         sync.RWMutex
	byWorkflow map[string]map[storage.DeadLetterGroupKey]string
}

func (o *openDeadLetters) add(g storage.DeadLetterGroupKey, id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.byWorkflow == nil {
		o.byWorkflow = map[string]map[storage.DeadLetterGroupKey]string{}
	}
	groups := o.byWorkflow[g.WorkflowID]
	if groups == nil {
		groups = map[storage.DeadLetterGroupKey]string{}
		o.byWorkflow[g.WorkflowID] = groups
	}
	groups[g] = id
}

// has reports whether workflowID has any record waiting, the cheap check
// every delivery makes first.
func (o *openDeadLetters) has(workflowID string) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return len(o.byWorkflow[workflowID]) > 0
}

// take removes g and returns the id of its row, if it had one.
func (o *openDeadLetters) take(g storage.DeadLetterGroupKey) (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	groups := o.byWorkflow[g.WorkflowID]
	id, ok := groups[g]
	if ok {
		delete(groups, g)
	}
	return id, ok
}

// openDeadLetterPage and openDeadLetterPages bound what a workflow start
// reads back: past that many waiting rows, a delivery of one of the rest
// simply does not close it, which is how every row behaved before.
const (
	openDeadLetterPage  = 500
	openDeadLetterPages = 20
)

// loadOpenDeadLetters reads the records wf has waiting at a sink into the
// set, so deliveries after a restart close them too.
func (r *Registry) loadOpenDeadLetters(ctx context.Context, wf storage.Workflow) {
	ds, ok := r.store().(storage.DeadLetterStore)
	if !ok || !wf.DeadLetters {
		return
	}
	for page := range openDeadLetterPages {
		rows, _, err := ds.ListDeadLetters(ctx, storage.DeadLetterFilter{
			WorkflowID: wf.ID, Status: storage.DeadLetterQueued, Limit: openDeadLetterPage, Offset: page * openDeadLetterPage,
		})
		if err != nil {
			r.logger.Warn("Registry: reading waiting dead letters failed; a later delivery will not close them",
				"workflow_id", wf.ID, "error", err)
			return
		}
		for _, d := range rows {
			if d.SinkID != "" && d.RecordKey != "" {
				r.openDLs.add(d.GroupKey(), d.ID)
			}
		}
		if len(rows) < openDeadLetterPage {
			return
		}
	}
}

// settleDeliveredTimeout bounds closing one row; it runs off the delivery path.
const settleDeliveredTimeout = 10 * time.Second

// deliveredObserver closes the waiting dead letter of a record a sink just
// took. A source that re-sends a record until it goes through is the case it
// is for: without it, the row stays on the page after the record was
// delivered, and replaying it sends the record twice.
//
// With nothing waiting in the workflow it returns at once. Otherwise each
// delivered message is fingerprinted, which costs one hash of the payload.
func (r *Registry) deliveredObserver(wf storage.Workflow) pkgengine.DeliveredObserver {
	fields := wf.DeadLetterKeyFields
	return func(sinkID string, m hermod.Message) {
		if !r.openDLs.has(wf.ID) {
			return
		}
		origin := storage.DeadLetterOrigin{Operation: string(m.Operation()), Table: m.Table(), Schema: m.Schema()}
		g := storage.DeadLetterGroupKey{
			WorkflowID: wf.ID, SinkID: sinkID,
			RecordKey: storage.DeadLetterRecordKey(m.Payload(), origin, fields),
		}
		id, ok := r.openDLs.take(g)
		if !ok {
			return
		}
		messageID := m.ID()
		go r.settleDelivered(wf.ID, id, messageID)
	}
}

func (r *Registry) settleDelivered(workflowID, id, messageID string) {
	ds, ok := r.store().(storage.DeadLetterStore)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), settleDeliveredTimeout)
	defer cancel()
	ev := storage.NewDeadLetterEvent("delivered", "", "a later attempt (message "+messageID+") was delivered")
	err := ds.SettleDeadLetter(ctx, id, storage.DeadLetterDelivered, ev)
	if err != nil && !errors.Is(err, storage.ErrDeadLetterSettled) && !errors.Is(err, storage.ErrNotFound) {
		r.logger.Warn("Registry: closing a delivered dead letter failed; it stays on the page",
			"workflow_id", workflowID, "dead_letter_id", id, "error", err)
	}
}
