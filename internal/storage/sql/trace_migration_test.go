package sql

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	_ "modernc.org/sqlite"
)

// The upgrade path, on the engine that cannot take it.
//
// message_trace_steps used to carry `id TEXT PRIMARY KEY` and `before_data`.
// This version writes neither. SQLite cannot drop a primary key column at all,
// so on an existing SQLite database the id survives — and it is NOT NULL, so an
// insert that omits it fails. Every trace write would break on upgrade.
//
// The insert has to notice and keep supplying a value. Nothing reads it; this
// is purely about not breaking a database that cannot be narrowed.
func TestInit_KeepsWritingTracesWhenTheLegacyIDCannotBeDropped(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	// Exactly the shape a database upgraded from an earlier release has.
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE message_trace_steps (
		id TEXT PRIMARY KEY,
		message_id TEXT NOT NULL,
		workflow_id TEXT NOT NULL,
		node_id TEXT NOT NULL,
		timestamp TIMESTAMP NOT NULL,
		duration_ms INTEGER,
		before_data TEXT,
		after_data TEXT,
		error TEXT
	)`); err != nil {
		t.Fatalf("creating the legacy table: %v", err)
	}

	s := NewSQLStorage(db, "sqlite")
	if err := s.(interface{ Init(context.Context) error }).Init(t.Context()); err != nil {
		t.Fatalf("Init refused to start against a legacy trace table: %v", err)
	}

	if !s.(*sqlStorage).traceStepsKeepsLegacyID {
		t.Fatal("Init reported the id column gone, but SQLite cannot drop a primary key; " +
			"the narrowed insert will violate NOT NULL on every trace write")
	}

	msg := uuid.New().String()
	if err := s.RecordTraceStep(t.Context(), "wf-1", msg, hermod.TraceStep{
		NodeID: "source", Timestamp: time.Now().UTC(), After: map[string]any{"ok": true},
	}); err != nil {
		t.Fatalf("RecordTraceStep failed against a legacy table: %v", err)
	}

	traces, err := s.ListMessageTraces(t.Context(), "wf-1", storage.TraceFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListMessageTraces: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1; the parent row is written regardless of the "+
			"step table's shape", len(traces))
	}

	if _, err := s.GetMessageTrace(t.Context(), "wf-1", msg); err != nil {
		t.Errorf("GetMessageTrace against a legacy table: %v", err)
	}
}

// A database created by this version has nothing to keep.
func TestInit_FreshDatabaseNeedsNoLegacyIDColumn(t *testing.T) {
	s, _ := newTraceStorage(t)
	if s.(*sqlStorage).traceStepsKeepsLegacyID {
		t.Error("a table created by this version should not carry the legacy id")
	}
}
