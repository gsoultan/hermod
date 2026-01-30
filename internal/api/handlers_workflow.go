package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/message"
)

func (s *Server) registerWorkflowRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workflows", s.listWorkflows)
	mux.HandleFunc("GET /api/workflows/{id}", s.getWorkflow)
	mux.HandleFunc("POST /api/workflows", s.createWorkflow)
	mux.HandleFunc("PUT /api/workflows/{id}", s.updateWorkflow)
	mux.HandleFunc("DELETE /api/workflows/{id}", s.deleteWorkflow)
	mux.HandleFunc("POST /api/workflows/{id}/toggle", s.toggleWorkflow)
	mux.HandleFunc("POST /api/workflows/{id}/drain", s.drainWorkflowDLQ)
	mux.HandleFunc("POST /api/workflows/{id}/rebuild", s.rebuildWorkflow)
	mux.HandleFunc("POST /api/workflows/test", s.testWorkflow)
	mux.HandleFunc("POST /api/transformations/test", s.testTransformation)
}

func (s *Server) listWorkflows(w http.ResponseWriter, r *http.Request) {
	filter := s.parseCommonFilter(r)
	role, vhosts := s.getRoleAndVHosts(r)

	if filter.VHost != "" && role != storage.RoleAdministrator {
		if !s.hasVHostAccess(filter.VHost, vhosts) {
			s.jsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	wfs, total, err := s.storage.ListWorkflows(r.Context(), filter)
	if err != nil {
		s.jsonError(w, "Failed to list workflows: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if role != "" && role != storage.RoleAdministrator {
		filtered := []storage.Workflow{}
		for _, wf := range wfs {
			if s.hasVHostAccess(wf.VHost, vhosts) {
				filtered = append(filtered, wf)
			}
		}
		wfs = filtered
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  wfs,
		"total": total,
	})
}

func (s *Server) getWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wf, err := s.storage.GetWorkflow(r.Context(), id)
	if err != nil {
		if err == storage.ErrNotFound {
			s.jsonError(w, "Workflow not found", http.StatusNotFound)
		} else {
			s.jsonError(w, "Failed to get workflow: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	role, vhosts := s.getRoleAndVHosts(r)
	if role != "" && role != storage.RoleAdministrator {
		if !s.hasVHostAccess(wf.VHost, vhosts) {
			s.jsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(wf)
}

func (s *Server) createWorkflow(w http.ResponseWriter, r *http.Request) {
	var wf storage.Workflow
	if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if wf.Name == "" {
		s.jsonError(w, "Workflow name is mandatory", http.StatusBadRequest)
		return
	}

	if err := s.storage.CreateWorkflow(r.Context(), wf); err != nil {
		s.jsonError(w, "Failed to create workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "Created workflow "+wf.Name, "create", wf.ID, "", "", wf)

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(wf)
}

func (s *Server) updateWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var wf storage.Workflow
	if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	wf.ID = id

	if err := s.storage.UpdateWorkflow(r.Context(), wf); err != nil {
		s.jsonError(w, "Failed to update workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "Updated workflow "+wf.Name, "update", wf.ID, "", "", wf)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(wf)
}

func (s *Server) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.storage.DeleteWorkflow(r.Context(), id); err != nil {
		s.jsonError(w, "Failed to delete workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "Deleted workflow "+id, "delete", id, "", "", nil)

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) toggleWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wf, err := s.storage.GetWorkflow(r.Context(), id)
	if err != nil {
		s.jsonError(w, "Workflow not found", http.StatusNotFound)
		return
	}

	if wf.Active {
		wf.Active = false
		wf.Status = "Stopped"
		_ = s.registry.StopEngine(id)
	} else {
		wf.Active = true
		wf.Status = "Active"
		if err := s.registry.StartWorkflow(id, wf); err != nil && !strings.Contains(err.Error(), "already running") {
			s.jsonError(w, "Failed to start workflow: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if err := s.storage.UpdateWorkflow(r.Context(), wf); err != nil {
		s.jsonError(w, "Failed to update workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	action := "stop"
	if wf.Active {
		action = "start"
	}
	s.recordAuditLog(r, "INFO", "Workflow "+wf.Name+" "+action+"ed", action, wf.ID, "", "", nil)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(wf)
}

func (s *Server) drainWorkflowDLQ(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.registry.DrainWorkflowDLQ(id); err != nil {
		s.jsonError(w, "Failed to drain DLQ: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "Drained DLQ for workflow "+id, "drain_dlq", id, "", "", nil)

	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) rebuildWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		FromOffset int64 `json:"from_offset"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Hour)
		defer cancel()
		if err := s.registry.RebuildWorkflow(ctx, id, req.FromOffset); err != nil {
			log.Printf("RebuildWorkflow %s failed: %v", id, err)
		}
	}()

	s.recordAuditLog(r, "INFO", "Started projection rebuilding for workflow "+id, "rebuild", id, "", "", nil)

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "rebuild started"})
}

func (s *Server) testWorkflow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Workflow storage.Workflow       `json:"workflow"`
		Message  map[string]interface{} `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "Failed to decode request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	for k, v := range req.Message {
		msg.SetData(k, v)
	}

	steps, err := s.registry.TestWorkflow(r.Context(), req.Workflow, msg)
	if err != nil {
		s.jsonError(w, "Failed to test workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(steps)
}

func (s *Server) testTransformation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Transformation storage.Transformation `json:"transformation"`
		Message        map[string]interface{} `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "Failed to decode request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	for k, v := range req.Message {
		msg.SetData(k, v)
	}

	res, err := s.registry.TestTransformationPipeline(r.Context(), []storage.Transformation{req.Transformation}, msg)
	if err != nil {
		s.jsonError(w, "Failed to test transformation: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if len(res) == 0 || res[0] == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "Filtered", "filtered": true})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res[0].Data())
}
