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
