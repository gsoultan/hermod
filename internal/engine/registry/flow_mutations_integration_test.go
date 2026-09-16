//go:build integration
// +build integration

package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// TestFlowMutationsReachTheSink follows one row through its whole life --
// INSERT, UPDATE, DELETE -- rather than only its creation.
//
// The destination of a CDC pipeline is meant to mirror the source, so after all
// three the row should be present once, then present once with the new value,
// then gone. That is asserted for both sink shapes, because they identify a row
// completely differently: a mapped sink keys on the column its mapping calls a
// primary key, and an unmapped sink writes (id, data) keyed on msg.ID() -- which
// for a CDC message is the LSN, and every event for the same row carries a
// different one.
func TestFlowMutationsReachTheSink(t *testing.T) {
	shapes := []struct {
		name     string
		table    string
		mappings bool
		countSQL string
		// afterUpdate and afterDelete are how many rows represent the business
		// key once that event has been applied. A mapped sink mirrors the source,
		// so 1 and 0. An unmapped sink cannot -- see runMutations.
		afterUpdate, afterDelete int
	}{
		{
			name: "mapped sink", table: "flow_orders_enriched", mappings: true,
			countSQL:    `SELECT count(*) FROM flow_orders_enriched WHERE order_id = $1`,
			afterUpdate: 1, afterDelete: 0,
		},
		{
			name: "unmapped sink", table: "flow_orders_raw", mappings: false,
			countSQL: `SELECT count(*) FROM flow_orders_raw WHERE data->>'id' = $1`,
			// Inherent to the mode, and no longer silent. Without a mapping the
			// sink keys rows on msg.ID(); whether that identifies a row depends on
			// the source, and PostgreSQL CDC uses an LSN that differs for every
			// event touching one row. So an UPDATE appends rather than replacing,
			// and a DELETE matches nothing.
			//
			// The delete is still issued -- sources whose id is stable per row,
			// such as MySQL CDC, do delete correctly and must keep working -- but
			// a miss now increments hermod_sink_delete_matched_nothing_total and
			// logs once per table instead of reporting success. See
			// TestADeleteThatMatchedNothingIsReported and its control.
			//
			// Map the source's primary key to get a destination that mirrors,
			// which the mapped case above asserts.
			afterUpdate: 2, afterDelete: 2,
		},
	}

	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			runMutations(t, shape.table, shape.mappings, shape.countSQL,
				shape.afterUpdate, shape.afterDelete)
		})
	}
}

func runMutations(t *testing.T, table string, mapped bool, countSQL string, afterUpdate, afterDelete int) {
	srcDSN, sinkDSN := flowDSNs(t)
	srcDB := openFlowDB(t, srcDSN)
	sinkDB := openFlowDB(t, sinkDSN)
	provisionFlowFixtures(t, srcDB, sinkDB)

	slot := "flow_slot_mut_" + sanitizeSlot(table)
	wfID := "flow-wf-mut-" + table

	dropSlot := func() {
		_, _ = srcDB.ExecContext(context.Background(),
			"SELECT pg_drop_replication_slot($1) FROM pg_replication_slots WHERE slot_name=$1", slot)
	}
	mustExec(t, srcDB, "TRUNCATE flow_orders")
	mustExec(t, sinkDB, "TRUNCATE "+table)
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
	ms.sources["lookup-src"] = storage.Source{
		ID: "lookup-src", Name: "customers", Type: "postgres",
		Config: map[string]string{"connection_string": srcDSN, "use_cdc": "false"},
	}

	sinkCfg := map[string]string{
		"connection_string":  sinkDSN,
		"table":              table,
		"use_existing_table": "true",
	}
	if mapped {
		mappings, _ := json.Marshal([]map[string]any{
			{"source_field": "id", "target_column": "order_id", "is_primary_key": true},
			{"source_field": "customer_id", "target_column": "customer_id"},
			{"source_field": "customer_name", "target_column": "customer_name"},
			{"source_field": "amount", "target_column": "amount"},
			{"source_field": "qty", "target_column": "qty"},
		})
		sinkCfg["column_mappings"] = string(mappings)
	}
	ms.sinks["pg-sink"] = storage.Sink{ID: "pg-sink", Name: table, Type: "postgres", Config: sinkCfg}

	reg := NewRegistry(ms)
	wf := storage.Workflow{
		ID: wfID, Name: "mutations", Active: true,
		Nodes: []storage.WorkflowNode{
			{ID: "n-src", Type: "source", RefID: "cdc-src", Config: map[string]any{"label": "CDC"}},
			{ID: "n-lookup", Type: "transformation", Config: map[string]any{
				"transType": "db_lookup", "label": "customer name",
				"sourceId": "lookup-src", "table": "flow_customers",
				"keyColumn": "code", "keyField": "customer_id",
				"valueColumn": "name", "targetField": "customer_name",
				// A delete carries no after-image, so the lookup key is read from
				// the before-image. Missing enrichment must not drop the delete.
				"onMiss": "passthrough",
			}},
			{ID: "n-amount", Type: "transformation", Config: map[string]any{
				"transType": "data_conversion", "label": "amount to float",
				"field": "amount", "targetType": "float",
				// Absent on a message that carries no such column.
				"errorBehavior": "keep",
			}},
			{ID: "n-sink", Type: "sink", RefID: "pg-sink", Config: map[string]any{
				"label": "Postgres", "sequential": false,
			}},
		},
		Edges: []storage.WorkflowEdge{
			{SourceID: "n-src", TargetID: "n-lookup"},
			{SourceID: "n-lookup", TargetID: "n-amount"},
			{SourceID: "n-amount", TargetID: "n-sink"},
		},
	}

	if err := reg.StartWorkflow(wf.ID, wf); err != nil {
		t.Fatalf("start workflow: %v", err)
	}
	t.Cleanup(func() { _ = reg.StopEngine(context.Background(), wf.ID) })

	time.Sleep(3 * time.Second)

	const key = "M-1"

	mustExec(t, srcDB,
		`INSERT INTO flow_orders (id,customer_id,amount,qty) VALUES ($1,'C-1','10.50','3')`, key)
	waitForCount(t, sinkDB, countSQL, key, 1, "INSERT")

	mustExec(t, srcDB, `UPDATE flow_orders SET amount='20.75' WHERE id=$1`, key)
	waitForAmount(t, sinkDB, table, key, 20.75)
	waitForCount(t, sinkDB, countSQL, key, afterUpdate, "UPDATE")

	mustExec(t, srcDB, `DELETE FROM flow_orders WHERE id=$1`, key)
	waitForCount(t, sinkDB, countSQL, key, afterDelete, "DELETE")
}

func sanitizeSlot(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' {
			out = append(out, c)
		}
	}
	return string(out)
}

func waitForCount(t *testing.T, db *sql.DB, countSQL, key string, want int, stage string) {
	t.Helper()
	var got int
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if err := db.QueryRowContext(t.Context(), countSQL, key).Scan(&got); err != nil {
			t.Fatalf("%s: count: %v", stage, err)
		}
		if got == want {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Errorf("%s: the destination holds %d row(s) for %s, want %d", stage, got, key, want)
}

func waitForAmount(t *testing.T, db *sql.DB, table, key string, want float64) {
	t.Helper()
	q := fmt.Sprintf(`SELECT amount FROM %s WHERE order_id = $1`, table)
	if table == "flow_orders_raw" {
		q = `SELECT (data->>'amount')::numeric FROM flow_orders_raw WHERE data->>'id' = $1 ORDER BY id DESC LIMIT 1`
	}
	var got sql.NullFloat64
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if err := db.QueryRowContext(t.Context(), q, key).Scan(&got); err == nil && got.Float64 == want {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Errorf("UPDATE: amount for %s is %v, want %v — the update did not reach the destination",
		key, got.Float64, want)
}
