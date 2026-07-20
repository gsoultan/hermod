package http

import (
	"encoding/json"
	"net/http"
)

func (h *DashboardHandler) RegisterDashboardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/dashboard/stats", h.GetDashboardStats)
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
