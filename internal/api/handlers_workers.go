package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/user/hermod/internal/storage"
)

func (s *Server) listWorkers(w http.ResponseWriter, r *http.Request) {
	workers, total, err := s.storage.ListWorkers(r.Context(), s.parseCommonFilter(r))
	if err != nil {
		s.jsonError(w, "failed to list workers", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// Sanitize tokens from list responses for security
	sanitized := make([]storage.Worker, len(workers))
	for i := range workers {
		sanitized[i] = workers[i]
		sanitized[i].Token = ""
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  sanitized,
		"total": total,
	})
}

func (s *Server) getWorker(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	worker, err := s.storage.GetWorker(r.Context(), id)
	if err != nil {
		if err == storage.ErrNotFound {
			s.jsonError(w, "worker not found", http.StatusNotFound)
		} else {
			s.jsonError(w, "failed to retrieve worker", http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// Do not expose the worker token on read
	worker.Token = ""
	_ = json.NewEncoder(w).Encode(worker)
}

func (s *Server) createWorker(w http.ResponseWriter, r *http.Request) {
	var worker storage.Worker
	if err := json.NewDecoder(r.Body).Decode(&worker); err != nil {
		s.jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if worker.ID == "" {
		worker.ID = uuid.New().String()
	}
	if worker.Token == "" {
		worker.Token = uuid.New().String()
	}
	if err := s.storage.CreateWorker(r.Context(), worker); err != nil {
		s.jsonError(w, "failed to create worker", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(worker)
}

func (s *Server) updateWorker(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var worker storage.Worker
	if err := json.NewDecoder(r.Body).Decode(&worker); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Preserve server-side token; do not allow token updates via this endpoint
	existing, err := s.storage.GetWorker(r.Context(), id)
	if err != nil {
		if err == storage.ErrNotFound {
			s.jsonError(w, "worker not found", http.StatusNotFound)
			return
		}
		s.jsonError(w, "failed to retrieve worker", http.StatusInternalServerError)
		return
	}
	worker.ID = id
	worker.Token = existing.Token
	if err := s.storage.UpdateWorker(r.Context(), worker); err != nil {
		s.jsonError(w, "failed to update worker", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	// Hide token on responses
	worker.Token = ""
	_ = json.NewEncoder(w).Encode(worker)
}

func (s *Server) updateWorkerHeartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		CPUUsage    float64 `json:"cpu_usage"`
		MemoryUsage float64 `json:"memory_usage"`
	}
	if r.Method == "POST" && r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if err := s.storage.UpdateWorkerHeartbeat(r.Context(), id, req.CPUUsage, req.MemoryUsage); err != nil {
		s.jsonError(w, "failed to update heartbeat", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteWorker(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.storage.DeleteWorker(r.Context(), id); err != nil {
		s.jsonError(w, "failed to delete worker", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
