package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/source/webhook"
)

func (s *Server) registerSourceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sources", s.listSources)
	mux.HandleFunc("GET /api/sources/{id}", s.getSource)
	mux.Handle("POST /api/sources", s.editorOnly(s.createSource))
	mux.Handle("PUT /api/sources/{id}", s.editorOnly(s.updateSource))
	mux.Handle("POST /api/sources/test", s.editorOnly(s.testSource))
	mux.Handle("POST /api/sources/discover/databases", s.editorOnly(s.discoverDatabases))
	mux.Handle("POST /api/sources/discover/tables", s.editorOnly(s.discoverTables))
	mux.Handle("POST /api/sources/sample", s.editorOnly(s.sampleSourceTable))
	mux.Handle("POST /api/proxy/fetch", s.editorOnly(s.proxyFetch))
	mux.Handle("DELETE /api/sources/{id}", s.editorOnly(s.deleteSource))
	mux.Handle("POST /api/sources/{id}/snapshot", s.editorOnly(s.triggerSnapshot))
	mux.HandleFunc("GET /api/sources/{id}/workflows", s.listWorkflowsReferencingSource)
	mux.HandleFunc("GET /api/webhooks/requests", s.listWebhookRequests)
	mux.Handle("POST /api/webhooks/requests/{id}/replay", s.editorOnly(s.replayWebhookRequest))
}

func (s *Server) listSources(w http.ResponseWriter, r *http.Request) {
	filter := s.parseCommonFilter(r)
	role, vhosts := s.getRoleAndVHosts(r)

	if filter.VHost != "" && role != storage.RoleAdministrator {
		if !s.hasVHostAccess(filter.VHost, vhosts) {
			s.jsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	sources, total, err := s.storage.ListSources(r.Context(), filter)
	if err != nil {
		s.jsonError(w, "Failed to list sources: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if role != "" && role != storage.RoleAdministrator {
		filtered := []storage.Source{}
		for _, src := range sources {
			if s.hasVHostAccess(src.VHost, vhosts) {
				filtered = append(filtered, src)
			}
		}
		sources = filtered
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  sources,
		"total": total,
	})
}

func (s *Server) getSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	src, err := s.storage.GetSource(r.Context(), id)
	if err != nil {
		if err == storage.ErrNotFound {
			s.jsonError(w, "Source not found", http.StatusNotFound)
		} else {
			s.jsonError(w, "Failed to retrieve source: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	role, vhosts := s.getRoleAndVHosts(r)
	if role != "" && role != storage.RoleAdministrator {
		if !s.hasVHostAccess(src.VHost, vhosts) {
			s.jsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(src)
}

func (s *Server) createSource(w http.ResponseWriter, r *http.Request) {
	var src storage.Source
	if err := json.NewDecoder(r.Body).Decode(&src); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if src.Name == "" || src.Type == "" || src.VHost == "" {
		s.jsonError(w, "Name, Type, and VHost are mandatory", http.StatusBadRequest)
		return
	}

	role, vhosts := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !s.hasVHostAccess(src.VHost, vhosts) {
			s.jsonError(w, "Forbidden: you don't have access to this vhost", http.StatusForbidden)
			return
		}
	}

	src.ID = uuid.New().String()
	src.Active = true
	if err := s.storage.CreateSource(r.Context(), src); err != nil {
		s.jsonError(w, "Failed to create source: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "Created source "+src.Name, "create", "", src.ID, "", src)

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(src)
}

func (s *Server) updateSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var src storage.Source
	if err := json.NewDecoder(r.Body).Decode(&src); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	src.ID = id

	role, vhosts := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !s.hasVHostAccess(src.VHost, vhosts) {
			s.jsonError(w, "Forbidden: you don't have access to this vhost", http.StatusForbidden)
			return
		}
	}

	if err := s.storage.UpdateSource(r.Context(), src); err != nil {
		s.jsonError(w, "Failed to update source: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "Updated source "+src.Name, "update", "", src.ID, "", src)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(src)
}

func (s *Server) deleteSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	src, err := s.storage.GetSource(ctx, id)
	if err != nil {
		if err == storage.ErrNotFound {
			s.jsonError(w, "Source not found", http.StatusNotFound)
		} else {
			s.jsonError(w, "Failed to get source: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// RBAC check
	role, vhosts := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !s.hasVHostAccess(src.VHost, vhosts) {
			s.jsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	wfs, _, err := s.storage.ListWorkflows(ctx, storage.CommonFilter{})
	if err == nil {
		for _, wf := range wfs {
			for _, node := range wf.Nodes {
				if node.Type == "source" && node.RefID == id {
					if src.Config["use_cdc"] != "true" {
						s.jsonError(w, "Cannot delete source: it is used by workflow "+wf.Name, http.StatusConflict)
						return
					}
					if wf.Active {
						_ = s.registry.StopEngine(wf.ID)
						wf.Active = false
						wf.Status = "Stopped"
						_ = s.storage.UpdateWorkflow(ctx, wf)
					}
				}
			}
		}
	}

	if err := s.storage.DeleteSource(ctx, id); err != nil {
		s.jsonError(w, "Failed to delete source: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "Deleted source "+src.Name, "delete", "", id, "", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) triggerSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	src, err := s.storage.GetSource(ctx, id)
	if err != nil {
		if err == storage.ErrNotFound {
			s.jsonError(w, "Source not found", http.StatusNotFound)
		} else {
			s.jsonError(w, "Failed to get source: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// RBAC check
	role, vhosts := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !s.hasVHostAccess(src.VHost, vhosts) {
			s.jsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	var req struct {
		Tables []string `json:"tables"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			s.jsonError(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	if err := s.registry.TriggerSnapshot(ctx, id, req.Tables...); err != nil {
		s.jsonError(w, "Failed to trigger snapshot: "+err.Error(), http.StatusBadRequest)
		return
	}

	s.recordAuditLog(r, "INFO", "Triggered snapshot for source "+src.Name, "snapshot", "", id, "", nil)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "Snapshot triggered successfully"})
}

// listWorkflowsReferencingSource returns workflows that reference the given source ID.
func (s *Server) listWorkflowsReferencingSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	src, err := s.storage.GetSource(ctx, id)
	if err != nil {
		if err == storage.ErrNotFound {
			s.jsonError(w, "Source not found", http.StatusNotFound)
		} else {
			s.jsonError(w, "Failed to get source: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// RBAC check based on the source's vhost
	role, vhosts := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !s.hasVHostAccess(src.VHost, vhosts) {
			s.jsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	wfs, _, err := s.storage.ListWorkflows(ctx, storage.CommonFilter{})
	if err != nil {
		s.jsonError(w, "Failed to list workflows: "+err.Error(), http.StatusInternalServerError)
		return
	}

	type wfRef struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Active bool   `json:"active"`
		Status string `json:"status"`
	}

	referencing := make([]wfRef, 0)
	for _, wf := range wfs {
		// Optional: filter by vhost if workflows carry vhost; otherwise rely on source RBAC above.
		for _, node := range wf.Nodes {
			if node.Type == "source" && node.RefID == id {
				referencing = append(referencing, wfRef{ID: wf.ID, Name: wf.Name, Active: wf.Active, Status: wf.Status})
				break
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": referencing})
}

func (s *Server) testSource(w http.ResponseWriter, r *http.Request) {
	var src storage.Source
	if err := json.NewDecoder(r.Body).Decode(&src); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := engine.SourceConfig{Type: src.Type, Config: src.Config}
	if err := s.registry.TestSource(r.Context(), cfg); err != nil {
		s.jsonError(w, "Test failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) discoverDatabases(w http.ResponseWriter, r *http.Request) {
	var src storage.Source
	if err := json.NewDecoder(r.Body).Decode(&src); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := engine.SourceConfig{Type: src.Type, Config: src.Config}
	dbs, err := s.registry.DiscoverDatabases(r.Context(), cfg)
	if err != nil {
		s.jsonError(w, "Discovery failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dbs)
}

func (s *Server) discoverTables(w http.ResponseWriter, r *http.Request) {
	var src storage.Source
	if err := json.NewDecoder(r.Body).Decode(&src); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := engine.SourceConfig{Type: src.Type, Config: src.Config}
	tables, err := s.registry.DiscoverTables(r.Context(), cfg)
	if err != nil {
		s.jsonError(w, "Discovery failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tables)
}

func (s *Server) sampleSourceTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source storage.Source `json:"source"`
		Table  string         `json:"table"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := engine.SourceConfig{Type: req.Source.Type, Config: req.Source.Config}
	msg, err := s.registry.SampleTable(r.Context(), cfg, req.Table)
	if err != nil {
		s.jsonError(w, "Sampling failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":        msg.ID(),
		"operation": msg.Operation(),
		"table":     msg.Table(),
		"after":     string(msg.After()),
	})
}

func (s *Server) proxyFetch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL     string            `json:"url"`
		Method  string            `json:"method"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	hreq, _ := http.NewRequestWithContext(r.Context(), req.Method, req.URL, nil)
	for k, v := range req.Headers {
		hreq.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(hreq)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"body": string(body),
	})
}

func (s *Server) listWebhookRequests(w http.ResponseWriter, r *http.Request) {
	filter := storage.WebhookRequestFilter{
		CommonFilter: s.parseCommonFilter(r),
		Path:         r.URL.Query().Get("path"),
	}

	requests, total, err := s.storage.ListWebhookRequests(r.Context(), filter)
	if err != nil {
		s.jsonError(w, "Failed to list webhook requests: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  requests,
		"total": total,
	})
}

func (s *Server) replayWebhookRequest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	req, err := s.storage.GetWebhookRequest(r.Context(), id)
	if err != nil {
		s.jsonError(w, "Webhook request not found", http.StatusNotFound)
		return
	}

	msg := message.AcquireMessage()
	msg.SetID(uuid.New().String())
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable("webhook")
	msg.SetAfter(req.Body)
	msg.SetMetadata("webhook_path", req.Path)
	msg.SetMetadata("http_method", req.Method)
	msg.SetMetadata("replayed", "true")
	msg.SetMetadata("original_request_id", req.ID)

	if err := webhook.Dispatch(req.Path, msg); err != nil {
		// Attempt to wake up workflow if it was parked
		if s.wakeUpWorkflow(r.Context(), "webhook", req.Path) {
			if err := webhook.Dispatch(req.Path, msg); err == nil {
				goto dispatched
			}
		}
		message.ReleaseMessage(msg)
		s.jsonError(w, "Failed to dispatch replayed webhook: "+err.Error(), http.StatusInternalServerError)
		return
	}

dispatched:
	s.recordAuditLog(r, "INFO", "Replayed webhook request "+id, "replay", "", "", "", req)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "dispatched", "id": msg.ID()})
}
