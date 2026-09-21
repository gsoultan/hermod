package pebble

// Pebble stores a whole trace in one value, so every one of these goes through
// the real embedded database rather than through the packing helpers alone.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
)

func newTracePebble(t *testing.T) storage.Storage {
	t.Helper()
	s, err := NewPebbleStorage(t.TempDir())
	if err != nil {
		t.Fatalf("opening pebble: %v", err)
	}
	t.Cleanup(func() { _ = s.(*pebbleStorage).Close() })
	return s
}

func recordTrace(t *testing.T, s storage.Storage, wf, msg string, afters ...map[string]any) {
	t.Helper()
	base := time.Now().UTC()
	for i, after := range afters {
		if err := s.RecordTraceStep(context.Background(), wf, msg, hermod.TraceStep{
			NodeID:    fmt.Sprintf("n%d", i),
			Timestamp: base.Add(time.Duration(i) * time.Millisecond),
			Before:    map[string]any{"should": "not be stored"},
			After:     after,
		}); err != nil {
			t.Fatalf("RecordTraceStep %d: %v", i, err)
		}
	}
}

// One payload per distinct content, however many steps carry it — and the
// before-image is not stored at all.
func TestPebbleStoresOnePayloadPerDistinctContent(t *testing.T) {
	s := newTracePebble(t)

	paid := map[string]any{"order_id": "ord_1", "status": "paid"}
	shipped := map[string]any{"order_id": "ord_1", "status": "shipped"}
	recordTrace(t, s, "wf", "m1", paid, paid, paid, shipped, shipped)

	raw := rawTraceValue(t, s, "wf", "m1")
	var doc storage.StoredTrace
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("stored value is not a StoredTrace: %v", err)
	}
	if len(doc.Payloads) != 2 {
		t.Errorf("five steps with two distinct payloads stored %d, want 2", len(doc.Payloads))
	}
	if len(doc.Steps) != 5 {
		t.Errorf("got %d steps, want 5", len(doc.Steps))
	}
	if bytesContain(raw, "should") {
		t.Error("the stored value carries a before-image; it is the previous step's " +
			"After and is reconstructed on read")
	}
	if bytesContain(raw, "ord_1") {
		t.Error("a payload is sitting in the value uncompressed")
	}

	got, err := s.GetMessageTrace(context.Background(), "wf", "m1")
	if err != nil {
		t.Fatalf("GetMessageTrace: %v", err)
	}
	if len(got.Steps) != 5 {
		t.Fatalf("read back %d steps, want 5", len(got.Steps))
	}
	for i, want := range []string{"paid", "paid", "paid", "shipped", "shipped"} {
		if got.Steps[i].After["status"] != want {
			t.Errorf("step %d status = %v, want %q", i, got.Steps[i].After["status"], want)
		}
	}
	if got.Steps[0].Before != nil {
		t.Error("the first step has no predecessor, so Before should be nil")
	}
	if got.Steps[3].Before["status"] != "paid" {
		t.Errorf("step 3 Before = %v, want the previous step's After", got.Steps[3].Before)
	}
}

// Values written before this change hold hermod.TraceStep inline, with both
// before and after. They must keep reading without a backfill.
func TestPebbleReadsLegacyInlineTraceValues(t *testing.T) {
	s := newTracePebble(t)

	legacy := storage.MessageTrace{
		WorkflowID: "wf", MessageID: "old", CreatedAt: time.Now().UTC(),
		Steps: []hermod.TraceStep{{
			NodeID:    "legacy_node",
			Timestamp: time.Now().UTC(),
			After:     map[string]any{"legacy": true},
		}},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	writeRawTrace(t, s, "wf", "old", data)

	got, err := s.GetMessageTrace(context.Background(), "wf", "old")
	if err != nil {
		t.Fatalf("GetMessageTrace: %v", err)
	}
	if len(got.Steps) != 1 || got.Steps[0].After["legacy"] != true {
		t.Fatalf("legacy value did not read back: %#v", got.Steps)
	}
}

// The list needs a summary, not the payloads. Decompressing every payload of
// every trace to render a list is the whole-table read the SQL backends added
// a parent row to avoid.
func TestPebbleListReportsCountsWithoutDecodingPayloads(t *testing.T) {
	s := newTracePebble(t)
	recordTrace(t, s, "wf", "m1", map[string]any{"a": 1}, map[string]any{"a": 2})

	traces, err := s.ListMessageTraces(context.Background(), "wf", storage.TraceFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListMessageTraces: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1", len(traces))
	}
	if traces[0].StepCount != 2 {
		t.Errorf("StepCount = %d, want 2", traces[0].StepCount)
	}
	if traces[0].MessageID != "m1" {
		t.Errorf("MessageID = %q, want m1", traces[0].MessageID)
	}
}

// What the change is worth, measured through the real database rather than
// estimated: a nine-step workflow whose payloads repeat the way real ones do.
func TestPebbleTraceValueFootprint(t *testing.T) {
	s := newTracePebble(t)

	row := func(stage string) map[string]any {
		m := map[string]any{
			"order_id": "ord_000000000042", "customer_id": "cus_000012345",
			"customer_email": "user42@example.com", "currency": "USD",
			"total_amount": 481.25, "tax_amount": 38.50, "item_count": 3,
			"warehouse_code": "WH-007", "created_at": "2026-09-21T10:00:00Z",
			"notes": "a representative order line for the footprint check",
		}
		if stage != "" {
			m["_stage"] = stage
		}
		return m
	}
	// workflow_start, source, validator, node+transType, node+transType, router, sink
	afters := []map[string]any{
		row(""), row(""), row(""), row("t1"), row("t1"), row("t2"), row("t2"), row(""), row("t2"),
	}
	recordTrace(t, s, "wf", "m1", afters...)

	stored := len(rawTraceValue(t, s, "wf", "m1"))

	// What the previous shape would have written: the full TraceStep per step,
	// before and after, inline and uncompressed.
	old := storage.MessageTrace{WorkflowID: "wf", MessageID: "m1", CreatedAt: time.Now().UTC()}
	for i, after := range afters {
		var before map[string]any
		if i > 0 {
			before = afters[i-1]
		}
		old.Steps = append(old.Steps, hermod.TraceStep{
			NodeID: fmt.Sprintf("n%d", i), Timestamp: time.Now().UTC(),
			Before: before, After: after,
		})
	}
	oldData, err := json.Marshal(old)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	t.Logf("nine-step trace value: %d bytes, was %d (%.2fx)",
		stored, len(oldData), float64(len(oldData))/float64(stored))

	if stored >= len(oldData)/2 {
		t.Errorf("stored %d bytes against the old shape's %d; dropping the before-image "+
			"alone should halve it before dedup and compression are counted",
			stored, len(oldData))
	}
}

func bytesContain(haystack []byte, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if string(haystack[i:i+len(needle)]) == needle {
					return true
				}
			}
			return false
		}()
}

// rawTraceValue reads the bytes actually on disk, which is the point: these
// tests are about what is stored, not about what GetMessageTrace hands back.
func rawTraceValue(t *testing.T, s storage.Storage, wf, msg string) []byte {
	t.Helper()
	db := s.(*pebbleStorage).db
	val, closer, err := db.Get([]byte(fmt.Sprintf("t:%s:%s", wf, msg)))
	if err != nil {
		t.Fatalf("reading the stored trace: %v", err)
	}
	defer func() { _ = closer.Close() }()
	out := make([]byte, len(val))
	copy(out, val)
	return out
}

func writeRawTrace(t *testing.T, s storage.Storage, wf, msg string, data []byte) {
	t.Helper()
	db := s.(*pebbleStorage).db
	if err := db.Set([]byte(fmt.Sprintf("t:%s:%s", wf, msg)), data, pebble.Sync); err != nil {
		t.Fatalf("writing a legacy trace: %v", err)
	}
}
