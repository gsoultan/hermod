//go:build integration
// +build integration

package registry

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// TestFlowLookupCacheReachesTheSink is the live form of the cache-hit defect:
//
//	postgres CDC -> db_lookup (query mode) -> postgres sink with no column mappings
//
// A sink with no column mappings writes msg.Payload() into a JSONB column, which
// is the serialisation that loses a field written into a message that was never
// read first. db_lookup in query mode has no keyField, so nothing reads the
// message before the result is written -- and with the Cache TTL field empty
// (ttl <= 0 means "never expire") only the very first message takes the query
// path that happens to hydrate it.
//
// Every row should carry the looked-up customer name.
func TestFlowLookupCacheReachesTheSink(t *testing.T) {
	srcDSN := os.Getenv("FLOW_SOURCE_DSN")
	sinkDSN := os.Getenv("FLOW_SINK_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || srcDSN == "" || sinkDSN == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1, FLOW_SOURCE_DSN, FLOW_SINK_DSN")
	}

	srcDB, err := sql.Open("pgx", srcDSN)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	t.Cleanup(func() { _ = srcDB.Close() })
	sinkDB, err := sql.Open("pgx", sinkDSN)
	if err != nil {
		t.Fatalf("open sink: %v", err)
	}
	t.Cleanup(func() { _ = sinkDB.Close() })

	const slot = "flow_slot_cache"
	dropSlot := func() {
		_, _ = srcDB.ExecContext(context.Background(),
			"SELECT pg_drop_replication_slot($1) FROM pg_replication_slots WHERE slot_name=$1", slot)
	}
	mustExec(t, srcDB, "TRUNCATE flow_orders")
	mustExec(t, sinkDB, "TRUNCATE flow_orders_raw")
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
	// No column_mappings: the sink writes msg.Payload() into a JSONB column.
	ms.sinks["pg-raw"] = storage.Sink{
		ID: "pg-raw", Name: "raw", Type: "postgres",
		Config: map[string]string{
			"connection_string":  sinkDSN,
			"table":              "flow_orders_raw",
			"use_existing_table": "true",
		},
	}

	reg := NewRegistry(ms)

	wf := storage.Workflow{
		ID: "flow-wf-cache", Name: "lookup cache", Active: true,
		Nodes: []storage.WorkflowNode{
			{ID: "n-src", Type: "source", RefID: "cdc-src", Config: map[string]any{"label": "CDC"}},
			{ID: "n-lookup", Type: "transformation", Config: map[string]any{
				"transType":     "db_lookup",
				"label":         "gold customer name",
				"sourceId":      "lookup-src",
				"mode":          "query",
				"queryTemplate": "SELECT name FROM flow_customers WHERE code = 'C-1'",
				"valueColumn":   "name",
				"targetField":   "customer_name",
				// Cache TTL deliberately left empty, as the editor leaves it.
			}},
			{ID: "n-sink", Type: "sink", RefID: "pg-raw", Config: map[string]any{
				"label": "Postgres raw", "sequential": false,
			}},
		},
		Edges: []storage.WorkflowEdge{
			{SourceID: "n-src", TargetID: "n-lookup"},
			{SourceID: "n-lookup", TargetID: "n-sink"},
		},
	}

	if err := reg.StartWorkflow(wf.ID, wf); err != nil {
		t.Fatalf("start workflow: %v", err)
	}
	t.Cleanup(func() { _ = reg.StopEngine(context.Background(), wf.ID) })

	time.Sleep(3 * time.Second)

	// One row at a time, with a gap. A burst is processed concurrently, so every
	// message reaches the cache lookup before the first one has populated it and
	// they all take the query path -- which is the path that happens to hydrate
	// the message. Spacing them is what a steady low-rate stream looks like, and
	// it is what lets the cache actually serve a hit.
	const rows = 5
	for i := 1; i <= rows; i++ {
		mustExec(t, srcDB,
			`INSERT INTO flow_orders (id,customer_id,amount,qty) VALUES ($1, 'C-1', '5.00', '1')`,
			fmt.Sprintf("K-%d", i))
		time.Sleep(1200 * time.Millisecond)
	}

	deadline := time.Now().Add(45 * time.Second)
	var count int
	for time.Now().Before(deadline) {
		if err := sinkDB.QueryRowContext(t.Context(),
			"SELECT count(*) FROM flow_orders_raw").Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count >= rows {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if count < rows {
		t.Fatalf("only %d of %d rows reached the sink", count, rows)
	}

	dbRows, err := sinkDB.QueryContext(t.Context(),
		"SELECT id, data::text FROM flow_orders_raw ORDER BY id")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	defer func() { _ = dbRows.Close() }()

	enriched, total := 0, 0
	t.Log("--- flow_orders_raw ---")
	for dbRows.Next() {
		var id, data string
		if err := dbRows.Scan(&id, &data); err != nil {
			t.Fatalf("scan: %v", err)
		}
		total++
		if strings.Contains(data, "ACME Corp") {
			enriched++
		}
		t.Logf("  %s -> %s", id, data)
	}

	if enriched != total {
		t.Errorf("DB_LOOKUP: %d of %d rows in the destination carry the looked-up value; "+
			"the rest were enriched in the pipeline and lost it before the write", enriched, total)
	}
}
