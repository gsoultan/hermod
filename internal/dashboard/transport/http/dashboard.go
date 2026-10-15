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
