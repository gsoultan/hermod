package sql

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// The trace purge filters on timestamp:
//
//	DELETE FROM message_trace_steps WHERE timestamp < ?
//
// but the only index on the table was (workflow_id, message_id), so the sweep
// was a sequential scan. That went unnoticed for as long as the sweep never ran
// — the duration parse failed on the UI's default "7d" and skipped it. With the
// sweep fixed, an unindexed filter means a full scan of the largest table
// Hermod owns, hourly, once per workflow. audit_logs already has idx_audit_ts
// for exactly this reason.
func TestMessageTraceStepsIndexesTheColumnThePurgeFilters(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	s := NewSQLStorage(db, "sqlite")
	if err := s.(interface{ Init(context.Context) error }).Init(t.Context()); err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}

	rows, err := db.QueryContext(t.Context(),
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND tbl_name = 'message_trace_steps'`)
	if err != nil {
		t.Fatalf("listing indexes: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var indexed bool
	for rows.Next() {
		var ddl sql.NullString
		if err := rows.Scan(&ddl); err != nil {
			t.Fatalf("scanning index ddl: %v", err)
		}
		if strings.Contains(strings.ToLower(ddl.String), "timestamp") {
			indexed = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("listing indexes: %v", err)
	}

	if !indexed {
		t.Error("no index on message_trace_steps(timestamp); the hourly retention sweep " +
			"sequentially scans the whole table, which on a real deployment was 50 GB")
	}
}
