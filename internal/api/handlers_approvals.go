package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/user/hermod/internal/storage"
)

func (s *Server) registerApprovalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/approvals", s.listApprovals)
	mux.HandleFunc("GET /api/approvals/{id}", s.getApproval)
	mux.Handle("POST /api/approvals/{id}/approve", s.editorOnly(s.approveApproval))
	mux.Handle("POST /api/approvals/{id}/reject", s.editorOnly(s.rejectApproval))
}

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	filter := s.parseCommonFilter(r)
	af := storage.ApprovalFilter{CommonFilter: filter}
	if v := r.URL.Query().Get("workflow_id"); v != "" {
		af.WorkflowID = v
	}
	if v := r.URL.Query().Get("status"); v != "" {
		af.Status = v
	}

	apps, total, err := s.storage.ListApprovals(r.Context(), af)
	if err != nil {
		s.jsonError(w, "Failed to list approvals: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  apps,
		"total": total,
	})
}

func (s *Server) getApproval(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	app, err := s.storage.GetApproval(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.jsonError(w, "Approval not found", http.StatusNotFound)
		} else {
			s.jsonError(w, "Failed to get approval: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(app)
}

type decisionBody struct {
	Notes string `json:"notes"`
}

func (s *Server) approveApproval(w http.ResponseWriter, r *http.Request) {
	s.handleApprovalDecision(w, r, "approved")
}

func (s *Server) rejectApproval(w http.ResponseWriter, r *http.Request) {
	s.handleApprovalDecision(w, r, "rejected")
}

func (s *Server) handleApprovalDecision(w http.ResponseWriter, r *http.Request, status string) {
	id := r.PathValue("id")

	app, err := s.storage.GetApproval(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.jsonError(w, "Approval not found", http.StatusNotFound)
		} else {
			s.jsonError(w, "Failed to get approval: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	var body decisionBody
	_ = json.NewDecoder(r.Body).Decode(&body)

	// Set processedBy from authenticated user when available
	processedBy := ""
	if u, ok := r.Context().Value(userContextKey).(*storage.User); ok {
		processedBy = u.Username
	}

	if err := s.storage.UpdateApprovalStatus(r.Context(), id, status, processedBy, body.Notes); err != nil {
		s.jsonError(w, "Failed to update approval: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Resume workflow from this approval node
	go func() {
		// small delay to ensure transactional visibility on some backends
		time.Sleep(10 * time.Millisecond)
		// reload approval to get updated fields if needed
		if app2, e := s.storage.GetApproval(r.Context(), id); e == nil {
			branch := "approved"
			if status == "rejected" {
				branch = "rejected"
			}
			_ = s.registry.ResumeApproval(r.Context(), app2, branch)
		}
	}()

	// Audit log
	action := "APPROVE"
	if status == "rejected" {
		action = "REJECT"
	}
	s.recordAuditLog(r, "INFO", action+" approval "+id, action, app.WorkflowID, "", "", map[string]string{"node_id": app.NodeID})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
}
