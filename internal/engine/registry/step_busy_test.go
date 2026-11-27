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
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// busyStep answers busy until it has been asked refusals+1 times.
type busyStep struct {
	mu       sync.Mutex
	refusals int
	calls    int
}

func (s *busyStep) Execute(_ context.Context, _ interfaces.NodeContext, _ string, _ *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls <= s.refusals {
		return nil, "", &hermod.Busy{Err: errors.New("no free connection")}
	}
	msg.SetData("looked_up", true)
	return []hermod.Message{msg}, "", nil
}

// countingWaiter repeats a busy attempt, as an engine does, and records which
// step it waited for.
type countingWaiter struct {
	mu    sync.Mutex
	names []string
}

func (w *countingWaiter) WaitOutBusy(ctx context.Context, name string, attempt func() error) error {
	w.mu.Lock()
	w.names = append(w.names, name)
	w.mu.Unlock()
	for {
		err := attempt()
		if !errors.Is(err, hermod.ErrBusy) || ctx.Err() != nil {
			return err
		}
	}
}

var stepBusy = &busyStep{}

func init() {
	interfaces.RegisterNodeExecutor("test_busy_step", stepBusy)
}

// A lookup whose database had no free connection failed the step, and with it
// the message, on the first busy answer: a step had no way to wait, so heavy
// traffic on a shared database turned into failed runs. A busy step is now
// waited out by whatever is running the workflow, the same way a busy sink is,
// and the message goes on once the database has room.
func TestABusyStepIsWaitedOutNotFailed(t *testing.T) {
	stepBusy.mu.Lock()
	stepBusy.refusals, stepBusy.calls = 3, 0
	stepBusy.mu.Unlock()

	reg := NewRegistry(nil)
	node := &storage.WorkflowNode{ID: "lookup-1", Type: "test_busy_step"}
	m := message.AcquireMessage()
	defer m.Release()
	m.SetID("m1")

	w := &countingWaiter{}
	ctx := hermod.WithBusyWaiter(t.Context(), w)
	msgs, _, err := reg.RunWorkflowNodeContext(ctx, "wf", node, m)
	if err != nil {
		t.Fatalf("a busy step failed the message: %v", err)
	}
	for _, out := range msgs {
		defer out.Release()
	}
	if len(msgs) != 1 || msgs[0].Data()["looked_up"] != true {
		t.Fatalf("the step's result was lost: %v", msgs)
	}
	if stepBusy.calls != 4 {
		t.Fatalf("step ran %d times, want 4 (3 busy answers, then room)", stepBusy.calls)
	}
	if len(w.names) != 1 || w.names[0] != "step:lookup-1" {
		t.Fatalf("waited for %v, want one wait for step:lookup-1", w.names)
	}
}

// Without anything to wait on its behalf -- a preview, a test run -- a busy
// answer is reported as it is, rather than spinning.
func TestABusyStepWithNoWaiterReportsBusy(t *testing.T) {
	stepBusy.mu.Lock()
	stepBusy.refusals, stepBusy.calls = 1, 0
	stepBusy.mu.Unlock()

	reg := NewRegistry(nil)
	node := &storage.WorkflowNode{ID: "lookup-1", Type: "test_busy_step"}
	m := message.AcquireMessage()
	defer m.Release()

	_, _, err := reg.RunWorkflowNodeContext(t.Context(), "wf", node, m)
	if !errors.Is(err, hermod.ErrBusy) {
		t.Fatalf("err = %v, want the busy answer", err)
	}
}
