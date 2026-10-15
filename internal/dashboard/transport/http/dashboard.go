// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package http

import (
	"encoding/json"
	"net/http"
)

func (h *DashboardHandler) RegisterDashboardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/dashboard/stats", h.GetDashboardStats)
	mux.HandleFunc("GET /api/dashboard/history", h.GetDashboardHistory)
}

func (h *DashboardHandler) GetDashboardStats(w http.ResponseWriter, r *http.Request) {
	vhost := r.URL.Query().Get("vhost")
	stats, err := h.Registry.GetDashboardStats(r.Context(), vhost)
	if err != nil {
		h.JsonError(w, "Failed to get dashboard stats: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(stats)
}
