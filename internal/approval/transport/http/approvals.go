package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

func (h *ApprovalHandler) RegisterApprovalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/approvals", h.ListApprovals)
	mux.HandleFunc("GET /api/approvals/{id}", h.GetApproval)
	mux.Handle("POST /api/approvals/{id}/approve", h.EditorOnly(h.ApproveApproval))
	mux.Handle("POST /api/approvals/{id}/reject", h.EditorOnly(h.RejectApproval))
}

func (h *ApprovalHandler) ListApprovals(w http.ResponseWriter, r *http.Request) {
	filter := h.ParseCommonFilter(r)
	af := storage.ApprovalFilter{CommonFilter: filter}
	if v := r.URL.Query().Get("workflow_id"); v != "" {
		af.WorkflowID = v
	}
	if v := r.URL.Query().Get("status"); v != "" {
		af.Status = v
	}

	apps, total, err := h.Storage.ListApprovals(r.Context(), af)
	if err != nil {
		h.JsonError(w, "Failed to list approvals: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  apps,
		"total": total,
	})
}

func (h *ApprovalHandler) GetApproval(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	app, err := h.Storage.GetApproval(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Approval not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to get approval: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(app)
}

type decisionBody struct {
	Notes    string         `json:"notes"`
	FormData map[string]any `json:"form_data"`
}

// approvalResumeTimeout bounds the walk an approval decision starts: the
// request has ended by then, so nothing else would stop a stuck resume.
const approvalResumeTimeout = 10 * time.Minute

func (h *ApprovalHandler) ApproveApproval(w http.ResponseWriter, r *http.Request) {
	h.HandleApprovalDecision(w, r, "approved")
}

func (h *ApprovalHandler) RejectApproval(w http.ResponseWriter, r *http.Request) {
	h.HandleApprovalDecision(w, r, "rejected")
}

func (h *ApprovalHandler) HandleApprovalDecision(w http.ResponseWriter, r *http.Request, status string) {
	id := r.PathValue("id")

	app, err := h.Storage.GetApproval(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Approval not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to get approval: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// A decision resumes the workflow, so deciding twice would resume it
	// twice: the message delivered again, and an AI agent's approved write
	// tool run again. Only a pending approval can be decided.
	if app.Status != "" && app.Status != "pending" {
		h.JsonError(w, "Approval was already "+app.Status, http.StatusConflict)
		return
	}

	var body decisionBody
	_ = json.NewDecoder(r.Body).Decode(&body)

	// Set processedBy from authenticated user when available
	processedBy := ""
	if u, ok := r.Context().Value(handlers.UserContextKey).(*storage.User); ok {
		processedBy = u.Username
	}

	if err := h.Storage.UpdateApprovalStatus(r.Context(), id, status, processedBy, body.Notes, body.FormData); err != nil {
		if errors.Is(err, storage.ErrApprovalDecided) {
			h.JsonError(w, "Approval was already decided", http.StatusConflict)
			return
		}
		h.JsonError(w, "Failed to update approval: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Resume workflow from this approval node.
	//
	// The resume outlives this request: the server ends r.Context() as soon as
	// the handler returns, and running on it the reload below failed and no
	// approved message was ever delivered. It keeps the request's values and
	// gets its own bound instead.
	resumeCtx, cancelResume := context.WithTimeout(context.WithoutCancel(r.Context()), approvalResumeTimeout)
	go func() {
		defer cancelResume()
		// small delay to ensure transactional visibility on some backends
		time.Sleep(10 * time.Millisecond)
		// reload approval to get updated fields if needed
		app2, e := h.Storage.GetApproval(resumeCtx, id)
		if e != nil {
			h.Registry.GetLogger().Error("Approval decided but its workflow could not be resumed",
				"approval_id", id, "error", e)
			return
		}
		branch := "approved"
		if status == "rejected" {
			branch = "rejected"
		}
		if e := h.Registry.ResumeApproval(resumeCtx, app2, branch); e != nil {
			h.Registry.GetLogger().Error("Resuming the workflow after an approval failed",
				"approval_id", id, "workflow_id", app2.WorkflowID, "error", e)
		}
	}()

	// Audit log
	action := "APPROVE"
	if status == "rejected" {
		action = "REJECT"
	}
	h.RecordAuditLog(r, "INFO", action+" approval "+id, action, app.WorkflowID, "", "", map[string]string{"node_id": app.NodeID})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
}
