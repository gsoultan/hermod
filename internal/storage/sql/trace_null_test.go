package sql

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestGetMessageTrace_NullValues(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	ctx := context.Background()
	if err := s.Init(ctx); err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}

	workflowID := uuid.New().String()
	messageID := uuid.New().String()
	traceID := uuid.New().String()

	// Insert a trace step with NULL values for before_data, after_data, and error
	_, err = db.Exec(`INSERT INTO message_trace_steps (id, message_id, workflow_id, node_id, timestamp, duration_ms, before_data, after_data, error)
		VALUES (?, ?, ?, ?, ?, ?, NULL, NULL, NULL)`,
		traceID, messageID, workflowID, "node-1", time.Now(), 100)
	if err != nil {
		t.Fatalf("failed to insert trace step: %v", err)
	}

	// This should fail before the fix
	trace, err := s.GetMessageTrace(ctx, workflowID, messageID)
	if err != nil {
		t.Errorf("GetMessageTrace failed: %v", err)
	}

	if len(trace.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(trace.Steps))
	}
}
