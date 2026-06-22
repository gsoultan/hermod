package sql

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
)

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
	base := time.Now()

	// Insert 5 distinct message traces with strictly increasing timestamps so
	// the newest message (msg-4) is returned first (ORDER BY start_time DESC).
	messageIDs := make([]string, 5)
	for i := range messageIDs {
		messageIDs[i] = uuid.New().String()
		_, err := db.Exec(`INSERT INTO message_trace_steps (id, message_id, workflow_id, node_id, timestamp, duration_ms, before_data, after_data, error)
			VALUES (?, ?, ?, ?, ?, ?, NULL, NULL, NULL)`,
			uuid.New().String(), messageIDs[i], workflowID, "node-1", base.Add(time.Duration(i)*time.Minute), 10)
		if err != nil {
			t.Fatalf("failed to insert trace step: %v", err)
		}
	}

	// Expected DESC order by start_time: newest (index 4) first.
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
			traces, err := s.ListMessageTraces(ctx, workflowID, tc.limit, tc.offset)
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
