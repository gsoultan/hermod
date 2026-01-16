package sql

import (
	"context"
	"database/sql"
	"testing"

	"github.com/user/hermod/internal/storage"
	_ "modernc.org/sqlite"
)

func TestSQLStorage_ConnectionStatus(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	s := NewSQLStorage(db)
	ctx := context.Background()

	if initer, ok := s.(interface{ Init(context.Context) error }); ok {
		if err := initer.Init(ctx); err != nil {
			t.Fatalf("failed to init storage: %v", err)
		}
	} else {
		t.Fatal("storage does not implement Init")
	}

	conn := storage.Connection{
		ID:       "conn1",
		Name:     "Test Connection",
		SourceID: "src1",
		SinkIDs:  []string{"snk1"},
		Active:   true,
		Status:   "reconnecting",
	}

	if err := s.CreateConnection(ctx, conn); err != nil {
		t.Fatalf("failed to create connection: %v", err)
	}

	got, err := s.GetConnection(ctx, "conn1")
	if err != nil {
		t.Fatalf("failed to get connection: %v", err)
	}

	if got.Status != "reconnecting" {
		t.Errorf("expected status reconnecting, got %s", got.Status)
	}

	conn.Status = "running"
	if err := s.UpdateConnection(ctx, conn); err != nil {
		t.Fatalf("failed to update connection: %v", err)
	}

	got, err = s.GetConnection(ctx, "conn1")
	if err != nil {
		t.Fatalf("failed to get connection: %v", err)
	}

	if got.Status != "running" {
		t.Errorf("expected status running, got %s", got.Status)
	}
}
