package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/governance"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/comm/message"
)

// validateWorkflow performs lightweight server-side validation for workflow configuration.
// Keeps UX-first by failing fast with clear messages. Extend as needed for more node types.
func validateWorkflow(wf storage.Workflow) error {
	for _, n := range wf.Nodes {
		// Support both explicit node type and legacy transType config
		nodeType := n.Type
		if nodeType == "" {
			if tt, ok := n.Config["transType"].(string); ok {
				nodeType = tt
			}
		}

		switch nodeType {
		case "foreach", "fanout":
			ap, _ := n.Config["arrayPath"].(string)
			if strings.TrimSpace(ap) == "" {
				return fmt.Errorf("node %s (foreach) requires non-empty config.arrayPath", n.ID)
			}
		}
	}
	return nil
}

func (h *Handler) RegisterWorkflowRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workflows", h.ListWorkflows)
	mux.HandleFunc("GET /api/workflows/{id}", h.GetWorkflow)
	mux.HandleFunc("PATCH /api/workflows/{id}/status", h.UpdateWorkflowStatus)
	mux.HandleFunc("GET /api/workflows/{id}/report", h.GetWorkflowComplianceReport)
	mux.Handle("POST /api/workflows", h.EditorOnly(h.CreateWorkflow))
	mux.Handle("PUT /api/workflows/{id}", h.EditorOnly(h.UpdateWorkflow))
	mux.Handle("DELETE /api/workflows/{id}", h.EditorOnly(h.DeleteWorkflow))
	mux.Handle("POST /api/workflows/{id}/toggle", h.EditorOnly(h.ToggleWorkflow))
	mux.Handle("POST /api/workflows/{id}/drain", h.EditorOnly(h.DrainWorkflowDLQ))
	mux.Handle("POST /api/workflows/{id}/rebuild", h.EditorOnly(h.RebuildWorkflow))
	mux.Handle("POST /api/workflows/test", h.EditorOnly(h.TestWorkflow))
	mux.Handle("POST /api/transformations/test", h.EditorOnly(h.TestTransformation))
	mux.HandleFunc("GET /api/workflows/{id}/traces/", h.GetMessageTrace)
	mux.HandleFunc("GET /api/workflows/{id}/traces", h.ListMessageTraces)
	mux.HandleFunc("GET /api/workflows/{id}/versions", h.ListWorkflowVersions)
	mux.HandleFunc("GET /api/workflows/{id}/versions/{version}", h.GetWorkflowVersion)
	mux.Handle("POST /api/workflows/{id}/rollback/{version}", h.EditorOnly(h.RollbackWorkflow))
	mux.HandleFunc("GET /api/workflows/pii-stats", h.GetPIIStats)
	mux.Handle("POST /api/ai/analyze-error", h.EditorOnly(h.HandleAIAnalyzeError))
	mux.Handle("POST /api/ai/analyze-schema", h.EditorOnly(h.HandleAIAnalyzeSchema))
	mux.Handle("POST /api/ai/generate-workflow", h.EditorOnly(h.HandleAIGenerateWorkflow))
	mux.Handle("POST /api/ai/copilot", h.EditorOnly(h.HandleAICopilot))
	mux.Handle("POST /api/ai/suggest-mapping", h.EditorOnly(h.HandleAISuggestMapping))
	mux.HandleFunc("POST /api/workflows/{id}/nodes/{node_id}/test", h.RunNodeUnitTests)
	mux.HandleFunc("GET /api/ws/live", h.HandleLiveMessagesWS)

	// Workspaces
	mux.HandleFunc("GET /api/workspaces", h.ListWorkspaces)
	mux.Handle("POST /api/workspaces", h.EditorOnly(h.CreateWorkspace))
	mux.Handle("DELETE /api/workspaces/{id}", h.EditorOnly(h.DeleteWorkspace))

	// Batch Operations
	mux.Handle("POST /api/workflows/batch/toggle", h.EditorOnly(h.BatchToggleWorkflows))
	mux.Handle("POST /api/workflows/batch/delete", h.EditorOnly(h.BatchDeleteWorkflows))
}

func (h *Handler) BatchToggleWorkflows(w http.ResponseWriter, r *http.Request) {
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
			_ = h.Registry.StopEngine(id)
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

func (h *Handler) BatchDeleteWorkflows(w http.ResponseWriter, r *http.Request) {
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
			_ = h.Registry.StopEngine(id)
			results[id] = "OK"
			h.RecordAuditLog(r, "INFO", "Batch deleted workflow "+id, "DELETE", id, "", "", nil)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func (h *Handler) ListWorkspaces(w http.ResponseWriter, r *http.Request) {
	wss, err := h.Storage.ListWorkspaces(r.Context())
	if err != nil {
		h.JsonError(w, "Failed to list workspaces: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(wss)
}

func (h *Handler) CreateWorkspace(w http.ResponseWriter, r *http.Request) {
	var ws storage.Workspace
	if err := json.NewDecoder(r.Body).Decode(&ws); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if ws.Name == "" {
		h.JsonError(w, "Workspace name is required", http.StatusBadRequest)
		return
	}
	if err := h.Storage.CreateWorkspace(r.Context(), ws); err != nil {
		h.JsonError(w, "Failed to create workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(ws)
}

func (h *Handler) DeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Storage.DeleteWorkspace(r.Context(), id); err != nil {
		h.JsonError(w, "Failed to delete workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) HandleAICopilot(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) HandleAIAnalyzeError(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) HandleAIAnalyzeSchema(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) HandleAISuggestMapping(w http.ResponseWriter, r *http.Request) {
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
		for i := 0; i < na; i++ {
			for j := 0; j < nb; j++ {
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

func (h *Handler) HandleAIGenerateWorkflow(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) GetPIIStats(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) RunNodeUnitTests(w http.ResponseWriter, r *http.Request) {
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
		populateMessageFromMap(msg, ut.Input)

		// Create a temporary transformation from the node config
		trans := storage.Transformation{
			Type:   targetNode.Type,
			Config: make(map[string]any),
		}
		if targetNode.Config != nil {
			for k, v := range targetNode.Config {
				trans.Config[k] = v
			}
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

func (h *Handler) UpdateWorkflowStatus(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) ListWorkflows(w http.ResponseWriter, r *http.Request) {
	filter := h.ParseCommonFilter(r)
	filter.WorkspaceID = r.URL.Query().Get("workspace_id")
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

func (h *Handler) GetWorkflow(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) CreateWorkflow(w http.ResponseWriter, r *http.Request) {
	var wf storage.Workflow
	if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if wf.Name == "" {
		h.JsonError(w, "Workflow name is mandatory", http.StatusBadRequest)
		return
	}

	// Quota Enforcement: Check MaxWorkflows
	if wf.WorkspaceID != "" {
		ws, err := h.Storage.GetWorkspace(r.Context(), wf.WorkspaceID)
		if err == nil && ws.MaxWorkflows > 0 {
			workflows, _, err := h.Storage.ListWorkflows(r.Context(), storage.CommonFilter{
				WorkspaceID: wf.WorkspaceID,
			})
			if err == nil && len(workflows) >= ws.MaxWorkflows {
				h.JsonError(w, fmt.Sprintf("Workspace quota exceeded: Maximum %d workflows allowed", ws.MaxWorkflows), http.StatusForbidden)
				return
			}
		}
	}

	if err := validateWorkflow(wf); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.Storage.CreateWorkflow(r.Context(), wf); err != nil {
		h.JsonError(w, "Failed to create workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "Created workflow "+wf.Name, "CREATE", wf.ID, "", "", wf)

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(wf)
}

func (h *Handler) UpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var wf storage.Workflow
	if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	wf.ID = id

	// Get current version count to determine next version
	versions, _ := h.Storage.ListWorkflowVersions(r.Context(), id)
	nextVersion := 1
	if len(versions) > 0 {
		nextVersion = versions[0].Version + 1
	}

	if err := validateWorkflow(wf); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.Storage.UpdateWorkflow(r.Context(), wf); err != nil {
		h.JsonError(w, "Failed to update workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Create a new version
	user, _ := r.Context().Value(UserContextKey).(*storage.User)
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

func (h *Handler) DeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Storage.DeleteWorkflow(r.Context(), id); err != nil {
		h.JsonError(w, "Failed to delete workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "Deleted workflow "+id, "DELETE", id, "", "", nil)

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ToggleWorkflow(w http.ResponseWriter, r *http.Request) {
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
		_ = h.Registry.StopEngine(id)
	} else {
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

func (h *Handler) DrainWorkflowDLQ(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Registry.DrainWorkflowDLQ(id); err != nil {
		h.JsonError(w, "Failed to drain DLQ: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.RecordAuditLog(r, "INFO", "Drained DLQ for workflow "+id, "drain_dlq", id, "", "", nil)

	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) RebuildWorkflow(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) TestWorkflow(w http.ResponseWriter, r *http.Request) {
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
	populateMessageFromMap(msg, req.Message)

	steps, err := h.Registry.TestWorkflow(r.Context(), req.Workflow, msg)
	if err != nil {
		h.JsonError(w, "Failed to test workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(steps)
}

func (h *Handler) TestTransformation(w http.ResponseWriter, r *http.Request) {
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
	populateMessageFromMap(msg, req.Message)

	res, err := h.Registry.TestTransformationPipeline(r.Context(), []storage.Transformation{req.Transformation}, msg)
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

	if len(res) == 0 || res[0] == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "Filtered", "filtered": true})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res[0])
}

func (h *Handler) GetMessageTrace(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) ListMessageTraces(w http.ResponseWriter, r *http.Request) {
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

	traces, err := h.LogStorage.ListMessageTraces(r.Context(), id, limit, offset)
	if err != nil {
		h.JsonError(w, "Failed to list message traces: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(traces)
}

func (h *Handler) ListWorkflowVersions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	versions, err := h.Storage.ListWorkflowVersions(r.Context(), id)
	if err != nil {
		h.JsonError(w, "Failed to list workflow versions: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(versions)
}

func (h *Handler) GetWorkflowVersion(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) GetWorkflowComplianceReport(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) RollbackWorkflow(w http.ResponseWriter, r *http.Request) {
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
	user, _ := r.Context().Value(UserContextKey).(*storage.User)
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

func populateMessageFromMap(msg hermod.Message, data map[string]any) {
	for k, v := range data {
		lower := strings.ToLower(k)
		switch lower {
		case "id":
			if id, ok := v.(string); ok && id != "" {
				if dm, ok := msg.(*message.DefaultMessage); ok {
					dm.SetID(id)
				}
			}
		case "operation", "op":
			if op, ok := v.(string); ok && op != "" {
				if dm, ok := msg.(*message.DefaultMessage); ok {
					dm.SetOperation(hermod.Operation(op))
				}
			}
		case "table":
			if t, ok := v.(string); ok && t != "" {
				if dm, ok := msg.(*message.DefaultMessage); ok {
					dm.SetTable(t)
				}
			}
		case "schema":
			if s, ok := v.(string); ok && s != "" {
				if dm, ok := msg.(*message.DefaultMessage); ok {
					dm.SetSchema(s)
				}
			}
		case "metadata":
			if md, ok := v.(map[string]any); ok {
				for mk, mv := range md {
					msg.SetMetadata(mk, fmt.Sprint(mv))
				}
			}
		case "before":
			if b, ok := v.(map[string]any); ok {
				jb, _ := json.Marshal(b)
				if dm, ok := msg.(*message.DefaultMessage); ok {
					dm.SetBefore(jb)
				}
			} else if s, ok := v.(string); ok {
				if dm, ok := msg.(*message.DefaultMessage); ok {
					dm.SetBefore([]byte(s))
				}
			}
		case "after":
			if a, ok := v.(map[string]any); ok {
				// For simulation purposes, we populate the root data with 'after' contents
				// so transformations work correctly on the record fields.
				for ak, av := range a {
					msg.SetData(ak, av)
				}
			} else if s, ok := v.(string); ok {
				var am map[string]any
				if err := json.Unmarshal([]byte(s), &am); err == nil {
					for ak, av := range am {
						msg.SetData(ak, av)
					}
				} else {
					if dm, ok := msg.(*message.DefaultMessage); ok {
						dm.SetAfter([]byte(s))
					}
				}
			}
		default:
			msg.SetData(k, v)
		}
	}
}

func (h *Handler) WakeUpWorkflow(ctx context.Context, resourceType string, path string) bool {
	// 1. Find the source with this path
	sources, _, err := h.Storage.ListSources(ctx, storage.CommonFilter{})
	if err != nil {
		return false
	}

	var sourceID string
	for _, src := range sources {
		if src.Type == resourceType && src.Config["path"] == path {
			sourceID = src.ID
			break
		}
	}

	if sourceID == "" {
		return false
	}

	// 2. Find workflows using this source
	workflows, _, err := h.Storage.ListWorkflows(ctx, storage.CommonFilter{})
	if err != nil {
		return false
	}

	wokeUp := false
	for _, wf := range workflows {
		if wf.Status != "Parked" {
			continue
		}

		for _, node := range wf.Nodes {
			if node.Type == "source" && node.RefID == sourceID {
				// Wake it up!
				wf.Status = ""
				_ = h.Storage.UpdateWorkflow(ctx, wf)
				wokeUp = true

				// Start it immediately in the local registry to minimize latency
				if h.Registry != nil {
					_ = h.Registry.StartWorkflow(wf.ID, wf)
				}
			}
		}
	}

	return wokeUp
}
