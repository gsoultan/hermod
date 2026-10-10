package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

// These drive POST /api/ai/build-workflow end to end against a fake model
// provider: an httptest server speaking the OpenAI chat API, reached through
// the same provider layer the AI nodes use. No real provider is called.

// secrets answers {{secret("NAME")}} per vhost.
type secrets map[string]map[string]string

func (s secrets) Get(context.Context, string) (string, error) { return "", nil }

func (s secrets) GetScoped(_ context.Context, vhost, key string) (string, error) {
	return s[vhost][key], nil
}

// store holds sources and sinks and fails the test on any write.
type store struct {
	storage.Storage
	t       *testing.T
	sources []storage.Source
	sinks   []storage.Sink
}

func (*store) ListUsers(context.Context, storage.CommonFilter) ([]storage.User, int, error) {
	return []storage.User{{ID: "u"}}, 1, nil
}

func (s *store) ListSources(_ context.Context, f storage.CommonFilter) ([]storage.Source, int, error) {
	var out []storage.Source
	for _, src := range s.sources {
		if f.VHost == "" || src.VHost == f.VHost {
			out = append(out, src)
		}
	}
	return out, len(out), nil
}

func (s *store) ListSinks(_ context.Context, f storage.CommonFilter) ([]storage.Sink, int, error) {
	var out []storage.Sink
	for _, snk := range s.sinks {
		if f.VHost == "" || snk.VHost == f.VHost {
			out = append(out, snk)
		}
	}
	return out, len(out), nil
}

func (s *store) GetSource(_ context.Context, id string) (storage.Source, error) {
	for _, src := range s.sources {
		if src.ID == id {
			return src, nil
		}
	}
	return storage.Source{}, storage.ErrNotFound
}

func (s *store) CreateWorkflow(context.Context, storage.Workflow) error {
	s.t.Error("the builder saved a workflow")
	return nil
}

func (s *store) UpdateWorkflow(context.Context, storage.Workflow) error {
	s.t.Error("the builder updated a workflow")
	return nil
}

func (*store) CreateLog(context.Context, storage.Log) error           { return nil }
func (*store) CreateAuditLog(context.Context, storage.AuditLog) error { return nil }

// fakeModel is an OpenAI-compatible chat endpoint.
type fakeModel struct {
	mu      sync.Mutex
	auth    []string
	bodies  []map[string]any
	content string
}

func (f *fakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	f.mu.Lock()
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.bodies = append(f.bodies, req)
	content := f.content
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "chatcmpl-1", "model": "fake-model",
		"choices": []map[string]any{{"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": content}}},
		"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 22},
	})
}

const draftAnswer = `{"name":"Support triage","nodes":[` +
	`{"id":"in","type":"source","ref_id":"src-a","config_json":""},` +
	`{"id":"cls","type":"ai_classify","ref_id":"","config_json":"{\"provider\":\"openai\",\"labels\":\"billing,bug\",\"apiKey\":\"{{secret(\\\"OPENAI\\\")}}\"}"},` +
	`{"id":"out","type":"sink","ref_id":"","config_json":"{\"sinkType\":\"slack\"}"}],` +
	`"edges":[{"source_id":"in","target_id":"cls","source_handle":""},{"source_id":"cls","target_id":"out","source_handle":"billing"}]}`

type fixture struct {
	mux   *http.ServeMux
	model *fakeModel
	url   string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db_config.yaml"),
		[]byte("type: sqlite\nconn: \"file::memory:\"\njwt_secret: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMOD_CONFIG_DIR", dir)

	evaluator.SetSecretSource(secrets{"tenant-a": {"OPENAI": "key-of-tenant-a"}})
	t.Cleanup(func() { evaluator.SetSecretSource(nil) })

	model := &fakeModel{content: draftAnswer}
	srv := httptest.NewServer(model)
	t.Cleanup(srv.Close)

	st := &store{t: t,
		sources: []storage.Source{
			{ID: "src-a", Name: "Support inbox", Type: "webhook", VHost: "tenant-a"},
			{ID: "src-b", Name: "Other tenant's inbox", Type: "webhook", VHost: "tenant-b"},
		},
		sinks: []storage.Sink{{ID: "snk-a", Name: "Slack", Type: "slack", VHost: "tenant-a"}},
	}
	h := &handlers.Handler{Storage: st, LogStorage: st}
	mux := http.NewServeMux()
	NewBuilderHandler(h).RegisterBuilderRoutes(mux)
	return fixture{mux: mux, model: model, url: srv.URL}
}

func (f fixture) post(t *testing.T, user *storage.User, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	ctx := context.WithValue(t.Context(), handlers.UserContextKey, user)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/ai/build-workflow", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func (f fixture) request(apiKey string) map[string]any {
	return map[string]any{
		"description": "When a support email arrives, classify it as billing or bug and post billing ones to Slack.",
		"vhost":       "tenant-a",
		"connection":  map[string]any{"provider": "openai", "model": "gpt-test", "apiKey": apiKey, "baseUrl": f.url},
	}
}

var (
	editorA = &storage.User{ID: "a", Username: "ada", Role: storage.RoleEditor, VHosts: []string{"tenant-a"}}
	editorB = &storage.User{ID: "b", Username: "bob", Role: storage.RoleEditor, VHosts: []string{"tenant-b"}}
	viewerA = &storage.User{ID: "v", Username: "vic", Role: storage.RoleViewer, VHosts: []string{"tenant-a"}}
)

type response struct {
	Workflow storage.Workflow `json:"workflow"`
	Issues   []struct {
		Severity string `json:"severity"`
		Message  string `json:"message"`
		NodeID   string `json:"node_id"`
	} `json:"issues"`
	Saved    bool   `json:"saved"`
	Provider string `json:"provider"`
	Usage    struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

func TestBuildWorkflowReturnsAValidatedDraftAndSavesNothing(t *testing.T) {
	f := newFixture(t)
	rec := f.post(t, editorA, f.request(`{{secret("OPENAI")}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Saved || resp.Workflow.Active || resp.Workflow.ID != "" || resp.Workflow.VHost != "tenant-a" {
		t.Errorf("the draft is not an unsaved, inactive workflow in the vhost: saved=%v %+v", resp.Saved, resp.Workflow)
	}
	if len(resp.Workflow.Nodes) != 3 || resp.Workflow.Nodes[0].RefID != "src-a" {
		t.Fatalf("nodes = %+v", resp.Workflow.Nodes)
	}
	// The sink was left unchosen, which the workflow validator reports.
	found := false
	for _, is := range resp.Issues {
		if is.NodeID == "out" && is.Severity == "error" && strings.Contains(is.Message, "not configured") {
			found = true
		}
	}
	if !found {
		t.Errorf("the validator's issues are not returned: %+v", resp.Issues)
	}
	if resp.Provider == "" || resp.Usage.OutputTokens != 22 {
		t.Errorf("provider %q usage %+v", resp.Provider, resp.Usage)
	}

	// The key was the vhost's secret, resolved for the caller's vhost.
	if len(f.model.auth) != 1 || f.model.auth[0] != "Bearer key-of-tenant-a" {
		t.Errorf("the provider was called with %v", f.model.auth)
	}
	// The prompt offers only the caller's vhost's sources.
	msgs, _ := json.Marshal(f.model.bodies[0]["messages"])
	if !strings.Contains(string(msgs), "src-a") || strings.Contains(string(msgs), "src-b") {
		t.Errorf("the prompt's sources are wrong: %s", msgs)
	}
	if f.model.bodies[0]["response_format"] == nil {
		t.Error("structured output was not requested")
	}
}

func TestBuildWorkflowRefusesAnotherTenantsVHost(t *testing.T) {
	f := newFixture(t)
	rec := f.post(t, editorB, f.request(`{{secret("OPENAI")}}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if len(f.model.auth) != 0 {
		t.Error("the provider was called for a vhost the caller cannot access")
	}
}

func TestBuildWorkflowRefusesAPlaintextKey(t *testing.T) {
	f := newFixture(t)
	rec := f.post(t, editorA, f.request("sk-plaintext"))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "secret(") {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if len(f.model.auth) != 0 {
		t.Error("the provider was called with a plaintext key")
	}
}

func TestBuildWorkflowNeedsTheEditorRole(t *testing.T) {
	f := newFixture(t)
	if rec := f.post(t, viewerA, f.request(`{{secret("OPENAI")}}`)); rec.Code != http.StatusForbidden {
		t.Fatalf("a Viewer got %d", rec.Code)
	}
}

func TestBuildWorkflowReportsAnUnreadableAnswer(t *testing.T) {
	f := newFixture(t)
	f.model.content = "Sorry, I can't do that."
	rec := f.post(t, editorA, f.request(`{{secret("OPENAI")}}`))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502: %s", rec.Code, rec.Body.String())
	}
}
