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
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

// busyOnce answers busy the first time per message and succeeds after.
type busyOnce struct{ calls atomic.Int32 }

func (b *busyOnce) Transform(_ context.Context, msg hermod.Message, _ map[string]any) (hermod.Message, error) {
	if b.calls.Add(1) == 1 {
		return nil, &hermod.Busy{Err: errors.New("db_lookup: no answer from the database within 30s"),
			Holders: []hermod.Holder{{WorkflowID: "wf-report", NodeID: "n3", Kind: "SQL step", Running: 25 * time.Second}}}
	}
	return msg, nil
}

// --- v1.27.1: a lookup that waited its turn showed as a failed run ----------
//
// Reported from the Runs tab. A lookup that found no free connection answered
// busy, the engine waited and asked again, and the second attempt succeeded --
// but each attempt was recorded as a trace step of its own, so the busy one
// sat in the run as an error, the run counted as failed, and the error read as
// though the message had been lost. A busy attempt that is going to be waited
// out is not an outcome; the step's single, final result is.
func TestABusyAttemptThatIsWaitedOutIsNotRecordedAsAFailedStep(t *testing.T) {
	store := newTraceStorage()
	r := NewRegistry(store)
	r.SetLogStorage(store)
	r.engines["wf-totals"] = &activeEngine{engine: pkgengine.NewEngine(nil, nil, nil), isWorkflow: true,
		workflow: storage.Workflow{ID: "wf-totals", TraceSampleRate: 1}}
	transformer.Register("test_busy_once", &busyOnce{})

	msg := message.AcquireMessage()
	msg.SetID("m-1")
	msg.SetMetadata("_hermod_workflow_id", "wf-totals")
	defer msg.Release()
	ctx := hermod.WithBusyWaiter(t.Context(), pkgengine.NewEngine(nil, nil, nil))
	node := &storage.WorkflowNode{ID: "n1", Type: "transformation", Config: map[string]any{"transType": "test_busy_once"}}

	out, _, err := r.runWorkflowNode(ctx, "wf-totals", node, msg)
	if err != nil {
		t.Fatalf("the step failed after its wait: %v", err)
	}
	for _, m := range out {
		m.Release()
	}

	waitUntil(t, 5*time.Second, "the successful attempt's step", func() bool { return len(store.stepsFor("m-1")) > 0 })
	time.Sleep(50 * time.Millisecond) // a late busy step would land by now
	for _, s := range store.stepsFor("m-1") {
		if s.Error != "" {
			t.Errorf("step %q recorded the busy attempt as an error: %s", s.NodeID, s.Error)
		}
	}
}

// Without a waiter -- a preview, a test run -- the busy answer is the result,
// and it is recorded as one.
func TestABusyAnswerWithNoWaiterIsRecordedAsTheStepsError(t *testing.T) {
	store := newTraceStorage()
	r := NewRegistry(store)
	r.SetLogStorage(store)
	r.engines["wf-totals"] = &activeEngine{engine: pkgengine.NewEngine(nil, nil, nil), isWorkflow: true,
		workflow: storage.Workflow{ID: "wf-totals", TraceSampleRate: 1}}
	transformer.Register("test_busy_always", &busyOnce{})

	msg := message.AcquireMessage()
	msg.SetID("m-2")
	msg.SetMetadata("_hermod_workflow_id", "wf-totals")
	defer msg.Release()
	node := &storage.WorkflowNode{ID: "n1", Type: "transformation", Config: map[string]any{"transType": "test_busy_always"}}

	if _, _, err := r.runWorkflowNode(t.Context(), "wf-totals", node, msg); !errors.Is(err, hermod.ErrBusy) {
		t.Fatalf("err = %v, want the busy answer", err)
	}
	waitUntil(t, 5*time.Second, "the busy step", func() bool { return len(store.stepsFor("m-2")) > 0 })
	if s := store.stepsFor("m-2"); s[0].Error == "" {
		t.Errorf("the busy answer was not recorded: %+v", s)
	}
}
