package storage

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// A document holds one payload per distinct content, however many steps carry
// it. Nine steps of a realistic workflow share three payloads.
func TestStoredTraceHoldsOnePayloadPerDistinctContent(t *testing.T) {
	doc := StoredTrace{WorkflowID: "wf", MessageID: "m", Payloads: map[string][]byte{}}
	base := time.Now().UTC()

	same := map[string]any{"order_id": "ord_1", "status": "paid"}
	changed := map[string]any{"order_id": "ord_1", "status": "shipped"}
	for i, after := range []map[string]any{same, same, same, changed, changed} {
		s, key, blob := PackTraceStep(hermod.TraceStep{
			NodeID:    fmt.Sprintf("n%d", i),
			Timestamp: base.Add(time.Duration(i) * time.Millisecond),
			After:     after,
		})
		doc.Steps = append(doc.Steps, s)
		if key != "" {
			doc.Payloads[key] = blob
		}
	}

	if len(doc.Payloads) != 2 {
		t.Errorf("five steps with two distinct payloads stored %d, want 2", len(doc.Payloads))
	}

	got := doc.UnpackTrace()
	if len(got.Steps) != 5 {
		t.Fatalf("got %d steps, want 5", len(got.Steps))
	}
	for i, want := range []string{"paid", "paid", "paid", "shipped", "shipped"} {
		if got.Steps[i].After["status"] != want {
			t.Errorf("step %d status = %v, want %q", i, got.Steps[i].After["status"], want)
		}
	}
}

// Before is reconstructed, not stored — the payload chain used to be persisted
// twice, which is what dropping before_data removed from SQL.
func TestStoredTraceReconstructsBeforeFromThePreviousStep(t *testing.T) {
	doc := StoredTrace{Payloads: map[string][]byte{}}
	base := time.Now().UTC()
	for i, after := range []map[string]any{{"stage": "in"}, {"stage": "mapped"}, {"stage": "out"}} {
		s, key, blob := PackTraceStep(hermod.TraceStep{
			NodeID: fmt.Sprintf("n%d", i), Timestamp: base.Add(time.Duration(i) * time.Millisecond),
			Before: map[string]any{"should": "not be stored"},
			After:  after,
		})
		doc.Steps = append(doc.Steps, s)
		doc.Payloads[key] = blob
	}

	// Nothing in the stored form carries a before-image.
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := "should"; contains(string(encoded), want) {
		t.Errorf("the stored document carries a before-image: %s", encoded)
	}

	got := doc.UnpackTrace()
	if got.Steps[0].Before != nil {
		t.Error("the first step has no predecessor, so Before should be nil")
	}
	if got.Steps[1].Before["stage"] != "in" {
		t.Errorf("step 1 Before = %v, want the previous step's After", got.Steps[1].Before)
	}
	if got.Steps[2].Before["stage"] != "mapped" {
		t.Errorf("step 2 Before = %v, want the previous step's After", got.Steps[2].Before)
	}
}

// Documents written before dedup carry their maps inline and must keep reading,
// without a backfill.
func TestStoredTraceReadsLegacyInlinePayloads(t *testing.T) {
	doc := StoredTrace{
		Steps: []StoredStep{
			{NodeID: "old", Timestamp: time.Now().UTC(), LegacyAfter: map[string]any{"legacy": true}},
		},
	}
	got := doc.UnpackTrace()
	if len(got.Steps) != 1 || got.Steps[0].After["legacy"] != true {
		t.Fatalf("legacy document did not read back: %#v", got.Steps)
	}
}

// A key with no payload behind it reads as absent, never as a neighbour's data.
func TestStoredTraceMissingPayloadIsNilNotStale(t *testing.T) {
	doc := StoredTrace{Payloads: map[string][]byte{}}
	base := time.Now().UTC()

	first, k1, b1 := PackTraceStep(hermod.TraceStep{
		NodeID: "n0", Timestamp: base, After: map[string]any{"stage": "first"}})
	second, _, _ := PackTraceStep(hermod.TraceStep{
		NodeID: "n1", Timestamp: base.Add(time.Millisecond), After: map[string]any{"stage": "second"}})
	doc.Steps = []StoredStep{first, second}
	doc.Payloads[k1] = b1 // second's payload is absent

	got := doc.UnpackTrace()
	if got.Steps[1].After != nil {
		t.Errorf("a step whose payload is gone resolved to %#v, want nil rather than "+
			"the neighbour's data", got.Steps[1].After)
	}
}

// The payload map is compressed, and a mixed document round-trips.
func TestStoredTracePayloadsAreCompressed(t *testing.T) {
	row := map[string]any{}
	for i := range 40 {
		row[fmt.Sprintf("column_name_number_%02d", i)] = "a fairly repetitive string value"
	}
	raw, _ := json.Marshal(row)

	_, key, blob := PackTraceStep(hermod.TraceStep{NodeID: "n", Timestamp: time.Now(), After: row})
	if len(blob) >= len(raw) {
		t.Errorf("stored %d bytes for %d bytes of JSON — not compressed", len(blob), len(raw))
	}

	doc := StoredTrace{
		Steps:    []StoredStep{{NodeID: "n", Timestamp: time.Now(), AfterKey: key}},
		Payloads: map[string][]byte{key: blob},
	}
	if got := doc.UnpackTrace(); len(got.Steps[0].After) != len(row) {
		t.Errorf("round trip lost fields: %d of %d", len(got.Steps[0].After), len(row))
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
