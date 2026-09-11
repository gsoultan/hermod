package sql

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
)

// Paging now walks message_traces — one row per traced message — rather than
// aggregating message_trace_steps, so this goes through RecordTraceStep to get
// the parent rows written the way production writes them.
func TestListMessageTraces_Paging(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	ctx := t.Context()
	if err := s.Init(ctx); err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}

	workflowID := uuid.New().String()
	base := time.Now().UTC()

	// Five messages with strictly increasing timestamps, so the newest is
	// returned first.
	messageIDs := make([]string, 5)
	for i := range messageIDs {
		messageIDs[i] = uuid.New().String()
		err := s.RecordTraceStep(ctx, workflowID, messageIDs[i], hermod.TraceStep{
			NodeID:    "node-1",
			Timestamp: base.Add(time.Duration(i) * time.Minute),
			Duration:  10 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("failed to record trace step: %v", err)
		}
	}

	descOrder := []string{messageIDs[4], messageIDs[3], messageIDs[2], messageIDs[1], messageIDs[0]}

	tests := []struct {
		name   string
		limit  int
		offset int
		want   []string
	}{
		{"FirstPage", 2, 0, descOrder[0:2]},
		{"SecondPage", 2, 2, descOrder[2:4]},
		{"LastPartialPage", 2, 4, descOrder[4:5]},
		{"OffsetBeyondEnd", 2, 10, []string{}},
		{"NegativeOffsetClamped", 2, -5, descOrder[0:2]},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			traces, err := s.ListMessageTraces(ctx, workflowID, storage.TraceFilter{
				Limit: tc.limit, Offset: tc.offset,
			})
			if err != nil {
				t.Fatalf("ListMessageTraces failed: %v", err)
			}
			if len(traces) != len(tc.want) {
				t.Fatalf("%s: expected %d traces, got %d", tc.name, len(tc.want), len(traces))
			}
			for i, want := range tc.want {
				if traces[i].MessageID != want {
					t.Errorf("%s: trace[%d] = %s; want %s", tc.name, i, traces[i].MessageID, want)
				}
			}
		})
	}
}
