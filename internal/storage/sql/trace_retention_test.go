package sql

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// The trace purge filters (workflow_id, timestamp):
//
//	DELETE FROM message_trace_steps WHERE workflow_id = ? AND timestamp < ?
//
// so that is the index it needs. An index on timestamp alone — which is what
// this table carried while the purge was unscoped — makes the scoped delete
// read every workflow's expired rows to find one workflow's, and an index on
// (workflow_id, message_id) alone makes it read all of one workflow's history
// to find the expired part.
//
// The history: the only index was (workflow_id, message_id), so the sweep was a
// sequential scan. That went unnoticed for as long as the sweep never ran — the
// duration parse failed on the UI's default "7d" and skipped it. audit_logs has
// idx_audit_ts for the same reason.
func TestMessageTraceStepsIndexesTheColumnsThePurgeFilters(t *testing.T) {
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

	var coversPurge, timestampOnly bool
	for rows.Next() {
		var ddl sql.NullString
		if err := rows.Scan(&ddl); err != nil {
			t.Fatalf("scanning index ddl: %v", err)
		}
		cols, ok := indexColumns(ddl.String)
		if !ok {
			continue
		}
		if len(cols) >= 2 && cols[0] == "workflow_id" && cols[1] == "timestamp" {
			coversPurge = true
		}
		if len(cols) == 1 && cols[0] == "timestamp" {
			timestampOnly = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("listing indexes: %v", err)
	}

	if !coversPurge {
		t.Error("no index leading (workflow_id, timestamp) on message_trace_steps; the " +
			"hourly retention sweep scans far more than the rows it deletes, and on a " +
			"real deployment this table reached 50 GB")
	}
	if timestampOnly {
		t.Error("message_trace_steps still carries an index on timestamp alone; it matched " +
			"the unscoped delete that a per-workflow one replaced, and is now maintained " +
			"on every trace write for a query nothing issues")
	}
}

// indexColumns pulls the column list out of a CREATE INDEX statement.
func indexColumns(ddl string) ([]string, bool) {
	open := strings.LastIndex(ddl, "(")
	close := strings.LastIndex(ddl, ")")
	if open == -1 || close <= open {
		return nil, false
	}
	var cols []string
	for _, c := range strings.Split(ddl[open+1:close], ",") {
		f := strings.Fields(strings.TrimSpace(strings.ToLower(c)))
		if len(f) > 0 {
			cols = append(cols, strings.Trim(f[0], `"`+"`"))
		}
	}
	return cols, len(cols) > 0
}
