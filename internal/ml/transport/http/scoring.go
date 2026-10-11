package http

import (
	"encoding/json"
	"net/http"

	"github.com/gsoultan/hermod/internal/storage"
)

// scoringRequest sets where a trained model is scored.
type scoringRequest struct {
	Scoring string `json:"scoring"`
}

// GetScoring says how a model's predictions are computed: the setting, and,
// for in-process scoring, whether its live version is scored in Hermod or
// why it falls back to the worker.
func (h *Handler) GetScoring(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, false)
	if !ok {
		return
	}
	status, err := h.service().Scoring(r.Context(), vhost, r.PathValue("name"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, status)
}

// PutScoring sets whether a trained model is scored by the ML worker or
// in-process, and answers with the status that results.
func (h *Handler) PutScoring(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, true)
	if !ok {
		return
	}
	var req scoringRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxModelBody)).Decode(&req); err != nil {
		h.JsonError(w, `the request body must be JSON: {"scoring": "worker" or "in_process"}`, http.StatusBadRequest)
		return
	}
	if err := storage.ValidateMLScoring(req.Scoring); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := r.PathValue("name")
	svc := h.service()
	if err := svc.SetScoring(r.Context(), vhost, name, req.Scoring); err != nil {
		h.fail(w, err)
		return
	}
	h.RecordAuditLog(r, "INFO", "Set how model "+name+" in vhost "+vhost+" is scored", "update", vhost, "vhost", "",
		map[string]string{"model": name, "scoring": req.Scoring})
	status, err := svc.Scoring(r.Context(), vhost, name)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, status)
}
