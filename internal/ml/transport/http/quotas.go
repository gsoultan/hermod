package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gsoultan/hermod/internal/storage"
)

// quotasRequest is what an Administrator may set: every quota, each null (or
// absent) to fall back to the server default, 0 for no limit.
type quotasRequest struct {
	MaxDatasets             *int64   `json:"max_datasets"`
	MaxDatasetRows          *int64   `json:"max_dataset_rows"`
	MaxDatasetBytes         *int64   `json:"max_dataset_bytes"`
	MaxModels               *int64   `json:"max_models"`
	MaxConcurrentTrainings  *int64   `json:"max_concurrent_trainings"`
	MaxPredictionsPerSecond *float64 `json:"max_predictions_per_second"`
}

// GetQuotas shows the vhost's ML quotas: its own, the server defaults, and
// the limits in force. Any role on the vhost may read them.
func (h *Handler) GetQuotas(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, false)
	if !ok {
		return
	}
	h.writeQuotas(w, r, vhost)
}

func (h *Handler) writeQuotas(w http.ResponseWriter, r *http.Request, vhost string) {
	svc := h.service()
	own, err := svc.StoredQuotas(r.Context(), vhost)
	if err != nil {
		h.JsonError(w, "Failed to read the ML quotas", http.StatusInternalServerError)
		return
	}
	eff, err := svc.Quotas(r.Context(), vhost)
	if err != nil {
		h.JsonError(w, "Failed to read the ML quotas", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"quotas": own, "defaults": svc.QuotaDefaults(), "effective": eff})
}

// PutQuotas replaces the vhost's ML quotas. Only an Administrator may: a
// quota limits what the vhost's own Editors can do.
func (h *Handler) PutQuotas(w http.ResponseWriter, r *http.Request) {
	vhost, user, ok := h.access(w, r, false)
	if !ok {
		return
	}
	if user.Role != storage.RoleAdministrator {
		h.JsonError(w, "Forbidden: setting ML quotas needs the Administrator role", http.StatusForbidden)
		return
	}
	var req quotasRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxModelBody)).Decode(&req); err != nil {
		h.JsonError(w, "the request body must be a JSON object of quotas", http.StatusBadRequest)
		return
	}
	q := storage.MLQuotas{
		VHost: vhost, MaxDatasets: req.MaxDatasets, MaxDatasetRows: req.MaxDatasetRows, MaxDatasetBytes: req.MaxDatasetBytes,
		MaxModels: req.MaxModels, MaxConcurrentTrainings: req.MaxConcurrentTrainings, MaxPredictionsPerSecond: req.MaxPredictionsPerSecond,
		UpdatedBy: user.Username,
	}
	if err := storage.ValidateMLQuotas(q); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.service().SetQuotas(r.Context(), q); err != nil {
		h.fail(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Set the ML quotas of vhost "+vhost, "update", vhost, "vhost", "", req)
	h.writeQuotas(w, r, vhost)
}

// SetMCPExposure offers a model to MCP clients as a predict tool, or stops.
func (h *Handler) SetMCPExposure(w http.ResponseWriter, r *http.Request) {
	vhost, user, ok := h.access(w, r, true)
	if !ok {
		return
	}
	var req struct {
		Exposed *bool `json:"exposed"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxModelBody)).Decode(&req); err != nil || req.Exposed == nil {
		h.JsonError(w, `the body must say whether to expose the model: {"exposed": true}`, http.StatusBadRequest)
		return
	}
	name := r.PathValue("name")
	m, err := h.service().SetMCPExposed(r.Context(), vhost, name, *req.Exposed, user.Username)
	if err != nil {
		h.fail(w, err)
		return
	}
	verb := "Stopped exposing"
	if *req.Exposed {
		verb = "Exposed"
	}
	h.RecordAuditLog(r, "INFO", verb+" model "+name+" in vhost "+vhost+" to MCP", "update", vhost, "vhost", "",
		map[string]string{"model": name, "mcp_exposed": strconv.FormatBool(*req.Exposed)})
	writeJSON(w, m)
}
