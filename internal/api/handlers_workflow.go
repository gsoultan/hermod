package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/governance"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/message"
)

func (s *Server) registerWorkflowRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workflows", s.listWorkflows)
	mux.HandleFunc("GET /api/workflows/{id}", s.getWorkflow)
	mux.HandleFunc("PATCH /api/workflows/{id}/status", s.updateWorkflowStatus)
	mux.HandleFunc("GET /api/workflows/{id}/report", s.getWorkflowComplianceReport)
	mux.Handle("POST /api/workflows", s.editorOnly(s.createWorkflow))
	mux.Handle("PUT /api/workflows/{id}", s.editorOnly(s.updateWorkflow))
	mux.Handle("DELETE /api/workflows/{id}", s.editorOnly(s.deleteWorkflow))
	mux.Handle("POST /api/workflows/{id}/toggle", s.editorOnly(s.toggleWorkflow))
	mux.Handle("POST /api/workflows/{id}/drain", s.editorOnly(s.drainWorkflowDLQ))
	mux.Handle("POST /api/workflows/{id}/rebuild", s.editorOnly(s.rebuildWorkflow))
	mux.Handle("POST /api/workflows/test", s.editorOnly(s.testWorkflow))
	mux.Handle("POST /api/transformations/test", s.editorOnly(s.testTransformation))
	mux.HandleFunc("GET /api/workflows/{id}/traces/", s.getMessageTrace)
	mux.HandleFunc("GET /api/workflows/{id}/traces", s.listMessageTraces)
	mux.HandleFunc("GET /api/workflows/{id}/versions", s.listWorkflowVersions)
	mux.HandleFunc("GET /api/workflows/{id}/versions/{version}", s.getWorkflowVersion)
	mux.Handle("POST /api/workflows/{id}/rollback/{version}", s.editorOnly(s.rollbackWorkflow))
	mux.HandleFunc("GET /api/workflows/pii-stats", s.getPIIStats)
	mux.Handle("POST /api/ai/analyze-error", s.editorOnly(s.handleAIAnalyzeError))
	mux.Handle("POST /api/ai/analyze-schema", s.editorOnly(s.handleAIAnalyzeSchema))
	mux.Handle("POST /api/ai/generate-workflow", s.editorOnly(s.handleAIGenerateWorkflow))
	mux.Handle("POST /api/ai/copilot", s.editorOnly(s.handleAICopilot))
	mux.Handle("POST /api/ai/suggest-mapping", s.editorOnly(s.handleAISuggestMapping))
	mux.HandleFunc("POST /api/workflows/{id}/nodes/{node_id}/test", s.runNodeUnitTests)
	mux.HandleFunc("GET /api/ws/live", s.handleLiveMessagesWS)

	// Workspaces
	mux.HandleFunc("GET /api/workspaces", s.listWorkspaces)
	mux.Handle("POST /api/workspaces", s.editorOnly(s.createWorkspace))
	mux.Handle("DELETE /api/workspaces/{id}", s.editorOnly(s.deleteWorkspace))

	// Batch Operations
	mux.Handle("POST /api/workflows/batch/toggle", s.editorOnly(s.batchToggleWorkflows))
	mux.Handle("POST /api/workflows/batch/delete", s.editorOnly(s.batchDeleteWorkflows))
}

func (s *Server) batchToggleWorkflows(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs    []string `json:"ids"`
		Active bool     `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	results := make(map[string]string)
	for _, id := range req.IDs {
		wf, err := s.storage.GetWorkflow(r.Context(), id)
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
			if err := s.registry.StartWorkflow(id, wf); err != nil && !strings.Contains(err.Error(), "already running") {
				results[id] = "Failed to start: " + err.Error()
				continue
			}
		} else {
			wf.Status = "Stopped"
			_ = s.registry.StopEngine(id)
		}

		if err := s.storage.UpdateWorkflow(r.Context(), wf); err != nil {
			results[id] = "Failed to update storage: " + err.Error()
		} else {
			results[id] = "OK"
			action := "STOP"
			if req.Active {
				action = "START"
			}
			s.recordAuditLog(r, "INFO", "Batch workflow "+wf.Name+" "+action+"ed", action, wf.ID, "", "", nil)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func (s *Server) batchDeleteWorkflows(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	results := make(map[string]string)
	for _, id := range req.IDs {
		if err := s.storage.DeleteWorkflow(r.Context(), id); err != nil {
			results[id] = "Error: " + err.Error()
		} else {
			_ = s.registry.StopEngine(id)
			results[id] = "OK"
			s.recordAuditLog(r, "INFO", "Batch deleted workflow "+id, "DELETE", id, "", "", nil)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func (s *Server) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	wss, err := s.storage.ListWorkspaces(r.Context())
	if err != nil {
		s.jsonError(w, "Failed to list workspaces: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(wss)
}

func (s *Server) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var ws storage.Workspace
	if err := json.NewDecoder(r.Body).Decode(&ws); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if ws.Name == "" {
		s.jsonError(w, "Workspace name is required", http.StatusBadRequest)
		return
	}
	if err := s.storage.CreateWorkspace(r.Context(), ws); err != nil {
		s.jsonError(w, "Failed to create workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(ws)
}

func (s *Server) deleteWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.storage.DeleteWorkspace(r.Context(), id); err != nil {
		s.jsonError(w, "Failed to delete workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAICopilot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	result, err := s.ai.GenerateLogic(ctx, req.Prompt)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "AI copilot generated logic", "AI_COPILOT", "", "", "", nil)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *Server) handleAIAnalyzeError(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkflowID string `json:"workflow_id"`
		NodeID     string `json:"node_id"`
		Error      string `json:"error"`
		Sample     any    `json:"sample,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	suggestion, err := s.ai.AnalyzeError(ctx, req.WorkflowID, req.NodeID, req.Error, req.Sample)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "AI analyzed error for workflow "+req.WorkflowID, "AI_ANALYZE", req.WorkflowID, "", "", map[string]string{"node_id": req.NodeID})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(suggestion)
}

func (s *Server) handleAIAnalyzeSchema(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkflowID string         `json:"workflow_id"`
		OldSchema  map[string]any `json:"old_schema"`
		NewSchema  map[string]any `json:"new_schema"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	res, err := s.ai.AnalyzeSchemaChange(ctx, req.OldSchema, req.NewSchema, req.WorkflowID)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "AI analyzed schema change for workflow "+req.WorkflowID, "AI_ANALYZE_SCHEMA", req.WorkflowID, "", "", nil)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (s *Server) handleAISuggestMapping(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SourceFields []string `json:"source_fields"`
		TargetFields []string `json:"target_fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.SourceFields) > 1000 || len(req.TargetFields) > 1000 {
		s.jsonError(w, "Too many fields for mapping suggestion", http.StatusBadRequest)
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

	s.recordAuditLog(r, "INFO", "AI suggested field mapping", "AI_SUGGEST_MAPPING", "", "", "", map[string]int{
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

func (s *Server) handleAIGenerateWorkflow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	wf, err := s.ai.GenerateWorkflow(ctx, req.Prompt)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "AI generated workflow", "AI_GENERATE_WORKFLOW", "", "", "", nil)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(wf)
}

func (s *Server) getPIIStats(w http.ResponseWriter, r *http.Request) {
	stats := s.registry.GetPIIStats()
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

func (s *Server) runNodeUnitTests(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	nodeID := r.PathValue("node_id")

	wf, err := s.storage.GetWorkflow(r.Context(), id)
	if err != nil {
		s.jsonError(w, "Workflow not found", http.StatusNotFound)
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
		s.jsonError(w, "Node not found", http.StatusNotFound)
		return
	}

	if len(targetNode.UnitTests) == 0 {
		s.jsonError(w, "No unit tests defined for this node", http.StatusBadRequest)
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

		res, err := s.registry.TestTransformationPipeline(r.Context(), []storage.Transformation{trans}, msg)
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

func (s *Server) updateWorkflowStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.jsonError(w, "missing workflow id", http.StatusBadRequest)
		return
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.storage.UpdateWorkflowStatus(r.Context(), id, req.Status); err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) listWorkflows(w http.ResponseWriter, r *http.Request) {
	filter := s.parseCommonFilter(r)
	filter.WorkspaceID = r.URL.Query().Get("workspace_id")
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
		if errors.Is(err, storage.ErrNotFound) {
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

	// Quota Enforcement: Check MaxWorkflows
	if wf.WorkspaceID != "" {
		ws, err := s.storage.GetWorkspace(r.Context(), wf.WorkspaceID)
		if err == nil && ws.MaxWorkflows > 0 {
			workflows, _, err := s.storage.ListWorkflows(r.Context(), storage.CommonFilter{
				WorkspaceID: wf.WorkspaceID,
			})
			if err == nil && len(workflows) >= ws.MaxWorkflows {
				s.jsonError(w, fmt.Sprintf("Workspace quota exceeded: Maximum %d workflows allowed", ws.MaxWorkflows), http.StatusForbidden)
				return
			}
		}
	}

	if err := s.storage.CreateWorkflow(r.Context(), wf); err != nil {
		s.jsonError(w, "Failed to create workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "Created workflow "+wf.Name, "CREATE", wf.ID, "", "", wf)

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

	// Get current version count to determine next version
	versions, _ := s.storage.ListWorkflowVersions(r.Context(), id)
	nextVersion := 1
	if len(versions) > 0 {
		nextVersion = versions[0].Version + 1
	}

	if err := s.storage.UpdateWorkflow(r.Context(), wf); err != nil {
		s.jsonError(w, "Failed to update workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Create a new version
	user, _ := r.Context().Value(userContextKey).(*storage.User)
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
	_ = s.storage.CreateWorkflowVersion(r.Context(), version)

	s.recordAuditLog(r, "INFO", "Updated workflow "+wf.Name, "UPDATE", wf.ID, "", "", wf)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(wf)
}

func (s *Server) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.storage.DeleteWorkflow(r.Context(), id); err != nil {
		s.jsonError(w, "Failed to delete workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditLog(r, "INFO", "Deleted workflow "+id, "DELETE", id, "", "", nil)

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) toggleWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wf, err := s.storage.GetWorkflow(r.Context(), id)
	if err != nil {
		s.jsonError(w, "Workflow not found", http.StatusNotFound)
		return
	}

	action := "STOP"
	if wf.Active {
		wf.Active = false
		wf.Status = "Stopped"
		_ = s.registry.StopEngine(id)
	} else {
		// Quota Enforcement: Check resource limits before starting
		if wf.WorkspaceID != "" {
			ws, err := s.storage.GetWorkspace(r.Context(), wf.WorkspaceID)
			if err == nil {
				// 1. Check MaxWorkflows (Active)
				// Note: createWorkflow already checks total workflows, but we might want to limit active ones too.
				// For now, let's focus on CPU/Memory/Throughput as requested.

				activeWorkflows, _, err := s.storage.ListWorkflows(r.Context(), storage.CommonFilter{
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
						s.jsonError(w, fmt.Sprintf("Workspace CPU quota exceeded: %f requested, %f available", wf.CPURequest, ws.MaxCPU-currentCPU), http.StatusForbidden)
						return
					}
					if ws.MaxMemory > 0 && currentMem+wf.MemoryRequest > ws.MaxMemory {
						s.jsonError(w, fmt.Sprintf("Workspace Memory quota exceeded: %f requested, %f available", wf.MemoryRequest, ws.MaxMemory-currentMem), http.StatusForbidden)
						return
					}
					if ws.MaxThroughput > 0 && currentThroughput+wf.ThroughputRequest > ws.MaxThroughput {
						s.jsonError(w, fmt.Sprintf("Workspace Throughput quota exceeded: %d requested, %d available", wf.ThroughputRequest, ws.MaxThroughput-currentThroughput), http.StatusForbidden)
						return
					}
				}
			}
		}

		wf.Active = true
		wf.Status = "Active"
		action = "START"
		if err := s.registry.StartWorkflow(id, wf); err != nil && !strings.Contains(err.Error(), "already running") {
			s.jsonError(w, "Failed to start workflow: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if err := s.storage.UpdateWorkflow(r.Context(), wf); err != nil {
		s.jsonError(w, "Failed to update workflow: "+err.Error(), http.StatusInternalServerError)
		return
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
		Workflow storage.Workflow `json:"workflow"`
		Message  map[string]any   `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "Failed to decode request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	populateMessageFromMap(msg, req.Message)

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
		Message        map[string]any         `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "Failed to decode request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	populateMessageFromMap(msg, req.Message)

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
	_ = json.NewEncoder(w).Encode(res[0])
}

func (s *Server) getMessageTrace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	messageID := r.URL.Query().Get("message_id")

	// Fallback to path extraction if not in query
	if messageID == "" {
		prefix := fmt.Sprintf("/api/workflows/%s/traces/", id)
		messageID = strings.TrimPrefix(r.URL.Path, prefix)
	}

	if messageID == "" {
		s.jsonError(w, "Message ID is required", http.StatusBadRequest)
		return
	}

	// RBAC: Check access to the workflow's VHost
	wf, err := s.storage.GetWorkflow(r.Context(), id)
	if err == nil {
		role, vhosts := s.getRoleAndVHosts(r)
		if role != "" && role != storage.RoleAdministrator {
			if !s.hasVHostAccess(wf.VHost, vhosts) {
				s.jsonError(w, "Forbidden", http.StatusForbidden)
				return
			}
		}
	}

	trace, err := s.logStorage.GetMessageTrace(r.Context(), id, messageID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.jsonError(w, "Trace not found", http.StatusNotFound)
		} else {
			s.jsonError(w, "Failed to get trace: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(trace)
}

func (s *Server) listMessageTraces(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// RBAC: Check access to the workflow's VHost
	wf, err := s.storage.GetWorkflow(r.Context(), id)
	if err == nil {
		role, vhosts := s.getRoleAndVHosts(r)
		if role != "" && role != storage.RoleAdministrator {
			if !s.hasVHostAccess(wf.VHost, vhosts) {
				s.jsonError(w, "Forbidden", http.StatusForbidden)
				return
			}
		}
	}

	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil {
			limit = val
		}
	}

	traces, err := s.logStorage.ListMessageTraces(r.Context(), id, limit)
	if err != nil {
		s.jsonError(w, "Failed to list message traces: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(traces)
}

func (s *Server) listWorkflowVersions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	versions, err := s.storage.ListWorkflowVersions(r.Context(), id)
	if err != nil {
		s.jsonError(w, "Failed to list workflow versions: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(versions)
}

func (s *Server) getWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	versionStr := r.PathValue("version")
	version, _ := strconv.Atoi(versionStr)

	v, err := s.storage.GetWorkflowVersion(r.Context(), id, version)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.jsonError(w, "Version not found", http.StatusNotFound)
		} else {
			s.jsonError(w, "Failed to get version: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) getWorkflowComplianceReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Workflow ID is required", http.StatusBadRequest)
		return
	}

	format := r.URL.Query().Get("format")
	reportService := governance.NewReportService(s.storage, s.registry.GetDQScorer())

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

func (s *Server) rollbackWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	versionStr := r.PathValue("version")
	version, _ := strconv.Atoi(versionStr)

	v, err := s.storage.GetWorkflowVersion(r.Context(), id, version)
	if err != nil {
		s.jsonError(w, "Failed to find version to rollback: "+err.Error(), http.StatusNotFound)
		return
	}

	// Restore workflow from version
	var wf storage.Workflow
	if err := json.Unmarshal([]byte(v.Config), &wf); err != nil {
		s.jsonError(w, "Failed to parse version config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	wf.ID = id
	wf.Nodes = v.Nodes
	wf.Edges = v.Edges
	wf.TraceRetention = v.TraceRetention
	wf.AuditRetention = v.AuditRetention

	if err := s.storage.UpdateWorkflow(r.Context(), wf); err != nil {
		s.jsonError(w, "Failed to restore workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Create a new version for the rollback action itself
	user, _ := r.Context().Value(userContextKey).(*storage.User)
	username := "System"
	if user != nil {
		username = user.Username
	}

	// Get current version count
	versions, _ := s.storage.ListWorkflowVersions(r.Context(), id)
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
	_ = s.storage.CreateWorkflowVersion(r.Context(), rollbackVersion)

	s.recordAuditLog(r, "INFO", fmt.Sprintf("Rolled back workflow %s to version %d", id, version), "ROLLBACK", id, "", "", wf)

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
