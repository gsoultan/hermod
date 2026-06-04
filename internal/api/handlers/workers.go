package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/user/hermod/internal/storage"
)

func (h *Handler) ListWorkers(w http.ResponseWriter, r *http.Request) {
	workers, total, err := h.Storage.ListWorkers(r.Context(), h.ParseCommonFilter(r))
	if err != nil {
		h.JsonError(w, "failed to list workers", http.StatusInternalServerError)
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

func (h *Handler) GetWorker(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	worker, err := h.Storage.GetWorker(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "worker not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "failed to retrieve worker", http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// Do not expose the worker token on read
	worker.Token = ""
	_ = json.NewEncoder(w).Encode(worker)
}

func (h *Handler) CreateWorker(w http.ResponseWriter, r *http.Request) {
	var worker storage.Worker
	if err := json.NewDecoder(r.Body).Decode(&worker); err != nil {
		h.JsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if worker.ID == "" {
		worker.ID = uuid.New().String()
	}
	if worker.Token == "" {
		worker.Token = uuid.New().String()
	}
	if err := h.Storage.CreateWorker(r.Context(), worker); err != nil {
		h.JsonError(w, "failed to create worker", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(worker)
}

func (h *Handler) UpdateWorker(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var worker storage.Worker
	if err := json.NewDecoder(r.Body).Decode(&worker); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Preserve server-side token; do not allow token updates via this endpoint
	existing, err := h.Storage.GetWorker(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "worker not found", http.StatusNotFound)
			return
		}
		h.JsonError(w, "failed to retrieve worker", http.StatusInternalServerError)
		return
	}
	worker.ID = id
	worker.Token = existing.Token
	if err := h.Storage.UpdateWorker(r.Context(), worker); err != nil {
		h.JsonError(w, "failed to update worker", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	// Hide token on responses
	worker.Token = ""
	_ = json.NewEncoder(w).Encode(worker)
}

func (h *Handler) UpdateWorkerHeartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		CPUUsage    float64 `json:"cpu_usage"`
		MemoryUsage float64 `json:"memory_usage"`
	}
	if r.Method == "POST" && r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if err := h.Storage.UpdateWorkerHeartbeat(r.Context(), id, req.CPUUsage, req.MemoryUsage); err != nil {
		h.JsonError(w, "failed to update heartbeat", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) RecommendWorker(w http.ResponseWriter, r *http.Request) {
	workers, _, err := h.Storage.ListWorkers(r.Context(), storage.CommonFilter{Limit: -1})
	if err != nil {
		h.JsonError(w, "failed to list workers", http.StatusInternalServerError)
		return
	}

	bestWorker := h.findBestWorker(workers)
	w.Header().Set("Content-Type", "application/json")
	bestWorker.Token = "" // Hide token
	_ = json.NewEncoder(w).Encode(bestWorker)
}

func (h *Handler) findBestWorker(workers []storage.Worker) storage.Worker {
	var bestWorker storage.Worker
	minLoad := 3.0 // Max theoretical load (CPU 1.0 + Mem 1.0 + Density 1.0)

	for _, wkr := range workers {
		load := wkr.CPUUsage + wkr.MemoryUsage
		// Add some weight for active workflows if available (placeholder for now)
		if load < minLoad {
			minLoad = load
			bestWorker = wkr
		}
	}
	return bestWorker
}

func (h *Handler) DeleteWorker(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Storage.DeleteWorker(r.Context(), id); err != nil {
		h.JsonError(w, "failed to delete worker", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
