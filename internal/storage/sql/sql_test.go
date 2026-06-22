package sql

import (
	"context"
	"database/sql"
	"testing"

	"github.com/user/hermod/internal/storage"
	_ "modernc.org/sqlite"
)

func TestSQLStorage_WorkflowStatus(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	s := NewSQLStorage(db, "sqlite")
	ctx := t.Context()

	if initer, ok := s.(interface{ Init(context.Context) error }); ok {
		if err := initer.Init(ctx); err != nil {
			t.Fatalf("failed to init storage: %v", err)
		}
	} else {
		t.Fatal("storage does not implement Init")
	}

	wf := storage.Workflow{
		ID:     "wf1",
		Name:   "Test Workflow",
		Active: true,
		Status: "reconnecting",
	}

	if err := s.CreateWorkflow(ctx, wf); err != nil {
		t.Fatalf("failed to create workflow: %v", err)
	}

	got, err := s.GetWorkflow(ctx, "wf1")
	if err != nil {
		t.Fatalf("failed to get workflow: %v", err)
	}

	if got.Status != "reconnecting" {
		t.Errorf("expected status reconnecting, got %s", got.Status)
	}

	wf.Status = "running"
	if err := s.UpdateWorkflow(ctx, wf); err != nil {
		t.Fatalf("failed to update workflow: %v", err)
	}

	got, err = s.GetWorkflow(ctx, "wf1")
	if err != nil {
		t.Fatalf("failed to get workflow: %v", err)
	}

	if got.Status != "running" {
		t.Errorf("expected status running, got %s", got.Status)
	}
}

func TestSQLStorage_ListAllSchemas(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	s := NewSQLStorage(db, "sqlite")
	ctx := t.Context()

	if err := s.Init(ctx); err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}

	// Create some schemas
	schemas := []storage.Schema{
		{ID: "1", Name: "schema1", Version: 1, Type: "json", Content: "{}"},
		{ID: "2", Name: "schema1", Version: 2, Type: "json", Content: "{}"},
		{ID: "3", Name: "schema2", Version: 1, Type: "json", Content: "{}"},
	}

	for _, sc := range schemas {
		if err := s.CreateSchema(ctx, sc); err != nil {
			t.Fatalf("failed to create schema: %v", err)
		}
	}

	got, err := s.ListAllSchemas(ctx)
	if err != nil {
		t.Fatalf("failed to list all schemas: %v", err)
	}

	if len(got) != 2 {
		t.Errorf("expected 2 schemas, got %d", len(got))
	}

	// Check if we got the latest versions
	for _, sc := range got {
		if sc.Name == "schema1" && sc.Version != 2 {
			t.Errorf("expected schema1 version 2, got %d", sc.Version)
		}
		if sc.Name == "schema2" && sc.Version != 1 {
			t.Errorf("expected schema2 version 1, got %d", sc.Version)
		}
	}
}

func TestSQLStorage_VHosts(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	s := NewSQLStorage(db, "sqlite")
	ctx := t.Context()

	if err := s.Init(ctx); err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}

	vh := storage.VHost{
		ID:          "vh1",
		Name:        "Test VHost",
		Description: "A test vhost",
	}

	if err := s.CreateVHost(ctx, vh); err != nil {
		t.Fatalf("failed to create vhost: %v", err)
	}

	got, err := s.GetVHost(ctx, "vh1")
	if err != nil {
		t.Fatalf("failed to get vhost: %v", err)
	}

	if got.Name != "Test VHost" {
		t.Errorf("expected name Test VHost, got %s", got.Name)
	}

	vh.Description = "Updated description"
	if err := s.UpdateVHost(ctx, vh); err != nil {
		t.Fatalf("failed to update vhost: %v", err)
	}

	got, err = s.GetVHost(ctx, "vh1")
	if err != nil {
		t.Fatalf("failed to get vhost: %v", err)
	}

	if got.Description != "Updated description" {
		t.Errorf("expected description Updated description, got %s", got.Description)
	}

	if err := s.DeleteVHost(ctx, "vh1"); err != nil {
		t.Fatalf("failed to delete vhost: %v", err)
	}

	_, err = s.GetVHost(ctx, "vh1")
	if err == nil {
		t.Fatal("expected error getting deleted vhost, got nil")
	}
}

// TestSQLStorage_ListWorkflowVersions verifies that listing workflow versions
// returns lightweight metadata (sorted by version descending) without the heavy
// nodes/edges/config payloads, while GetWorkflowVersion still returns the full
// payload. This guards against regressing the fix for the slow /versions endpoint.
func TestSQLStorage_ListWorkflowVersions(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	s := NewSQLStorage(db, "sqlite")
	ctx := t.Context()

	if err := s.Init(ctx); err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}

	heavyNodes := []storage.WorkflowNode{{ID: "n1", Type: "source"}, {ID: "n2", Type: "sink"}}
	heavyEdges := []storage.WorkflowEdge{{ID: "e1", SourceID: "n1", TargetID: "n2"}}

	for _, v := range []storage.WorkflowVersion{
		{ID: "v1", WorkflowID: "wf1", Version: 1, Nodes: heavyNodes, Edges: heavyEdges, Config: `{"name":"a"}`, CreatedBy: "alice", Message: "first"},
		{ID: "v2", WorkflowID: "wf1", Version: 2, Nodes: heavyNodes, Edges: heavyEdges, Config: `{"name":"b"}`, CreatedBy: "bob", Message: "second"},
	} {
		if err := s.CreateWorkflowVersion(ctx, v); err != nil {
			t.Fatalf("failed to create workflow version %d: %v", v.Version, err)
		}
	}

	versions, err := s.ListWorkflowVersions(ctx, "wf1")
	if err != nil {
		t.Fatalf("failed to list workflow versions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}

	// Must be sorted by version descending.
	if versions[0].Version != 2 || versions[1].Version != 1 {
		t.Errorf("expected versions sorted desc [2,1], got [%d,%d]", versions[0].Version, versions[1].Version)
	}

	// Metadata must be populated, heavy payloads must be omitted from the list.
	for _, v := range versions {
		if v.CreatedBy == "" || v.Message == "" {
			t.Errorf("version %d: expected metadata to be populated, got created_by=%q message=%q", v.Version, v.CreatedBy, v.Message)
		}
		if len(v.Nodes) != 0 || len(v.Edges) != 0 || v.Config != "" {
			t.Errorf("version %d: expected lightweight list (no nodes/edges/config), got nodes=%d edges=%d config=%q",
				v.Version, len(v.Nodes), len(v.Edges), v.Config)
		}
	}

	// The full payload must still be retrievable via GetWorkflowVersion.
	full, err := s.GetWorkflowVersion(ctx, "wf1", 2)
	if err != nil {
		t.Fatalf("failed to get workflow version: %v", err)
	}
	if len(full.Nodes) != len(heavyNodes) || len(full.Edges) != len(heavyEdges) || full.Config == "" {
		t.Errorf("expected full payload from GetWorkflowVersion, got nodes=%d edges=%d config=%q",
			len(full.Nodes), len(full.Edges), full.Config)
	}
}

// TestSQLStorage_VHostEmptyIDBackfill verifies that vhosts inserted directly
// into the database with an empty/NULL id (e.g. via a SQL console) get a stable
// id backfilled on Init so they can be edited and deleted from the UI.
func TestSQLStorage_VHostEmptyIDBackfill(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	s := NewSQLStorage(db, "sqlite")
	ctx := t.Context()

	if err := s.Init(ctx); err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}

	// Simulate a vhost row created outside the application without an id.
	if _, err := db.ExecContext(ctx,
		"INSERT INTO vhosts (id, name, description) VALUES ('', 'default', 'orphan vhost')"); err != nil {
		t.Fatalf("failed to insert empty-id vhost: %v", err)
	}

	// Re-run Init to trigger the idempotent backfill migration.
	if err := s.Init(ctx); err != nil {
		t.Fatalf("failed to re-init storage: %v", err)
	}

	got, err := s.GetVHost(ctx, "default")
	if err != nil {
		t.Fatalf("expected to fetch backfilled vhost by name as id: %v", err)
	}
	if got.ID != "default" {
		t.Errorf("expected backfilled id 'default', got %q", got.ID)
	}

	// The backfilled vhost must now be editable (UpdateVHost keys on id).
	got.Description = "now editable"
	if err := s.UpdateVHost(ctx, got); err != nil {
		t.Fatalf("failed to update backfilled vhost: %v", err)
	}
	updated, err := s.GetVHost(ctx, "default")
	if err != nil {
		t.Fatalf("failed to re-fetch vhost: %v", err)
	}
	if updated.Description != "now editable" {
		t.Errorf("expected description 'now editable', got %q", updated.Description)
	}
}
