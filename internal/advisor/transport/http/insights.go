// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package http serves the performance advisor's reading of a flow.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gsoultan/hermod/internal/advisor"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/llm"
)

// explainTimeout bounds one explanation, model call included.
const explainTimeout = 45 * time.Second

// InsightsHandler serves a workflow's insights and their explanation.
type InsightsHandler struct {
	*handlers.Handler

	// report and explainer are set by tests; nil means the registry's report
	// and the explainer the environment configures.
	report    func(ctx context.Context, id string) advisor.Report
	explainer func() *advisor.Explainer

	envOnce      sync.Once
	envExplainer *advisor.Explainer
	// envErr is why the environment's explainer was refused, if it was.
	envErr error
}

// NewInsightsHandler wraps the shared handler.
func NewInsightsHandler(h *handlers.Handler) *InsightsHandler {
	return &InsightsHandler{Handler: h}
}

// RegisterInsightsRoutes registers the endpoints. Reading the report changes
// nothing, so access to the workflow's vhost is enough. An explanation
// spends the vhost's AI budget, so it takes an editor.
func (h *InsightsHandler) RegisterInsightsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workflows/{id}/insights", h.GetInsights)
	mux.Handle("POST /api/workflows/{id}/insights/explain", h.EditorOnly(h.Explain))
}

// ai returns the explainer, or nil when AI explanations are off.
func (h *InsightsHandler) ai() *advisor.Explainer {
	if h.explainer != nil {
		return h.explainer()
	}
	h.envOnce.Do(func() {
		h.envExplainer, h.envErr = advisor.ExplainerFromEnv(os.Getenv)
	})
	return h.envExplainer
}

// workflow loads the workflow a request names and checks the caller may see
// it. It fails closed like the run history: a workflow that cannot be read
// is not served. False means the response has been written.
func (h *InsightsHandler) workflow(w http.ResponseWriter, r *http.Request) (storage.Workflow, bool) {
	wf, err := h.Storage.GetWorkflow(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Workflow not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to load workflow", http.StatusInternalServerError)
		}
		return wf, false
	}
	role, vhosts := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator && !h.HasVHostAccess(wf.VHost, vhosts) {
		h.JsonError(w, "Forbidden: you do not have access to this workflow's vhost", http.StatusForbidden)
		return wf, false
	}
	if h.report == nil && h.Registry == nil {
		h.JsonError(w, "The engine registry is not available", http.StatusServiceUnavailable)
		return wf, false
	}
	return wf, true
}

func (h *InsightsHandler) reportFor(ctx context.Context, id string) advisor.Report {
	if h.report != nil {
		return h.report(ctx, id)
	}
	return h.Registry.WorkflowInsights(ctx, id)
}

// insightsView is the report with what the page may offer for it.
type insightsView struct {
	advisor.Report
	// CanExplain says an AI explanation of the findings can be asked for.
	CanExplain bool `json:"can_explain"`
}

// GetInsights returns the advisor's report for a workflow.
func (h *InsightsHandler) GetInsights(w http.ResponseWriter, r *http.Request) {
	wf, ok := h.workflow(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(insightsView{
		Report:     h.reportFor(r.Context(), wf.ID),
		CanExplain: h.ai() != nil,
	})
}

// Explain answers POST /api/workflows/{id}/insights/explain: a model's plain
// reading of the workflow's current findings. It sends the findings only and
// changes nothing.
func (h *InsightsHandler) Explain(w http.ResponseWriter, r *http.Request) {
	wf, ok := h.workflow(w, r)
	if !ok {
		return
	}
	ai := h.ai()
	if ai == nil {
		msg := "AI explanations are not set up on this server"
		if h.envErr != nil {
			msg = h.envErr.Error()
		}
		h.JsonError(w, msg, http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), explainTimeout)
	defer cancel()

	got, err := ai.Explain(ctx, wf.VHost, h.reportFor(ctx, wf.ID))
	switch {
	case errors.Is(err, advisor.ErrNothingToExplain):
		h.JsonError(w, "This flow has no recommendations to explain right now", http.StatusConflict)
		return
	case errors.Is(err, llm.ErrBudgetExceeded):
		h.JsonError(w, err.Error(), http.StatusTooManyRequests)
		return
	case err != nil:
		h.JsonError(w, "The explanation failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	h.RecordAuditLog(r, "INFO", "AI explained a flow's recommendations", "AI_EXPLAIN_INSIGHTS", wf.ID, "", "", map[string]any{
		"provider": got.Provider, "model": got.Model,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(got)
}
