// Package http serves the self-healing proposals of a workflow.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/selfheal"
	"github.com/gsoultan/hermod/internal/storage"
	workflowhttp "github.com/gsoultan/hermod/internal/workflow/transport/http"
)

// ProposalHandler lists, approves and rejects proposals.
type ProposalHandler struct {
	*handlers.Handler
	svc *selfheal.Service
}

// NewProposalHandler builds the handler. Approval applies through the
// workflow handler's update path.
func NewProposalHandler(h *handlers.Handler) *ProposalHandler {
	return &ProposalHandler{
		Handler: h,
		svc:     selfheal.NewService(h.Storage, workflowhttp.NewWorkflowHandler(h)),
	}
}

// Service is the proposal service the handler uses.
func (h *ProposalHandler) Service() *selfheal.Service { return h.svc }

// RegisterProposalRoutes mounts the API. Reading needs a Viewer; deciding
// changes the workflow and needs an Editor.
func (h *ProposalHandler) RegisterProposalRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/workflows/{id}/proposals", h.RbacMiddleware(storage.RoleViewer)(http.HandlerFunc(h.List)))
	mux.Handle("POST /api/workflows/{id}/proposals/{pid}/approve", h.EditorOnly(h.Approve))
	mux.Handle("POST /api/workflows/{id}/proposals/{pid}/reject", h.EditorOnly(h.Reject))
}

// workflow answers whether the caller may see the workflow, having written
// the refusal when not.
func (h *ProposalHandler) workflow(w http.ResponseWriter, r *http.Request) (storage.Workflow, bool) {
	wf, err := h.Storage.GetWorkflow(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, storage.ErrNotFound):
		h.JsonError(w, "Workflow not found", http.StatusNotFound)
		return wf, false
	case err != nil:
		h.JsonError(w, "Failed to get workflow: "+err.Error(), http.StatusInternalServerError)
		return wf, false
	}
	role, vhosts := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator && !h.HasVHostAccess(wf.VHost, vhosts) {
		h.JsonError(w, "Forbidden: you do not have access to this vhost", http.StatusForbidden)
		return wf, false
	}
	return wf, true
}

// List answers GET /api/workflows/{id}/proposals with {"data": [...]},
// newest first.
func (h *ProposalHandler) List(w http.ResponseWriter, r *http.Request) {
	wf, ok := h.workflow(w, r)
	if !ok {
		return
	}
	ps, err := h.svc.List(r.Context(), wf.ID)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"data": ps})
}

// Approve applies a pending proposal.
func (h *ProposalHandler) Approve(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, h.svc.Approve, "Approved self-healing proposal", "SELF_HEALING_APPROVE")
}

// Reject closes a pending proposal without applying it.
func (h *ProposalHandler) Reject(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, h.svc.Reject, "Rejected self-healing proposal", "SELF_HEALING_REJECT")
}

// decision is Service.Approve or Service.Reject.
type decision func(ctx context.Context, workflowID, id, username string) (selfheal.Proposal, error)

func (h *ProposalHandler) decide(w http.ResponseWriter, r *http.Request, act decision, what, action string) {
	wf, ok := h.workflow(w, r)
	if !ok {
		return
	}
	username := "System"
	if user, _ := r.Context().Value(handlers.UserContextKey).(*storage.User); user != nil {
		username = user.Username
	}
	p, err := act(r.Context(), wf.ID, r.PathValue("pid"), username)
	if err != nil {
		h.JsonError(w, err.Error(), statusOf(err))
		return
	}
	h.RecordAuditLog(r, "INFO", what+" for workflow "+wf.Name, action, wf.ID, "", "", p)
	writeJSON(w, p)
}

func statusOf(err error) int {
	switch {
	case errors.Is(err, selfheal.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, selfheal.ErrNotPending), errors.Is(err, selfheal.ErrStale):
		return http.StatusConflict
	case errors.Is(err, workflowhttp.ErrInvalidWorkflow):
		return http.StatusUnprocessableEntity
	default:
		return http.StatusInternalServerError
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
