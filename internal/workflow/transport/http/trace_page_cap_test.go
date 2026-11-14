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
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

type tracePageSpy struct {
	storage.Storage
	asked storage.TraceFilter
}

func (s *tracePageSpy) GetWorkflow(context.Context, string) (storage.Workflow, error) {
	return storage.Workflow{ID: "wf"}, nil
}

func (s *tracePageSpy) ListMessageTraces(_ context.Context, _ string, f storage.TraceFilter) ([]storage.MessageTrace, error) {
	s.asked = f
	return nil, nil
}

func TestTracePageSizeIsCapped(t *testing.T) {
	spy := &tracePageSpy{}
	h := &WorkflowHandler{Handler: &handlers.Handler{Storage: spy, LogStorage: spy}}
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/api/workflows/wf/traces?limit=2000000000", nil)
	req.SetPathValue("id", "wf")
	rec := httptest.NewRecorder()
	h.ListMessageTraces(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if spy.asked.Limit != handlers.MaxListPageSize {
		t.Errorf("asked storage for %d traces, want the cap of %d", spy.asked.Limit, handlers.MaxListPageSize)
	}
}
