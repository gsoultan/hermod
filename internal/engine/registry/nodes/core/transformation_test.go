// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
