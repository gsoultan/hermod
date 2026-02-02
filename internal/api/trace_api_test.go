package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
)

type mockTraceStorage struct {
	storage.Storage
	traces map[string]storage.MessageTrace
}

func (m *mockTraceStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	return storage.Workflow{ID: id, VHost: "default"}, nil
}

func (m *mockTraceStorage) GetMessageTrace(ctx context.Context, workflowID, messageID string) (storage.MessageTrace, error) {
	if t, ok := m.traces[workflowID+":"+messageID]; ok {
		return t, nil
	}
	return storage.MessageTrace{}, storage.ErrNotFound
}

func (m *mockTraceStorage) ListMessageTraces(ctx context.Context, workflowID string, limit int) ([]storage.MessageTrace, error) {
	return nil, nil
}

func TestGetMessageTraceAPI(t *testing.T) {
	ms := &mockTraceStorage{
		traces: map[string]storage.MessageTrace{
			"wf1:msg1": {
				WorkflowID: "wf1",
				MessageID:  "msg1",
				Steps: []hermod.TraceStep{
					{NodeID: "node1", Timestamp: time.Now()},
				},
			},
			"wf1:msg/with/slashes": {
				WorkflowID: "wf1",
				MessageID:  "msg/with/slashes",
				Steps: []hermod.TraceStep{
					{NodeID: "node1", Timestamp: time.Now()},
				},
			},
		},
	}

	s := &Server{
		storage:    ms,
		logStorage: ms,
	}

	mux := http.NewServeMux()
	s.registerWorkflowRoutes(mux)

	tests := []struct {
		name       string
		url        string
		wantID     string
		wantStatus int
	}{
		{
			name:       "Standard ID",
			url:        "/api/workflows/wf1/traces/?message_id=msg1",
			wantID:     "msg1",
			wantStatus: http.StatusOK,
		},
		{
			name:       "ID in path",
			url:        "/api/workflows/wf1/traces/msg1",
			wantID:     "msg1",
			wantStatus: http.StatusOK,
		},
		{
			name:       "ID with slashes in path",
			url:        "/api/workflows/wf1/traces/msg/with/slashes",
			wantID:     "msg/with/slashes",
			wantStatus: http.StatusOK,
		},
		{
			name:       "ID with slashes in query",
			url:        "/api/workflows/wf1/traces/?message_id=msg%2Fwith%2Fslashes",
			wantID:     "msg/with/slashes",
			wantStatus: http.StatusOK,
		},
		{
			name:       "Not found",
			url:        "/api/workflows/wf1/traces/missing",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.url, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, w.Code)
			}

			if tt.wantStatus == http.StatusOK {
				var tr storage.MessageTrace
				if err := json.NewDecoder(w.Body).Decode(&tr); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if tr.MessageID != tt.wantID {
					t.Errorf("expected message ID %s, got %s", tt.wantID, tr.MessageID)
				}
			}
		})
	}
}
