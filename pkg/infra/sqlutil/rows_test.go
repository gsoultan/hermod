package sqlutil

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestScanRows(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	_, err = db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, name TEXT, age INTEGER)")
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	_, err = db.Exec("INSERT INTO test (name, age) VALUES (?, ?), (?, ?)", "Alice", 30, "Bob", 25)
	if err != nil {
		t.Fatalf("failed to insert data: %v", err)
	}

	rows, err := db.Query("SELECT * FROM test ORDER BY id")
	if err != nil {
		t.Fatalf("failed to query data: %v", err)
	}
	defer rows.Close()

	results, err := ScanRows(rows)
	if err != nil {
		t.Fatalf("ScanRows failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	if results[0]["name"] != "Alice" || results[0]["age"].(int64) != 30 {
		t.Errorf("unexpected result for Alice: %v", results[0])
	}

	if results[1]["name"] != "Bob" || results[1]["age"].(int64) != 25 {
		t.Errorf("unexpected result for Bob: %v", results[1])
	}
}
