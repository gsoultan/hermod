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
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// InsightsHandler serves GET /api/workflows/{id}/insights.
type InsightsHandler struct {
	*handlers.Handler
}

// NewInsightsHandler wraps the shared handler.
func NewInsightsHandler(h *handlers.Handler) *InsightsHandler {
	return &InsightsHandler{Handler: h}
}

// RegisterInsightsRoutes registers the endpoint. Reading it changes nothing,
// so access to the workflow's vhost is enough.
func (h *InsightsHandler) RegisterInsightsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workflows/{id}/insights", h.GetInsights)
}

// GetInsights returns the advisor's report for a workflow. It fails closed
// like the run history: a workflow that cannot be read is not served.
func (h *InsightsHandler) GetInsights(w http.ResponseWriter, r *http.Request) {
	wf, err := h.Storage.GetWorkflow(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Workflow not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to load workflow", http.StatusInternalServerError)
		}
		return
	}
	role, vhosts := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator && !h.HasVHostAccess(wf.VHost, vhosts) {
		h.JsonError(w, "Forbidden: you do not have access to this workflow's vhost", http.StatusForbidden)
		return
	}
	if h.Registry == nil {
		h.JsonError(w, "The engine registry is not available", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.Registry.WorkflowInsights(r.Context(), wf.ID))
}
