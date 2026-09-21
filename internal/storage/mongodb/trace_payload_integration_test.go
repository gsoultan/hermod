//go:build integration
// +build integration

package mongodb

// What MongoDB actually writes for a trace.
//
// The packing is covered as pure functions in internal/storage, but the two
// things that can only be wrong against a real server are here: that a payload
// map keyed by content survives a `$set` on a dotted path, and that a document
// written by the previous release still decodes.

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func newTraceMongo(t *testing.T) (storage.Storage, *mongo.Database) {
	t.Helper()
	if os.Getenv("HERMOD_INTEGRATION") != "1" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 to run")
	}
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		t.Skip("integration: set MONGODB_URI to run")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connecting to %s: %v", uri, err)
	}
	// A scratch database per run. Re-using one leaves the previous shape's
	// documents behind, which is exactly how a half-migrated collection turns
	// into a test that passes for the wrong reason.
	name := fmt.Sprintf("hermod_trace_%d", time.Now().UnixNano()/1e6)
	db := client.Database(name)
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})
	return NewMongoStorage(client, name), db
}

func recordMongoTrace(t *testing.T, s storage.Storage, wf, msg string, afters ...map[string]any) {
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

func TestMongoStoresOnePayloadPerDistinctContent(t *testing.T) {
	s, db := newTraceMongo(t)
	ctx := context.Background()

	paid := map[string]any{"order_id": "ord_1", "status": "paid"}
	shipped := map[string]any{"order_id": "ord_1", "status": "shipped"}
	recordMongoTrace(t, s, "wf", "m1", paid, paid, paid, shipped, shipped)

	var doc storage.StoredTrace
	if err := db.Collection("message_traces").
		FindOne(ctx, bson.M{"workflow_id": "wf", "message_id": "m1"}).Decode(&doc); err != nil {
		t.Fatalf("reading the stored document: %v", err)
	}
	if len(doc.Steps) != 5 {
		t.Errorf("got %d steps, want 5", len(doc.Steps))
	}
	if len(doc.Payloads) != 2 {
		t.Errorf("five steps with two distinct payloads stored %d, want 2 — the payload "+
			"map is keyed by content, so a repeat is a repeated $set of one field",
			len(doc.Payloads))
	}
	for _, st := range doc.Steps {
		if st.LegacyBefore != nil || st.LegacyAfter != nil {
			t.Errorf("step %s still carries an inline payload; Before is the previous "+
				"step's After and is reconstructed on read", st.NodeID)
		}
		if st.AfterKey == "" {
			t.Errorf("step %s names no payload", st.NodeID)
		}
	}

	got, err := s.GetMessageTrace(ctx, "wf", "m1")
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

// Documents written by the previous release push the whole hermod.TraceStep,
// so the driver derived field names from its Go fields. They must keep reading.
func TestMongoReadsLegacyInlineTraceDocuments(t *testing.T) {
	s, db := newTraceMongo(t)
	ctx := context.Background()

	if _, err := db.Collection("message_traces").InsertOne(ctx, bson.M{
		"id": "legacy-1", "workflow_id": "wf", "message_id": "old",
		"created_at": time.Now().UTC(),
		"steps": bson.A{bson.M{
			"node_id":   "legacy_node",
			"timestamp": time.Now().UTC(),
			"before":    bson.M{"stage": "in"},
			"after":     bson.M{"legacy": true},
		}},
	}); err != nil {
		t.Fatalf("seeding a pre-upgrade document: %v", err)
	}

	got, err := s.GetMessageTrace(ctx, "wf", "old")
	if err != nil {
		t.Fatalf("GetMessageTrace: %v", err)
	}
	if len(got.Steps) != 1 || got.Steps[0].After["legacy"] != true {
		t.Fatalf("pre-upgrade document did not read back: %#v", got.Steps)
	}
}

// The saving, measured against what the previous shape would have written.
func TestMongoTraceDocumentFootprint(t *testing.T) {
	s, db := newTraceMongo(t)
	ctx := context.Background()

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
	afters := []map[string]any{
		row(""), row(""), row(""), row("t1"), row("t1"), row("t2"), row("t2"), row(""), row("t2"),
	}
	recordMongoTrace(t, s, "wf", "m1", afters...)

	var stored bson.Raw
	if err := db.Collection("message_traces").
		FindOne(ctx, bson.M{"workflow_id": "wf", "message_id": "m1"}).Decode(&stored); err != nil {
		t.Fatalf("reading the stored document: %v", err)
	}

	// What the previous release would have pushed: the whole TraceStep per
	// step, before and after, inline and uncompressed.
	var oldSteps bson.A
	for i, after := range afters {
		var before map[string]any
		if i > 0 {
			before = afters[i-1]
		}
		oldSteps = append(oldSteps, bson.M{
			"node_id": fmt.Sprintf("n%d", i), "timestamp": time.Now().UTC(),
			"before": before, "after": after,
		})
	}
	oldDoc, err := bson.Marshal(bson.M{
		"id": "x", "workflow_id": "wf", "message_id": "m1",
		"created_at": time.Now().UTC(), "steps": oldSteps,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	t.Logf("nine-step trace document: %d bytes, was %d (%.2fx)",
		len(stored), len(oldDoc), float64(len(oldDoc))/float64(len(stored)))

	if len(stored) >= len(oldDoc)/2 {
		t.Errorf("stored %d bytes against the old shape's %d; dropping the before-image "+
			"alone should halve it before dedup and compression are counted",
			len(stored), len(oldDoc))
	}
}
