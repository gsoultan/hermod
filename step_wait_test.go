// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package hermod_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// A step that waited for a connection used to leave nothing behind but a
// longer duration: the Runs view could not say that it had waited, or behind
// whom. The wait is now collected on the step's context and read back by
// whatever records the step.
func TestStepWaitCollectsWhatTheStepWaitedBehind(t *testing.T) {
	ctx := hermod.WithStepWait(t.Context())
	holders := []hermod.Holder{{WorkflowID: "wf-report", NodeID: "n3", Kind: "SQL step", Running: 7 * time.Minute}}
	busy := &hermod.Busy{Err: errors.New("no connection free"), Holders: holders}

	hermod.AddStepWait(ctx, 2*time.Second, fmt.Errorf("lookup: %w", busy))
	hermod.AddStepWait(ctx, 3*time.Second, &hermod.Busy{Err: errors.New("later answer")})

	got := hermod.StepWaitFrom(ctx)
	if got == nil {
		t.Fatal("StepWaitFrom = nil after two waits")
	}
	if got.For != 5*time.Second {
		t.Errorf("For = %s, want the two waits added: 5s", got.For)
	}
	if got.Reason != "lookup: no connection free" {
		t.Errorf("Reason = %q, want the first busy answer", got.Reason)
	}
	if len(got.Holders) != 1 || got.Holders[0] != holders[0] {
		t.Errorf("Holders = %+v, want %+v", got.Holders, holders)
	}
}

func TestStepWaitIsNilWhenTheStepDidNotWait(t *testing.T) {
	if got := hermod.StepWaitFrom(hermod.WithStepWait(t.Context())); got != nil {
		t.Errorf("StepWaitFrom = %+v for a step that never waited, want nil", got)
	}
	// Without a slot, adding is a no-op rather than a panic: previews and
	// tests run steps with no recorder.
	hermod.AddStepWait(context.Background(), time.Second, hermod.ErrBusy)
	if got := hermod.StepWaitFrom(context.Background()); got != nil {
		t.Errorf("StepWaitFrom without a slot = %+v, want nil", got)
	}
}

// The reader gets a copy: a nested step still adding to the slot must not
// change a step that has already been recorded.
func TestStepWaitFromReturnsACopy(t *testing.T) {
	ctx := hermod.WithStepWait(t.Context())
	hermod.AddStepWait(ctx, time.Second, &hermod.Busy{Holders: []hermod.Holder{{Kind: "SQL step"}}})
	first := hermod.StepWaitFrom(ctx)
	first.Holders[0].Kind = "changed"
	if again := hermod.StepWaitFrom(ctx); again.Holders[0].Kind != "SQL step" {
		t.Errorf("a caller's change reached the slot: %+v", again.Holders)
	}
}

func TestStepWaitIsSafeForConcurrentSteps(t *testing.T) {
	ctx := hermod.WithStepWait(t.Context())
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() { hermod.AddStepWait(ctx, time.Millisecond, hermod.ErrBusy) })
	}
	wg.Wait()
	if got := hermod.StepWaitFrom(ctx); got.For != 50*time.Millisecond {
		t.Errorf("For = %s, want 50ms", got.For)
	}
}
