package http

import (
	"encoding/json"
	"net/http"

	"github.com/user/hermod/internal/storage"
)

func (h *LogHandler) RegisterLogRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/logs", h.ListLogs)
	mux.HandleFunc("POST /api/logs", h.CreateLog)
	mux.HandleFunc("DELETE /api/logs", h.DeleteLogs)
}

func (h *LogHandler) ListLogs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := storage.LogFilter{
		CommonFilter: h.ParseCommonFilter(r),
		SourceID:     query.Get("source_id"),
		SinkID:       query.Get("sink_id"),
		WorkflowID:   query.Get("workflow_id"),
		Level:        query.Get("level"),
		Action:       query.Get("action"),
	}

	logs, total, err := h.LogStorage.ListLogs(r.Context(), filter)
	if err != nil {
		h.JsonError(w, "Failed to list logs: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  logs,
		"total": total,
	})
}

func (h *LogHandler) CreateLog(w http.ResponseWriter, r *http.Request) {
	var log storage.Log
	if err := json.NewDecoder(r.Body).Decode(&log); err != nil {
		h.JsonError(w, "Failed to decode request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.LogStorage.CreateLog(r.Context(), log); err != nil {
		h.JsonError(w, "Failed to create log: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func (h *LogHandler) DeleteLogs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := storage.LogFilter{
		SourceID:   query.Get("source_id"),
		SinkID:     query.Get("sink_id"),
		WorkflowID: query.Get("workflow_id"),
		Level:      query.Get("level"),
		Action:     query.Get("action"),
	}

	if err := h.LogStorage.DeleteLogs(r.Context(), filter); err != nil {
		h.JsonError(w, "Failed to delete logs: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
