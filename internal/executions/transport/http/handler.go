// Package http serves a workflow's runs: running it once with a payload the
// user supplies, and (see executions.go) its run history.
package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// ExecutionsHandler serves the run endpoints.
type ExecutionsHandler struct {
	*handlers.Handler
}

// NewExecutionsHandler wraps the shared handler.
func NewExecutionsHandler(h *handlers.Handler) *ExecutionsHandler {
	return &ExecutionsHandler{Handler: h}
}

// RegisterExecutionRoutes registers the run endpoints. Running a workflow
// changes things (its sinks write), so it needs an editor; reading history
// needs access to the workflow's vhost.
func (h *ExecutionsHandler) RegisterExecutionRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/workflows/{id}/run", h.EditorOnly(h.RunWorkflow))
	mux.HandleFunc("GET /api/workflows/{id}/executions", h.ListExecutions)
	mux.HandleFunc("GET /api/workflows/{id}/executions/{run_id}", h.GetExecution)
	mux.Handle("POST /api/workflows/{id}/executions/{run_id}/replay", h.EditorOnly(h.ReplayExecution))
}

// loadWorkflow reads the workflow named in the path and checks the caller may
// use its vhost. It writes the error response itself and returns false when
// the request must stop. Unlike the older workflow endpoints it fails closed:
// a workflow that cannot be read is not served, and a caller with no vhost
// list reaches only the default vhost.
func (h *ExecutionsHandler) loadWorkflow(w http.ResponseWriter, r *http.Request) (storage.Workflow, bool) {
	wf, err := h.Storage.GetWorkflow(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Workflow not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to load workflow", http.StatusInternalServerError)
		}
		return storage.Workflow{}, false
	}
	role, vhosts := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator && !h.HasVHostAccess(wf.VHost, vhosts) {
		h.JsonError(w, "Forbidden: you do not have access to this workflow's vhost", http.StatusForbidden)
		return storage.Workflow{}, false
	}
	return wf, true
}

// runContext bounds a run started from a request. The run stops if the caller
// goes away: a manual run is something a person is waiting on.
func runContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), manualRunTimeout)
}
