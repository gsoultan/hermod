package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/user/hermod/internal/engine"
	"github.com/user/hermod/internal/storage"
)

// fakeStorage is a minimal implementation of storage.Storage for testing readiness.
type fakeStorage struct {
	storage.Storage
	listWorkflowsErr error
	listSourcesErr   error
	workers          []storage.Worker
	workflows        []storage.Workflow
}

func (f *fakeStorage) Init(ctx context.Context) error { return nil }

func (f *fakeStorage) ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error) {
	return nil, 0, f.listSourcesErr
}
func (f *fakeStorage) CreateSource(ctx context.Context, src storage.Source) error { return nil }
func (f *fakeStorage) UpdateSource(ctx context.Context, src storage.Source) error { return nil }
func (f *fakeStorage) UpdateSourceStatus(ctx context.Context, id string, status string) error {
	return nil
}
func (f *fakeStorage) UpdateSourceState(ctx context.Context, id string, state map[string]string) error {
	return nil
}
func (f *fakeStorage) DeleteSource(ctx context.Context, id string) error { return nil }
func (f *fakeStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	return storage.Source{}, storage.ErrNotFound
}

func (f *fakeStorage) ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error) {
	return nil, 0, nil
}
func (f *fakeStorage) CreateSink(ctx context.Context, snk storage.Sink) error { return nil }
func (f *fakeStorage) UpdateSink(ctx context.Context, snk storage.Sink) error { return nil }
func (f *fakeStorage) UpdateSinkStatus(ctx context.Context, id string, status string) error {
	return nil
}
func (f *fakeStorage) DeleteSink(ctx context.Context, id string) error { return nil }
func (f *fakeStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	return storage.Sink{}, storage.ErrNotFound
}

func (f *fakeStorage) ListUsers(ctx context.Context, filter storage.CommonFilter) ([]storage.User, int, error) {
	return nil, 0, nil
}
func (f *fakeStorage) CreateUser(ctx context.Context, user storage.User) error { return nil }
func (f *fakeStorage) UpdateUser(ctx context.Context, user storage.User) error { return nil }
func (f *fakeStorage) DeleteUser(ctx context.Context, id string) error         { return nil }
func (f *fakeStorage) GetUser(ctx context.Context, id string) (storage.User, error) {
	return storage.User{}, storage.ErrNotFound
}
func (f *fakeStorage) GetUserByUsername(ctx context.Context, username string) (storage.User, error) {
	return storage.User{}, storage.ErrNotFound
}
func (f *fakeStorage) GetUserByEmail(ctx context.Context, email string) (storage.User, error) {
	return storage.User{}, storage.ErrNotFound
}

func (f *fakeStorage) ListVHosts(ctx context.Context, filter storage.CommonFilter) ([]storage.VHost, int, error) {
	return nil, 0, nil
}
func (f *fakeStorage) CreateVHost(ctx context.Context, vhost storage.VHost) error { return nil }
func (f *fakeStorage) DeleteVHost(ctx context.Context, id string) error           { return nil }
func (f *fakeStorage) GetVHost(ctx context.Context, id string) (storage.VHost, error) {
	return storage.VHost{}, storage.ErrNotFound
}

func (f *fakeStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	if f.listWorkflowsErr != nil {
		return nil, 0, f.listWorkflowsErr
	}
	return f.workflows, len(f.workflows), nil
}
func (f *fakeStorage) ListWorkspaces(ctx context.Context) ([]storage.Workspace, error) {
	return nil, nil
}
func (f *fakeStorage) CreateWorkspace(ctx context.Context, ws storage.Workspace) error {
	return nil
}
func (f *fakeStorage) DeleteWorkspace(ctx context.Context, id string) error {
	return nil
}
func (f *fakeStorage) CreateWorkflow(ctx context.Context, wf storage.Workflow) error { return nil }
func (f *fakeStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error { return nil }
func (f *fakeStorage) UpdateWorkflowStatus(ctx context.Context, id string, status string) error {
	return nil
}
func (f *fakeStorage) DeleteWorkflow(ctx context.Context, id string) error { return nil }
func (f *fakeStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	return storage.Workflow{}, storage.ErrNotFound
}

func (f *fakeStorage) ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error) {
	return f.workers, len(f.workers), nil
}
func (f *fakeStorage) CreateWorker(ctx context.Context, worker storage.Worker) error { return nil }
func (f *fakeStorage) UpdateWorker(ctx context.Context, worker storage.Worker) error { return nil }
func (f *fakeStorage) UpdateWorkerHeartbeat(ctx context.Context, id string, cpu, mem float64) error {
	return nil
}
func (f *fakeStorage) DeleteWorker(ctx context.Context, id string) error { return nil }
func (f *fakeStorage) GetWorker(ctx context.Context, id string) (storage.Worker, error) {
	return storage.Worker{}, storage.ErrNotFound
}

func (f *fakeStorage) ListLogs(ctx context.Context, filter storage.LogFilter) ([]storage.Log, int, error) {
	return nil, 0, nil
}
func (f *fakeStorage) CreateLog(ctx context.Context, log storage.Log) error           { return nil }
func (f *fakeStorage) DeleteLogs(ctx context.Context, filter storage.LogFilter) error { return nil }
func (f *fakeStorage) CreateAuditLog(ctx context.Context, log storage.AuditLog) error { return nil }
func (f *fakeStorage) ListAuditLogs(ctx context.Context, filter storage.AuditFilter) ([]storage.AuditLog, int, error) {
	return nil, 0, nil
}
func (f *fakeStorage) PurgeAuditLogs(ctx context.Context, before time.Time) error {
	return nil
}
func (f *fakeStorage) PurgeMessageTraces(ctx context.Context, before time.Time) error {
	return nil
}

func (f *fakeStorage) GetSetting(ctx context.Context, key string) (string, error) {
	return "", storage.ErrNotFound
}
func (f *fakeStorage) SaveSetting(ctx context.Context, key string, value string) error { return nil }

func (f *fakeStorage) UpdateNodeState(ctx context.Context, workflowID, nodeID string, state interface{}) error {
	return nil
}
func (f *fakeStorage) GetNodeStates(ctx context.Context, workflowID string) (map[string]interface{}, error) {
	return nil, nil
}

func (f *fakeStorage) AcquireWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	return true, nil
}
func (f *fakeStorage) RenewWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	return true, nil
}
func (f *fakeStorage) ReleaseWorkflowLease(ctx context.Context, workflowID, ownerID string) error {
	return nil
}

func (f *fakeStorage) ListWebhookRequests(ctx context.Context, filter storage.WebhookRequestFilter) ([]storage.WebhookRequest, int, error) {
	return nil, 0, nil
}
func (f *fakeStorage) CreateWebhookRequest(ctx context.Context, req storage.WebhookRequest) error {
	return nil
}
func (f *fakeStorage) GetWebhookRequest(ctx context.Context, id string) (storage.WebhookRequest, error) {
	return storage.WebhookRequest{}, storage.ErrNotFound
}
func (f *fakeStorage) DeleteWebhookRequests(ctx context.Context, filter storage.WebhookRequestFilter) error {
	return nil
}

func (f *fakeStorage) ListSchemas(ctx context.Context, name string) ([]storage.Schema, error) {
	return nil, nil
}
func (f *fakeStorage) ListAllSchemas(ctx context.Context) ([]storage.Schema, error) {
	return nil, nil
}
func (f *fakeStorage) GetSchema(ctx context.Context, name string, version int) (storage.Schema, error) {
	return storage.Schema{}, storage.ErrNotFound
}
func (f *fakeStorage) GetLatestSchema(ctx context.Context, name string) (storage.Schema, error) {
	return storage.Schema{}, storage.ErrNotFound
}
func (f *fakeStorage) CreateSchema(ctx context.Context, schema storage.Schema) error {
	return nil
}

func (f *fakeStorage) RecordTraceStep(ctx context.Context, workflowID, messageID string, step storage.TraceStep) error {
	return nil
}
func (f *fakeStorage) GetMessageTrace(ctx context.Context, workflowID, messageID string) (storage.MessageTrace, error) {
	return storage.MessageTrace{}, storage.ErrNotFound
}
func (f *fakeStorage) ListMessageTraces(ctx context.Context, workflowID string, limit int) ([]storage.MessageTrace, error) {
	return nil, nil
}

func (f *fakeStorage) CreateWorkflowVersion(ctx context.Context, version storage.WorkflowVersion) error {
	return nil
}
func (f *fakeStorage) ListWorkflowVersions(ctx context.Context, workflowID string) ([]storage.WorkflowVersion, error) {
	return nil, nil
}
func (f *fakeStorage) GetWorkflowVersion(ctx context.Context, workflowID string, version int) (storage.WorkflowVersion, error) {
	return storage.WorkflowVersion{}, storage.ErrNotFound
}

func (f *fakeStorage) CreateOutboxItem(ctx context.Context, item storage.OutboxItem) error {
	return nil
}
func (f *fakeStorage) ListOutboxItems(ctx context.Context, status string, limit int) ([]storage.OutboxItem, error) {
	return nil, nil
}
func (f *fakeStorage) DeleteOutboxItem(ctx context.Context, id string) error {
	return nil
}
func (f *fakeStorage) UpdateOutboxItem(ctx context.Context, item storage.OutboxItem) error {
	return nil
}
func (f *fakeStorage) GetLineage(ctx context.Context) ([]storage.LineageEdge, error) {
	return nil, nil
}

func TestLivenessOK(t *testing.T) {
	s := NewServer(nil, &fakeStorage{}, nil, nil)
	handler := s.Routes()

	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestReadiness(t *testing.T) {
	tests := []struct {
		name         string
		workflowsErr error
		sourcesErr   error
		want         int
	}{
		{name: "ok", workflowsErr: nil, sourcesErr: nil, want: http.StatusOK},
		{name: "db failure", workflowsErr: errors.New("db error"), sourcesErr: nil, want: http.StatusServiceUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer(nil, &fakeStorage{listWorkflowsErr: tc.workflowsErr, listSourcesErr: tc.sourcesErr}, nil, nil)
			h := s.Routes()

			req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, rr.Code)
			}
		})
	}
}

func TestReadiness_Schema_And_NonGating(t *testing.T) {
	now := time.Now()
	stale := now.Add(-2 * time.Minute)
	fs := &fakeStorage{
		workers: []storage.Worker{
			{ID: "w1", LastSeen: &now},
			{ID: "w2", LastSeen: &stale},
		},
	}

	s := NewServer(nil, fs, nil, nil)
	// With a non-nil registry, registry check should be ok
	s.registry = engine.NewRegistry(nil)

	h := s.Routes()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var body struct {
		Version string                 `json:"version"`
		Status  string                 `json:"status"`
		Checks  map[string]interface{} `json:"checks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if body.Version != "v1" {
		t.Fatalf("expected version v1, got %s", body.Version)
	}
	if body.Status != "ok" {
		t.Fatalf("expected status ok, got %s", body.Status)
	}
	// Validate workers shape
	workers, ok := body.Checks["workers"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing workers check")
	}
	if workers["ttl_seconds"].(float64) != 60 {
		t.Fatalf("expected ttl_seconds 60, got %v", workers["ttl_seconds"])
	}
	if workers["recent"].(float64) != 1 || workers["stale"].(float64) != 1 {
		t.Fatalf("unexpected workers counts: %+v", workers)
	}
}

func TestReadiness_Debounce_FailureTransition(t *testing.T) {
	t.Setenv("HERMOD_READY_DEBOUNCE", "200ms")

	fs := &fakeStorage{}
	s := NewServer(nil, fs, nil, nil)
	h := s.Routes()

	// 1) Initial OK
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected initial 200, got %d", rr.Code)
	}

	// 2) Introduce transient DB failure and verify debounce keeps 200
	fs.listWorkflowsErr = errors.New("db down")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 during debounce window, got %d", rr.Code)
	}

	// 3) After debounce duration, status should flip to 503
	time.Sleep(220 * time.Millisecond)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 after debounce, got %d", rr.Code)
	}
}

func TestReadiness_Leases_Check_And_Gating(t *testing.T) {
	now := time.Now()
	past := now.Add(-1 * time.Minute)

	fs := &fakeStorage{
		workflows: []storage.Workflow{
			{ID: "wf-owned", Name: "wf-owned", Active: true, OwnerID: "w1", LeaseUntil: &now},
			{ID: "wf-missing", Name: "wf-missing", Active: true},
			{ID: "wf-expired", Name: "wf-expired", Active: true, OwnerID: "w2", LeaseUntil: &past},
		},
	}

	s := NewServer(nil, fs, nil, nil)
	h := s.Routes()

	// Non-gating by default: should be 200 but leases ok=false
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body struct {
		Checks map[string]map[string]interface{} `json:"checks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if body.Checks["leases"]["ok"].(bool) {
		t.Fatalf("expected leases ok=false due to missing/expired")
	}

	// Enable gating: expect 503 when leases not ok
	t.Setenv("HERMOD_READY_LEASES_REQUIRED", "true")
	rr = httptest.NewRecorder()
	h = s.Routes()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when leases gating enabled, got %d", rr.Code)
	}
}
