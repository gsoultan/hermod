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

// ScanRows stops at the cap, which is right for a SELECT nobody asked to read a
// million rows of. A statement that writes and returns what it wrote is a
// different case: stopping means closing a cursor the write is still feeding,
// and the count of rows written is then unknowable. ScanRowsCounted keeps the
// same bounded set and reads the rest only to count it.
func TestScanRowsCountedReadsToTheEndAndKeepsTheCap(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(t.Context(), "CREATE TABLE test (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	const total = DefaultMaxRows + 7
	// In a closure so the cursor is closed before the count below: this is one
	// connection, and an in-memory SQLite database belongs to its connection.
	kept, n := func() ([]map[string]any, int64) {
		rows, err := db.QueryContext(t.Context(), `INSERT INTO test (id)
			SELECT x FROM (
				WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c WHERE x < ?)
				SELECT x FROM c
			) RETURNING id`, total)
		if err != nil {
			t.Fatalf("failed to insert: %v", err)
		}
		defer rows.Close()

		kept, n, err := ScanRowsCounted(rows)
		if err != nil {
			t.Fatalf("ScanRowsCounted failed: %v", err)
		}
		return kept, n
	}()

	if len(kept) != DefaultMaxRows {
		t.Errorf("kept %d rows, want the cap of %d", len(kept), DefaultMaxRows)
	}
	if n != total {
		t.Errorf("counted %d rows, want %d", n, total)
	}
	if kept[0]["id"].(int64) != 1 {
		t.Errorf("first kept row = %v, want id 1", kept[0])
	}

	var written int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM test").Scan(&written); err != nil {
		t.Fatalf("count: %v", err)
	}
	if written != total {
		t.Errorf("the statement wrote %d rows, want %d", written, total)
	}
}
