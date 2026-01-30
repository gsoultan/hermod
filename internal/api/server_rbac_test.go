package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/storage"
)

// fakeRBACStorage provides just enough implementation for RBAC tests
type fakeRBACStorage struct {
	user      storage.User
	sources   map[string]storage.Source
	sinks     map[string]storage.Sink
	workflows map[string]storage.Workflow
}

func (f *fakeRBACStorage) Init(ctx context.Context) error { return nil }

// Implement required subset
func (f *fakeRBACStorage) GetUser(ctx context.Context, id string) (storage.User, error) {
	return f.user, nil
}
func (f *fakeRBACStorage) GetUserByUsername(ctx context.Context, username string) (storage.User, error) {
	return storage.User{}, storage.ErrNotFound
}
func (f *fakeRBACStorage) GetUserByEmail(ctx context.Context, email string) (storage.User, error) {
	return storage.User{}, storage.ErrNotFound
}
func (f *fakeRBACStorage) ListUsers(ctx context.Context, filter storage.CommonFilter) ([]storage.User, int, error) {
	return nil, 0, nil
}
func (f *fakeRBACStorage) CreateUser(ctx context.Context, user storage.User) error { return nil }
func (f *fakeRBACStorage) UpdateUser(ctx context.Context, user storage.User) error { return nil }
func (f *fakeRBACStorage) DeleteUser(ctx context.Context, id string) error         { return nil }

func (f *fakeRBACStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	if s, ok := f.sources[id]; ok {
		return s, nil
	}
	return storage.Source{}, storage.ErrNotFound
}
func (f *fakeRBACStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	if s, ok := f.sinks[id]; ok {
		return s, nil
	}
	return storage.Sink{}, storage.ErrNotFound
}
func (f *fakeRBACStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	if w, ok := f.workflows[id]; ok {
		return w, nil
	}
	return storage.Workflow{}, storage.ErrNotFound
}

// The rest of Storage methods used by routes but not relevant to RBAC tests
func (f *fakeRBACStorage) ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error) {
	return nil, 0, nil
}
func (f *fakeRBACStorage) CreateSource(ctx context.Context, src storage.Source) error { return nil }
func (f *fakeRBACStorage) UpdateSource(ctx context.Context, src storage.Source) error { return nil }
func (f *fakeRBACStorage) UpdateSourceState(ctx context.Context, id string, state map[string]string) error {
	return nil
}
func (f *fakeRBACStorage) DeleteSource(ctx context.Context, id string) error { return nil }

func (f *fakeRBACStorage) ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error) {
	return nil, 0, nil
}
func (f *fakeRBACStorage) CreateSink(ctx context.Context, s storage.Sink) error { return nil }
func (f *fakeRBACStorage) UpdateSink(ctx context.Context, s storage.Sink) error { return nil }
func (f *fakeRBACStorage) DeleteSink(ctx context.Context, id string) error      { return nil }

func (f *fakeRBACStorage) ListVHosts(ctx context.Context, filter storage.CommonFilter) ([]storage.VHost, int, error) {
	return nil, 0, nil
}
func (f *fakeRBACStorage) CreateVHost(ctx context.Context, v storage.VHost) error { return nil }
func (f *fakeRBACStorage) DeleteVHost(ctx context.Context, id string) error       { return nil }
func (f *fakeRBACStorage) GetVHost(ctx context.Context, id string) (storage.VHost, error) {
	return storage.VHost{}, storage.ErrNotFound
}

func (f *fakeRBACStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	return nil, 0, nil
}
func (f *fakeRBACStorage) CreateWorkflow(ctx context.Context, wf storage.Workflow) error { return nil }
func (f *fakeRBACStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error { return nil }
func (f *fakeRBACStorage) DeleteWorkflow(ctx context.Context, id string) error           { return nil }

func (f *fakeRBACStorage) ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error) {
	return nil, 0, nil
}
func (f *fakeRBACStorage) CreateWorker(ctx context.Context, w storage.Worker) error   { return nil }
func (f *fakeRBACStorage) UpdateWorker(ctx context.Context, w storage.Worker) error   { return nil }
func (f *fakeRBACStorage) UpdateWorkerHeartbeat(ctx context.Context, id string) error { return nil }
func (f *fakeRBACStorage) DeleteWorker(ctx context.Context, id string) error          { return nil }
func (f *fakeRBACStorage) GetWorker(ctx context.Context, id string) (storage.Worker, error) {
	return storage.Worker{}, storage.ErrNotFound
}

func (f *fakeRBACStorage) ListLogs(ctx context.Context, filter storage.LogFilter) ([]storage.Log, int, error) {
	return nil, 0, nil
}
func (f *fakeRBACStorage) CreateLog(ctx context.Context, log storage.Log) error           { return nil }
func (f *fakeRBACStorage) DeleteLogs(ctx context.Context, filter storage.LogFilter) error { return nil }
func (f *fakeRBACStorage) CreateAuditLog(ctx context.Context, log storage.AuditLog) error { return nil }

func (f *fakeRBACStorage) UpdateNodeState(ctx context.Context, workflowID, nodeID string, state interface{}) error {
	return nil
}
func (f *fakeRBACStorage) GetNodeStates(ctx context.Context, workflowID string) (map[string]interface{}, error) {
	return nil, nil
}
func (f *fakeRBACStorage) ListAuditLogs(ctx context.Context, filter storage.AuditFilter) ([]storage.AuditLog, int, error) {
	return nil, 0, nil
}

func (f *fakeRBACStorage) GetSetting(ctx context.Context, key string) (string, error) {
	return "", storage.ErrNotFound
}
func (f *fakeRBACStorage) SaveSetting(ctx context.Context, key string, value string) error {
	return nil
}

// Lease API stubs
func (f *fakeRBACStorage) AcquireWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	return false, nil
}
func (f *fakeRBACStorage) RenewWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	return false, nil
}
func (f *fakeRBACStorage) ReleaseWorkflowLease(ctx context.Context, workflowID, ownerID string) error {
	return nil
}

// Helper to write a db_config.yaml with a known JWT secret for tests
func writeTestDBConfig(t *testing.T, secret string) func() {
	t.Helper()
	cfg := &config.DBConfig{Type: "sqlite", Conn: "test", JWTSecret: secret}
	if err := config.SaveDBConfig(cfg); err != nil {
		t.Fatalf("save db config: %v", err)
	}
	return func() { _ = os.Remove(config.DBConfigPath) }
}

func makeJWT(t *testing.T, secret, userID, role string, vhosts []string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"id":     userID,
		"role":   role,
		"vhosts": vhosts,
		"exp":    time.Now().Add(1 * time.Hour).Unix(),
	})
	s, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return s
}

func TestRBAC_VHost_Scoped_Access(t *testing.T) {
	cleanup := writeTestDBConfig(t, "secret")
	defer cleanup()

	fs := &fakeRBACStorage{
		user: storage.User{ID: "u1", Role: storage.RoleViewer, VHosts: []string{"team-a"}},
		sources: map[string]storage.Source{
			"srcA": {ID: "srcA", VHost: "team-a"},
		},
	}
	s := NewServer(nil, fs)
	h := s.Routes()

	token := makeJWT(t, "secret", "u1", string(storage.RoleViewer), []string{"team-a"})
	// Allowed: viewer reading source in allowed vhost
	req := httptest.NewRequest(http.MethodGet, "/api/sources/srcA", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	// Forbidden: viewer reading source in disallowed vhost
	fs.user = storage.User{ID: "u2", Role: storage.RoleViewer, VHosts: []string{"team-b"}}
	token = makeJWT(t, "secret", "u2", string(storage.RoleViewer), []string{"team-b"})
	req = httptest.NewRequest(http.MethodGet, "/api/sources/srcA", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestRBAC_Editor_Denied_VHost_Admin_Only_Actions(t *testing.T) {
	cleanup := writeTestDBConfig(t, "secret")
	defer cleanup()

	fs := &fakeRBACStorage{
		user: storage.User{ID: "u1", Role: storage.RoleEditor, VHosts: []string{"team-a"}},
	}
	s := NewServer(nil, fs)
	h := s.Routes()

	token := makeJWT(t, "secret", "u1", string(storage.RoleEditor), []string{"team-a"})

	// Editors cannot manage vhosts
	req := httptest.NewRequest(http.MethodPost, "/api/vhosts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestRBAC_Viewer_List_Filter_VHost_Enforced(t *testing.T) {
	cleanup := writeTestDBConfig(t, "secret")
	defer cleanup()

	fs := &fakeRBACStorage{user: storage.User{ID: "u1", Role: storage.RoleViewer, VHosts: []string{"team-a"}}}
	s := NewServer(nil, fs)
	h := s.Routes()
	token := makeJWT(t, "secret", "u1", string(storage.RoleViewer), []string{"team-a"})

	// Viewer listing workflows with a disallowed vhost should be forbidden
	req := httptest.NewRequest(http.MethodGet, "/api/workflows?vhost=team-b", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	// Handler may still attempt to list, but middleware should block -> 403
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for disallowed vhost filter, got %d", rr.Code)
	}
}
