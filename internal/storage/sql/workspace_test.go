package sql

import (
	"errors"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"

	_ "modernc.org/sqlite"
)

// Workspaces are the one governance object with no edit path and no cleanup on
// delete. These cover the two storage methods that fix that.
//
// sqlite is the harness here, as in every other test in this package. It is
// enough for these assertions because they are about which rows a statement
// touches, not about ordering or collation — the two things sqlite is known to
// disagree with PostgreSQL on.

func seedWorkspace(t *testing.T, s storage.Storage, ws storage.Workspace) {
	t.Helper()
	if err := s.CreateWorkspace(t.Context(), ws); err != nil {
		t.Fatalf("create workspace %s: %v", ws.ID, err)
	}
}

func mustCreateWorkflow(t *testing.T, s storage.Storage, wf storage.Workflow) {
	t.Helper()
	if err := s.CreateWorkflow(t.Context(), wf); err != nil {
		t.Fatalf("create workflow %s: %v", wf.ID, err)
	}
}

func TestUpdateWorkspaceRewritesQuotasAndKeepsIdentity(t *testing.T) {
	s, _ := workflowStore(t)
	seedWorkspace(t, s, storage.Workspace{
		ID: "ws-1", Name: "Before", Description: "old", MaxWorkflows: 1,
	})

	created, err := s.GetWorkspace(t.Context(), "ws-1")
	if err != nil {
		t.Fatalf("get after create: %v", err)
	}

	err = s.UpdateWorkspace(t.Context(), storage.Workspace{
		ID: "ws-1", Name: "After", Description: "new",
		MaxWorkflows: 10, MaxCPU: 4.5, MaxMemory: 2048, MaxThroughput: 500,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := s.GetWorkspace(t.Context(), "ws-1")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Name != "After" || got.Description != "new" {
		t.Errorf("name/description not written: %+v", got)
	}
	if got.MaxWorkflows != 10 || got.MaxCPU != 4.5 || got.MaxMemory != 2048 || got.MaxThroughput != 500 {
		t.Errorf("quotas not written: %+v", got)
	}
	// A rename that moved the id would orphan every member keyed on it, and a
	// rewritten created_at would turn the row's birth into its last touch.
	if got.ID != "ws-1" {
		t.Errorf("id changed to %q", got.ID)
	}
	if !got.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("created_at moved from %v to %v", created.CreatedAt, got.CreatedAt)
	}
}

// UPDATE ... WHERE id = ? on a missing row is a silent no-op, so without an
// explicit existence check the handler would answer 200 for a workspace that
// does not exist.
func TestUpdateWorkspaceUnknownIDIsNotFound(t *testing.T) {
	s, _ := workflowStore(t)
	err := s.UpdateWorkspace(t.Context(), storage.Workspace{ID: "nope", Name: "x"})
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("want storage.ErrNotFound, got %v", err)
	}
}

// workspace_id lives on three tables with no foreign key, so deleting the
// workspace row on its own leaves references that resolve to nothing.
func TestClearWorkspaceAssignmentsSweepsAllThreeTables(t *testing.T) {
	s, _ := workflowStore(t)
	seedWorkspace(t, s, storage.Workspace{ID: "ws-1", Name: "Doomed"})
	seedWorkspace(t, s, storage.Workspace{ID: "ws-2", Name: "Bystander"})

	mustCreateWorkflow(t, s, storage.Workflow{ID: "wf-1", Name: "a", WorkspaceID: "ws-1"})
	mustCreateWorkflow(t, s, storage.Workflow{ID: "wf-2", Name: "b", WorkspaceID: "ws-1"})
	mustCreateWorkflow(t, s, storage.Workflow{ID: "wf-3", Name: "c", WorkspaceID: "ws-2"})

	if err := s.CreateSource(t.Context(), storage.Source{ID: "src-1", Name: "s", Type: "http", WorkspaceID: "ws-1"}); err != nil {
		t.Fatalf("create source: %v", err)
	}
	if err := s.CreateSink(t.Context(), storage.Sink{ID: "snk-1", Name: "k", Type: "http", WorkspaceID: "ws-1"}); err != nil {
		t.Fatalf("create sink: %v", err)
	}

	n, err := s.ClearWorkspaceAssignments(t.Context(), "ws-1")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if n != 4 {
		t.Errorf("want 4 rows cleared (2 workflows, 1 source, 1 sink), got %d", n)
	}

	for _, id := range []string{"wf-1", "wf-2"} {
		wf, err := s.GetWorkflow(t.Context(), id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if wf.WorkspaceID != "" {
			t.Errorf("%s still assigned to %q", id, wf.WorkspaceID)
		}
	}

	// The sweep is keyed on the workspace, so a member of another one must
	// survive it untouched.
	other, err := s.GetWorkflow(t.Context(), "wf-3")
	if err != nil {
		t.Fatalf("get wf-3: %v", err)
	}
	if other.WorkspaceID != "ws-2" {
		t.Errorf("wf-3 belongs to ws-2 but came back %q", other.WorkspaceID)
	}

	src, err := s.GetSource(t.Context(), "src-1")
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if src.WorkspaceID != "" {
		t.Errorf("src-1 still assigned to %q", src.WorkspaceID)
	}
	snk, err := s.GetSink(t.Context(), "snk-1")
	if err != nil {
		t.Fatalf("get sink: %v", err)
	}
	if snk.WorkspaceID != "" {
		t.Errorf("snk-1 still assigned to %q", snk.WorkspaceID)
	}
}

// An empty id would otherwise match every row that has no workspace and report
// them as cleared.
func TestClearWorkspaceAssignmentsIgnoresTheEmptyWorkspace(t *testing.T) {
	s, _ := workflowStore(t)
	mustCreateWorkflow(t, s, storage.Workflow{ID: "wf-1", Name: "a"})

	n, err := s.ClearWorkspaceAssignments(t.Context(), "")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if n != 0 {
		t.Errorf("want 0 rows cleared for an empty workspace id, got %d", n)
	}
}

// Sources and sinks carry workspace_id and index it, but only the workflow list
// ever read the query parameter, so neither could be filtered by workspace.
func TestListSourcesAndSinksFilterByWorkspace(t *testing.T) {
	s, _ := workflowStore(t)
	for _, src := range []storage.Source{
		{ID: "src-1", Name: "in", Type: "http", WorkspaceID: "ws-1"},
		{ID: "src-2", Name: "out", Type: "http", WorkspaceID: "ws-2"},
	} {
		if err := s.CreateSource(t.Context(), src); err != nil {
			t.Fatalf("create source %s: %v", src.ID, err)
		}
	}
	for _, snk := range []storage.Sink{
		{ID: "snk-1", Name: "in", Type: "http", WorkspaceID: "ws-1"},
		{ID: "snk-2", Name: "out", Type: "http", WorkspaceID: "ws-2"},
	} {
		if err := s.CreateSink(t.Context(), snk); err != nil {
			t.Fatalf("create sink %s: %v", snk.ID, err)
		}
	}

	srcs, total, err := s.ListSources(t.Context(), storage.CommonFilter{WorkspaceID: "ws-1"})
	if err != nil {
		t.Fatalf("list sources: %v", err)
	}
	if total != 1 || len(srcs) != 1 || srcs[0].ID != "src-1" {
		t.Errorf("want only src-1, got %v (total %d)", sourceIDs(srcs), total)
	}

	snks, total, err := s.ListSinks(t.Context(), storage.CommonFilter{WorkspaceID: "ws-1"})
	if err != nil {
		t.Fatalf("list sinks: %v", err)
	}
	if total != 1 || len(snks) != 1 || snks[0].ID != "snk-1" {
		t.Errorf("want only snk-1, got %v (total %d)", sinkIDs(snks), total)
	}
}
