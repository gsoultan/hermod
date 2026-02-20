package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/user/hermod/internal/ai"
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/storage"
)

func withContextRole(r *http.Request, role storage.Role) *http.Request {
	u := &storage.User{Role: role}
	ctx := context.WithValue(r.Context(), userContextKey, u)
	return r.WithContext(ctx)
}

func withEditorContext(r *http.Request) *http.Request {
	return withContextRole(r, storage.RoleEditor)
}

type mockLogStorage struct {
	storage.Storage
}

func (m *mockLogStorage) CreateLog(ctx context.Context, l storage.Log) error           { return nil }
func (m *mockLogStorage) CreateAuditLog(ctx context.Context, a storage.AuditLog) error { return nil }

type mockAIStorage struct {
	storage.Storage
}

func (m *mockAIStorage) ListUsers(ctx context.Context, filter storage.CommonFilter) ([]storage.User, int, error) {
	return []storage.User{{ID: "1"}}, 1, nil
}

type mockLogger struct{}

func (m *mockLogger) Info(msg string, keysAndValues ...any)  {}
func (m *mockLogger) Error(msg string, keysAndValues ...any) {}
func (m *mockLogger) Debug(msg string, keysAndValues ...any) {}
func (m *mockLogger) Warn(msg string, keysAndValues ...any)  {}

func setupTestEnv(t *testing.T) {
	tempDir := t.TempDir()
	prev, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(prev) })
	os.Chdir(tempDir)
	os.WriteFile(config.DBConfigPath, []byte("type: sqlite\nconn: :memory:"), 0644)
}

func TestHandleAISuggestMapping(t *testing.T) {
	setupTestEnv(t)
	s := &Server{
		storage:    &mockAIStorage{},
		logStorage: &mockLogStorage{},
	}
	mux := http.NewServeMux()
	s.registerWorkflowRoutes(mux)

	tests := []struct {
		name           string
		payload        any
		role           storage.Role
		expectedStatus int
	}{
		{
			name: "Happy path - overlapping fields",
			payload: map[string][]string{
				"source_fields": {"user_id", "email", "created_at"},
				"target_fields": {"id", "email_address", "created_at"},
			},
			role:           storage.RoleEditor,
			expectedStatus: http.StatusOK,
		},
		{
			name: "Partial overlap",
			payload: map[string][]string{
				"source_fields": {"uid", "first_name"},
				"target_fields": {"user_id", "name"},
			},
			role:           storage.RoleEditor,
			expectedStatus: http.StatusOK,
		},
		{
			name: "Empty inputs",
			payload: map[string][]string{
				"source_fields": {},
				"target_fields": {},
			},
			role:           storage.RoleEditor,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Bad payload",
			payload:        "invalid json",
			role:           storage.RoleEditor,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "Forbidden for viewer",
			payload: map[string][]string{
				"source_fields": {"a"},
				"target_fields": {"b"},
			},
			role:           storage.RoleViewer,
			expectedStatus: http.StatusForbidden,
		},
		{
			name: "Limit check",
			payload: map[string][]string{
				"source_fields": make([]string, 1001),
				"target_fields": {"id"},
			},
			role:           storage.RoleEditor,
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.payload)
			if s, ok := tt.payload.(string); ok {
				body = []byte(s)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/ai/suggest-mapping", bytes.NewReader(body))
			req = withContextRole(req, tt.role)

			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != tt.expectedStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.expectedStatus)
			}

			if rr.Code == http.StatusOK {
				var resp map[string]any
				if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
					t.Errorf("failed to unmarshal response: %v", err)
				}
				if _, ok := resp["suggestions"]; !ok {
					t.Errorf("response missing suggestions")
				}
				if _, ok := resp["scores"]; !ok {
					t.Errorf("response missing scores")
				}
			}
		})
	}
}

func TestHandleAIAnalyzeError(t *testing.T) {
	setupTestEnv(t)
	s := &Server{
		storage:    &mockAIStorage{},
		ai:         ai.NewSelfHealingService(&mockLogger{}),
		logStorage: &mockLogStorage{},
	}
	mux := http.NewServeMux()
	s.registerWorkflowRoutes(mux)

	payload := map[string]any{
		"workflow_id": "w1",
		"node_id":     "n1",
		"error":       "missing field user_id",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/ai/analyze-error", bytes.NewReader(body))
	req = withEditorContext(req)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var resp ai.FixSuggestion
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Explanation == "" {
		t.Errorf("expected explanation in suggestion")
	}
}
