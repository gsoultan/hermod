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

	"github.com/gsoultan/hermod/internal/storage"
)

func (h *LogHandler) RegisterLogRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/logs", h.ListLogs)
	// Workers ship their logs here; a person reads the log and does not write it.
	mux.Handle("POST /api/logs", h.AgentOnly(h.CreateLog))
	mux.Handle("POST /api/logs/batch", h.AgentOnly(h.CreateLogs))
	// Wiping the log store destroys the audit trail; administrators only.
	mux.Handle("DELETE /api/logs", h.AdminOnly(h.DeleteLogs))
}

// maxLogBatchBytes bounds a single log-shipping request.
//
// This endpoint takes writes from every remote worker, and the engine's logger
// batches fifty entries at a time, so the realistic ceiling is far below this.
// The bound exists because decoding an unbounded body would let one worker — or
// one malformed client — exhaust the platform's memory.
const maxLogBatchBytes = 4 << 20 // 4 MiB

// CreateLogs accepts a batch of workflow logs.
//
// A remote worker has no database of its own: its engines' logs exist only in
// its process log unless they are shipped here. Sending them one request per
// entry does not scale to a pipeline's log volume, which is why the batch form
// exists.
func (h *LogHandler) CreateLogs(w http.ResponseWriter, r *http.Request) {
	var logs []storage.Log
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLogBatchBytes))
	if err := dec.Decode(&logs); err != nil {
		h.JsonError(w, "Failed to decode request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if len(logs) == 0 {
		w.WriteHeader(http.StatusCreated)
		return
	}

	if err := h.LogStorage.CreateLogs(r.Context(), logs); err != nil {
		h.JsonError(w, "Failed to create logs: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
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

	// An Administrator reads the whole log. Anyone else reads the entries of
	// the workflows, sources and sinks in the vhosts they were given.
	scope := h.ScopeOf(r)
	if !scope.LogFilter(filter) {
		h.JsonError(w, "Forbidden: you do not have access to this vhost", http.StatusForbidden)
		return
	}

	logs, total, err := h.LogStorage.ListLogs(r.Context(), filter)
	if err != nil {
		h.JsonError(w, "Failed to list logs: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if scope != nil {
		visible := make([]storage.Log, 0, len(logs))
		for _, l := range logs {
			if scope.Log(l) {
				visible = append(visible, l)
			}
		}
		logs = visible
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
