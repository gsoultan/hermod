package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
)

// mockWorkspaceStorage is a workspace-aware in-memory store. It keeps the
// membership side of the relationship honest: ListWorkflows filters on
// WorkspaceID the way every real backend does (sql.go:1468, mongodb.go:855),
// because the quota checks are built on that filter and a mock that ignored it
// would make an unenforced quota look enforced.
type mockWorkspaceStorage struct {
	testutil.BaseMockStorage
	workspaces map[string]storage.Workspace
	workflows  map[string]storage.Workflow
	sources    map[string]storage.Source
	sinks      map[string]storage.Sink
}

func newMockWorkspaceStorage() *mockWorkspaceStorage {
	return &mockWorkspaceStorage{
		workspaces: map[string]storage.Workspace{},
		workflows:  map[string]storage.Workflow{},
		sources:    map[string]storage.Source{},
		sinks:      map[string]storage.Sink{},
	}
}

func (m *mockWorkspaceStorage) ListWorkspaces(ctx context.Context) ([]storage.Workspace, error) {
	out := []storage.Workspace{}
	for _, ws := range m.workspaces {
		out = append(out, ws)
	}
	return out, nil
}

func (m *mockWorkspaceStorage) CreateWorkspace(ctx context.Context, ws storage.Workspace) error {
	m.workspaces[ws.ID] = ws
	return nil
}

func (m *mockWorkspaceStorage) GetWorkspace(ctx context.Context, id string) (storage.Workspace, error) {
	ws, ok := m.workspaces[id]
	if !ok {
		return storage.Workspace{}, storage.ErrNotFound
	}
	return ws, nil
}

func (m *mockWorkspaceStorage) UpdateWorkspace(ctx context.Context, ws storage.Workspace) error {
	if _, ok := m.workspaces[ws.ID]; !ok {
		return storage.ErrNotFound
	}
	m.workspaces[ws.ID] = ws
	return nil
}

func (m *mockWorkspaceStorage) DeleteWorkspace(ctx context.Context, id string) error {
	delete(m.workspaces, id)
	return nil
}

func (m *mockWorkspaceStorage) ClearWorkspaceAssignments(ctx context.Context, workspaceID string) (int, error) {
	n := 0
	for id, wf := range m.workflows {
		if wf.WorkspaceID == workspaceID {
			wf.WorkspaceID = ""
			m.workflows[id] = wf
			n++
		}
	}
	for id, src := range m.sources {
		if src.WorkspaceID == workspaceID {
			src.WorkspaceID = ""
			m.sources[id] = src
			n++
		}
	}
	for id, snk := range m.sinks {
		if snk.WorkspaceID == workspaceID {
			snk.WorkspaceID = ""
			m.sinks[id] = snk
			n++
		}
	}
	return n, nil
}

func (m *mockWorkspaceStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	out := []storage.Workflow{}
	for _, wf := range m.workflows {
		if filter.WorkspaceID != "" && wf.WorkspaceID != filter.WorkspaceID {
			continue
		}
		out = append(out, wf)
	}
	return out, len(out), nil
}

func (m *mockWorkspaceStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	wf, ok := m.workflows[id]
	if !ok {
		return storage.Workflow{}, storage.ErrNotFound
	}
	return wf, nil
}

func (m *mockWorkspaceStorage) CreateWorkflow(ctx context.Context, wf storage.Workflow) error {
	m.workflows[wf.ID] = wf
	return nil
}

func (m *mockWorkspaceStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error {
	m.workflows[wf.ID] = wf
	return nil
}

func (m *mockWorkspaceStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	src, ok := m.sources[id]
	if !ok {
		return storage.Source{}, storage.ErrNotFound
	}
	return src, nil
}

func (m *mockWorkspaceStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	snk, ok := m.sinks[id]
	if !ok {
		return storage.Sink{}, storage.ErrNotFound
	}
	return snk, nil
}

// newWorkspaceTestHandler wires the handler and a mux the way production does,
// so route registration (and the EditorOnly wrappers) are under test too.
func newWorkspaceTestHandler(t *testing.T) (*mockWorkspaceStorage, *http.ServeMux) {
	t.Helper()
	store := newMockWorkspaceStorage()
	h := &WorkflowHandler{
		Handler: &handlers.Handler{
			Storage:    store,
			LogStorage: store,
		},
	}
	mux := http.NewServeMux()
	h.RegisterWorkflowRoutes(mux)
	return store, mux
}

func do(t *testing.T, mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequestWithContext(t.Context(), method, path, nil)
	} else {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		r = httptest.NewRequestWithContext(t.Context(), method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, r)
	return rr
}

// A workflow the validator accepts, so a 400 never masquerades as a quota 403.
func validWorkflow(id, name, workspaceID string) storage.Workflow {
	return storage.Workflow{
		ID:          id,
		Name:        name,
		VHost:       "default",
		WorkspaceID: workspaceID,
		Nodes: []storage.WorkflowNode{
			{ID: "n1", Type: "source", RefID: "src-1"},
			{ID: "n2", Type: "sink", RefID: "snk-1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "n1", TargetID: "n2"},
		},
	}
}

// A created workspace's ID has to reach the caller. Storage mints it, so a
// handler that echoes the decoded request body answers `"id": ""` and every
// API client that creates-then-references a workspace gets an empty string.
func TestCreateWorkspaceReturnsTheStoredRow(t *testing.T) {
	_, mux := newWorkspaceTestHandler(t)

	rr := do(t, mux, "POST", "/api/workspaces", map[string]any{
		"name":          "Team Alpha",
		"max_workflows": 3,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var got storage.Workspace
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if got.ID == "" {
		t.Error("create response carries an empty id, so the caller cannot reference the workspace it just made")
	}
	if got.CreatedAt.IsZero() {
		t.Error("create response carries a zero created_at")
	}

	listed := do(t, mux, "GET", "/api/workspaces", nil)
	var all []storage.Workspace
	if err := json.Unmarshal(listed.Body.Bytes(), &all); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("want 1 workspace listed, got %d", len(all))
	}
	if all[0].ID != got.ID {
		t.Errorf("listed id %q does not match the id the create call returned (%q)", all[0].ID, got.ID)
	}
}

// The editor's Save button is a PUT. That is the only path the UI offers for
// putting a workflow in a workspace, and it never consulted the quota.
func TestUpdateWorkflowEnforcesWorkspaceQuota(t *testing.T) {
	store, mux := newWorkspaceTestHandler(t)
	store.sources["src-1"] = storage.Source{ID: "src-1", Name: "src", Type: "http"}
	store.sinks["snk-1"] = storage.Sink{ID: "snk-1", Name: "snk", Type: "http"}
	store.workspaces["ws-1"] = storage.Workspace{ID: "ws-1", Name: "Small", MaxWorkflows: 1}
	store.workflows["wf-1"] = validWorkflow("wf-1", "first", "ws-1")
	store.workflows["wf-2"] = validWorkflow("wf-2", "second", "")

	rr := do(t, mux, "PUT", "/api/workflows/wf-2", validWorkflow("wf-2", "second", "ws-1"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("moving a second workflow into a max_workflows=1 workspace: want 403, got %d: %s",
			rr.Code, rr.Body.String())
	}
	if got := store.workflows["wf-2"].WorkspaceID; got != "" {
		t.Errorf("rejected move still wrote workspace_id=%q", got)
	}
}

// The quota is about admission, not about every subsequent edit. A workflow
// already inside a full workspace has to stay editable, or the workspace
// becomes a trap the moment it fills up.
func TestUpdateWorkflowAllowsEditWithinItsOwnFullWorkspace(t *testing.T) {
	store, mux := newWorkspaceTestHandler(t)
	store.sources["src-1"] = storage.Source{ID: "src-1", Name: "src", Type: "http"}
	store.sinks["snk-1"] = storage.Sink{ID: "snk-1", Name: "snk", Type: "http"}
	store.workspaces["ws-1"] = storage.Workspace{ID: "ws-1", Name: "Small", MaxWorkflows: 1}
	store.workflows["wf-1"] = validWorkflow("wf-1", "first", "ws-1")

	rr := do(t, mux, "PUT", "/api/workflows/wf-1", validWorkflow("wf-1", "renamed", "ws-1"))
	if rr.Code != http.StatusOK {
		t.Fatalf("editing a workflow inside its own full workspace: want 200, got %d: %s",
			rr.Code, rr.Body.String())
	}
	if got := store.workflows["wf-1"].Name; got != "renamed" {
		t.Errorf("edit was not applied, name is %q", got)
	}
}

// Deleting a workspace used to leave its members pointing at an id that no
// longer resolves: the list rendered a raw UUID, no filter matched them, and
// the quota checks silently stopped applying because GetWorkspace errored.
func TestDeleteWorkspaceClearsItsMembers(t *testing.T) {
	store, mux := newWorkspaceTestHandler(t)
	store.workspaces["ws-1"] = storage.Workspace{ID: "ws-1", Name: "Doomed"}
	store.workflows["wf-1"] = validWorkflow("wf-1", "first", "ws-1")
	store.workflows["wf-2"] = validWorkflow("wf-2", "second", "ws-1")
	store.workflows["wf-3"] = validWorkflow("wf-3", "outsider", "ws-other")
	store.sources["src-1"] = storage.Source{ID: "src-1", Name: "src", WorkspaceID: "ws-1"}
	store.sinks["snk-1"] = storage.Sink{ID: "snk-1", Name: "snk", WorkspaceID: "ws-1"}

	rr := do(t, mux, "DELETE", "/api/workspaces/ws-1", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d: %s", rr.Code, rr.Body.String())
	}

	for _, id := range []string{"wf-1", "wf-2"} {
		if got := store.workflows[id].WorkspaceID; got != "" {
			t.Errorf("%s still points at the deleted workspace (%q)", id, got)
		}
	}
	if got := store.workflows["wf-3"].WorkspaceID; got != "ws-other" {
		t.Errorf("wf-3 belongs to another workspace and must not be touched, got %q", got)
	}
	if got := store.sources["src-1"].WorkspaceID; got != "" {
		t.Errorf("src-1 still points at the deleted workspace (%q)", got)
	}
	if got := store.sinks["snk-1"].WorkspaceID; got != "" {
		t.Errorf("snk-1 still points at the deleted workspace (%q)", got)
	}
}

// Quotas were write-once: set blind in the create modal, never displayed, never
// editable. Correcting one meant deleting the workspace, which orphaned its
// members.
func TestUpdateWorkspace(t *testing.T) {
	store, mux := newWorkspaceTestHandler(t)
	store.workspaces["ws-1"] = storage.Workspace{ID: "ws-1", Name: "Before", MaxWorkflows: 1}

	rr := do(t, mux, "PUT", "/api/workspaces/ws-1", map[string]any{
		"name":           "After",
		"description":    "roomier",
		"max_workflows":  10,
		"max_cpu":        4.5,
		"max_memory":     2048.0,
		"max_throughput": 500,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: want 200, got %d: %s", rr.Code, rr.Body.String())
	}

	got := store.workspaces["ws-1"]
	if got.Name != "After" || got.Description != "roomier" {
		t.Errorf("name/description not updated: %+v", got)
	}
	if got.MaxWorkflows != 10 || got.MaxCPU != 4.5 || got.MaxMemory != 2048 || got.MaxThroughput != 500 {
		t.Errorf("quotas not updated: %+v", got)
	}
	if got.ID != "ws-1" {
		t.Errorf("update rewrote the id to %q", got.ID)
	}
}

func TestUpdateWorkspaceUnknownIDIs404(t *testing.T) {
	_, mux := newWorkspaceTestHandler(t)
	rr := do(t, mux, "PUT", "/api/workspaces/nope", map[string]any{"name": "x"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// The list page already had row checkboxes and a Batch Actions menu; assigning
// a workspace is the action it was missing.
func TestBatchAssignWorkspace(t *testing.T) {
	store, mux := newWorkspaceTestHandler(t)
	store.sources["src-1"] = storage.Source{ID: "src-1", Name: "src", Type: "http"}
	store.sinks["snk-1"] = storage.Sink{ID: "snk-1", Name: "snk", Type: "http"}
	store.workspaces["ws-1"] = storage.Workspace{ID: "ws-1", Name: "Room for 2", MaxWorkflows: 2}
	store.workflows["wf-1"] = validWorkflow("wf-1", "first", "")
	store.workflows["wf-2"] = validWorkflow("wf-2", "second", "")
	store.workflows["wf-3"] = validWorkflow("wf-3", "third", "")

	rr := do(t, mux, "POST", "/api/workflows/batch/workspace", map[string]any{
		"ids":          []string{"wf-1", "wf-2", "wf-3"},
		"workspace_id": "ws-1",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("batch assign: want 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var results map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode batch results: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("want a result per id, got %d: %v", len(results), results)
	}

	assigned := 0
	for _, id := range []string{"wf-1", "wf-2", "wf-3"} {
		if store.workflows[id].WorkspaceID == "ws-1" {
			assigned++
		}
	}
	if assigned != 2 {
		t.Errorf("max_workflows=2 should have admitted exactly 2, got %d (results: %v)", assigned, results)
	}
}

// Clearing the workspace is how a workflow leaves one, and it must never be
// refused for quota reasons.
func TestBatchAssignEmptyWorkspaceClears(t *testing.T) {
	store, mux := newWorkspaceTestHandler(t)
	store.sources["src-1"] = storage.Source{ID: "src-1", Name: "src", Type: "http"}
	store.sinks["snk-1"] = storage.Sink{ID: "snk-1", Name: "snk", Type: "http"}
	store.workspaces["ws-1"] = storage.Workspace{ID: "ws-1", Name: "Full", MaxWorkflows: 1}
	store.workflows["wf-1"] = validWorkflow("wf-1", "first", "ws-1")

	rr := do(t, mux, "POST", "/api/workflows/batch/workspace", map[string]any{
		"ids":          []string{"wf-1"},
		"workspace_id": "",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("batch clear: want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := store.workflows["wf-1"].WorkspaceID; got != "" {
		t.Errorf("workflow was not removed from its workspace, still %q", got)
	}
}

// failingListStorage reads workspaces fine but cannot list their members.
type failingListStorage struct {
	*mockWorkspaceStorage
}

func (f *failingListStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	if filter.WorkspaceID != "" {
		return nil, 0, errors.New("database is down")
	}
	return f.mockWorkspaceStorage.ListWorkflows(ctx, filter)
}

// A quota that stops being enforced when storage hiccups is not a quota. An
// earlier version swallowed the error so a blip could not block a save, which
// meant any failure to count the members admitted the workflow unchecked.
//
// The status matters as much as the refusal: a database failure reported as
// 403 tells the user their workspace is full when it is not.
func TestAdmissionFailsClosedWhenMembersCannotBeCounted(t *testing.T) {
	store := newMockWorkspaceStorage()
	store.sources["src-1"] = storage.Source{ID: "src-1", Name: "src", Type: "http"}
	store.sinks["snk-1"] = storage.Sink{ID: "snk-1", Name: "snk", Type: "http"}
	store.workspaces["ws-1"] = storage.Workspace{ID: "ws-1", Name: "Small", MaxWorkflows: 1}
	store.workflows["wf-1"] = validWorkflow("wf-1", "first", "")

	broken := &failingListStorage{mockWorkspaceStorage: store}
	h := &WorkflowHandler{Handler: &handlers.Handler{Storage: broken, LogStorage: broken}}
	mux := http.NewServeMux()
	h.RegisterWorkflowRoutes(mux)

	rr := do(t, mux, "PUT", "/api/workflows/wf-1", validWorkflow("wf-1", "first", "ws-1"))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 when the quota cannot be evaluated, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := store.workflows["wf-1"].WorkspaceID; got != "" {
		t.Errorf("workflow was admitted without a quota check, workspace_id=%q", got)
	}
}
