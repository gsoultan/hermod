package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/source/webhook"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// These drive the endpoint the way an MCP client does: over HTTP, through
// Hermod's own AuthMiddleware and route guard, with the official SDK client.

const testJWTSecret = "mcp-test-secret"

func withJWTConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db_config.yaml"),
		[]byte("type: sqlite\nconn: \"file::memory:\"\njwt_secret: "+testJWTSecret+"\n"), 0o600); err != nil {
		t.Fatalf("writing db_config.yaml: %v", err)
	}
	t.Setenv("HERMOD_CONFIG_DIR", dir)
	t.Setenv("HERMOD_JWT_SECRET", testJWTSecret)
}

func mint(t *testing.T, role string, vhosts ...string) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"id": "u-" + role, "username": strings.ToLower(role), "role": role, "vhosts": vhosts,
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}
	return s
}

// store is an in-memory slice of storage.Storage.
type store struct {
	storage.Storage
	mu        sync.Mutex
	workflows []storage.Workflow
	sources   map[string]storage.Source
	audits    []storage.AuditLog
}

func (s *store) ListWorkflows(context.Context, storage.CommonFilter) ([]storage.Workflow, int, error) {
	return s.workflows, len(s.workflows), nil
}

func (s *store) GetWorkflow(_ context.Context, id string) (storage.Workflow, error) {
	for _, wf := range s.workflows {
		if wf.ID == id {
			return wf, nil
		}
	}
	return storage.Workflow{}, storage.ErrNotFound
}

func (s *store) GetSource(_ context.Context, id string) (storage.Source, error) {
	if src, ok := s.sources[id]; ok {
		return src, nil
	}
	return storage.Source{}, storage.ErrNotFound
}

func (s *store) ListSources(context.Context, storage.CommonFilter) ([]storage.Source, int, error) {
	var out []storage.Source
	for _, src := range s.sources {
		out = append(out, src)
	}
	return out, len(out), nil
}

// ListUsers reports one user, so the install is past its first run.
func (*store) ListUsers(context.Context, storage.CommonFilter) ([]storage.User, int, error) {
	return []storage.User{{ID: "u-admin"}}, 1, nil
}

func (*store) CreateWebhookRequest(context.Context, storage.WebhookRequest) error { return nil }
func (*store) CreateLog(context.Context, storage.Log) error                       { return nil }

func (s *store) CreateAuditLog(_ context.Context, l storage.AuditLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audits = append(s.audits, l)
	return nil
}

func (s *store) auditActions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, a := range s.audits {
		out = append(out, a.Action)
	}
	return out
}

type sink struct{}

func (sink) Write(context.Context, hermod.Message) error { return nil }
func (sink) Ping(context.Context) error                  { return nil }
func (sink) Close() error                                { return nil }

func fixture(t *testing.T) (*httptest.Server, *store) {
	t.Helper()
	withJWTConfig(t)
	st := &store{sources: map[string]storage.Source{
		"hook": {ID: "hook", Type: "webhook", VHost: "tenant-a",
			Config: map[string]string{"path": "/api/webhooks/mcp-e2e", "response_mode": "sync"}},
		"other": {ID: "other", Type: "webhook", VHost: "tenant-b",
			Config: map[string]string{"path": "/api/webhooks/mcp-e2e-b"}},
	}}
	node := func(ref string) []storage.WorkflowNode {
		return []storage.WorkflowNode{{ID: "n1", Type: "source", RefID: ref}}
	}
	st.workflows = []storage.Workflow{
		{ID: "wf-exposed", Name: "Order lookup", VHost: "tenant-a", Active: true, Tags: []string{"mcp"}, Nodes: node("hook")},
		{ID: "wf-private", Name: "Payroll", VHost: "tenant-a", Active: true, Nodes: node("hook")},
		{ID: "wf-other", Name: "Other tenant", VHost: "tenant-b", Active: true, Tags: []string{"mcp"}, Nodes: node("other")},
	}

	// The exposed workflow is running: its webhook has a listener.
	src := webhook.NewWebhookSource("/api/webhooks/mcp-e2e")
	eng := pkgengine.NewEngine(src, []hermod.Sink{sink{}}, buffer.NewRingBuffer(8))
	eng.SetConfig(pkgengine.Config{MaxRetries: 1, RetryInterval: 10 * time.Millisecond, StatusInterval: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = eng.Start(ctx)
	}()

	h := &handlers.Handler{Storage: st, LogStorage: st}
	mux := http.NewServeMux()
	NewMCPHandler(h).RegisterMCPRoutes(mux)
	srv := httptest.NewServer(h.AuthMiddleware(mux))
	t.Cleanup(func() {
		srv.Close()
		cancel()
		<-done
	})
	return srv, st
}

type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

func connect(t *testing.T, srv *httptest.Server, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	cs, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + "/api/mcp",
		HTTPClient: &http.Client{Transport: bearer{token: token, next: http.DefaultTransport}},
		MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("calling %s: %v", name, err)
	}
	return res
}

func structured(t *testing.T, res *mcp.CallToolResult, into any) {
	t.Helper()
	if res.IsError {
		t.Fatalf("the tool failed: %+v", res.Content)
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatalf("decoding %s: %v", b, err)
	}
}

func TestTheMCPEndpointRequiresAHermodSession(t *testing.T) {
	srv, _ := fixture(t)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/api/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an anonymous MCP request got %d, want 401", resp.StatusCode)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "anon", Version: "1"}, nil)
	if _, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: srv.URL + "/api/mcp", MaxRetries: -1,
	}, nil); err == nil {
		t.Fatal("an MCP client with no credentials connected")
	}
}

func TestAnMCPClientSeesOnlyExposedWorkflowsInItsVHosts(t *testing.T) {
	srv, _ := fixture(t)
	cs := connect(t, srv, mint(t, "Viewer", "tenant-a"))

	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("listing tools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"list_workflows", "get_workflow_status", "run_workflow"} {
		if !names[want] {
			t.Errorf("tool %s is not offered; got %v", want, names)
		}
	}

	var listed struct {
		Workflows []struct {
			ID                string `json:"id"`
			Runnable          bool   `json:"runnable"`
			RepliesWithResult bool   `json:"replies_with_result"`
		} `json:"workflows"`
	}
	structured(t, call(t, cs, "list_workflows", nil), &listed)
	if len(listed.Workflows) != 1 || listed.Workflows[0].ID != "wf-exposed" ||
		!listed.Workflows[0].Runnable || !listed.Workflows[0].RepliesWithResult {
		t.Fatalf("listed %+v, want only wf-exposed, runnable with a reply", listed.Workflows)
	}

	for _, id := range []string{"wf-private", "wf-other"} {
		if res := call(t, cs, "get_workflow_status", map[string]any{"workflow_id": id}); !res.IsError {
			t.Errorf("the status of %s was returned to a caller who may not see it: %+v", id, res.StructuredContent)
		}
	}
	var st struct {
		ID     string `json:"id"`
		Active bool   `json:"active"`
	}
	structured(t, call(t, cs, "get_workflow_status", map[string]any{"workflow_id": "wf-exposed"}), &st)
	if st.ID != "wf-exposed" || !st.Active {
		t.Errorf("status = %+v", st)
	}
}

func TestAViewerCannotRunAWorkflowOverMCP(t *testing.T) {
	srv, _ := fixture(t)
	cs := connect(t, srv, mint(t, "Viewer", "tenant-a"))

	res := call(t, cs, "run_workflow", map[string]any{"workflow_id": "wf-exposed", "input": map[string]any{"q": 1}})
	if !res.IsError {
		t.Fatalf("a Viewer ran a workflow: %+v", res.StructuredContent)
	}
}

func TestRunWorkflowReturnsTheWebhookReplyAsTheToolResult(t *testing.T) {
	srv, st := fixture(t)
	cs := connect(t, srv, mint(t, "Editor", "tenant-a"))

	var run struct {
		ID     string         `json:"id"`
		Status string         `json:"status"`
		Record map[string]any `json:"record"`
	}
	structured(t, call(t, cs, "run_workflow", map[string]any{
		"workflow_id": "wf-exposed", "input": map[string]any{"order_id": 42},
	}), &run)
	if run.Status != "delivered" || run.ID == "" {
		t.Fatalf("run = %+v", run)
	}
	if b, _ := json.Marshal(run.Record); !strings.Contains(string(b), `"order_id":42`) {
		t.Errorf("the tool result does not carry the workflow's record: %s", b)
	}

	found := false
	for _, a := range st.auditActions() {
		if a == "MCP_RUN_WORKFLOW" {
			found = true
		}
	}
	if !found {
		t.Errorf("the run was not audited; actions: %v", st.auditActions())
	}
}
