package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/governance"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer/security"

	"github.com/gsoultan/hermod/pkg/engine/telemetry"
)

// validateWorkflow performs lightweight server-side validation for workflow configuration.
// Keeps UX-first by failing fast with clear messages.
func (h *WorkflowHandler) validateWorkflow(ctx context.Context, wf storage.Workflow) error {
	issues := h.ValidateWorkflow(ctx, wf)
	for _, issue := range issues {
		if issue.Severity == "error" {
			return fmt.Errorf("%s (Recommendation: %s)", issue.Message, issue.Recommendation)
		}
	}
	return nil
}

func (h *WorkflowHandler) RegisterWorkflowRoutes(mux *http.ServeMux) {
	// Register more specific routes first to avoid potential shadowing.
	mux.Handle("GET /api/workflows/{export_id}/export", h.EditorOnly(http.HandlerFunc(h.ExportWorkflow)))
	mux.Handle("POST /api/workflows/import", h.EditorOnly(http.HandlerFunc(h.ImportWorkflow)))

	mux.HandleFunc("GET /api/workflows", h.ListWorkflows)
	mux.HandleFunc("GET /api/workflows/{id}", h.GetWorkflow)
	mux.HandleFunc("PATCH /api/workflows/{id}/status", h.UpdateWorkflowStatus)
	mux.HandleFunc("PATCH /api/workflows/{id}/stats", h.UpdateWorkflowStats)
	mux.HandleFunc("GET /api/workflows/{id}/report", h.GetWorkflowComplianceReport)
	mux.HandleFunc("GET /api/workflows/{id}/health", h.GetWorkflowHealth)
	mux.Handle("POST /api/workflows", h.EditorOnly(http.HandlerFunc(h.CreateWorkflow)))
	mux.Handle("PUT /api/workflows/{id}", h.EditorOnly(http.HandlerFunc(h.UpdateWorkflow)))
	mux.Handle("DELETE /api/workflows/{id}", h.EditorOnly(http.HandlerFunc(h.DeleteWorkflow)))
	mux.Handle("POST /api/workflows/{id}/toggle", h.EditorOnly(http.HandlerFunc(h.ToggleWorkflow)))
	mux.Handle("POST /api/workflows/{id}/drain", h.EditorOnly(http.HandlerFunc(h.DrainWorkflowDLQ)))
	mux.Handle("POST /api/workflows/{id}/rebuild", h.EditorOnly(http.HandlerFunc(h.RebuildWorkflow)))
	mux.Handle("POST /api/workflows/{id}/test", h.EditorOnly(http.HandlerFunc(h.TestWorkflowByID)))
	mux.Handle("POST /api/workflows/test", h.EditorOnly(http.HandlerFunc(h.TestWorkflow)))
	mux.Handle("POST /api/transformations/test", h.EditorOnly(http.HandlerFunc(h.TestTransformation)))
	mux.Handle("POST /api/transformations/detect-decryption", h.EditorOnly(http.HandlerFunc(h.DetectDecryptionSettings)))
	mux.HandleFunc("GET /api/workflows/{id}/traces/", h.GetMessageTrace)
	mux.HandleFunc("GET /api/workflows/{id}/traces", h.ListMessageTraces)
	mux.HandleFunc("GET /api/workflows/{id}/versions", h.ListWorkflowVersions)
	mux.HandleFunc("GET /api/workflows/{id}/versions/{version}", h.GetWorkflowVersion)
	mux.HandleFunc("GET /api/workflows/{id}/validate", h.HandleValidateWorkflow)
	mux.Handle("POST /api/workflows/{id}/rollback/{version}", h.EditorOnly(http.HandlerFunc(h.RollbackWorkflow)))
	mux.HandleFunc("GET /api/workflows/pii-stats", h.GetPIIStats)
	mux.Handle("POST /api/ai/analyze-error", h.EditorOnly(http.HandlerFunc(h.HandleAIAnalyzeError)))
	mux.Handle("POST /api/ai/analyze-workflow/{id}", h.EditorOnly(http.HandlerFunc(h.HandleAIAnalyzeWorkflow)))
	mux.Handle("POST /api/ai/analyze-schema", h.EditorOnly(http.HandlerFunc(h.HandleAIAnalyzeSchema)))
	mux.Handle("POST /api/ai/generate-workflow", h.EditorOnly(http.HandlerFunc(h.HandleAIGenerateWorkflow)))
	mux.Handle("POST /api/ai/copilot", h.EditorOnly(http.HandlerFunc(h.HandleAICopilot)))
	mux.Handle("POST /api/ai/suggest-mapping", h.EditorOnly(http.HandlerFunc(h.HandleAISuggestMapping)))
	mux.HandleFunc("POST /api/workflows/{id}/nodes/{node_id}/test", h.RunNodeUnitTests)

	// Workspaces
	mux.HandleFunc("GET /api/workspaces", h.ListWorkspaces)
	mux.Handle("POST /api/workspaces", h.EditorOnly(http.HandlerFunc(h.CreateWorkspace)))
	mux.Handle("PUT /api/workspaces/{id}", h.EditorOnly(http.HandlerFunc(h.UpdateWorkspace)))
	mux.Handle("DELETE /api/workspaces/{id}", h.EditorOnly(http.HandlerFunc(h.DeleteWorkspace)))

	// Batch Operations
	mux.Handle("POST /api/workflows/batch/toggle", h.EditorOnly(http.HandlerFunc(h.BatchToggleWorkflows)))
	mux.Handle("POST /api/workflows/batch/delete", h.EditorOnly(http.HandlerFunc(h.BatchDeleteWorkflows)))
	mux.Handle("POST /api/workflows/batch/workspace", h.EditorOnly(http.HandlerFunc(h.BatchAssignWorkspace)))
}

func (h *WorkflowHandler) BatchToggleWorkflows(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs    []string `json:"ids"`
		Active bool     `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	results := make(map[string]string)
	for _, id := range req.IDs {
		wf, err := h.Storage.GetWorkflow(r.Context(), id)
		if err != nil {
			results[id] = "Error: " + err.Error()
			continue
		}

		if wf.Active == req.Active {
			results[id] = "No change"
			continue
		}

		wf.Active = req.Active
		if wf.Active {
			wf.Status = "Active"
			// Update DB first to avoid race with worker sync loop
			if err := h.Storage.UpdateWorkflow(r.Context(), wf); err != nil {
				results[id] = "Failed to update storage: " + err.Error()
				continue
			}
			if err := h.Registry.StartWorkflow(id, wf); err != nil && !strings.Contains(err.Error(), "already running") {
				// Rollback
				wf.Active = false
				wf.Status = "Error: " + err.Error()
				_ = h.Storage.UpdateWorkflow(r.Context(), wf)
				results[id] = "Failed to start: " + err.Error()
				continue
			}
		} else {
			wf.Active = false
			wf.Status = "Stopped"
			// Update DB first
			if err := h.Storage.UpdateWorkflow(r.Context(), wf); err != nil {
				results[id] = "Failed to update storage: " + err.Error()
				continue
			}
			_ = h.Registry.StopEngine(r.Context(), id)
		}

		results[id] = "OK"
		action := "STOP"
		if req.Active {
			action = "START"
		}
		h.RecordAuditLog(r, "INFO", "Batch workflow "+wf.Name+" "+action+"ed", action, wf.ID, "", "", nil)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func (h *WorkflowHandler) BatchDeleteWorkflows(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	results := make(map[string]string)
	for _, id := range req.IDs {
		if err := h.Storage.DeleteWorkflow(r.Context(), id); err != nil {
			results[id] = "Error: " + err.Error()
		} else {
			_ = h.Registry.StopEngine(r.Context(), id)
			// See DeleteWorkflow for both of these: traces are only reachable
			// through their workflow, and Prometheus keeps a series forever
			// once seen.
			h.Registry.DeleteWorkflowTraces(r.Context(), id)
			telemetry.ForgetWorkflow(id)
			results[id] = "OK"
			h.RecordAuditLog(r, "INFO", "Batch deleted workflow "+id, "DELETE", id, "", "", nil)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func (h *WorkflowHandler) ListWorkspaces(w http.ResponseWriter, r *http.Request) {
	wss, err := h.Storage.ListWorkspaces(r.Context())
	if err != nil {
		h.JsonError(w, "Failed to list workspaces: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(wss)
}

func (h *WorkflowHandler) CreateWorkspace(w http.ResponseWriter, r *http.Request) {
	var ws storage.Workspace
	if err := json.NewDecoder(r.Body).Decode(&ws); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if ws.Name == "" {
		h.JsonError(w, "Workspace name is required", http.StatusBadRequest)
		return
	}
	// Mint the identity here rather than leaving it to the backend, the way
	// CreateWorkflow does. Each storage backend used to generate its own id
	// internally, so the row the caller got back said `"id": ""` and nothing
	// that created a workspace could then reference it.
	if ws.ID == "" {
		ws.ID = uuid.New().String()
	}
	if ws.CreatedAt.IsZero() {
		ws.CreatedAt = time.Now()
	}
	if err := h.Storage.CreateWorkspace(r.Context(), ws); err != nil {
		h.JsonError(w, "Failed to create workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.RecordAuditLog(r, "INFO", "Created workspace "+ws.Name, "CREATE", ws.ID, "", "", ws)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(ws)
}

// UpdateWorkspace rewrites a workspace's name, description and quotas.
// Without it the four quota numbers were write-once — set blind in the create
// modal, never displayed again — and correcting one meant deleting the
// workspace, which took its members' assignments with it.
func (h *WorkflowHandler) UpdateWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var ws storage.Workspace
	if err := json.NewDecoder(r.Body).Decode(&ws); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	// The path owns the identity; a body that disagrees must not move members.
	ws.ID = id
	if ws.Name == "" {
		h.JsonError(w, "Workspace name is required", http.StatusBadRequest)
		return
	}
	if err := h.Storage.UpdateWorkspace(r.Context(), ws); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Workspace not found", http.StatusNotFound)
			return
		}
		h.JsonError(w, "Failed to update workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.RecordAuditLog(r, "INFO", "Updated workspace "+ws.Name, "UPDATE", ws.ID, "", "", ws)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ws)
}

func (h *WorkflowHandler) DeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Un-assign the members *before* dropping the row. Deleting first would
	// leave workflows, sources and sinks pointing at an id that no longer
	// resolves: the list renders a raw UUID, no workspace filter matches them,
	// and their quota checks silently stop applying.
	cleared, err := h.Storage.ClearWorkspaceAssignments(r.Context(), id)
	if err != nil {
		h.JsonError(w, "Failed to clear workspace members: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.Storage.DeleteWorkspace(r.Context(), id); err != nil {
		h.JsonError(w, "Failed to delete workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.RecordAuditLog(r, "INFO",
		fmt.Sprintf("Deleted workspace %s, un-assigning %d member(s)", id, cleared),
		"DELETE", id, "", "", nil)
	w.WriteHeader(http.StatusNoContent)
}

// quotaRefusal is an admission decision the caller reports as 403. Anything
// else checkWorkspaceAdmission returns is a failure to *reach* a decision,
// which is a 500 — the two must not collapse into one status, or a database
// blip reads to the user as "your workspace is full".
type quotaRefusal struct{ reason string }

func (e quotaRefusal) Error() string { return e.reason }

func refuse(format string, args ...any) error {
	return quotaRefusal{reason: fmt.Sprintf(format, args...)}
}

// writeAdmissionError maps an admission outcome onto a status code.
func (h *WorkflowHandler) writeAdmissionError(w http.ResponseWriter, err error) {
	var refusal quotaRefusal
	if errors.As(err, &refusal) {
		h.JsonError(w, refusal.reason, http.StatusForbidden)
		return
	}
	h.JsonError(w, "Failed to check workspace quota: "+err.Error(), http.StatusInternalServerError)
}

// checkWorkspaceAdmission answers whether wf may occupy its target workspace.
//
// It is the one place the max_workflows quota is decided, because the quota
// used to live only in CreateWorkflow: PUT /api/workflows/{id} — the request
// the editor's Save button sends, and the only path the UI offered for putting
// a workflow in a workspace — never consulted it, so a max_workflows=1
// workspace happily accepted three.
//
// prev is the workflow's current workspace. Admission is only charged when the
// workspace actually changes, so a workflow already inside a full workspace
// stays editable; leaving a workspace (target == "") is never refused.
//
// It fails closed. An earlier version swallowed storage errors so a blip could
// not block a save, which meant any failure to read the workspace or its
// members admitted the workflow without a check — a quota that stops being
// enforced under load is not a quota.
func (h *WorkflowHandler) checkWorkspaceAdmission(ctx context.Context, wf storage.Workflow, prev string) error {
	target := wf.WorkspaceID
	if target == "" || target == prev {
		return nil
	}

	ws, err := h.Storage.GetWorkspace(ctx, target)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return refuse("workspace %s does not exist", target)
		}
		return fmt.Errorf("reading workspace %s: %w", target, err)
	}

	members, _, err := h.Storage.ListWorkflows(ctx, storage.CommonFilter{WorkspaceID: target})
	if err != nil {
		return fmt.Errorf("listing members of workspace %s: %w", target, err)
	}

	if ws.MaxWorkflows > 0 {
		count := 0
		for _, m := range members {
			if m.ID != wf.ID { // an update re-counts itself otherwise
				count++
			}
		}
		if count >= ws.MaxWorkflows {
			return refuse("workspace quota exceeded: maximum %d workflows allowed in %q",
				ws.MaxWorkflows, ws.Name)
		}
	}

	return checkWorkspaceResourceQuota(ws, wf, members)
}

// checkWorkspaceResourceQuota charges CPU, memory and throughput against the
// workspace's *running* workflows, which is why ToggleWorkflow applies the same
// limits at start. Moving an already-active workflow in enters that pool
// without ever passing through Toggle, so the check belongs here too.
func checkWorkspaceResourceQuota(ws storage.Workspace, wf storage.Workflow, members []storage.Workflow) error {
	if !wf.Active {
		return nil
	}
	var cpu, mem float64
	var throughput int
	for _, m := range members {
		if m.Active && m.ID != wf.ID {
			cpu += m.CPURequest
			mem += m.MemoryRequest
			throughput += m.ThroughputRequest
		}
	}
	if ws.MaxCPU > 0 && cpu+wf.CPURequest > ws.MaxCPU {
		return refuse("workspace CPU quota exceeded: %g requested, %g available",
			wf.CPURequest, ws.MaxCPU-cpu)
	}
	if ws.MaxMemory > 0 && mem+wf.MemoryRequest > ws.MaxMemory {
		return refuse("workspace memory quota exceeded: %g requested, %g available",
			wf.MemoryRequest, ws.MaxMemory-mem)
	}
	if ws.MaxThroughput > 0 && throughput+wf.ThroughputRequest > ws.MaxThroughput {
		return refuse("workspace throughput quota exceeded: %d requested, %d available",
			wf.ThroughputRequest, ws.MaxThroughput-throughput)
	}
	return nil
}

// assignOneWorkspace moves a single workflow and reports the outcome as the
// string the batch response carries for that id.
//
// Admission is charged per workflow rather than once for the batch: each
// admitted workflow consumes a slot, so three workflows into a workspace with
// room for two must admit two and refuse the third.
func (h *WorkflowHandler) assignOneWorkspace(ctx context.Context, id, workspaceID string) string {
	wf, err := h.Storage.GetWorkflow(ctx, id)
	if err != nil {
		return "Error: " + err.Error()
	}
	if wf.WorkspaceID == workspaceID {
		return "No change"
	}

	prev := wf.WorkspaceID
	wf.WorkspaceID = workspaceID
	if err := h.checkWorkspaceAdmission(ctx, wf, prev); err != nil {
		return "Error: " + err.Error()
	}
	if err := h.Storage.UpdateWorkflow(ctx, wf); err != nil {
		return "Error: " + err.Error()
	}
	return "OK"
}

// BatchAssignWorkspace moves the selected workflows into a workspace, or out of
// one when workspace_id is empty. This is the action the Workflows list was
// missing: it already showed a Workspace column and offered no way to set it,
// so the only route in was five clicks deep in the editor's settings drawer.
func (h *WorkflowHandler) BatchAssignWorkspace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs         []string `json:"ids"`
		WorkspaceID string   `json:"workspace_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Reject an unknown target once rather than per id, so a typo does not
	// report itself as N separate quota failures.
	if req.WorkspaceID != "" {
		if _, err := h.Storage.GetWorkspace(r.Context(), req.WorkspaceID); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				h.JsonError(w, "Workspace not found", http.StatusNotFound)
				return
			}
			h.JsonError(w, "Failed to read workspace: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	results := make(map[string]string, len(req.IDs))
	for _, id := range req.IDs {
		results[id] = h.assignOneWorkspace(r.Context(), id, req.WorkspaceID)
	}

	h.RecordAuditLog(r, "INFO",
		fmt.Sprintf("Batch assigned %d workflow(s) to workspace %q", len(req.IDs), req.WorkspaceID),
		"UPDATE", "", "", "", results)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func (h *WorkflowHandler) HandleAICopilot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	result, err := h.AI.GenerateLogic(ctx, req.Prompt)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "AI copilot generated logic", "AI_COPILOT", "", "", "", nil)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (h *WorkflowHandler) HandleAIAnalyzeError(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkflowID string `json:"workflow_id"`
		NodeID     string `json:"node_id"`
		Error      string `json:"error"`
		Sample     any    `json:"sample,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	suggestion, err := h.AI.AnalyzeError(ctx, req.WorkflowID, req.NodeID, req.Error, req.Sample)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "AI analyzed error for workflow "+req.WorkflowID, "AI_ANALYZE", req.WorkflowID, "", "", map[string]string{"node_id": req.NodeID})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(suggestion)
}

func (h *WorkflowHandler) HandleAIAnalyzeWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	wf, err := h.Storage.GetWorkflow(ctx, id)
	if err != nil {
		h.JsonError(w, "Workflow not found", http.StatusNotFound)
		return
	}

	// RBAC check
	role, vhosts := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		if !h.HasVHostAccess(wf.VHost, vhosts) {
			h.JsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	aiCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	recommendation, err := h.AI.AnalyzeWorkflow(aiCtx, wf)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "AI analyzed workflow "+wf.Name, "AI_ANALYZE_WORKFLOW", wf.ID, "", "", nil)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(recommendation)
}

func (h *WorkflowHandler) HandleAIAnalyzeSchema(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkflowID string         `json:"workflow_id"`
		OldSchema  map[string]any `json:"old_schema"`
		NewSchema  map[string]any `json:"new_schema"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	res, err := h.AI.AnalyzeSchemaChange(ctx, req.OldSchema, req.NewSchema, req.WorkflowID)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "AI analyzed schema change for workflow "+req.WorkflowID, "AI_ANALYZE_SCHEMA", req.WorkflowID, "", "", nil)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (h *WorkflowHandler) HandleAISuggestMapping(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SourceFields []string `json:"source_fields"`
		TargetFields []string `json:"target_fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.SourceFields) > 1000 || len(req.TargetFields) > 1000 {
		h.JsonError(w, "Too many fields for mapping suggestion", http.StatusBadRequest)
		return
	}

	// Heuristic mapping by name similarity
	norm := func(s string) string {
		s = strings.ToLower(s)
		s = strings.ReplaceAll(s, "_", "")
		s = strings.ReplaceAll(s, "-", "")
		s = strings.ReplaceAll(s, " ", "")
		return s
	}
	lcs := func(a, b string) int {
		na, nb := len(a), len(b)
		best := 0
		for i := range na {
			for j := range nb {
				k := 0
				for i+k < na && j+k < nb && a[i+k] == b[j+k] {
					k++
				}
				if k > best {
					best = k
				}
			}
		}
		return best
	}
	suggestions := map[string]string{}
	scores := map[string]float64{}
	for _, tgt := range req.TargetFields {
		tn := norm(tgt)
		bestScore := 0.0
		bestSrc := ""
		for _, src := range req.SourceFields {
			sn := norm(src)
			score := 0.0
			if sn == tn {
				score = 1.0
			} else if strings.Contains(tn, sn) || strings.Contains(sn, tn) {
				score = 0.8
			} else {
				lc := lcs(sn, tn)
				maxLen := float64(len(sn))
				if float64(len(tn)) > maxLen {
					maxLen = float64(len(tn))
				}
				if maxLen > 0 {
					score = float64(lc) / maxLen
				}
			}
			if score > bestScore {
				bestScore = score
				bestSrc = src
			}
		}
		if bestSrc != "" && bestScore >= 0.5 {
			suggestions[tgt] = bestSrc
			scores[tgt] = bestScore
		}
	}

	h.RecordAuditLog(r, "INFO", "AI suggested field mapping", "AI_SUGGEST_MAPPING", "", "", "", map[string]int{
		"source_count": len(req.SourceFields),
		"target_count": len(req.TargetFields),
		"suggestions":  len(suggestions),
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"suggestions": suggestions,
		"scores":      scores,
	})
}

func (h *WorkflowHandler) HandleAIGenerateWorkflow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	wf, err := h.AI.GenerateWorkflow(ctx, req.Prompt)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "AI generated workflow", "AI_GENERATE_WORKFLOW", "", "", "", nil)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(wf)
}

func (h *WorkflowHandler) GetPIIStats(w http.ResponseWriter, r *http.Request) {
	stats := h.Registry.GetPIIStats()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(stats)
}

type UnitTestResult struct {
	Name    string         `json:"name"`
	Passed  bool           `json:"passed"`
	Actual  map[string]any `json:"actual"`
	Error   string         `json:"error,omitempty"`
	Elapsed time.Duration  `json:"elapsed"`
}

func (h *WorkflowHandler) RunNodeUnitTests(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	nodeID := r.PathValue("node_id")

	wf, err := h.Storage.GetWorkflow(r.Context(), id)
	if err != nil {
		h.JsonError(w, "Workflow not found", http.StatusNotFound)
		return
	}

	var targetNode *storage.WorkflowNode
	for _, node := range wf.Nodes {
		if node.ID == nodeID {
			targetNode = &node
			break
		}
	}

	if targetNode == nil {
		h.JsonError(w, "Node not found", http.StatusNotFound)
		return
	}

	if len(targetNode.UnitTests) == 0 {
		h.JsonError(w, "No unit tests defined for this node", http.StatusBadRequest)
		return
	}

	results := make([]UnitTestResult, 0, len(targetNode.UnitTests))

	// For each test, run it through the transformation pipeline (mocked for just this node)
	for _, ut := range targetNode.UnitTests {
		start := time.Now()
		msg := message.AcquireMessage()
		message.PopulateFromMap(msg, ut.Input)

		// Create a temporary transformation from the node config
		trans := storage.Transformation{
			Type:   targetNode.Type,
			Config: make(map[string]any),
		}
		if targetNode.Config != nil {
			maps.Copy(trans.Config, targetNode.Config)
		}
		// If it's a subType like "mapping", the engine needs that
		if subType, ok := targetNode.Config["transType"].(string); ok {
			trans.Type = subType
		}

		res, err := h.Registry.TestTransformationPipeline(r.Context(), []storage.Transformation{trans}, msg)
		message.ReleaseMessage(msg)

		var actual map[string]any
		passed := false
		var errStr string

		if err != nil {
			errStr = err.Error()
		} else if len(res) == 0 || res[0] == nil {
			errStr = "Message was filtered out"
		} else {
			actual = res[0].Data()
			// Simple deep equal check for expected output
			passed = true
			for k, expected := range ut.ExpectedOutput {
				if actualVal, ok := actual[k]; !ok || fmt.Sprint(actualVal) != fmt.Sprint(expected) {
					passed = false
					break
				}
			}
		}

		// Ensure all messages returned by the pipeline are released
		for _, m := range res {
			if m != nil {
				m.Release()
			}
		}

		results = append(results, UnitTestResult{
			Name:    ut.Name,
			Passed:  passed,
			Actual:  actual,
			Error:   errStr,
			Elapsed: time.Since(start),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func (h *WorkflowHandler) UpdateWorkflowStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		h.JsonError(w, "missing workflow id", http.StatusBadRequest)
		return
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.Storage.UpdateWorkflowStatus(r.Context(), id, req.Status); err != nil {
		h.JsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *WorkflowHandler) GetWorkflowHealth(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		h.JsonError(w, "missing workflow id", http.StatusBadRequest)
		return
	}

	health, ok := h.Registry.GetWorkflowHealth(id)
	if !ok {
		// If not in registry, try to return basic health from storage
		wf, err := h.Storage.GetWorkflow(r.Context(), id)
		if err != nil {
			h.JsonError(w, "workflow not found", http.StatusNotFound)
			return
		}
		health = storage.WorkflowHealth{
			WorkflowID: id,
			Status:     "stopped",
			Processed:  wf.TotalProcessed,
			Errors:     wf.TotalErrors,
			Lag:        wf.TotalLag,
		}
		if wf.Active {
			health.Status = "starting"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(health)
}

func (h *WorkflowHandler) UpdateWorkflowStats(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		h.JsonError(w, "missing workflow id", http.StatusBadRequest)
		return
	}

	var req struct {
		Processed uint64 `json:"processed"`
		Errors    uint64 `json:"errors"`
		Lag       uint64 `json:"lag"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.Storage.UpdateWorkflowStats(r.Context(), id, req.Processed, req.Errors, req.Lag); err != nil {
		h.JsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *WorkflowHandler) ListWorkflows(w http.ResponseWriter, r *http.Request) {
	filter := h.ParseCommonFilter(r)
	role, vhosts := h.GetRoleAndVHosts(r)

	if filter.VHost != "" && role != storage.RoleAdministrator {
		if !h.HasVHostAccess(filter.VHost, vhosts) {
			h.JsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	wfs, total, err := h.Storage.ListWorkflows(r.Context(), filter)
	if err != nil {
		h.JsonError(w, "Failed to list workflows: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if role != "" && role != storage.RoleAdministrator {
		filtered := []storage.Workflow{}
		for _, wf := range wfs {
			if h.HasVHostAccess(wf.VHost, vhosts) {
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

func (h *WorkflowHandler) GetWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wf, err := h.Storage.GetWorkflow(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Workflow not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to get workflow: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	role, vhosts := h.GetRoleAndVHosts(r)
	if role != "" && role != storage.RoleAdministrator {
		if !h.HasVHostAccess(wf.VHost, vhosts) {
			h.JsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(wf)
}

func (h *WorkflowHandler) CreateWorkflow(w http.ResponseWriter, r *http.Request) {
	var wf storage.Workflow
	if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if wf.Name == "" {
		h.JsonError(w, "Workflow name is mandatory", http.StatusBadRequest)
		return
	}

	// A brand-new workflow has no previous workspace, so every non-empty
	// target is an admission.
	if err := h.checkWorkspaceAdmission(r.Context(), wf, ""); err != nil {
		h.writeAdmissionError(w, err)
		return
	}

	if err := h.validateWorkflow(r.Context(), wf); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if wf.ID == "" {
		wf.ID = uuid.New().String()
	}

	if err := h.Storage.CreateWorkflow(r.Context(), wf); err != nil {
		h.JsonError(w, "Failed to create workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "Created workflow "+wf.Name, "CREATE", wf.ID, "", "", wf)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(wf)
}

func (h *WorkflowHandler) UpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var wf storage.Workflow
	if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	wf.ID = id

	// The stored row is the only source of truth for which workspace this
	// workflow is leaving, and admission is charged on the change rather than
	// on every save — otherwise a workflow inside a full workspace could never
	// be edited again.
	prevWorkspaceID := ""
	if existing, err := h.Storage.GetWorkflow(r.Context(), id); err == nil {
		prevWorkspaceID = existing.WorkspaceID
	}
	if err := h.checkWorkspaceAdmission(r.Context(), wf, prevWorkspaceID); err != nil {
		h.writeAdmissionError(w, err)
		return
	}

	// Get current version count to determine next version
	versions, _ := h.Storage.ListWorkflowVersions(r.Context(), id)
	nextVersion := 1
	if len(versions) > 0 {
		nextVersion = versions[0].Version + 1
	}

	if err := h.validateWorkflow(r.Context(), wf); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.Storage.UpdateWorkflow(r.Context(), wf); err != nil {
		h.JsonError(w, "Failed to update workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Create a new version
	user, _ := r.Context().Value(handlers.UserContextKey).(*storage.User)
	username := "System"
	if user != nil {
		username = user.Username
	}

	// Extract config excluding nodes and edges
	wfCopy := wf
	wfCopy.Nodes = nil
	wfCopy.Edges = nil
	configJSON, _ := json.Marshal(wfCopy)

	version := storage.WorkflowVersion{
		ID:             uuid.New().String(),
		WorkflowID:     id,
		Version:        nextVersion,
		Nodes:          wf.Nodes,
		Edges:          wf.Edges,
		TraceRetention: wf.TraceRetention,
		AuditRetention: wf.AuditRetention,
		Config:         string(configJSON),
		CreatedAt:      time.Now(),
		CreatedBy:      username,
		Message:        "Auto-saved on update",
	}
	_ = h.Storage.CreateWorkflowVersion(r.Context(), version)

	h.RecordAuditLog(r, "INFO", "Updated workflow "+wf.Name, "UPDATE", wf.ID, "", "", wf)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(wf)
}

func (h *WorkflowHandler) DeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Storage.DeleteWorkflow(r.Context(), id); err != nil {
		h.JsonError(w, "Failed to delete workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Stop the engine before forgetting its metrics, so a still-running engine
	// cannot immediately recreate the series we are about to drop. The batch
	// delete path already did this; the single delete left the engine running
	// until a worker sync happened to notice the row was gone.
	if h.Registry != nil {
		_ = h.Registry.StopEngine(r.Context(), id)
		// Traces outlive their workflow otherwise. DeleteWorkflow removes one
		// row, and a trace is only reachable through its workflow, so every
		// workflow ever deleted left an unbounded pile of unreachable rows in
		// the largest table Hermod owns.
		h.Registry.DeleteWorkflowTraces(r.Context(), id)
	}
	// Prometheus never reclaims a series on its own, so a deleted workflow's
	// metrics would otherwise be exported for the life of the process.
	telemetry.ForgetWorkflow(id)

	h.RecordAuditLog(r, "INFO", "Deleted workflow "+id, "DELETE", id, "", "", nil)

	w.WriteHeader(http.StatusNoContent)
}

func (h *WorkflowHandler) ToggleWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wf, err := h.Storage.GetWorkflow(r.Context(), id)
	if err != nil {
		h.JsonError(w, "Workflow not found", http.StatusNotFound)
		return
	}

	action := "STOP"
	if wf.Active {
		wf.Active = false
		wf.Status = "Stopped"
		_ = h.Registry.StopEngine(r.Context(), id)
	} else {
		// Validation check before starting
		if err := h.validateWorkflow(r.Context(), wf); err != nil {
			h.JsonError(w, "Cannot start invalid workflow: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Quota Enforcement: Check resource limits before starting
		if wf.WorkspaceID != "" {
			ws, err := h.Storage.GetWorkspace(r.Context(), wf.WorkspaceID)
			if err == nil {
				// 1. Check MaxWorkflows (Active)
				// Note: createWorkflow already checks total workflows, but we might want to limit active ones too.
				// For now, let's focus on CPU/Memory/Throughput as requested.

				activeWorkflows, _, err := h.Storage.ListWorkflows(r.Context(), storage.CommonFilter{
					WorkspaceID: wf.WorkspaceID,
				})
				if err == nil {
					var currentCPU, currentMem float64
					var currentThroughput int
					for _, awf := range activeWorkflows {
						if awf.Active && awf.ID != wf.ID {
							currentCPU += awf.CPURequest
							currentMem += awf.MemoryRequest
							currentThroughput += awf.ThroughputRequest
						}
					}

					if ws.MaxCPU > 0 && currentCPU+wf.CPURequest > ws.MaxCPU {
						h.JsonError(w, fmt.Sprintf("Workspace CPU quota exceeded: %f requested, %f available", wf.CPURequest, ws.MaxCPU-currentCPU), http.StatusForbidden)
						return
					}
					if ws.MaxMemory > 0 && currentMem+wf.MemoryRequest > ws.MaxMemory {
						h.JsonError(w, fmt.Sprintf("Workspace Memory quota exceeded: %f requested, %f available", wf.MemoryRequest, ws.MaxMemory-currentMem), http.StatusForbidden)
						return
					}
					if ws.MaxThroughput > 0 && currentThroughput+wf.ThroughputRequest > ws.MaxThroughput {
						h.JsonError(w, fmt.Sprintf("Workspace Throughput quota exceeded: %d requested, %d available", wf.ThroughputRequest, ws.MaxThroughput-currentThroughput), http.StatusForbidden)
						return
					}
				}
			}
		}

		wf.Active = true
		wf.Status = "Active"
		if err := h.Storage.UpdateWorkflow(r.Context(), wf); err != nil {
			h.JsonError(w, "Failed to update workflow: "+err.Error(), http.StatusInternalServerError)
			return
		}

		action = "START"
		if err := h.Registry.StartWorkflow(id, wf); err != nil && !strings.Contains(err.Error(), "already running") {
			// Rollback Active status if start failed
			wf.Active = false
			wf.Status = "Error: " + err.Error()
			_ = h.Storage.UpdateWorkflow(r.Context(), wf)
			h.JsonError(w, "Failed to start workflow: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	h.RecordAuditLog(r, "INFO", "Workflow "+wf.Name+" "+action+"ed", action, wf.ID, "", "", nil)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(wf)
}

func (h *WorkflowHandler) DrainWorkflowDLQ(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Registry.DrainWorkflowDLQ(r.Context(), id); err != nil {
		h.JsonError(w, "Failed to drain DLQ: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "Drained DLQ for workflow "+id, "drain_dlq", id, "", "", nil)

	w.WriteHeader(http.StatusAccepted)
}

func (h *WorkflowHandler) RebuildWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		FromOffset int64 `json:"from_offset"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Hour)
		defer cancel()
		if err := h.Registry.RebuildWorkflow(ctx, id, req.FromOffset); err != nil {
			h.Registry.GetLogger().Error("RebuildWorkflow failed", "workflow_id", id, "error", err)
		}
	}()

	h.RecordAuditLog(r, "INFO", "Started projection rebuilding for workflow "+id, "rebuild", id, "", "", nil)

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "rebuild started"})
}

func (h *WorkflowHandler) TestWorkflow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Workflow storage.Workflow `json:"workflow"`
		Message  map[string]any   `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, "Failed to decode request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	message.PopulateFromMap(msg, req.Message)

	steps, err := h.Registry.TestWorkflow(r.Context(), req.Workflow, msg)
	if err != nil {
		h.JsonError(w, "Failed to test workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(steps)
}

func (h *WorkflowHandler) TestWorkflowByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wf, err := h.Storage.GetWorkflow(r.Context(), id)
	if err != nil {
		h.JsonError(w, "Failed to load workflow: "+err.Error(), http.StatusNotFound)
		return
	}

	var req struct {
		Message map[string]any `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, "Failed to decode request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	message.PopulateFromMap(msg, req.Message)

	steps, err := h.Registry.TestWorkflow(r.Context(), wf, msg)
	if err != nil {
		h.JsonError(w, "Failed to test workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(steps)
}

func (h *WorkflowHandler) TestTransformation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Transformation storage.Transformation `json:"transformation"`
		Message        map[string]any         `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, "Failed to decode request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	message.PopulateFromMap(msg, req.Message)

	transType := req.Transformation.Type
	if transType == "transformation" {
		if tt, ok := req.Transformation.Config["transType"].(string); ok && tt != "" {
			transType = tt
		}
	}

	// A routing node is not a transformation, and the editor offers Test for
	// both. switch, condition and router are registered as node executors, so
	// running them through the transformer registry answered "unknown
	// transformation type" for nodes that work in a live workflow. Their result
	// is the branch taken, not a changed message, so it is reported separately.
	if h.Registry.IsBranchPreviewable(transType) {
		out, branch, err := h.Registry.PreviewBranch(r.Context(), transType, req.Transformation.Config, msg)
		if err != nil {
			h.JsonError(w, "Failed to test transformation: "+err.Error(), http.StatusInternalServerError)
			return
		}
		result := map[string]any{}
		if len(out) > 0 && out[0] != nil {
			result = out[0].ToMap()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"branch": branch, "result": result})
		return
	}

	// A node that is not a transformer and cannot be previewed gets told so.
	// Falling through would run it against the transformer registry and answer
	// "no transformer is registered under that name", which describes a typo --
	// and "stateful" is not a typo, it is a node whose whole job is to mutate
	// state a preview must not touch.
	if !h.Registry.CanTransform(transType) && h.Registry.IsWorkflowNode(transType) {
		h.JsonError(w,
			fmt.Sprintf("%q is a workflow node, not a transformation. It runs when the "+
				"workflow runs; there is nothing to preview here because previewing it "+
				"would have effects beyond this panel.", transType),
			http.StatusBadRequest)
		return
	}

	res, err := h.Registry.TestTransformationPipeline(r.Context(), []storage.Transformation{{
		Type:   transType,
		Config: req.Transformation.Config,
	}}, msg)
	if err != nil {
		h.JsonError(w, "Failed to test transformation: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Ensure all messages in the results slice are released after encoding
	defer func() {
		for _, m := range res {
			if dm, ok := m.(*message.DefaultMessage); ok {
				message.ReleaseMessage(dm)
			}
		}
	}()

	if len(res) == 0 {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "Filtered", "filtered": true})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if len(res) == 1 {
		if res[0] == nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "Filtered", "filtered": true})
			return
		}
		_ = json.NewEncoder(w).Encode(res[0].ToMap())
	} else {
		results := make([]map[string]any, len(res))
		for i, m := range res {
			if m != nil {
				results[i] = m.ToMap()
			}
		}
		_ = json.NewEncoder(w).Encode(results)
	}
}

func (h *WorkflowHandler) GetMessageTrace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	messageID := r.URL.Query().Get("message_id")

	// Fallback to path extraction if not in query
	if messageID == "" {
		prefix := fmt.Sprintf("/api/workflows/%s/traces/", id)
		messageID = strings.TrimPrefix(r.URL.Path, prefix)
	}

	if messageID == "" {
		h.JsonError(w, "Message ID is required", http.StatusBadRequest)
		return
	}

	// RBAC: Check access to the workflow's VHost
	wf, err := h.Storage.GetWorkflow(r.Context(), id)
	if err == nil {
		role, vhosts := h.GetRoleAndVHosts(r)
		if role != "" && role != storage.RoleAdministrator {
			if !h.HasVHostAccess(wf.VHost, vhosts) {
				h.JsonError(w, "Forbidden", http.StatusForbidden)
				return
			}
		}
	}

	trace, err := h.LogStorage.GetMessageTrace(r.Context(), id, messageID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Trace not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to get trace: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(trace)
}

func (h *WorkflowHandler) ListMessageTraces(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// RBAC: Check access to the workflow's VHost
	wf, err := h.Storage.GetWorkflow(r.Context(), id)
	if err == nil {
		role, vhosts := h.GetRoleAndVHosts(r)
		if role != "" && role != storage.RoleAdministrator {
			if !h.HasVHostAccess(wf.VHost, vhosts) {
				h.JsonError(w, "Forbidden", http.StatusForbidden)
				return
			}
		}
	}

	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 {
			limit = val
		}
	}

	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if val, err := strconv.Atoi(o); err == nil && val > 0 {
			offset = val
		}
	} else if p := r.URL.Query().Get("page"); p != "" {
		// Support page-based paging as an alternative to a raw offset.
		if page, err := strconv.Atoi(p); err == nil && page > 1 {
			offset = (page - 1) * limit
		}
	}

	// A `before` cursor is the fast path: it turns the next page into an index
	// seek instead of reading and discarding everything ahead of it. limit and
	// offset stay supported so existing clients and bookmarked URLs keep
	// working.
	filter := storage.TraceFilter{Limit: limit, Offset: offset}
	if b := r.URL.Query().Get("before"); b != "" {
		if ts, err := time.Parse(time.RFC3339Nano, b); err == nil {
			filter.Before = ts
			filter.Offset = 0
		}
	}

	traces, err := h.LogStorage.ListMessageTraces(r.Context(), id, filter)
	if err != nil {
		h.JsonError(w, "Failed to list message traces: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(traces)
}

func (h *WorkflowHandler) ListWorkflowVersions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	versions, err := h.Storage.ListWorkflowVersions(r.Context(), id)
	if err != nil {
		h.JsonError(w, "Failed to list workflow versions: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(versions)
}

func (h *WorkflowHandler) GetWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	versionStr := r.PathValue("version")
	version, _ := strconv.Atoi(versionStr)

	v, err := h.Storage.GetWorkflowVersion(r.Context(), id, version)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Version not found", http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to get version: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (h *WorkflowHandler) GetWorkflowComplianceReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Workflow ID is required", http.StatusBadRequest)
		return
	}

	format := r.URL.Query().Get("format")
	reportService := governance.NewReportService(h.Storage, h.Registry.GetDQScorer())

	if format == "pdf" || format == "md" {
		report, filename, err := reportService.GeneratePDFReport(r.Context(), id)
		if err != nil {
			http.Error(w, "Failed to generate report: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/markdown")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
		w.WriteHeader(http.StatusOK)
		w.Write(report)
		return
	}

	report, err := reportService.GenerateComplianceReport(r.Context(), id)
	if err != nil {
		http.Error(w, "Failed to generate report: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(report))
}

func (h *WorkflowHandler) RollbackWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	versionStr := r.PathValue("version")
	version, _ := strconv.Atoi(versionStr)

	v, err := h.Storage.GetWorkflowVersion(r.Context(), id, version)
	if err != nil {
		h.JsonError(w, "Failed to find version to rollback: "+err.Error(), http.StatusNotFound)
		return
	}

	// Restore workflow from version
	var wf storage.Workflow
	if err := json.Unmarshal([]byte(v.Config), &wf); err != nil {
		h.JsonError(w, "Failed to parse version config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	wf.ID = id
	wf.Nodes = v.Nodes
	wf.Edges = v.Edges
	wf.TraceRetention = v.TraceRetention
	wf.AuditRetention = v.AuditRetention

	if err := h.Storage.UpdateWorkflow(r.Context(), wf); err != nil {
		h.JsonError(w, "Failed to restore workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Create a new version for the rollback action itself
	user, _ := r.Context().Value(handlers.UserContextKey).(*storage.User)
	username := "System"
	if user != nil {
		username = user.Username
	}

	// Get current version count
	versions, _ := h.Storage.ListWorkflowVersions(r.Context(), id)
	nextVersion := 1
	if len(versions) > 0 {
		nextVersion = versions[0].Version + 1
	}

	rollbackVersion := storage.WorkflowVersion{
		ID:             uuid.New().String(),
		WorkflowID:     id,
		Version:        nextVersion,
		Nodes:          wf.Nodes,
		Edges:          wf.Edges,
		TraceRetention: wf.TraceRetention,
		AuditRetention: wf.AuditRetention,
		Config:         v.Config,
		CreatedAt:      time.Now(),
		CreatedBy:      username,
		Message:        fmt.Sprintf("Rolled back to version %d", version),
	}
	_ = h.Storage.CreateWorkflowVersion(r.Context(), rollbackVersion)

	h.RecordAuditLog(r, "INFO", fmt.Sprintf("Rolled back workflow %s to version %d", id, version), "ROLLBACK", id, "", "", wf)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(wf)
}

// exportRef is one dependency an export has to resolve, remembered together
// with the node that named it so a missing one can be reported usefully.
type exportRef struct {
	id     string
	nodeID string
}

// collectWorkflowRefs answers which sources and sinks a workflow depends on.
//
// A node's RefID is only one of the ways to name one. Scanning for `type ==
// "source"` alone missed every source a transformation looks up (db_lookup,
// execute_sql), and those exports imported into a workflow that started and
// then failed every message on a source that was not there. Order is the node
// order so that exporting the same workflow twice produces the same file.
func collectWorkflowRefs(wf storage.Workflow) (sources, sinks []exportRef) {
	seenSrc := map[string]bool{}
	seenSnk := map[string]bool{}

	addSource := func(id, nodeID string) {
		if id == "" || seenSrc[id] {
			return
		}
		seenSrc[id] = true
		sources = append(sources, exportRef{id: id, nodeID: nodeID})
	}
	addSink := func(id, nodeID string) {
		if id == "" || seenSnk[id] {
			return
		}
		seenSnk[id] = true
		sinks = append(sinks, exportRef{id: id, nodeID: nodeID})
	}

	for _, node := range wf.Nodes {
		switch node.Type {
		case "source":
			addSource(node.RefID, node.ID)
		case "sink":
			addSink(node.RefID, node.ID)
		}
		for _, key := range storage.NodeConfigSourceKeys {
			if id, ok := node.Config[key].(string); ok {
				addSource(id, node.ID)
			}
		}
	}

	addSink(wf.DeadLetterSinkID, "")
	return sources, sinks
}

// stripSourceRuntime removes the columns that describe a source's execution on
// *this* instance rather than its configuration: which worker holds it, what it
// is doing, the CDC cursor it has reached and the payload last sampled from it.
// A bundle is a description of a workflow, not of a running one.
func stripSourceRuntime(src storage.Source) storage.Source {
	src.Status = ""
	src.WorkerID = ""
	src.State = nil
	src.Sample = ""
	return src
}

func stripSinkRuntime(snk storage.Sink) storage.Sink {
	snk.Status = ""
	snk.WorkerID = ""
	return snk
}

func stripWorkflowRuntime(wf storage.Workflow) storage.Workflow {
	wf.Status = ""
	wf.WorkerID = ""
	wf.OwnerID = ""
	wf.LeaseUntil = nil
	wf.TotalProcessed = 0
	wf.TotalErrors = 0
	wf.TotalLag = 0
	wf.Nodes = stripCapturedSamples(wf.Nodes)
	return wf
}

// capturedSampleKeys are node config keys holding data the editor captured
// from a live source: `lastSample` is the row Test Connection sampled, and
// `testResult` a simulated message. They are rows out of someone's database,
// not configuration, so they stay behind for the same reason
// stripSourceRuntime drops a source's own sample.
var capturedSampleKeys = []string{"lastSample", "testResult"}

// stripCapturedSamples returns nodes without captured sample data, copying
// any config it changes rather than editing the map it was handed.
func stripCapturedSamples(nodes []storage.WorkflowNode) []storage.WorkflowNode {
	out := make([]storage.WorkflowNode, len(nodes))
	for i, node := range nodes {
		out[i] = node
		captured := slices.ContainsFunc(capturedSampleKeys, func(key string) bool {
			_, ok := node.Config[key]
			return ok
		})
		if !captured {
			continue
		}
		out[i].Config = maps.Clone(node.Config)
		for _, key := range capturedSampleKeys {
			delete(out[i].Config, key)
		}
	}
	return out
}

// exportFilenamePattern keeps a workflow name usable inside a
// Content-Disposition filename. Anything else — quotes, slashes, control bytes —
// becomes an underscore rather than a malformed header.
var exportFilenamePattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func exportFilename(name string) string {
	safe := exportFilenamePattern.ReplaceAllString(name, "_")
	safe = strings.Trim(safe, "._-")
	if safe == "" {
		safe = "workflow"
	}
	if len(safe) > 100 {
		safe = safe[:100]
	}
	return safe
}

func (h *WorkflowHandler) ExportWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("export_id")
	if id == "" {
		h.JsonError(w, "Workflow ID is required", http.StatusBadRequest)
		return
	}

	wf, err := h.Storage.GetWorkflow(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			h.JsonError(w, "Workflow not found in database: "+id, http.StatusNotFound)
		} else {
			h.JsonError(w, "Failed to retrieve workflow: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// Permission check (Role + VHost)
	role, vhosts := h.GetRoleAndVHosts(r)
	if role != "" && role != storage.RoleAdministrator {
		if !h.HasVHostAccess(wf.VHost, vhosts) {
			h.JsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	bundle := storage.WorkflowExportBundle{
		Workflow: stripWorkflowRuntime(wf),
	}

	sourceRefs, sinkRefs := collectWorkflowRefs(wf)

	// A batch_sql source delegates its connection to another source, so the
	// queue grows while it is walked. seen guards against a self-reference.
	seen := map[string]bool{}
	for i := 0; i < len(sourceRefs); i++ {
		ref := sourceRefs[i]
		if seen[ref.id] {
			continue
		}
		seen[ref.id] = true

		src, err := h.Storage.GetSource(r.Context(), ref.id)
		if errors.Is(err, storage.ErrNotFound) {
			bundle.MissingRefs = append(bundle.MissingRefs, storage.MissingRef{Kind: "source", ID: ref.id, NodeID: ref.nodeID})
			continue
		}
		if err != nil {
			// Not the same thing as a missing reference: the instance may well
			// have this source and the bundle would silently lose it.
			h.JsonError(w, "Failed to read source "+ref.id+" for export: "+err.Error(), http.StatusInternalServerError)
			return
		}

		bundle.Sources = append(bundle.Sources, stripSourceRuntime(src))
		if src.Type == "batch_sql" {
			if underlying := src.Config["source_id"]; underlying != "" && !seen[underlying] {
				sourceRefs = append(sourceRefs, exportRef{id: underlying, nodeID: ref.nodeID})
			}
		}
	}

	for _, ref := range sinkRefs {
		snk, err := h.Storage.GetSink(r.Context(), ref.id)
		if errors.Is(err, storage.ErrNotFound) {
			bundle.MissingRefs = append(bundle.MissingRefs, storage.MissingRef{Kind: "sink", ID: ref.id, NodeID: ref.nodeID})
			continue
		}
		if err != nil {
			h.JsonError(w, "Failed to read sink "+ref.id+" for export: "+err.Error(), http.StatusInternalServerError)
			return
		}
		bundle.Sinks = append(bundle.Sinks, stripSinkRuntime(snk))
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "workflow-"+exportFilename(wf.Name)+".json"))
	_ = json.NewEncoder(w).Encode(bundle)
}

func (h *WorkflowHandler) ImportWorkflow(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.JsonError(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var bundle storage.WorkflowExportBundle
	if err := json.Unmarshal(body, &bundle); err != nil || bundle.Workflow.ID == "" {
		// Try fallback to single workflow
		var wf storage.Workflow
		if err := json.Unmarshal(body, &wf); err != nil || wf.ID == "" {
			h.JsonError(w, "Invalid workflow or bundle format", http.StatusBadRequest)
			return
		}
		bundle.Workflow = wf
	}

	ctx := r.Context()
	role, vhosts := h.GetRoleAndVHosts(r)

	// Permission check. The workflow's vhost is not the only one in play: the
	// bundle's sources and sinks carry their own and are upserted by ID, so
	// checking only the workflow let an editor confined to one vhost hand in a
	// bundle that overwrote a connection belonging to another.
	if role != "" && role != storage.RoleAdministrator {
		vhost := bundle.Workflow.VHost
		if vhost == "" {
			vhost = "default"
		}
		if !h.HasVHostAccess(vhost, vhosts) {
			h.JsonError(w, "Forbidden: you do not have access to vhost "+vhost, http.StatusForbidden)
			return
		}
		for _, src := range bundle.Sources {
			if !h.HasVHostAccess(src.VHost, vhosts) {
				h.JsonError(w, "Forbidden: the bundle contains source "+src.ID+" in vhost "+src.VHost, http.StatusForbidden)
				return
			}
		}
		for _, snk := range bundle.Sinks {
			if !h.HasVHostAccess(snk.VHost, vhosts) {
				h.JsonError(w, "Forbidden: the bundle contains sink "+snk.ID+" in vhost "+snk.VHost, http.StatusForbidden)
				return
			}
		}
	}

	// 1. Upsert Sources.
	//
	// Every save below reports its failure. They used to run as
	// `_ = h.Storage.CreateSource(...)`, so an import whose dependencies all
	// failed still answered 201 Created and left a workflow pointing at
	// sources that were never written. Dependencies are saved before the
	// workflow so that a failure here stops short of writing one.
	for _, src := range bundle.Sources {
		existing, getErr := h.Storage.GetSource(ctx, src.ID)
		if getErr == nil {
			// Keep this instance's runtime columns. Writing the bundle's State
			// over an existing source rewinds or fast-forwards a live CDC
			// cursor, which loses or replays everything in between.
			src.Status = existing.Status
			src.WorkerID = existing.WorkerID
			src.State = existing.State
			src.Sample = existing.Sample
			if err := h.Storage.UpdateSource(ctx, src); err != nil {
				h.JsonError(w, "Failed to save source "+src.ID+": "+err.Error(), http.StatusInternalServerError)
				return
			}
			continue
		}
		if err := h.Storage.CreateSource(ctx, stripSourceRuntime(src)); err != nil {
			h.JsonError(w, "Failed to save source "+src.ID+": "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// 2. Upsert Sinks
	for _, snk := range bundle.Sinks {
		existing, getErr := h.Storage.GetSink(ctx, snk.ID)
		if getErr == nil {
			snk.Status = existing.Status
			snk.WorkerID = existing.WorkerID
			if err := h.Storage.UpdateSink(ctx, snk); err != nil {
				h.JsonError(w, "Failed to save sink "+snk.ID+": "+err.Error(), http.StatusInternalServerError)
				return
			}
			continue
		}
		if err := h.Storage.CreateSink(ctx, stripSinkRuntime(snk)); err != nil {
			h.JsonError(w, "Failed to save sink "+snk.ID+": "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// 3. Upsert Workflow
	// saveErr must be declared out here. Assigning to the `err` that the
	// create-vs-update check declares in its own init statement writes to a
	// variable scoped to that if/else, which dies at the closing brace — the
	// check below then read the function-scoped err from io.ReadAll, which is
	// always nil by this point. A workflow that failed to save was reported to
	// the caller as imported successfully.
	var isUpdate bool
	var saveErr error
	wf := bundle.Workflow
	if existing, getErr := h.Storage.GetWorkflow(ctx, wf.ID); getErr == nil {
		// Same rule as the sources: the lease, the owning worker and the
		// counters describe this instance's run, not the bundle's.
		wf.Status = existing.Status
		wf.WorkerID = existing.WorkerID
		wf.OwnerID = existing.OwnerID
		wf.LeaseUntil = existing.LeaseUntil
		wf.TotalProcessed = existing.TotalProcessed
		wf.TotalErrors = existing.TotalErrors
		wf.TotalLag = existing.TotalLag
		saveErr = h.Storage.UpdateWorkflow(ctx, wf)
		isUpdate = true
	} else {
		wf = stripWorkflowRuntime(wf)
		saveErr = h.Storage.CreateWorkflow(ctx, wf)
	}
	bundle.Workflow = wf

	if err := saveErr; err != nil {
		h.JsonError(w, "Failed to save workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	status := http.StatusCreated
	if isUpdate {
		status = http.StatusOK
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(bundle.Workflow)

	h.RecordAuditLog(r, "INFO", "Imported workflow "+bundle.Workflow.Name, "IMPORT", bundle.Workflow.ID, "", "", nil)
}

// DetectDecryptionSettings answers "how was this value encrypted?" for the
// decrypt node editor.
//
// The settings on that node interact — key format decides the key bytes,
// encoding decides the payload bytes, nonce length decides where the ciphertext
// starts, tag placement decides which end the tag is on, AAD decides whether
// authentication can succeed — so one wrong setting is indistinguishable from
// all of them wrong. Without this an operator matching an external system has
// to search by hand, which is how the node earned a reputation for not working.
//
// Editor-only, like the preview it sits beside. It grants no new capability:
// the caller supplies the key, so it could already decrypt anything this
// returns. The response carries the settings and a truncated preview, never the
// key and never a full plaintext.
func (h *WorkflowHandler) DetectDecryptionSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sample string `json:"sample"`
		Key    string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.JsonError(w, "Failed to decode request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	result := security.DetectDecryption(req.Sample, req.Key)

	w.Header().Set("Content-Type", "application/json")
	// The sample is a secret the caller already holds, but a cached copy of this
	// response in a proxy or a browser is a copy nobody asked for.
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(result)
}
