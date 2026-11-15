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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
)

func previewStatus(t *testing.T, transformation map[string]any) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"transformation": transformation,
		"message":        map[string]any{"id": "m", "operation": "create", "table": "t", "after": map[string]any{"name": "ana"}},
	})
	h := &WorkflowHandler{Handler: &handlers.Handler{Registry: &registry.Registry{}}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/transformations/test", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.TestTransformation(rec, req)
	return rec.Code, rec.Body.String()
}

// A step that cannot run with the settings it was given is the answer to the
// preview, and the settings are the caller's. It answered 500, the status for
// a fault in the server: it reads as an outage in the logs and on a dashboard,
// and a client that retries server errors sends the same broken script again.
func TestPreviewOfAStepThatCannotRunIsNotAServerError(t *testing.T) {
	steps, _ := json.Marshal([]map[string]any{{"transType": "lua", "script": "this is not lua ("}})
	for name, transformation := range map[string]map[string]any{
		"a Lua script that does not parse": {"type": "lua", "config": map[string]any{"script": "this is not lua ("}},
		"a type nothing is registered for": {"type": "no_such_step", "config": map[string]any{}},
		"a pipeline holding a broken step": {"type": "transformation", "config": map[string]any{"transType": "pipeline", "steps": string(steps)}},
	} {
		code, body := previewStatus(t, transformation)
		if code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, want 422: %s", name, code, body)
		}
	}
}

// A preview that ran out of time, or whose caller went away, did not fail
// because of its settings.
func TestPreviewCutShortKeepsItsServerStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := previewFailureStatus(ctx.Err()); got != http.StatusInternalServerError {
		t.Errorf("a cancelled preview answers %d, want 500", got)
	}
	if got := previewFailureStatus(context.DeadlineExceeded); got != http.StatusInternalServerError {
		t.Errorf("a preview past its deadline answers %d, want 500", got)
	}
}
