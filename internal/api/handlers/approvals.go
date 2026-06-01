package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/user/hermod/internal/storage"
)

func (h *Handler) RegisterApprovalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/approvals", h.ListApprovals)
	mux.HandleFunc("GET /api/approvals/{id}", h.GetApproval)
	mux.Handle("POST /api/approvals/{id}/approve", h.EditorOnly(h.ApproveApproval))
	mux.Handle("POST /api/approvals/{id}/reject", h.EditorOnly(h.RejectApproval))
}

func (h *Handler) ListApprovals(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) GetApproval(w http.ResponseWriter, r *http.Request) {
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
	Notes string `json:"notes"`
}

func (h *Handler) ApproveApproval(w http.ResponseWriter, r *http.Request) {
	h.HandleApprovalDecision(w, r, "approved")
}

func (h *Handler) RejectApproval(w http.ResponseWriter, r *http.Request) {
	h.HandleApprovalDecision(w, r, "rejected")
}

func (h *Handler) HandleApprovalDecision(w http.ResponseWriter, r *http.Request, status string) {
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

	var body decisionBody
	_ = json.NewDecoder(r.Body).Decode(&body)

	// Set processedBy from authenticated user when available
	processedBy := ""
	if u, ok := r.Context().Value(UserContextKey).(*storage.User); ok {
		processedBy = u.Username
	}

	if err := h.Storage.UpdateApprovalStatus(r.Context(), id, status, processedBy, body.Notes); err != nil {
		h.JsonError(w, "Failed to update approval: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Resume workflow from this approval node
	go func() {
		// small delay to ensure transactional visibility on some backends
		time.Sleep(10 * time.Millisecond)
		// reload approval to get updated fields if needed
		if app2, e := h.Storage.GetApproval(r.Context(), id); e == nil {
			branch := "approved"
			if status == "rejected" {
				branch = "rejected"
			}
			_ = h.Registry.ResumeApproval(r.Context(), app2, branch)
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
