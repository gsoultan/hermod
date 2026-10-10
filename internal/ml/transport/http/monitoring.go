package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
)

// maxMonitoringBody bounds a monitoring setting.
const maxMonitoringBody = 64 << 10

// ListPredictionLogs returns a model's most recently logged predictions,
// newest first: ?limit= of them, at most storage.MaxMLPredictionLogPage.
// Anyone with a role on the vhost may read them; what they hold was masked
// as the model's monitoring says before it was written.
func (h *Handler) ListPredictionLogs(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, false)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	logs, err := h.service().PredictionLogs(r.Context(), vhost, r.PathValue("name"), limit)
	if err != nil {
		if errors.Is(err, storage.ErrMLPredictionLogsUnsupported) {
			h.JsonError(w, err.Error(), http.StatusNotImplemented)
			return
		}
		if errors.Is(err, storage.ErrMLModelsUnsupported) || isNotFound(err) {
			h.fail(w, err)
			return
		}
		h.JsonError(w, "Failed to read the prediction log", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"data": logs, "total": len(logs)})
}

// GetDrift returns the model's latest drift report, or why it has none.
func (h *Handler) GetDrift(w http.ResponseWriter, r *http.Request) {
	vhost, _, ok := h.access(w, r, false)
	if !ok {
		return
	}
	st, err := h.service().Drift(r.Context(), vhost, r.PathValue("name"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, st)
}

// PutMonitoring replaces how a model is monitored: prediction logging and
// drift thresholds. It needs Editor, and is audited: turning logging on
// starts keeping what the model is sent.
func (h *Handler) PutMonitoring(w http.ResponseWriter, r *http.Request) {
	vhost, user, ok := h.access(w, r, true)
	if !ok {
		return
	}
	var mon storage.MLMonitoring
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMonitoringBody)).Decode(&mon); err != nil {
		h.JsonError(w, "the request body must be a JSON monitoring setting", http.StatusBadRequest)
		return
	}
	if err := mon.Validate(); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := r.PathValue("name")
	m, err := h.service().SetMonitoring(r.Context(), vhost, name, mon, user.Username)
	if err != nil {
		if isNotFound(err) || errors.Is(err, storage.ErrMLModelsUnsupported) {
			h.fail(w, err)
			return
		}
		h.JsonError(w, "Failed to save the monitoring setting", http.StatusInternalServerError)
		return
	}
	h.RecordAuditLog(r, "INFO", "Changed monitoring of model "+name+" in vhost "+vhost, "update", vhost, "vhost", "",
		map[string]string{"model": name, "log_sample_rate": fmt.Sprint(mon.LogSampleRate)})
	writeJSON(w, m)
}

func isNotFound(err error) bool { return errors.Is(err, ml.ErrModelNotFound) }
