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

func (m *mockStorage) CreateSource(ctx context.Context, src storage.Source) error { return nil }
func (m *mockStorage) UpdateSource(ctx context.Context, src storage.Source) error { return nil }
func (m *mockStorage) UpdateSourceState(ctx context.Context, id string, state map[string]string) error {
	return nil
}
func (m *mockStorage) DeleteSource(ctx context.Context, id string) error { return nil }
func (m *mockStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	return storage.Source{}, nil
}

func (m *mockStorage) ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error) {
	return nil, 0, nil
}
func (m *mockStorage) CreateSink(ctx context.Context, snk storage.Sink) error { return nil }
func (m *mockStorage) UpdateSink(ctx context.Context, snk storage.Sink) error { return nil }
func (m *mockStorage) DeleteSink(ctx context.Context, id string) error        { return nil }
func (m *mockStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	return storage.Sink{}, nil
}

func (m *mockStorage) CreateUser(ctx context.Context, user storage.User) error { return nil }
func (m *mockStorage) UpdateUser(ctx context.Context, user storage.User) error { return nil }
func (m *mockStorage) DeleteUser(ctx context.Context, id string) error         { return nil }
func (m *mockStorage) GetUser(ctx context.Context, id string) (storage.User, error) {
	return storage.User{}, nil
}
func (m *mockStorage) GetUserByEmail(ctx context.Context, email string) (storage.User, error) {
	return storage.User{}, nil
}

func (m *mockStorage) ListVHosts(ctx context.Context, filter storage.CommonFilter) ([]storage.VHost, int, error) {
	return nil, 0, nil
}
func (m *mockStorage) CreateVHost(ctx context.Context, vhost storage.VHost) error { return nil }
func (m *mockStorage) DeleteVHost(ctx context.Context, id string) error           { return nil }
func (m *mockStorage) GetVHost(ctx context.Context, id string) (storage.VHost, error) {
	return storage.VHost{}, nil
}

func (m *mockStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	return nil, 0, nil
}
func (m *mockStorage) CreateWorkflow(ctx context.Context, wf storage.Workflow) error { return nil }
func (m *mockStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error { return nil }
func (m *mockStorage) DeleteWorkflow(ctx context.Context, id string) error           { return nil }
func (m *mockStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	return storage.Workflow{}, nil
}

func (m *mockStorage) ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error) {
	return nil, 0, nil
}
func (m *mockStorage) CreateWorker(ctx context.Context, worker storage.Worker) error { return nil }
func (m *mockStorage) UpdateWorker(ctx context.Context, worker storage.Worker) error { return nil }
func (m *mockStorage) UpdateWorkerHeartbeat(ctx context.Context, id string, cpu, mem float64) error {
	return nil
}
func (m *mockStorage) DeleteWorker(ctx context.Context, id string) error { return nil }
func (m *mockStorage) GetWorker(ctx context.Context, id string) (storage.Worker, error) {
	return storage.Worker{}, nil
}

func (m *mockStorage) ListLogs(ctx context.Context, filter storage.LogFilter) ([]storage.Log, int, error) {
	return nil, 0, nil
}
func (m *mockStorage) CreateLog(ctx context.Context, log storage.Log) error           { return nil }
func (m *mockStorage) DeleteLogs(ctx context.Context, filter storage.LogFilter) error { return nil }

func (m *mockStorage) ListWebhookRequests(ctx context.Context, filter storage.WebhookRequestFilter) ([]storage.WebhookRequest, int, error) {
	return nil, 0, nil
}
func (m *mockStorage) CreateWebhookRequest(ctx context.Context, req storage.WebhookRequest) error {
	return nil
}
func (m *mockStorage) GetWebhookRequest(ctx context.Context, id string) (storage.WebhookRequest, error) {
	return storage.WebhookRequest{}, nil
}
func (m *mockStorage) DeleteWebhookRequests(ctx context.Context, filter storage.WebhookRequestFilter) error {
	return nil
}

func (m *mockStorage) GetSetting(ctx context.Context, key string) (string, error) { return "", nil }
func (m *mockStorage) SaveSetting(ctx context.Context, key string, value string) error {
	return nil
}

func (m *mockStorage) AcquireWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	return true, nil
}
func (m *mockStorage) RenewWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	return true, nil
}
func (m *mockStorage) ReleaseWorkflowLease(ctx context.Context, workflowID, ownerID string) error {
	return nil
}

func (m *mockStorage) ListSchemas(ctx context.Context, name string) ([]storage.Schema, error) {
	return nil, nil
}
func (m *mockStorage) GetSchema(ctx context.Context, name string, version int) (storage.Schema, error) {
	return storage.Schema{}, nil
}
func (m *mockStorage) GetLatestSchema(ctx context.Context, name string) (storage.Schema, error) {
	return storage.Schema{}, nil
}
func (m *mockStorage) CreateSchema(ctx context.Context, schema storage.Schema) error {
	return nil
}

func (m *mockStorage) RecordTraceStep(ctx context.Context, workflowID, messageID string, step storage.TraceStep) error {
	return nil
}
func (m *mockStorage) GetMessageTrace(ctx context.Context, workflowID, messageID string) (storage.MessageTrace, error) {
	return storage.MessageTrace{}, nil
}
func (m *mockStorage) ListMessageTraces(ctx context.Context, workflowID string, limit int) ([]storage.MessageTrace, error) {
	return nil, nil
}

func (m *mockStorage) Init(ctx context.Context) error { return nil }

func (m *mockStorage) UpdateNodeState(ctx context.Context, workflowID, nodeID string, state interface{}) error {
	return nil
}

func (m *mockStorage) GetNodeStates(ctx context.Context, workflowID string) (map[string]interface{}, error) {
	return nil, nil
}

func TestAuthMiddleware(t *testing.T) {
	registry := engine.NewRegistry(nil)
	// Use a mock storage that won't panic when used
	server := NewServer(registry, &mockStorage{}, nil, nil)
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
