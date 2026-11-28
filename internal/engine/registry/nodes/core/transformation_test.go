// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package core

import (
	"context"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/llm"
)

// scopeCtx records the scope each transformation was applied in.
type scopeCtx struct {
	interfaces.NodeContext
	scopes map[string]llm.Scope
}

func (c *scopeCtx) ApplyTransformation(ctx context.Context, msg hermod.Message, transType string, _ map[string]any) (hermod.Message, error) {
	c.scopes[transType] = llm.ScopeFrom(ctx)
	return msg, nil
}

func (c *scopeCtx) ContextWithPipelineSnapshot(ctx context.Context) context.Context { return ctx }

// An AI transformation, alone or as a pipeline step, runs in its workflow's
// scope, so the workflow's AI spending cap counts its calls.
func TestTransformation_AIStepsRunInTheWorkflowScope(t *testing.T) {
	cases := []struct {
		name   string
		config map[string]any
		step   string
	}{
		{"single node", map[string]any{"transType": "ai_prompt"}, "ai_prompt"},
		{"pipeline step", map[string]any{"transType": "pipeline", "steps": `[{"transType":"ai_extract"}]`}, "ai_extract"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nctx := &scopeCtx{scopes: map[string]llm.Scope{}}
			msg := message.AcquireMessage()
			defer message.ReleaseMessage(msg)
			node := &storage.WorkflowNode{ID: "n1", Type: "transformation", Config: tc.config}
			if _, _, err := (&TransformationNode{}).Execute(t.Context(), nctx, "wf-t", node, msg); err != nil {
				t.Fatal(err)
			}
			if got := nctx.scopes[tc.step]; got.WorkflowID != "wf-t" {
				t.Fatalf("%s ran in scope %+v, want workflow wf-t", tc.step, got)
			}
		})
	}
}

// workflowCtx records the workflow each step ran for, and can answer busy.
type workflowCtx struct {
	interfaces.NodeContext
	seen      map[string]string
	busy      bool
	broadcast []string
}

func (c *workflowCtx) ApplyTransformation(ctx context.Context, _ hermod.Message, transType string, _ map[string]any) (hermod.Message, error) {
	c.seen[transType], _ = ctx.Value(hermod.StepWorkflowKey).(string)
	if c.busy {
		return nil, hermod.ErrBusy
	}
	return nil, nil
}

func (c *workflowCtx) ContextWithPipelineSnapshot(ctx context.Context) context.Context { return ctx }

func (c *workflowCtx) BroadcastLiveMessage(_, _ string, _ hermod.Message, isError bool, _ string) {
	if isError {
		c.broadcast = append(c.broadcast, "error")
	}
}

// v1.27.1 named the SQL steps holding a database "an unnamed workflow": only
// ml_predict had its workflow on the context. Every step, pipeline steps
// included, now carries it under StepWorkflowKey, which nothing keys state by.
func TestTransformation_EveryStepCarriesItsWorkflow(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config map[string]any
		step   string
	}{
		{"single node", map[string]any{"transType": "execute_sql"}, "execute_sql"},
		{"pipeline step", map[string]any{"transType": "pipeline", "steps": `[{"transType":"db_lookup"}]`}, "db_lookup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nctx := &workflowCtx{seen: map[string]string{}}
			msg := message.AcquireMessage()
			defer message.ReleaseMessage(msg)
			node := &storage.WorkflowNode{ID: "n1", Type: "transformation", Config: tc.config}
			_, _, _ = (&TransformationNode{}).Execute(t.Context(), nctx, "wf-report", node, msg)
			if got := nctx.seen[tc.step]; got != "wf-report" {
				t.Errorf("%s ran for workflow %q, want wf-report", tc.step, got)
			}
		})
	}
}

// A busy answer the engine will wait out is not shown as a failure in the
// live view; with no waiter it is the result and is shown.
func TestTransformation_BusyIsNotBroadcastAsAnErrorWhileItIsWaitedOut(t *testing.T) {
	node := &storage.WorkflowNode{ID: "n1", Type: "transformation", Config: map[string]any{"transType": "db_lookup"}}
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)

	waited := &workflowCtx{seen: map[string]string{}, busy: true}
	ctx := hermod.WithBusyWaiter(t.Context(), noWaiter{})
	_, _, _ = (&TransformationNode{}).Execute(ctx, waited, "wf", node, msg)
	if len(waited.broadcast) != 0 {
		t.Errorf("a busy attempt being waited out was broadcast as an error")
	}

	final := &workflowCtx{seen: map[string]string{}, busy: true}
	_, _, _ = (&TransformationNode{}).Execute(t.Context(), final, "wf", node, msg)
	if len(final.broadcast) != 1 {
		t.Errorf("a busy answer with no waiter was not broadcast as the step's error")
	}
}

type noWaiter struct{}

func (noWaiter) WaitOutBusy(_ context.Context, _ string, attempt func() error) error {
	return attempt()
}
