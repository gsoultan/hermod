package sql

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	_ "modernc.org/sqlite"
)

func newTraceStorage(t *testing.T) (storage.Storage, *sql.DB) {
	t.Helper()

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
	return s, db
}

func recordSteps(t *testing.T, s storage.Storage, wf, msg string, nodes ...string) {
	t.Helper()
	base := time.Now().UTC().Add(-time.Minute)
	for i, node := range nodes {
		err := s.RecordTraceStep(t.Context(), wf, msg, hermod.TraceStep{
			NodeID:    node,
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Duration:  10 * time.Millisecond,
			After:     map[string]any{"node": node, "seq": i},
		})
		if err != nil {
			t.Fatalf("RecordTraceStep(%s): %v", node, err)
		}
	}
}

// --- Storage -----------------------------------------------------------------

// message_trace_steps is the largest table Hermod owns. Measured on PostgreSQL
// with realistic payloads, 250k rows cost 262 MB; dropping the write-only id
// and the duplicated before_data took it to 123 MB. A column that is written
// and never read is not a rounding error on a table with a row per node per
// message.
//
// Compares what Init creates against what the read query selects, so the next
// write-only column fails here for the same stated reason.
func TestMessageTraceStepsStoresNoColumnItNeverReadsBack(t *testing.T) {
	_, db := newTraceStorage(t)

	stored := map[string]bool{}
	rows, err := db.QueryContext(t.Context(), "PRAGMA table_info(message_trace_steps)")
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scanning table_info: %v", err)
		}
		stored[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading table_info: %v", err)
	}

	cols := traceReadColumns(t, db)

	// workflow_id and message_id are the lookup key rather than projected data.
	for _, c := range append(cols, "workflow_id", "message_id") {
		delete(stored, c)
	}
	for c := range stored {
		t.Errorf("message_trace_steps stores column %q that no read selects; on a table "+
			"with a row per node per message that is disk paid for forever", c)
	}
}

// before_data held the payload as it entered a node — which is exactly the
// after_data of the node before it. Storing both put the whole payload chain in
// twice: measured, half of the table.
func TestGetMessageTrace_ReconstructsBeforeFromThePreviousStep(t *testing.T) {
	s, _ := newTraceStorage(t)
	recordSteps(t, s, "wf-1", "msg-1", "source", "transform", "sink")

	tr, err := s.GetMessageTrace(t.Context(), "wf-1", "msg-1")
	if err != nil {
		t.Fatalf("GetMessageTrace: %v", err)
	}
	if len(tr.Steps) != 3 {
		t.Fatalf("got %d steps, want 3", len(tr.Steps))
	}

	if tr.Steps[0].Before != nil {
		t.Errorf("the first step has no predecessor, so Before should be nil, got %v",
			tr.Steps[0].Before)
	}
	for i := 1; i < len(tr.Steps); i++ {
		before := tr.Steps[i].Before
		if before == nil {
			t.Fatalf("step %d has no Before; it should carry the previous step's After", i)
		}
		if before["node"] != tr.Steps[i-1].After["node"] {
			t.Errorf("step %d Before = %v, want the previous step's After %v",
				i, before, tr.Steps[i-1].After)
		}
	}
}

// --- The parent row ----------------------------------------------------------

// Listing traces used to aggregate every step row the workflow had ever
// produced — SELECT DISTINCT message_id, MIN(timestamp) ... GROUP BY — which no
// index can satisfy. Measured on PostgreSQL 17: a Seq Scan of 250k rows and
// ~390 MB of I/O to return 25 rows, 58.5 ms, growing linearly with the table.
// One row per traced message turns that into an index scan: 0.071 ms, flat.
func TestRecordTraceStep_MaintainsTheParentTraceRow(t *testing.T) {
	s, _ := newTraceStorage(t)
	recordSteps(t, s, "wf-1", "msg-1", "source", "transform", "sink")

	traces, err := s.ListMessageTraces(t.Context(), "wf-1", storage.TraceFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListMessageTraces: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1", len(traces))
	}
	if traces[0].MessageID != "msg-1" {
		t.Errorf("MessageID = %q, want msg-1", traces[0].MessageID)
	}
	if traces[0].StepCount != 3 {
		t.Errorf("StepCount = %d, want 3; the parent row must count every step", traces[0].StepCount)
	}
	if traces[0].CreatedAt.IsZero() {
		t.Error("CreatedAt is zero; the list orders on it")
	}
}

func TestListMessageTraces_NewestFirstAndKeysetPaged(t *testing.T) {
	s, _ := newTraceStorage(t)
	for i := range 5 {
		recordSteps(t, s, "wf-1", fmt.Sprintf("msg-%d", i), "source", "sink")
		time.Sleep(2 * time.Millisecond)
	}

	page1, err := s.ListMessageTraces(t.Context(), "wf-1", storage.TraceFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListMessageTraces: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page 1 returned %d, want 2", len(page1))
	}
	if !page1[0].CreatedAt.After(page1[1].CreatedAt) && !page1[0].CreatedAt.Equal(page1[1].CreatedAt) {
		t.Errorf("page 1 is not newest-first: %v then %v", page1[0].CreatedAt, page1[1].CreatedAt)
	}

	// Keyset: the cursor is the last row seen, not a row count, so the cost does
	// not grow with how deep the reader has gone.
	page2, err := s.ListMessageTraces(t.Context(), "wf-1", storage.TraceFilter{
		Limit: 2, Before: page1[1].CreatedAt,
	})
	if err != nil {
		t.Fatalf("ListMessageTraces(keyset): %v", err)
	}
	if len(page2) == 0 {
		t.Fatal("keyset page 2 was empty")
	}
	for _, tr := range page2 {
		if !tr.CreatedAt.Before(page1[1].CreatedAt) {
			t.Errorf("keyset page 2 returned %v, which is not older than the cursor %v",
				tr.CreatedAt, page1[1].CreatedAt)
		}
	}
}

func TestListMessageTraces_FiltersByWorkflow(t *testing.T) {
	s, _ := newTraceStorage(t)
	recordSteps(t, s, "wf-1", "msg-a", "source")
	recordSteps(t, s, "wf-2", "msg-b", "source")

	traces, err := s.ListMessageTraces(t.Context(), "wf-1", storage.TraceFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListMessageTraces: %v", err)
	}
	if len(traces) != 1 || traces[0].MessageID != "msg-a" {
		t.Errorf("got %+v, want only msg-a", traces)
	}
}

// The parent row is what the list reads, so leaving it behind would show traces
// whose steps the sweep already deleted.
func TestPurgeMessageTraces_RemovesTheParentRowsToo(t *testing.T) {
	s, _ := newTraceStorage(t)
	recordSteps(t, s, "wf-1", "msg-1", "source", "sink")

	if err := s.PurgeMessageTraces(t.Context(), time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("PurgeMessageTraces: %v", err)
	}

	traces, err := s.ListMessageTraces(t.Context(), "wf-1", storage.TraceFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListMessageTraces: %v", err)
	}
	if len(traces) != 0 {
		t.Errorf("purge left %d parent rows pointing at steps it deleted", len(traces))
	}
}

// --- Bounding one step -------------------------------------------------------

// A trace is a diagnostic, not an archive. One oversized message must not be
// able to write an unbounded row, and the marker has to stay valid JSON or the
// viewer cannot render it.
func TestRecordTraceStep_CapsAnOversizedPayload(t *testing.T) {
	t.Setenv("HERMOD_TRACE_MAX_PAYLOAD_BYTES", "256")

	s, _ := newTraceStorage(t)
	big := make([]byte, 4096)
	for i := range big {
		big[i] = 'x'
	}
	err := s.RecordTraceStep(t.Context(), "wf-1", "msg-1", hermod.TraceStep{
		NodeID:    "source",
		Timestamp: time.Now().UTC(),
		After:     map[string]any{"blob": string(big)},
	})
	if err != nil {
		t.Fatalf("RecordTraceStep: %v", err)
	}

	tr, err := s.GetMessageTrace(t.Context(), "wf-1", "msg-1")
	if err != nil {
		t.Fatalf("GetMessageTrace: %v", err)
	}
	after := tr.Steps[0].After
	if after == nil {
		t.Fatal("After is nil, want a JSON object carrying the truncation marker")
	}
	if after["_hermod_truncated"] != true {
		t.Errorf("After = %v, want a _hermod_truncated marker", after)
	}
}

// traceReadColumns is what the trace detail query projects.
func traceReadColumns(t *testing.T, db *sql.DB) []string {
	t.Helper()

	rows, err := db.QueryContext(t.Context(), commonQueries[QueryGetMessageTrace], "wf", "msg")
	if err != nil {
		t.Fatalf("running the trace read query: %v", err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("reading result columns: %v", err)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the trace read query: %v", err)
	}
	return cols
}
