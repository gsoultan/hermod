package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod/internal/api/handlers"
	"github.com/user/hermod/internal/storage"
)

func TestValidateWorkflow(t *testing.T) {
	h := &WorkflowHandler{Handler: &handlers.Handler{}} // Minimal handler for testing validation logic

	tests := []struct {
		name           string
		wf             storage.Workflow
		expectedIssues int
		expectError    bool
	}{
		{
			name:           "Empty workflow",
			wf:             storage.Workflow{},
			expectedIssues: 2, // No name, no nodes
			expectError:    true,
		},
		{
			name: "Source without RefID",
			wf: storage.Workflow{
				Name: "Test",
				Nodes: []storage.WorkflowNode{
					{ID: "node1", Type: "source"},
				},
			},
			expectedIssues: 3, // No RefID, no sink (warning), no outgoing (warning)
			expectError:    true,
		},
		{
			name: "Valid minimal workflow",
			wf: storage.Workflow{
				Name: "Test",
				Nodes: []storage.WorkflowNode{
					{ID: "src1", Type: "source", RefID: "src-config-id"},
					{ID: "snk1", Type: "sink", RefID: "snk-config-id"},
				},
				Edges: []storage.WorkflowEdge{
					{ID: "e1", SourceID: "src1", TargetID: "snk1"},
				},
			},
			expectedIssues: 0,
			expectError:    false,
		},
		{
			name: "Dangling node",
			wf: storage.Workflow{
				Name: "Test",
				Nodes: []storage.WorkflowNode{
					{ID: "src1", Type: "source", RefID: "src-config-id"},
					{ID: "trans1", Type: "transformer", RefID: "trans-id"},
					{ID: "snk1", Type: "sink", RefID: "snk-config-id"},
				},
				Edges: []storage.WorkflowEdge{
					{ID: "e1", SourceID: "src1", TargetID: "snk1"},
				},
			},
			expectedIssues: 2,     // transformer has no incoming, no outgoing
			expectError:    false, // They are warnings
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			issues := h.ValidateWorkflow(tc.wf)
			if len(issues) != tc.expectedIssues {
				t.Errorf("expected %d issues, got %d", tc.expectedIssues, len(issues))
				for _, issue := range issues {
					t.Logf("- %s: %s", issue.Severity, issue.Message)
				}
			}

			err := h.validateWorkflow(tc.wf)
			if tc.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

type mockStorageForValidation struct {
	storage.Storage
	wf *storage.Workflow
}

func (m *mockStorageForValidation) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	if m.wf != nil && m.wf.ID == id {
		return *m.wf, nil
	}
	return storage.Workflow{}, storage.ErrNotFound
}

func TestHandleValidateWorkflow(t *testing.T) {
	mock := &mockStorageForValidation{
		wf: &storage.Workflow{
			ID:   "wf123",
			Name: "Valid Workflow",
			Nodes: []storage.WorkflowNode{
				{ID: "src1", Type: "source", RefID: "src1"},
				{ID: "snk1", Type: "sink", RefID: "snk1"},
			},
			Edges: []storage.WorkflowEdge{
				{ID: "e1", SourceID: "src1", TargetID: "snk1"},
			},
		},
	}
	h := &WorkflowHandler{Handler: &handlers.Handler{Storage: mock}}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workflows/{id}/validate", h.HandleValidateWorkflow)

	req := httptest.NewRequest("GET", "/api/workflows/wf123/validate", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status OK, got %d: %s", rr.Code, rr.Body.String())
	}

	var issues []ValidationIssue
	if err := json.NewDecoder(rr.Body).Decode(&issues); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(issues) != 0 {
		t.Errorf("expected 0 issues, got %d", len(issues))
	}
}
