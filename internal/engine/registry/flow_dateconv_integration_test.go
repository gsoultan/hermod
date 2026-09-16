//go:build integration
// +build integration

package registry

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// TestFlowDateConversionReachesATimestampColumn follows a date conversion all the
// way into a real timestamptz, using a source format PostgreSQL cannot read.
//
// data_conversion's date branch returns a time.Time, and nothing downstream
// treats that as a special case: SanitizeValue has no clause for it, so it sits
// in the data map as a time.Time, while any round trip through Payload() turns
// it into RFC3339 text. The sink's convertValue then takes one path for a string
// and another for everything else. Two representations of one value, decided by
// whether something serialised the message in between -- the shape that has
// produced most of the defects in this pipeline -- so what actually lands in the
// column is worth asserting rather than assuming.
//
// The source timestamp is deliberately "16-09-2026 08:30" rather than RFC3339.
// With an RFC3339 source this test passes with the conversion node removed
// altogether, because PostgreSQL casts the string itself and the transformation
// is doing nothing a timestamptz column did not already do. A day-first format
// with no timezone is one only the configured layout can read, so the assertion
// below fails if the conversion did not run.
func TestFlowDateConversionReachesATimestampColumn(t *testing.T) {
	srcDSN, sinkDSN := flowDSNs(t)
	srcDB := openFlowDB(t, srcDSN)
	sinkDB := openFlowDB(t, sinkDSN)
	provisionFlowFixtures(t, srcDB, sinkDB)

	const slot = "flow_slot_date"
	dropSlot := func() {
		_, _ = srcDB.ExecContext(context.Background(),
			"SELECT pg_drop_replication_slot($1) FROM pg_replication_slots WHERE slot_name=$1", slot)
	}
	mustExec(t, srcDB, "TRUNCATE flow_orders")
	mustExec(t, sinkDB, "TRUNCATE flow_orders_enriched")
	dropSlot()
	t.Cleanup(dropSlot)

	ms := &mockE2EStorage{
		sources:   make(map[string]storage.Source),
		sinks:     make(map[string]storage.Sink),
		workflows: make(map[string]storage.Workflow),
	}
	ms.sources["cdc-src"] = storage.Source{
		ID: "cdc-src", Name: "orders cdc", Type: "postgres",
		Config: map[string]string{
			"connection_string": srcDSN,
			"tables":            "flow_orders",
			"use_cdc":           "true",
			"slot_name":         slot,
			"publication_name":  "flow_pub",
		},
	}

	mappings, _ := json.Marshal([]map[string]any{
		{"source_field": "id", "target_column": "order_id", "is_primary_key": true},
		{"source_field": "placed_at", "target_column": "placed_at", "data_type": "TIMESTAMPTZ"},
	})
	ms.sinks["pg-sink"] = storage.Sink{
		ID: "pg-sink", Name: "enriched", Type: "postgres",
		Config: map[string]string{
			"connection_string":  sinkDSN,
			"table":              "flow_orders_enriched",
			"use_existing_table": "true",
			"column_mappings":    string(mappings),
		},
	}

	reg := NewRegistry(ms)
	wf := storage.Workflow{
		ID: "flow-wf-date", Name: "date conversion", Active: true,
		Nodes: []storage.WorkflowNode{
			{ID: "n-src", Type: "source", RefID: "cdc-src", Config: map[string]any{"label": "CDC"}},
			{ID: "n-date", Type: "transformation", Config: map[string]any{
				"transType":  "data_conversion",
				"label":      "placed_at to date",
				"field":      "placed_at",
				"targetType": "date",
				"format":     "02-01-2006 15:04",
			}},
			{ID: "n-sink", Type: "sink", RefID: "pg-sink", Config: map[string]any{
				"label": "Postgres", "sequential": false,
			}},
		},
		Edges: []storage.WorkflowEdge{
			{SourceID: "n-src", TargetID: "n-date"},
			{SourceID: "n-date", TargetID: "n-sink"},
		},
	}

	if err := reg.StartWorkflow(wf.ID, wf); err != nil {
		t.Fatalf("start workflow: %v", err)
	}
	t.Cleanup(func() { _ = reg.StopEngine(context.Background(), wf.ID) })

	time.Sleep(3 * time.Second)

	want := time.Date(2026, 9, 16, 8, 30, 0, 0, time.UTC)
	mustExec(t, srcDB,
		`INSERT INTO flow_orders (id,customer_id,amount,qty,placed_at) VALUES ($1,'C-1','1.00','1',$2)`,
		"D-1", want.Format("02-01-2006 15:04"))

	var got time.Time
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		err := sinkDB.QueryRowContext(t.Context(),
			"SELECT placed_at FROM flow_orders_enriched WHERE order_id='D-1'").Scan(&got)
		if err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if got.IsZero() {
		t.Fatal("DATA_CONVERSION(date): no row with a placed_at reached the sink")
	}
	if !got.UTC().Equal(want) {
		t.Errorf("DATA_CONVERSION(date): placed_at = %s, want %s",
			got.UTC().Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}
