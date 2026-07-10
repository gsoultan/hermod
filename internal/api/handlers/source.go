package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/factory"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/comm/source/webhook"
	"github.com/user/hermod/pkg/infra/httpclient"
)

func (h *Handler) RegisterSourceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sources", h.ListSources)
	mux.HandleFunc("GET /api/sources/{id}", h.GetSource)
	mux.Handle("POST /api/sources", h.EditorOnly(h.CreateSource))
	mux.Handle("PUT /api/sources/{id}", h.EditorOnly(h.UpdateSource))
	mux.Handle("POST /api/sources/test", h.EditorOnly(h.TestSource))
	mux.Handle("POST /api/sources/discover/databases", h.EditorOnly(h.DiscoverDatabases))
	mux.Handle("POST /api/sources/discover/tables", h.EditorOnly(h.DiscoverTables))
	mux.Handle("POST /api/sources/discover/columns", h.EditorOnly(h.DiscoverSourceColumns))
	mux.Handle("POST /api/sources/discover/replication", h.EditorOnly(h.DiscoverReplication))
	mux.Handle("POST /api/sources/sample", h.EditorOnly(h.SampleSourceTable))
	mux.Handle("POST /api/sources/query", h.EditorOnly(h.QuerySource))
	mux.Handle("POST /api/sources/upload", h.EditorOnly(h.UploadFile))
	mux.Handle("POST /api/proxy/fetch", h.EditorOnly(h.ProxyFetch))
	mux.Handle("DELETE /api/sources/{id}", h.EditorOnly(h.DeleteSource))
	mux.Handle("POST /api/sources/{id}/snapshot", h.EditorOnly(h.TriggerSnapshot))
	mux.HandleFunc("GET /api/sources/{id}/workflows", h.ListWorkflowsReferencingSource)
	mux.HandleFunc("GET /api/webhooks/requests", h.ListWebhookRequests)
	mux.Handle("POST /api/webhooks/requests/{id}/replay", h.EditorOnly(h.ReplayWebhookRequest))
}

func (h *Handler) ListSources(w http.ResponseWriter, r *http.Request) {
	filter := h.ParseCommonFilter(r)
	role, vhosts := h.GetRoleAndVHosts(r)

	if filter.VHost != "" && role != storage.RoleAdministrator {
		if !h.HasVHostAccess(filter.VHost, vhosts) {
			h.JsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	sources, total, err := h.Storage.ListSources(r.Context(), filter)
	if err != nil {
		h.JsonError(w, "Failed to list sources: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if role != "" && role != storage.RoleAdministrator {
		filtered := []storage.Source{}
		for _, src := range sources {
			if h.HasVHostAccess(src.VHost, vhosts) {
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

func (h *Handler) GetSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	src, err := h.Storage.GetSource(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Source not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to retrieve source: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	role, vhosts := h.GetRoleAndVHosts(r)
	if role != "" && role != storage.RoleAdministrator {
		if !h.HasVHostAccess(src.VHost, vhosts) {
			h.JsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(src)
}

func (h *Handler) CreateSource(w http.ResponseWriter, r *http.Request) {
	var src storage.Source
	if err := json.NewDecoder(r.Body).Decode(&src); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if src.Name == "" || src.Type == "" || src.VHost == "" {
		h.JsonError(w, "Name, Type, and VHost are mandatory", http.StatusBadRequest)
		return
	}

	role, vhosts := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !h.HasVHostAccess(src.VHost, vhosts) {
			h.JsonError(w, "Forbidden: you don't have access to this vhost", http.StatusForbidden)
			return
		}
	}

	src.ID = uuid.New().String()
	src.Active = true
	if err := h.Storage.CreateSource(r.Context(), src); err != nil {
		h.JsonError(w, "Failed to create source: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "Created source "+src.Name, "create", "", src.ID, "", src)

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(src)
}

func (h *Handler) UpdateSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var src storage.Source
	if err := json.NewDecoder(r.Body).Decode(&src); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	src.ID = id

	role, vhosts := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !h.HasVHostAccess(src.VHost, vhosts) {
			h.JsonError(w, "Forbidden: you don't have access to this vhost", http.StatusForbidden)
			return
		}
	}

	if err := h.Storage.UpdateSource(r.Context(), src); err != nil {
		h.JsonError(w, "Failed to update source: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "Updated source "+src.Name, "update", "", src.ID, "", src)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(src)
}

func (h *Handler) DeleteSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	src, err := h.Storage.GetSource(ctx, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Source not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to get source: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// RBAC check
	role, vhosts := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !h.HasVHostAccess(src.VHost, vhosts) {
			h.JsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	wfs, _, err := h.Storage.ListWorkflows(ctx, storage.CommonFilter{})
	if err == nil {
		for _, wf := range wfs {
			for _, node := range wf.Nodes {
				if node.Type == "source" && node.RefID == id {
					if src.Config["use_cdc"] != "true" {
						h.JsonError(w, "Cannot delete source: it is used by workflow "+wf.Name, http.StatusConflict)
						return
					}
					if wf.Active {
						_ = h.Registry.StopEngine(ctx, wf.ID)
						wf.Active = false
						wf.Status = "Stopped"
						_ = h.Storage.UpdateWorkflow(ctx, wf)
					}
				}
			}
		}
	}

	if err := h.Storage.DeleteSource(ctx, id); err != nil {
		h.JsonError(w, "Failed to delete source: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "Deleted source "+src.Name, "delete", "", id, "", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) TriggerSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	src, err := h.Storage.GetSource(ctx, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Source not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to get source: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// RBAC check
	role, vhosts := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !h.HasVHostAccess(src.VHost, vhosts) {
			h.JsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	var req struct {
		Tables []string `json:"tables"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			h.JsonError(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	if err := h.Registry.TriggerSnapshot(ctx, id, req.Tables...); err != nil {
		h.JsonError(w, "Failed to trigger snapshot: "+err.Error(), http.StatusBadRequest)
		return
	}

	h.RecordAuditLog(r, "INFO", "Triggered snapshot for source "+src.Name, "snapshot", "", id, "", map[string]any{"tables": req.Tables})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "Snapshot triggered successfully"})
}

// listWorkflowsReferencingSource returns workflows that reference the given source ID.
func (h *Handler) ListWorkflowsReferencingSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	src, err := h.Storage.GetSource(ctx, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Source not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to get source: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// RBAC check based on the source's vhost
	role, vhosts := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !h.HasVHostAccess(src.VHost, vhosts) {
			h.JsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	wfs, _, err := h.Storage.ListWorkflows(ctx, storage.CommonFilter{})
	if err != nil {
		h.JsonError(w, "Failed to list workflows: "+err.Error(), http.StatusInternalServerError)
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
		// Only include active workflows
		if !wf.Active {
			continue
		}
		// Enforce workflow-level RBAC by vhost for non-admins
		if role != storage.RoleAdministrator {
			if !h.HasVHostAccess(wf.VHost, vhosts) {
				continue
			}
		}
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

func (h *Handler) TestSource(w http.ResponseWriter, r *http.Request) {
	// Decode only the minimal fields required to test a source to avoid
	// strict coupling with storage.Source (which includes optional fields
	// like Sample that may vary in type across UIs).
	var req struct {
		Type   string           `json:"type"`
		Config hermod.StringMap `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := factory.SourceConfig{Type: req.Type, Config: req.Config}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	if err := h.Registry.TestSource(ctx, cfg); err != nil {
		h.JsonError(w, "Test failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) DiscoverDatabases(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type   string           `json:"type"`
		Config hermod.StringMap `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := factory.SourceConfig{Type: req.Type, Config: req.Config}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	dbs, err := h.Registry.DiscoverDatabases(ctx, cfg)
	if err != nil {
		h.JsonError(w, "Discovery failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dbs)
}

func (h *Handler) DiscoverTables(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type   string           `json:"type"`
		Config hermod.StringMap `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := factory.SourceConfig{Type: req.Type, Config: req.Config}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	tables, err := h.Registry.DiscoverTables(ctx, cfg)
	if err != nil {
		h.JsonError(w, "Discovery failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tables)
}

func (h *Handler) DiscoverSourceColumns(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source struct {
			Type   string           `json:"type"`
			Config hermod.StringMap `json:"config"`
		} `json:"source"`
		Table string `json:"table"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := factory.SourceConfig{Type: req.Source.Type, Config: req.Source.Config}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	columns, err := h.Registry.DiscoverSourceColumns(ctx, cfg, req.Table)
	if err != nil {
		h.JsonError(w, "Discovery failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(columns)
}

// DiscoverReplication returns the existing logical replication slots and
// publications for a CDC source so the user can reuse one or create a new one.
func (h *Handler) DiscoverReplication(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type   string           `json:"type"`
		Config hermod.StringMap `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := factory.SourceConfig{Type: req.Type, Config: req.Config}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	slots, err := h.Registry.DiscoverReplicationSlots(ctx, cfg)
	if err != nil {
		h.JsonError(w, "Discovery failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	publications, err := h.Registry.DiscoverPublications(ctx, cfg)
	if err != nil {
		h.JsonError(w, "Discovery failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"slots":        slots,
		"publications": publications,
	})
}

func (h *Handler) SampleSourceTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source struct {
			Type   string           `json:"type"`
			Config hermod.StringMap `json:"config"`
		} `json:"source"`
		Table string `json:"table"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg := factory.SourceConfig{Type: req.Source.Type, Config: req.Source.Config}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	msg, err := h.Registry.SampleTable(ctx, cfg, req.Table)
	if err != nil {
		h.JsonError(w, "Sampling failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(msg)
}

func (h *Handler) QuerySource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Config     factory.SourceConfig `json:"config"`
		Query      string               `json:"query"`
		SampleData map[string]any       `json:"sampleData"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	results, err := h.Registry.ExecuteSQL(ctx, req.Config, req.Query, req.SampleData)
	if err != nil {
		h.JsonError(w, "Query failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (h *Handler) ProxyFetch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL     string            `json:"url"`
		Method  string            `json:"method"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	hreq, _ := http.NewRequestWithContext(ctx, req.Method, req.URL, nil)
	for k, v := range req.Headers {
		hreq.Header.Set(k, v)
	}

	resp, err := httpclient.DefaultClient.Do(hreq)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"body": string(body),
	})
}

func (h *Handler) ListWebhookRequests(w http.ResponseWriter, r *http.Request) {
	filter := storage.WebhookRequestFilter{
		CommonFilter: h.ParseCommonFilter(r),
		Path:         r.URL.Query().Get("path"),
	}

	requests, total, err := h.Storage.ListWebhookRequests(r.Context(), filter)
	if err != nil {
		h.JsonError(w, "Failed to list webhook requests: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  requests,
		"total": total,
	})
}

func (h *Handler) ReplayWebhookRequest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	req, err := h.Storage.GetWebhookRequest(r.Context(), id)
	if err != nil {
		h.JsonError(w, "Webhook request not found", http.StatusNotFound)
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
		if h.WakeUpWorkflow(r.Context(), "webhook", req.Path) {
			if err := webhook.Dispatch(req.Path, msg); err == nil {
				goto dispatched
			}
		}
		message.ReleaseMessage(msg)
		h.JsonError(w, "Failed to dispatch replayed webhook: "+err.Error(), http.StatusInternalServerError)
		return
	}

dispatched:
	h.RecordAuditLog(r, "INFO", "Replayed webhook request "+id, "replay", "", "", "", req)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "dispatched", "id": msg.ID()})
}
