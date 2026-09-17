package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/source/webhook"
	"github.com/gsoultan/hermod/pkg/infra/httpclient"
)

func (h *SourceHandler) RegisterSourceRoutes(mux *http.ServeMux) {
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

func (h *SourceHandler) ListSources(w http.ResponseWriter, r *http.Request) {
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

func (h *SourceHandler) GetSource(w http.ResponseWriter, r *http.Request) {
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

func (h *SourceHandler) CreateSource(w http.ResponseWriter, r *http.Request) {
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

func (h *SourceHandler) UpdateSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := h.checkActiveWorkflows(r.Context(), id); err != nil {
		h.JsonError(w, "Cannot update source: "+err.Error()+". Please stop the workflow first.", http.StatusConflict)
		return
	}

	// Fetch current source to compare CDC settings
	oldSrc, err := h.Storage.GetSource(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Source not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to get source: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	var src storage.Source
	if err := json.NewDecoder(r.Body).Decode(&src); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	src.ID = id
	src = carryRuntimeColumns(src, oldSrc)

	role, vhosts := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !h.HasVHostAccess(src.VHost, vhosts) {
			h.JsonError(w, "Forbidden: you don't have access to this vhost", http.StatusForbidden)
			return
		}
	}

	// Postgres CDC Protection Logic: prevent changing slot/publication but allow tracking tables.
	if oldSrc.Type == "postgres" && src.Type == "postgres" {
		oldUseCDC := oldSrc.Config["use_cdc"] == "true"
		newUseCDC := src.Config["use_cdc"] == "true"

		if oldUseCDC && newUseCDC {
			oldSlot := oldSrc.Config["slot_name"]
			newSlot := src.Config["slot_name"]
			oldPub := oldSrc.Config["publication_name"]
			newPub := src.Config["publication_name"]

			// If user tried to change slot or publication, revert them but allow the update
			// (so tracking tables change, but CDC infra remains fixed as requested).
			// This matches pgAdmin behavior where some fields are immutable during edit.
			if (newSlot != "" && newSlot != oldSlot) || (newPub != "" && newPub != oldPub) {
				src.Config["slot_name"] = oldSlot
				src.Config["publication_name"] = oldPub
			}
		}
	}

	if err := h.checkCDCFlipForQueryTargets(r.Context(), oldSrc, src); err != nil {
		h.JsonError(w, "Cannot update source: "+err.Error(), http.StatusConflict)
		return
	}

	if err := h.Registry.UpdateSource(r.Context(), src); err != nil {
		h.JsonError(w, "Failed to update source: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "Updated source "+src.Name, "update", "", src.ID, "", src)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(src)
}

// carryRuntimeColumns copies the runtime columns an update did not mention from
// the stored row onto the incoming one.
//
// State describes how far a source has read — a batch_sql `last_value`
// watermark, a Postgres CDC cursor — and storage.UpdateSource writes it on
// every update, so a body that never mentioned it used to overwrite the column
// with null. The clients that do this are not editing the cursor and have no
// business resetting it: useSourceForm.onSampleReady fires on every Test
// Connection, and the wizard's own save carries `sample` across but never
// `state`. The next scheduled run then replayed everything the source had
// already delivered, with nothing in the request or the response to say why.
// The bundle-import path in internal/workflow/transport/http/workflow.go has
// restored the stored State for this reason for a while; this is the same rule
// on the path the editor actually writes through.
//
// A nil map means the field was absent (or explicitly null, which decodes the
// same way and reads the same: "I am not telling you about state"). An empty
// object is how a caller says it means it, and clears the cursor.
func carryRuntimeColumns(src, oldSrc storage.Source) storage.Source {
	if src.State == nil {
		src.State = oldSrc.State
	}
	return src
}

func (h *SourceHandler) DeleteSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	if err := h.checkActiveWorkflows(ctx, id); err != nil {
		h.JsonError(w, "Cannot delete source: "+err.Error()+". Please stop the workflow first.", http.StatusConflict)
		return
	}

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
						// Both errors were discarded, and the delete then
						// returned 204. A stop that failed left the engine
						// running against a source about to be deleted, and an
						// update that failed left the workflow marked active
						// while pointing at nothing — which the next worker sync
						// tries to start and cannot.
						if err := h.Registry.StopEngine(ctx, wf.ID); err != nil {
							h.JsonError(w, "cannot delete this source: workflow "+wf.Name+
								" is running on it and could not be stopped: "+err.Error(),
								http.StatusConflict)
							return
						}
						wf.Active = false
						wf.Status = "Stopped"
						if err := h.Storage.UpdateWorkflow(ctx, wf); err != nil {
							h.JsonError(w, "cannot delete this source: workflow "+wf.Name+
								" was stopped but could not be recorded as stopped: "+err.Error(),
								http.StatusInternalServerError)
							return
						}
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

func (h *SourceHandler) TriggerSnapshot(w http.ResponseWriter, r *http.Request) {
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
func (h *SourceHandler) ListWorkflowsReferencingSource(w http.ResponseWriter, r *http.Request) {
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

func (h *SourceHandler) TestSource(w http.ResponseWriter, r *http.Request) {
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

func (h *SourceHandler) DiscoverDatabases(w http.ResponseWriter, r *http.Request) {
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

func (h *SourceHandler) DiscoverTables(w http.ResponseWriter, r *http.Request) {
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

func (h *SourceHandler) DiscoverSourceColumns(w http.ResponseWriter, r *http.Request) {
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
func (h *SourceHandler) DiscoverReplication(w http.ResponseWriter, r *http.Request) {
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

func (h *SourceHandler) SampleSourceTable(w http.ResponseWriter, r *http.Request) {
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

func (h *SourceHandler) QuerySource(w http.ResponseWriter, r *http.Request) {
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

func (h *SourceHandler) ProxyFetch(w http.ResponseWriter, r *http.Request) {
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

func (h *SourceHandler) ListWebhookRequests(w http.ResponseWriter, r *http.Request) {
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

func (h *SourceHandler) ReplayWebhookRequest(w http.ResponseWriter, r *http.Request) {
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

// checkCDCFlipForQueryTargets refuses an update that switches CDC on for a
// source a running workflow queries rather than streams.
//
// checkActiveWorkflows above protects a source held in a source node's RefID.
// That is only one of the ways a workflow names one: a db_lookup holds its
// source in the node config under sourceId, and a batch_sql source holds its
// database in source_id. Both of those are query targets, and the engine
// refuses to query a CDC source -- so switching the flag on breaks the workflow
// on its next message, from an edit that looks unrelated to it.
//
// Only active workflows. Nothing is running against a stopped one, and the
// validation panel will report it before it starts again.
func (h *SourceHandler) checkCDCFlipForQueryTargets(ctx context.Context, oldSrc, newSrc storage.Source) error {
	// Only a transition into CDC is a new break. Leaving it on, or turning it
	// off, cannot take away something that was working.
	if !hermod.SourceUsesCDC(newSrc.Config) || hermod.SourceUsesCDC(oldSrc.Config) {
		return nil
	}
	if hermod.SourceAllowsDirectQueries(newSrc.Type, newSrc.Config) {
		return nil
	}

	wfs, _, err := h.Storage.ListWorkflows(ctx, storage.CommonFilter{})
	if err != nil {
		return err
	}

	// Resolves a source node's ref to the database a batch_sql source borrows.
	// Memoised: every workflow is walked, and installs share sources between
	// them, so the uncached form is a read per source node per workflow.
	delegates := map[string]string{}
	delegateOf := func(id string) string {
		if d, ok := delegates[id]; ok {
			return d
		}
		delegate := ""
		if src, err := h.Storage.GetSource(ctx, id); err == nil && src.Type == "batch_sql" {
			delegate = src.Config["source_id"]
		}
		delegates[id] = delegate
		return delegate
	}

	for _, wf := range wfs {
		if !wf.Active {
			continue
		}
		if storage.WorkflowQueriesSource(wf, newSrc.ID, delegateOf) {
			return fmt.Errorf("workflow %q runs queries against this source, which needs CDC off; stop that workflow or point it at a different source first", wf.Name)
		}
	}
	return nil
}

func (h *SourceHandler) checkActiveWorkflows(ctx context.Context, sourceID string) error {
	wfs, _, err := h.Storage.ListWorkflows(ctx, storage.CommonFilter{})
	if err != nil {
		return err
	}
	for _, wf := range wfs {
		if !wf.Active {
			continue
		}
		for _, node := range wf.Nodes {
			if node.Type == "source" && node.RefID == sourceID {
				return fmt.Errorf("source is used by active workflow %q", wf.Name)
			}
		}
	}
	return nil
}
