package sqlite

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/user/hermod"
)

func TestSQLiteSource_Read(t *testing.T) {
	dbPath := "test.db"
	defer os.Remove(dbPath)

	s := NewSQLiteSource(dbPath, []string{"test_table"})
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	msg, err := s.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read from SQLiteSource: %v", err)
	}

	if msg.ID() != "sqlite-1" {
		t.Errorf("expected ID sqlite-1, got %s", msg.ID())
	}

	if msg.Operation() != hermod.OpCreate {
		t.Errorf("expected operation create, got %s", msg.Operation())
	}

	if msg.Table() != "test_table" {
		t.Errorf("expected table test_table, got %s", msg.Table())
	}

	if msg.Metadata()["source"] != "sqlite" {
		t.Errorf("expected source sqlite, got %s", msg.Metadata()["source"])
	}
}

func TestSQLiteSource_Ping(t *testing.T) {
	dbPath := "test_ping.db"
	defer os.Remove(dbPath)

	s := NewSQLiteSource(dbPath, nil)
	defer s.Close()

	ctx := context.Background()
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("failed to ping SQLiteSource: %v", err)
	}
}
