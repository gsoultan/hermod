package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	_ "modernc.org/sqlite"
)

// A message's payload is stored once, however many steps carry it.
//
// message_trace_steps holds one row per node per message, and most nodes do not
// change the payload: workflow_start, the source ingest step, the validator and
// the router all record the message verbatim, and a transformation node records
// its output twice (once under node.ID in the traversal, once under its
// transType in doApplyTransformation). Measured on a realistic nine-step
// workflow, 66.6% of all payload bytes in the table were byte-identical to
// another step of the same message.
//
// Dropping before_data took the table from two copies of the chain to one. This
// takes it from one copy per step to one copy per distinct payload.
func TestTraceStepPayloadIsStoredOncePerMessage(t *testing.T) {
	s, db := newTraceStorage(t)

	same := map[string]any{"order_id": "ord_1", "total": 42.5, "status": "paid"}
	base := time.Now().UTC()
	for i, node := range []string{"workflow_start", "src_orders", "validator", "router"} {
		if err := s.RecordTraceStep(t.Context(), "wf1", "msg1", hermod.TraceStep{
			NodeID:    node,
			Timestamp: base.Add(time.Duration(i) * time.Millisecond),
			After:     same,
		}); err != nil {
			t.Fatalf("record %s: %v", node, err)
		}
	}

	var carriers int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM message_trace_steps
		WHERE workflow_id = 'wf1' AND message_id = 'msg1'
		  AND (after_blob IS NOT NULL OR after_data IS NOT NULL)`).Scan(&carriers); err != nil {
		t.Fatalf("count carriers: %v", err)
	}
	if carriers != 1 {
		t.Errorf("four steps with one payload stored it %d times, want 1", carriers)
	}

	// Every step still reads back with its payload intact.
	tr, err := s.GetMessageTrace(t.Context(), "wf1", "msg1")
	if err != nil {
		t.Fatalf("GetMessageTrace: %v", err)
	}
	if len(tr.Steps) != 4 {
		t.Fatalf("got %d steps, want 4", len(tr.Steps))
	}
	for _, st := range tr.Steps {
		if fmt.Sprint(st.After["order_id"]) != "ord_1" {
			t.Errorf("step %s lost its payload: %#v", st.NodeID, st.After)
		}
	}
}

// Dedup is scoped to one message. Two messages that happen to carry identical
// payloads must each store their own copy, or purging one would blank the other
// and the trace of a deleted workflow would take live traces with it.
func TestTraceStepPayloadIsNotSharedAcrossMessages(t *testing.T) {
	s, db := newTraceStorage(t)

	same := map[string]any{"k": "v"}
	for _, msg := range []string{"msgA", "msgB"} {
		if err := s.RecordTraceStep(t.Context(), "wf1", msg, hermod.TraceStep{
			NodeID: "n1", Timestamp: time.Now().UTC(), After: same,
		}); err != nil {
			t.Fatalf("record %s: %v", msg, err)
		}
	}

	var carriers int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM message_trace_steps
		WHERE after_blob IS NOT NULL OR after_data IS NOT NULL`).Scan(&carriers); err != nil {
		t.Fatalf("count: %v", err)
	}
	if carriers != 2 {
		t.Errorf("two messages stored %d payloads, want 2 (one each)", carriers)
	}
}

// Distinct payloads are all kept: dedup must not collapse a real change.
func TestTraceStepDistinctPayloadsAreAllStored(t *testing.T) {
	s, _ := newTraceStorage(t)

	base := time.Now().UTC()
	want := []map[string]any{
		{"stage": "in"},
		{"stage": "mapped"},
		{"stage": "mapped"}, // the transType twin of the node step
		{"stage": "out"},
	}
	for i, p := range want {
		if err := s.RecordTraceStep(t.Context(), "wf1", "msg1", hermod.TraceStep{
			NodeID: fmt.Sprintf("n%d", i), Timestamp: base.Add(time.Duration(i) * time.Millisecond), After: p,
		}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	tr, err := s.GetMessageTrace(t.Context(), "wf1", "msg1")
	if err != nil {
		t.Fatalf("GetMessageTrace: %v", err)
	}
	for i, st := range tr.Steps {
		if got := fmt.Sprint(st.After["stage"]); got != want[i]["stage"] {
			t.Errorf("step %d: stage %q, want %q", i, got, want[i]["stage"])
		}
	}
}

// Rows written before this change hold plain JSON in after_data and nothing in
// after_blob. They must keep reading, without a backfill.
func TestTraceStepReadsLegacyAfterDataRows(t *testing.T) {
	s, db := newTraceStorage(t)

	if _, err := db.ExecContext(t.Context(), `INSERT INTO message_trace_steps
		(message_id, workflow_id, node_id, timestamp, duration_ms, after_data, error)
		VALUES ('msg1','wf1','legacy_node',?,5,'{"legacy":true}','')`,
		time.Now().UTC()); err != nil {
		t.Fatalf("insert legacy: %v", err)
	}

	tr, err := s.GetMessageTrace(t.Context(), "wf1", "msg1")
	if err != nil {
		t.Fatalf("GetMessageTrace: %v", err)
	}
	if len(tr.Steps) != 1 || tr.Steps[0].After["legacy"] != true {
		t.Fatalf("legacy row did not read back: %#v", tr.Steps)
	}
}

// A payload whose carrier row is gone — the retention sweep cuts on timestamp
// and a message spans a few milliseconds, so a cutoff can fall inside one —
// must read as absent, not as the neighbouring step's payload. Showing one
// node's data under another node's name is the failure this whole table exists
// to rule out.
func TestTraceStepUnresolvedReferenceIsNilNotStale(t *testing.T) {
	s, db := newTraceStorage(t)

	base := time.Now().UTC()
	p1 := map[string]any{"stage": "first"}
	p2 := map[string]any{"stage": "second"}
	for i, p := range []map[string]any{p1, p2, p2} {
		if err := s.RecordTraceStep(t.Context(), "wf1", "msg1", hermod.TraceStep{
			NodeID: fmt.Sprintf("n%d", i), Timestamp: base.Add(time.Duration(i) * time.Millisecond), After: p,
		}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	// Delete the carrier of p2, leaving n2 referencing a payload that is gone.
	if _, err := db.ExecContext(t.Context(), `DELETE FROM message_trace_steps
		WHERE workflow_id='wf1' AND message_id='msg1' AND node_id='n1'`); err != nil {
		t.Fatalf("delete carrier: %v", err)
	}

	tr, err := s.GetMessageTrace(t.Context(), "wf1", "msg1")
	if err != nil {
		t.Fatalf("GetMessageTrace: %v", err)
	}
	var orphan *hermod.TraceStep
	for i := range tr.Steps {
		if tr.Steps[i].NodeID == "n2" {
			orphan = &tr.Steps[i]
		}
	}
	if orphan == nil {
		t.Fatal("n2 missing from trace")
	}
	if orphan.After != nil {
		t.Errorf("orphaned step resolved to %#v, want nil rather than a neighbour's payload", orphan.After)
	}
}

// The payload column is compressed. JSON is the most compressible thing this
// database stores and PostgreSQL does not compress it for us: a trace payload
// is well under the ~2 KB TOAST threshold, so it sits in the heap verbatim
// (measured: toast 0.01 MB against a 127 MB heap).
func TestTraceStepPayloadIsCompressed(t *testing.T) {
	s, db := newTraceStorage(t)

	// Repetitive but realistic: a row with many similar keys.
	row := map[string]any{}
	for i := range 40 {
		row[fmt.Sprintf("column_name_number_%02d", i)] = "a fairly repetitive string value"
	}
	raw, _ := json.Marshal(row)

	if err := s.RecordTraceStep(t.Context(), "wf1", "msg1", hermod.TraceStep{
		NodeID: "n1", Timestamp: time.Now().UTC(), After: row,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	var stored []byte
	if err := db.QueryRowContext(t.Context(), `SELECT after_blob FROM message_trace_steps
		WHERE workflow_id='wf1' AND message_id='msg1'`).Scan(&stored); err != nil {
		t.Fatalf("read blob: %v", err)
	}
	if len(stored) >= len(raw) {
		t.Errorf("stored %d bytes for %d bytes of JSON — not compressed", len(stored), len(raw))
	}

	tr, err := s.GetMessageTrace(t.Context(), "wf1", "msg1")
	if err != nil {
		t.Fatalf("GetMessageTrace: %v", err)
	}
	if len(tr.Steps[0].After) != len(row) {
		t.Errorf("round trip lost fields: %d of %d", len(tr.Steps[0].After), len(row))
	}
}

// The dedup bookkeeping is keyed by message id, which a source supplies. It
// needs a bound and an eviction or a busy workflow grows it without limit.
func TestTraceDedupCacheIsBounded(t *testing.T) {
	c := newTraceDedupCache(8)
	h := []byte("0123456789abcdef")
	for i := range 100 {
		c.markStored("wf", fmt.Sprintf("msg%d", i), h)
	}
	if n := c.len(); n > 8 {
		t.Errorf("cache holds %d entries, cap is 8", n)
	}
	// The most recent entry is still there; the oldest is not.
	if !c.alreadyStored("wf", "msg99", h) {
		t.Error("most recent entry evicted")
	}
	if c.alreadyStored("wf", "msg0", h) {
		t.Error("oldest entry survived a 100-insert run through an 8-slot cache")
	}
}

// Compression can be turned off without changing what reads back: the codec is
// recorded in the value, not assumed from configuration, so a database written
// by one setting is readable under the other.
func TestTraceStepPayloadCodecIsSelfDescribing(t *testing.T) {
	t.Setenv("HERMOD_TRACE_COMPRESSION", "off")
	s, db := newTraceStorage(t)

	payload := map[string]any{"hello": "world"}
	if err := s.RecordTraceStep(t.Context(), "wf1", "msg1", hermod.TraceStep{
		NodeID: "n1", Timestamp: time.Now().UTC(), After: payload,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	var stored []byte
	if err := db.QueryRowContext(t.Context(), `SELECT after_blob FROM message_trace_steps`).Scan(&stored); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(stored), `"hello"`) {
		t.Errorf("compression off should store readable JSON, got %q", stored)
	}

	tr, err := s.GetMessageTrace(t.Context(), "wf1", "msg1")
	if err != nil {
		t.Fatalf("GetMessageTrace: %v", err)
	}
	if tr.Steps[0].After["hello"] != "world" {
		t.Errorf("uncompressed round trip failed: %#v", tr.Steps[0].After)
	}
}

// Belt and braces on the schema: the columns the reader selects are the columns
// Init creates. The sibling test in message_traces_test.go guards the other
// direction (a column written and never read).
func TestTraceStepSchemaCoversTheReadQuery(t *testing.T) {
	_, db := newTraceStorage(t)
	for _, col := range []string{"after_hash", "after_blob"} {
		var n int
		err := db.QueryRowContext(t.Context(), fmt.Sprintf(
			"SELECT COUNT(*) FROM pragma_table_info('message_trace_steps') WHERE name = '%s'", col)).Scan(&n)
		if err != nil {
			t.Fatalf("pragma: %v", err)
		}
		if n != 1 {
			t.Errorf("message_trace_steps has no %s column", col)
		}
	}
}

// The upgrade, starting from the table the previous release actually leaves
// behind rather than from a table this release created.
//
// Three shipped bugs had full unit and integration coverage of the parts and
// none of the assembly, which is why a feature that reaches production through
// storage gets one test that starts from storage. Here the assembly is
// autoMigrate: the two new columns are added by nothing except the DDL diff, so
// if parseColumnDef ever stops recognising a BLOB line, every trace write after
// an upgrade fails against a column that was never created — and the unit tests
// above would all still pass, because they run against a table Init built.
func TestTracePayloadColumnsAreAddedToAnExistingTable(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	// Exactly the shape the previous release leaves: already narrowed (no id,
	// no before_data), but with the payload still in plain-JSON after_data.
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE message_trace_steps (
		message_id TEXT NOT NULL,
		workflow_id TEXT NOT NULL,
		node_id TEXT NOT NULL,
		timestamp TIMESTAMP NOT NULL,
		duration_ms INTEGER,
		after_data TEXT,
		error TEXT
	)`); err != nil {
		t.Fatalf("creating the previous release's table: %v", err)
	}
	old := time.Now().UTC().Add(-time.Hour)
	if _, err := db.ExecContext(t.Context(), `INSERT INTO message_trace_steps
		(message_id, workflow_id, node_id, timestamp, duration_ms, after_data, error)
		VALUES ('msg_old','wf1','n_old',?,3,'{"written_by":"the previous release"}','')`,
		old); err != nil {
		t.Fatalf("seeding a pre-upgrade row: %v", err)
	}

	s := NewSQLStorage(db, "sqlite")
	if err := s.(interface{ Init(context.Context) error }).Init(t.Context()); err != nil {
		t.Fatalf("Init against a pre-upgrade trace table: %v", err)
	}

	// The pre-upgrade row still reads, with no backfill.
	tr, err := s.GetMessageTrace(t.Context(), "wf1", "msg_old")
	if err != nil {
		t.Fatalf("reading a pre-upgrade trace: %v", err)
	}
	if len(tr.Steps) != 1 || tr.Steps[0].After["written_by"] != "the previous release" {
		t.Fatalf("pre-upgrade row did not survive the migration: %#v", tr.Steps)
	}

	// And writes after the upgrade use the new columns and dedup.
	same := map[string]any{"order_id": "ord_9", "status": "paid"}
	base := time.Now().UTC()
	for i, node := range []string{"workflow_start", "validator", "router"} {
		if err := s.RecordTraceStep(t.Context(), "wf1", "msg_new", hermod.TraceStep{
			NodeID:    node,
			Timestamp: base.Add(time.Duration(i) * time.Millisecond),
			After:     same,
		}); err != nil {
			t.Fatalf("recording after the upgrade: %v", err)
		}
	}

	var carriers, wroteLegacy int
	if err := db.QueryRowContext(t.Context(), `SELECT
			COUNT(*) FILTER (WHERE after_blob IS NOT NULL),
			COUNT(*) FILTER (WHERE after_data IS NOT NULL)
		FROM message_trace_steps WHERE message_id = 'msg_new'`).Scan(&carriers, &wroteLegacy); err != nil {
		t.Fatalf("inspecting post-upgrade rows: %v", err)
	}
	if carriers != 1 {
		t.Errorf("three identical payloads stored %d times after the upgrade, want 1", carriers)
	}
	if wroteLegacy != 0 {
		t.Errorf("%d post-upgrade rows still wrote after_data", wroteLegacy)
	}

	tr, err = s.GetMessageTrace(t.Context(), "wf1", "msg_new")
	if err != nil {
		t.Fatalf("reading a post-upgrade trace: %v", err)
	}
	for _, st := range tr.Steps {
		if st.After["order_id"] != "ord_9" {
			t.Errorf("step %s lost its payload after the upgrade: %#v", st.NodeID, st.After)
		}
	}
}
