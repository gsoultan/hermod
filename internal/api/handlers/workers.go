package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strconv"

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
	if r.Method == http.MethodPost && r.Body != nil {
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

// StartWorker launches an offline worker by relaunching the current hermod
// binary in worker mode on the platform host. It is restricted to administrators.
func (h *Handler) StartWorker(w http.ResponseWriter, r *http.Request) {
	role, _ := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		h.JsonError(w, "forbidden", http.StatusForbidden)
		return
	}

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

	exePath, err := os.Executable()
	if err != nil {
		h.JsonError(w, "failed to locate hermod executable", http.StatusInternalServerError)
		return
	}

	pid, err := spawnWorkerProcess(exePath, worker, platformURLFromRequest(r))
	if err != nil {
		h.JsonError(w, "failed to start worker process", http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "info", "Started worker "+worker.Name, "START", "", "", "", map[string]any{"worker_id": worker.ID})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":    "starting",
		"worker_id": worker.ID,
		"pid":       pid,
	})
}

// spawnWorkerProcess starts the hermod binary in worker mode for the given
// worker and returns the new process PID. The process is detached so it
// keeps running independently of the API request lifecycle.
// It is a package-level variable so it can be substituted in tests.
var spawnWorkerProcess = func(exePath string, worker storage.Worker, platformURL string) (int, error) {
	args := []string{
		"--mode", "worker",
		"--worker-guid", worker.ID,
		"--worker-token", worker.Token,
		"--platform-url", platformURL,
	}
	if worker.Host != "" {
		args = append(args, "--worker-host", worker.Host)
	}
	if worker.Port != 0 {
		args = append(args, "--worker-port", strconv.Itoa(worker.Port))
	}

	// #nosec G204 -- exePath is the current trusted hermod binary and arguments
	// are derived from the stored worker record, not arbitrary user input.
	cmd := exec.Command(exePath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	// Reap the child asynchronously to avoid leaving a zombie process.
	go func() { _ = cmd.Wait() }()
	return cmd.Process.Pid, nil
}

// platformURLFromRequest reconstructs the platform API base URL from the
// incoming request so the spawned worker can connect back to this server.
func platformURLFromRequest(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return scheme + "://" + r.Host
}
