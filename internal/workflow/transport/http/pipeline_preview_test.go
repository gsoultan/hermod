// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
)

// A Pipeline step runs its steps in order when the workflow runs. Run Preview
// sent it to the transformer registry, where "pipeline" is not a transformer,
// so the editor showed "unknown transformation type" for a step that works.
func TestPreviewRunsAPipelineStepsInOrder(t *testing.T) {
	steps, _ := json.Marshal([]map[string]any{
		{"transType": "set", "column.name": "upper(source.name)"},
		{"transType": "set", "column.greeting": "concat(\"Hi \", source.name)"},
	})
	out := postTransformation(t,
		map[string]any{"transType": "pipeline", "steps": string(steps)},
		"transformation",
		map[string]any{"id": "m", "operation": "create", "table": "t", "after": map[string]any{"name": "ana"}})

	after, _ := out["after"].(map[string]any)
	if after["name"] != "ANA" {
		t.Errorf("first step not applied: %v", out)
	}
	// The second step reads what the first one wrote.
	if after["greeting"] != "Hi ANA" {
		t.Errorf("second step did not run on the first step's result: %v", out)
	}
}

func TestPreviewOfAnEmptyPipelineReturnsTheMessage(t *testing.T) {
	out := postTransformation(t, map[string]any{"transType": "pipeline"}, "transformation",
		map[string]any{"id": "m", "operation": "create", "table": "t", "after": map[string]any{"name": "ana"}})
	if after, _ := out["after"].(map[string]any); after["name"] != "ana" {
		t.Errorf("want the message unchanged, got %v", out)
	}
}

// Merge, Note and the subflow markers change nothing in a message. The answer
// to previewing one is that there is nothing to preview, not a server error
// about a type nobody mistyped.
func TestPreviewOfAStructuralNodeSaysThereIsNothingToPreview(t *testing.T) {
	for _, nodeType := range []string{"merge", "note", "subflow_input", "subflow_return"} {
		body, _ := json.Marshal(map[string]any{
			"transformation": map[string]any{"type": nodeType, "config": map[string]any{}},
			"message":        map[string]any{"id": "m", "after": map[string]any{"name": "ana"}},
		})
		h := &WorkflowHandler{Handler: &handlers.Handler{Registry: &registry.Registry{}}}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/transformations/test", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		h.TestTransformation(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", nodeType, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "unknown transformation type") {
			t.Errorf("%s: still reported as a mistyped transformation: %s", nodeType, rec.Body)
		}
	}
}
