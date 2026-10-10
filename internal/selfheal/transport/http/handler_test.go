package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/optimizer"
	"github.com/gsoultan/hermod/internal/selfheal"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
)

// These drive the proposals API against the real workflow update path: an
// approval lands in the workflow's version history like any edit.

type store struct {
	testutil.BaseMockStorage
	mu        sync.Mutex
	settings  map[string]string
	workflows map[string]storage.Workflow
	versions  []storage.WorkflowVersion
}

func (s *store) GetSetting(_ context.Context, k string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings[k], nil
}

func (s *store) SaveSetting(_ context.Context, k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings[k] = v
	return nil
}

func (s *store) GetWorkflow(_ context.Context, id string) (storage.Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wf, ok := s.workflows[id]
	if !ok {
		return storage.Workflow{}, storage.ErrNotFound
	}
	return wf, nil
}

func (s *store) UpdateWorkflow(_ context.Context, wf storage.Workflow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workflows[wf.ID] = wf
	return nil
}

func (s *store) CreateWorkflowVersion(_ context.Context, v storage.WorkflowVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions = slices.Insert(s.versions, 0, v)
	return nil
}

func (s *store) ListWorkflowVersions(context.Context, string) ([]storage.WorkflowVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.versions), nil
}

func (*store) GetSource(_ context.Context, id string) (storage.Source, error) {
	return storage.Source{ID: id, Name: "src", Type: "http"}, nil
}

func (*store) GetSink(_ context.Context, id string) (storage.Sink, error) {
	return storage.Sink{ID: id, Name: "snk", Type: "http"}, nil
}

func (*store) ListUsers(context.Context, storage.CommonFilter) ([]storage.User, int, error) {
	return []storage.User{{ID: "u"}}, 1, nil
}

func (*store) CreateAuditLog(context.Context, storage.AuditLog) error { return nil }

func orders() storage.Workflow {
	return storage.Workflow{
		ID: "wf", Name: "orders", VHost: "tenant-a", MaxRetries: 2, RetryInterval: "1s",
		Nodes: []storage.WorkflowNode{{ID: "n1", Type: "source", RefID: "src-1"}, {ID: "n2", Type: "sink", RefID: "snk-1"}},
		Edges: []storage.WorkflowEdge{{ID: "e1", SourceID: "n1", TargetID: "n2"}},
	}
}

type fixture struct {
	st  *store
	mux *http.ServeMux
	svc *selfheal.Service
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	// A configured install, so the role guards are not in first-run bypass.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db_config.yaml"),
		[]byte("type: sqlite\nconn: \"file::memory:\"\njwt_secret: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMOD_CONFIG_DIR", dir)

	st := &store{settings: map[string]string{}, workflows: map[string]storage.Workflow{"wf": orders()}}
	h := &handlers.Handler{Storage: st, LogStorage: st}
	ph := NewProposalHandler(h)
	mux := http.NewServeMux()
	ph.RegisterProposalRoutes(mux)
	if err := ph.Service().Propose(t.Context(), optimizer.Suggestion{
		WorkflowID: "wf", NodeID: "n2", Action: optimizer.ActionIncreaseRetry, Reason: "n2 fails",
	}); err != nil {
		t.Fatal(err)
	}
	return fixture{st: st, mux: mux, svc: ph.Service()}
}

var (
	editorA = &storage.User{ID: "a", Username: "ada", Role: storage.RoleEditor, VHosts: []string{"tenant-a"}}
	editorB = &storage.User{ID: "b", Username: "bob", Role: storage.RoleEditor, VHosts: []string{"tenant-b"}}
	viewerA = &storage.User{ID: "v", Username: "vic", Role: storage.RoleViewer, VHosts: []string{"tenant-a"}}
)

func (f fixture) do(t *testing.T, user *storage.User, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	ctx := context.WithValue(t.Context(), handlers.UserContextKey, user)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, method, path, nil))
	return rec
}

func (f fixture) list(t *testing.T, user *storage.User) []selfheal.Proposal {
	t.Helper()
	rec := f.do(t, user, http.MethodGet, "/api/workflows/wf/proposals")
	if rec.Code != http.StatusOK {
		t.Fatalf("list %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []selfheal.Proposal `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}

func TestProposalsAreListedForTheWorkflowsVHost(t *testing.T) {
	f := newFixture(t)
	got := f.list(t, viewerA)
	if len(got) != 1 || got[0].Status != selfheal.StatusPending || len(got[0].Patch) != 2 {
		t.Fatalf("proposals = %+v", got)
	}
	if rec := f.do(t, editorB, http.MethodGet, "/api/workflows/wf/proposals"); rec.Code != http.StatusForbidden {
		t.Fatalf("another tenant got %d", rec.Code)
	}
	if rec := f.do(t, editorA, http.MethodGet, "/api/workflows/nope/proposals"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown workflow got %d", rec.Code)
	}
}

func TestApprovingAppliesTheFixAsANewVersion(t *testing.T) {
	f := newFixture(t)
	id := f.list(t, editorA)[0].ID

	rec := f.do(t, editorA, http.MethodPost, "/api/workflows/wf/proposals/"+id+"/approve")
	if rec.Code != http.StatusOK {
		t.Fatalf("approve %d: %s", rec.Code, rec.Body.String())
	}
	var p selfheal.Proposal
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p.Status != selfheal.StatusApplied || p.PreviousVersion != 1 || p.AppliedVersion != 2 || p.DecidedBy != "ada" {
		t.Fatalf("proposal = %+v", p)
	}
	wf := f.st.workflows["wf"]
	if wf.MaxRetries != 3 || wf.RetryInterval != "2s" {
		t.Fatalf("workflow = %+v", wf)
	}
	if len(f.st.versions) != 2 || f.st.versions[0].CreatedBy != "ada" {
		t.Fatalf("versions = %+v", f.st.versions)
	}
	if rec := f.do(t, editorA, http.MethodPost, "/api/workflows/wf/proposals/"+id+"/approve"); rec.Code != http.StatusConflict {
		t.Fatalf("second approval got %d", rec.Code)
	}
}

func TestAStaleProposalIsRefusedWithConflict(t *testing.T) {
	f := newFixture(t)
	id := f.list(t, editorA)[0].ID
	wf := orders()
	wf.MaxRetries = 6
	f.st.workflows["wf"] = wf

	rec := f.do(t, editorA, http.MethodPost, "/api/workflows/wf/proposals/"+id+"/approve")
	if rec.Code != http.StatusConflict {
		t.Fatalf("approve %d: %s", rec.Code, rec.Body.String())
	}
	if f.st.workflows["wf"].MaxRetries != 6 || len(f.st.versions) != 0 {
		t.Fatal("a stale patch was applied")
	}
}

func TestRejectingAndRoleAndTenantChecks(t *testing.T) {
	f := newFixture(t)
	id := f.list(t, editorA)[0].ID

	if rec := f.do(t, viewerA, http.MethodPost, "/api/workflows/wf/proposals/"+id+"/approve"); rec.Code != http.StatusForbidden {
		t.Fatalf("a Viewer approving got %d", rec.Code)
	}
	if rec := f.do(t, editorB, http.MethodPost, "/api/workflows/wf/proposals/"+id+"/approve"); rec.Code != http.StatusForbidden {
		t.Fatalf("another tenant approving got %d", rec.Code)
	}
	if rec := f.do(t, editorA, http.MethodPost, "/api/workflows/wf/proposals/missing/reject"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown proposal got %d", rec.Code)
	}
	rec := f.do(t, editorA, http.MethodPost, "/api/workflows/wf/proposals/"+id+"/reject")
	if rec.Code != http.StatusOK {
		t.Fatalf("reject %d: %s", rec.Code, rec.Body.String())
	}
	if f.st.workflows["wf"].MaxRetries != 2 || len(f.st.versions) != 0 {
		t.Fatal("rejecting changed the workflow")
	}
	if got := f.list(t, editorA); got[0].Status != selfheal.StatusRejected {
		t.Fatalf("status = %s", got[0].Status)
	}
}
