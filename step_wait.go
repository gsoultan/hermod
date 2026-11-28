// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package hermod

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// StepWait is how long one workflow step waited for room on its database or
// API, and what held it.
//
// A step that answers busy is waited out rather than failed (see ErrBusy), so
// it succeeds, only later. Before this was recorded, that left nothing in the
// run but a longer duration: the Runs view could not say the step had waited,
// or behind whom.
type StepWait struct {
	// For is the time spent waiting, which is part of the step's duration.
	For time.Duration `json:"for"`
	// Reason is the first busy answer, which says where the wait was.
	Reason string `json:"reason,omitempty"`
	// Holders are Hermod's own long queries on the database when the step
	// was refused, longest first. Empty when the answer named none.
	Holders []Holder `json:"holders,omitempty"`
}

// Holder is a query Hermod had running on a database that another step waited
// behind. It names the query's workflow and step, never its text, which can
// hold personal data.
type Holder struct {
	WorkflowID string        `json:"workflow_id,omitempty"`
	NodeID     string        `json:"node_id,omitempty"`
	Kind       string        `json:"kind"`
	Running    time.Duration `json:"running"`
}

// stepWaitSlot collects one step's waits. It is locked because a step that
// fans out (a pipeline, a subflow) can run nested steps on the same context
// at once.
type stepWaitSlot struct {
	mu     sync.Mutex
	wait   StepWait
	waited bool
}

type stepWaitKey struct{}

// WithStepWait returns ctx with an empty slot for the step about to run on
// it. Whatever records the step reads it afterwards with StepWaitFrom.
func WithStepWait(ctx context.Context) context.Context {
	return context.WithValue(ctx, stepWaitKey{}, &stepWaitSlot{})
}

// AddStepWait records that the step running on ctx waited d after the busy
// answer err. Waits add up; the first answer's reason is kept, and holders
// come from the first answer that named any. Without a slot -- a preview, a
// test -- it does nothing.
func AddStepWait(ctx context.Context, d time.Duration, err error) {
	slot, ok := ctx.Value(stepWaitKey{}).(*stepWaitSlot)
	if !ok {
		return
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	slot.waited = true
	slot.wait.For += d
	if slot.wait.Reason == "" && err != nil {
		slot.wait.Reason = err.Error()
	}
	if len(slot.wait.Holders) == 0 {
		slot.wait.Holders = slices.Clone(holdersOf(err))
	}
}

// StepWaitFrom returns a copy of what the step running on ctx waited, or nil
// when it did not wait.
func StepWaitFrom(ctx context.Context) *StepWait {
	slot, ok := ctx.Value(stepWaitKey{}).(*stepWaitSlot)
	if !ok {
		return nil
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if !slot.waited {
		return nil
	}
	w := slot.wait
	w.Holders = slices.Clone(w.Holders)
	return &w
}

func holdersOf(err error) []Holder {
	var b *Busy
	if errors.As(err, &b) {
		return b.Holders
	}
	return nil
}
