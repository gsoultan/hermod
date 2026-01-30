package batchsql

import (
	"context"
	"database/sql"
	"testing"
	"time"

	pkgengine "github.com/user/hermod/pkg/engine"
	_ "modernc.org/sqlite"
)

type mockDBProvider struct {
	db *sql.DB
}

func (m *mockDBProvider) GetOrOpenDBByID(ctx context.Context, id string) (*sql.DB, string, error) {
	return m.db, "sqlite", nil
}

func TestBatchSQLSource(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, name TEXT)")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO test (name) VALUES ('test1'), ('test2')")
	if err != nil {
		t.Fatal(err)
	}

	provider := &mockDBProvider{db: db}
	config := Config{
		SourceID:          "test-source",
		Cron:              "* * * * * *", // Every second
		Queries:           "SELECT * FROM test WHERE id > '{{.last_value}}' OR '{{.last_value}}' = '' ORDER BY id",
		IncrementalColumn: "id",
	}

	source := NewBatchSQLSource(provider, config)
	source.SetLogger(pkgengine.NewDefaultLogger())
	defer source.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// First Read should start the cron and fetch both rows
	msg, err := source.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if msg.Data()["name"] != "test1" {
		t.Errorf("expected name test1, got %v", msg.Data()["name"])
	}

	msg2, err := source.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if msg2.Data()["name"] != "test2" {
		t.Errorf("expected name test2, got %v", msg2.Data()["name"])
	}

	// Verify state was updated
	state := source.GetState()
	if state["last_value"] != "2" {
		t.Errorf("expected last_value 2, got %s", state["last_value"])
	}

	// Insert more data
	_, err = db.Exec("INSERT INTO test (name) VALUES ('test3')")
	if err != nil {
		t.Fatal(err)
	}

	// Next read should fetch test3
	msg3, err := source.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if msg3.Data()["name"] != "test3" {
		t.Errorf("expected name test3, got %v", msg3.Data()["name"])
	}

	if source.GetState()["last_value"] != "3" {
		t.Errorf("expected last_value 3, got %s", source.GetState()["last_value"])
	}
}
