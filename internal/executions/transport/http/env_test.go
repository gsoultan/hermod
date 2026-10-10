package http

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	_ "github.com/gsoultan/hermod/internal/engine/registry/nodes"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/advanced"
	_ "modernc.org/sqlite"
)

// recordingSink keeps what it was sent.
type recordingSink struct {
	mu  sync.Mutex
	got []map[string]any
}

func (s *recordingSink) Write(_ context.Context, msg hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, msg.ToMap())
	return nil
}
func (s *recordingSink) Ping(context.Context) error { return nil }
func (s *recordingSink) Close() error               { return nil }
func (s *recordingSink) received() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.got...)
}

type env struct {
	store storage.Storage
	reg   *registry.Registry
	sink  *recordingSink
	mux   *http.ServeMux
}

// newEnv is a configured install (so RBAC is enforced) on a real SQL store
// holding workflow wf-1 in vhost "team-a": source -> set greeting -> sink.
func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db_config.yaml"), []byte("type: sqlite\nconn: \"file::memory:\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMOD_CONFIG_DIR", dir)

	db, err := sql.Open("sqlite", "file:exec_"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sqlstorage.NewSQLStorage(db, "sqlite")
	ctx := t.Context()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(ctx, storage.User{ID: "u-admin", Username: "admin", Password: "x", Role: storage.RoleAdministrator}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSink(ctx, storage.Sink{ID: "snk-1", Name: "out", Type: "sqlite", VHost: "team-a",
		Config: map[string]string{"path": ":memory:", "table": "t"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflow(ctx, storage.Workflow{
		ID: "wf-1", Name: "wf-1", VHost: "team-a",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			{ID: "greet", Type: "transformation", Config: map[string]any{"transType": "set", "column.greeting": "hello"}},
			{ID: "out", Type: "sink", RefID: "snk-1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "greet"},
			{ID: "e2", SourceID: "greet", TargetID: "out"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	sink := &recordingSink{}
	reg := registry.NewRegistry(store)
	t.Cleanup(reg.Close)
	reg.SetFactories(nil, func(cfg factory.SinkConfig) (hermod.Sink, error) {
		if cfg.ID == "snk-1" {
			return sink, nil
		}
		return nil, fmt.Errorf("no sink fixture for %q", cfg.ID)
	})

	h := NewExecutionsHandler(&handlers.Handler{Storage: store, LogStorage: store, Registry: reg})
	mux := http.NewServeMux()
	h.RegisterExecutionRoutes(mux)
	return &env{store: store, reg: reg, sink: sink, mux: mux}
}

// do sends a request as a user with role and vhosts.
func (e *env) do(t *testing.T, method, path, body string, role storage.Role, vhosts ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.UserContextKey,
		&storage.User{ID: "u-1", Username: "tester", Role: role, VHosts: vhosts}))
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}
