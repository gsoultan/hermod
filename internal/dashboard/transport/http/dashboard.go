// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
