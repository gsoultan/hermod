package api

import (
	"encoding/json"
	"net/http"

	"github.com/user/hermod/internal/storage"
)

func (s *Server) listLogs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := storage.LogFilter{
		CommonFilter: s.parseCommonFilter(r),
		SourceID:     query.Get("source_id"),
		SinkID:       query.Get("sink_id"),
		WorkflowID:   query.Get("workflow_id"),
		Level:        query.Get("level"),
		Action:       query.Get("action"),
	}

	logs, total, err := s.storage.ListLogs(r.Context(), filter)
	if err != nil {
		s.jsonError(w, "Failed to list logs: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  logs,
		"total": total,
	})
}

func (s *Server) createLog(w http.ResponseWriter, r *http.Request) {
	var log storage.Log
	if err := json.NewDecoder(r.Body).Decode(&log); err != nil {
		s.jsonError(w, "Failed to decode request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.storage.CreateLog(r.Context(), log); err != nil {
		s.jsonError(w, "Failed to create log: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func (s *Server) deleteLogs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := storage.LogFilter{
		SourceID:   query.Get("source_id"),
		SinkID:     query.Get("sink_id"),
		WorkflowID: query.Get("workflow_id"),
		Level:      query.Get("level"),
		Action:     query.Get("action"),
	}

	if err := s.storage.DeleteLogs(r.Context(), filter); err != nil {
		s.jsonError(w, "Failed to delete logs: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
