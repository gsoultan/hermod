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

	s := NewSQLiteSource(dbPath, []string{"test_table"}, true)
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Initialize DB and create table
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	_, err := s.db.Exec("CREATE TABLE test_table (id INTEGER PRIMARY KEY, name TEXT)")
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}
	_, err = s.db.Exec("INSERT INTO test_table (name) VALUES ('test-name')")
	if err != nil {
		t.Fatalf("failed to insert data: %v", err)
	}

	msg, err := s.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read from SQLiteSource: %v", err)
	}

	if msg.ID() != "sqlite-test_table-1" {
		t.Errorf("expected ID sqlite-test_table-1, got %s", msg.ID())
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

	s := NewSQLiteSource(dbPath, nil, false)
	defer s.Close()

	ctx := context.Background()
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("failed to ping SQLiteSource: %v", err)
	}
}

func TestSQLiteSource_Sample(t *testing.T) {
	dbPath := "test_sample.db"
	defer os.Remove(dbPath)

	s := NewSQLiteSource(dbPath, nil, false)
	defer s.Close()

	ctx := context.Background()
	// Using Ping to initialize
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	_, err := s.db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)")
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	_, err = s.db.Exec("INSERT INTO users (id, name) VALUES (1, 'John Doe')")
	if err != nil {
		t.Fatalf("failed to insert data: %v", err)
	}

	msg, err := s.Sample(ctx, "users")
	if err != nil {
		t.Fatalf("failed to sample table: %v", err)
	}

	data := msg.Data()
	if data["name"] != "John Doe" {
		t.Errorf("expected name John Doe, got %v", data["name"])
	}
}
