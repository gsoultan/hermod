package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user/hermod/internal/engine"
	"github.com/user/hermod/internal/storage"
)

type mockStorage struct {
	storage.Storage
}

func (m *mockStorage) ListUsers(ctx context.Context, filter storage.CommonFilter) ([]storage.User, int, error) {
	return nil, 0, nil
}

func (m *mockStorage) GetUserByUsername(ctx context.Context, username string) (storage.User, error) {
	return storage.User{}, nil
}

func (m *mockStorage) CreateAuditLog(ctx context.Context, audit storage.AuditLog) error {
	return nil
}

func (m *mockStorage) ListAuditLogs(ctx context.Context, filter storage.AuditFilter) ([]storage.AuditLog, int, error) {
	return nil, 0, nil
}

func (m *mockStorage) UpdateNodeState(ctx context.Context, workflowID, nodeID string, state interface{}) error {
	return nil
}

func (m *mockStorage) GetNodeStates(ctx context.Context, workflowID string) (map[string]interface{}, error) {
	return nil, nil
}

func TestAuthMiddleware(t *testing.T) {
	registry := engine.NewRegistry(nil)
	// Use a mock storage that won't panic when used
	server := NewServer(registry, &mockStorage{})
	handler := server.Routes()

	tests := []struct {
		name           string
		method         string
		path           string
		body           string
		expectedStatus int
	}{
		{
			name:           "Root path (index.html) should be public",
			method:         "GET",
			path:           "/",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Login API should be public",
			method:         "POST",
			path:           "/api/login",
			body:           `{"username":"test","password":"test"}`,
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Protected API should be unauthorized",
			method:         "GET",
			path:           "/api/sources",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Workflow API should be unauthorized without token",
			method:         "GET",
			path:           "/api/workflows",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Initial admin creation allowed without auth when no users exist",
			method:         "POST",
			path:           "/api/users",
			body:           "not-a-json", // force handler to return 400 after passing middleware
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "DB config save allowed during initial setup",
			method:         "POST",
			path:           "/api/config/database",
			body:           "not-a-json",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "DB config test allowed during initial setup",
			method:         "POST",
			path:           "/api/config/database/test",
			body:           "not-a-json",
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			if tt.body != "" {
				req, _ = http.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			} else {
				req, _ = http.NewRequest(tt.method, tt.path, nil)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.expectedStatus {
				// Special check for /api/login which might return 400 or 500 if it passes middleware
				if tt.path == "/api/login" && (rr.Code == http.StatusBadRequest || rr.Code == http.StatusInternalServerError) {
					return
				}
				// Special check for / which might return 404 if static files are not built, but it should NOT be 401
				if tt.path == "/" && rr.Code == http.StatusNotFound {
					return
				}
				t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, tt.expectedStatus)
			}
		})
	}
}
