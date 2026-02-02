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
	defer db.Close()

	s := NewSQLStorage(db, "sqlite")
	ctx := context.Background()

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
	defer db.Close()

	s := NewSQLStorage(db, "sqlite")
	ctx := context.Background()

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
