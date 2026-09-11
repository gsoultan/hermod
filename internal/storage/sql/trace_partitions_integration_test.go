package sql

import (
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Partitioning is PostgreSQL-only and entirely invisible to SQLite, so the
// unit tests above can check the naming and the expiry arithmetic and nothing
// else. Whether Init actually produces a partitioned table, whether a trace
// lands in the right child, and whether the sweep drops whole days rather than
// deleting rows, can only be answered by PostgreSQL.
func TestTracePartitioning_OnPostgres(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to enable")
	}

	ctx := t.Context()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("POSTGRES_DSN names a server that could not be opened (%s): %v", dsn, err)
	}
	t.Cleanup(func() { _ = db.Close() })

	st := NewSQLStorage(db, "pgx")
	if err := st.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	s := st.(*sqlStorage)

	if !s.traceStepsIsPartitioned(ctx) {
		t.Fatal("Init created message_trace_steps unpartitioned on PostgreSQL; retention " +
			"falls back to a range delete, which on this table is the 50 GB problem")
	}

	// DEFAULT must exist, or an insert outside every range fails outright.
	var defaultExists bool
	if err := db.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_class WHERE relname = $1)", tracePartitionDefault).
		Scan(&defaultExists); err != nil {
		t.Fatalf("checking the default partition: %v", err)
	}
	if !defaultExists {
		t.Error("no DEFAULT partition; a lagging maintenance run would start failing inserts")
	}

	// A trace written today must round-trip through the parent table.
	now := time.Now().UTC()
	if err := s.RecordTraceStep(ctx, "wf-part", "msg-part", hermod.TraceStep{
		NodeID: "source", Timestamp: now, Duration: time.Millisecond,
		After: map[string]any{"ok": true},
	}); err != nil {
		t.Fatalf("RecordTraceStep: %v", err)
	}
	traces, err := s.ListMessageTraces(ctx, "wf-part", storage.TraceFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListMessageTraces: %v", err)
	}
	if len(traces) != 1 || traces[0].MessageID != "msg-part" {
		t.Fatalf("got %+v, want one trace for msg-part", traces)
	}

	// The parent upsert is dialect-specific — ON CONFLICT here, ON DUPLICATE KEY
	// on MySQL, MERGE on SQL Server — and only its INSERT branch has run so
	// far. A second step for the same message takes the UPDATE branch, which is
	// what keeps step_count honest.
	for i, node := range []string{"transform", "sink"} {
		if err := s.RecordTraceStep(ctx, "wf-part", "msg-part", hermod.TraceStep{
			NodeID: node, Timestamp: now.Add(time.Duration(i+1) * time.Second),
			Duration: 2 * time.Millisecond, After: map[string]any{"node": node},
		}); err != nil {
			t.Fatalf("RecordTraceStep(%s): %v", node, err)
		}
	}
	traces, err = s.ListMessageTraces(ctx, "wf-part", storage.TraceFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListMessageTraces after more steps: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("the upsert created %d rows for one message; ON CONFLICT did not match", len(traces))
	}
	if traces[0].StepCount != 3 {
		t.Errorf("StepCount = %d, want 3; the ON CONFLICT UPDATE branch is not accumulating",
			traces[0].StepCount)
	}

	// And Before must be reconstructed from the preceding step's After.
	full, err := s.GetMessageTrace(ctx, "wf-part", "msg-part")
	if err != nil {
		t.Fatalf("GetMessageTrace: %v", err)
	}
	if len(full.Steps) != 3 {
		t.Fatalf("got %d steps, want 3", len(full.Steps))
	}
	if full.Steps[0].Before != nil {
		t.Error("the first step has no predecessor, so Before should be nil")
	}
	if full.Steps[2].Before["node"] != "transform" {
		t.Errorf("step 2 Before = %v, want the transform step's After", full.Steps[2].Before)
	}

	// An old day must go by DROP TABLE, not by deleting its rows.
	old := now.AddDate(0, 0, -30).Truncate(24 * time.Hour)
	oldPart := tracePartitionName(old)
	if _, err := db.ExecContext(ctx,
		"CREATE TABLE IF NOT EXISTS "+oldPart+
			" PARTITION OF message_trace_steps FOR VALUES FROM ('"+
			old.Format("2006-01-02 15:04:05")+"') TO ('"+
			old.AddDate(0, 0, 1).Format("2006-01-02 15:04:05")+"')"); err != nil {
		t.Fatalf("creating an old partition: %v", err)
	}

	dropped, err := s.dropTracePartitionsBefore(ctx, now.AddDate(0, 0, -7))
	if err != nil {
		t.Fatalf("dropTracePartitionsBefore: %v", err)
	}
	if dropped == 0 {
		t.Error("the expired day was not dropped; retention is still a range delete")
	}

	var stillThere bool
	if err := db.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_class WHERE relname = $1)", oldPart).Scan(&stillThere); err != nil {
		t.Fatalf("checking the dropped partition: %v", err)
	}
	if stillThere {
		t.Errorf("%s survived the sweep", oldPart)
	}

	// Today's trace must be untouched by that sweep.
	traces, err = s.ListMessageTraces(ctx, "wf-part", storage.TraceFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListMessageTraces after drop: %v", err)
	}
	if len(traces) != 1 {
		t.Errorf("dropping expired days removed a trace inside the window: got %d, want 1", len(traces))
	}
	if traces[0].StepCount != 3 {
		t.Errorf("StepCount = %d after the sweep, want 3", traces[0].StepCount)
	}
}

// The upgrade path on the engine that can take it.
//
// PostgreSQL can drop both legacy columns, so an upgraded database ends up with
// the narrow table and stops paying for them. This is the path the 50 GB
// deployment actually takes, and the one that must not leave the id behind: it
// is NOT NULL, and the narrowed insert omits it.
func TestNarrowTraceSteps_DropsLegacyColumnsOnPostgres(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to enable")
	}

	ctx := t.Context()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("POSTGRES_DSN names a server that could not be opened (%s): %v", dsn, err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Start from the shape an earlier release left behind, with a row in it.
	for _, stmt := range []string{
		"DROP TABLE IF EXISTS message_trace_steps CASCADE",
		`CREATE TABLE message_trace_steps (
			id TEXT PRIMARY KEY, message_id TEXT NOT NULL, workflow_id TEXT NOT NULL,
			node_id TEXT NOT NULL, timestamp TIMESTAMP NOT NULL, duration_ms INTEGER,
			before_data TEXT, after_data TEXT, error TEXT)`,
		`INSERT INTO message_trace_steps VALUES
			('legacy-1','msg-legacy','wf-legacy','source', now(), 1, '{"in":1}', '{"out":1}', NULL)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("preparing the legacy table (%s): %v", stmt, err)
		}
	}

	st := NewSQLStorage(db, "pgx")
	if err := st.Init(ctx); err != nil {
		t.Fatalf("Init against a legacy trace table: %v", err)
	}
	s := st.(*sqlStorage)

	if s.traceStepsKeepsLegacyID {
		t.Error("the id column survived on PostgreSQL, which can drop it; the table " +
			"keeps paying for a UUID nothing reads")
	}
	if s.traceStepsHasLegacyID(ctx) {
		t.Error("id was not dropped")
	}
	if s.traceStepsHasBeforeData(ctx) {
		t.Error("before_data was not dropped")
	}

	// The pre-existing row survives the narrowing, and writes still work.
	var kept int
	if err := db.QueryRowContext(ctx,
		"SELECT count(*) FROM message_trace_steps WHERE message_id = 'msg-legacy'").Scan(&kept); err != nil {
		t.Fatalf("counting kept rows: %v", err)
	}
	if kept != 1 {
		t.Errorf("the migration lost the existing trace row: got %d, want 1", kept)
	}
	if err := s.RecordTraceStep(ctx, "wf-legacy", "msg-new", hermod.TraceStep{
		NodeID: "source", Timestamp: time.Now().UTC(), After: map[string]any{"ok": true},
	}); err != nil {
		t.Errorf("RecordTraceStep after narrowing: %v", err)
	}
}
