//go:build integration
// +build integration

package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
)

// TestFlowNodeByNode drives a real pipeline end to end and checks what each node
// contributed:
//
//	postgres CDC source -> db_lookup -> data_conversion(amount) -> data_conversion(qty) -> postgres sink
//
// It asserts per node rather than only on the final row, so a failure names the
// node that dropped or failed to change the data instead of just "row missing".
//
// It runs twice, once per sink delivery model. `sequential: true` makes the sink
// node write inline; false makes the engine's sink writer deliver it. Both must
// put the same row in the destination, acknowledge the source, and report no
// drops.
func TestFlowNodeByNode(t *testing.T) {
	for _, seq := range []bool{false, true} {
		t.Run(fmt.Sprintf("sequential=%v", seq), func(t *testing.T) {
			runFlow(t, seq)
		})
	}
}

func runFlow(t *testing.T, sequential bool) {
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

	for _, db := range []*sql.DB{srcDB, sinkDB} {
		if err := db.PingContext(t.Context()); err != nil {
			t.Fatalf("database not reachable: %v", err)
		}
	}

	slot := fmt.Sprintf("flow_slot_%v", sequential)
	wfID := fmt.Sprintf("flow-wf-%v", sequential)

	dropSlot := func() {
		_, _ = srcDB.ExecContext(context.Background(),
			"SELECT pg_drop_replication_slot($1) FROM pg_replication_slots WHERE slot_name=$1", slot)
	}
	mustExec(t, srcDB, "TRUNCATE flow_orders")
	mustExec(t, sinkDB, "TRUNCATE flow_orders_enriched")
	dropSlot()
	t.Cleanup(dropSlot) // a leaked logical slot pins WAL forever

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
	// db_lookup refuses a CDC source, so the lookup source is the same server
	// with CDC off.
	ms.sources["lookup-src"] = storage.Source{
		ID: "lookup-src", Name: "customers", Type: "postgres",
		Config: map[string]string{"connection_string": srcDSN, "use_cdc": "false"},
	}

	mappings, _ := json.Marshal([]map[string]any{
		{"source_field": "id", "target_column": "order_id", "is_primary_key": true},
		{"source_field": "customer_id", "target_column": "customer_id"},
		{"source_field": "customer_name", "target_column": "customer_name"},
		{"source_field": "amount", "target_column": "amount"},
		{"source_field": "qty", "target_column": "qty"},
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
		ID: wfID, Name: "node by node", Active: true,
		Nodes: []storage.WorkflowNode{
			{ID: "n-src", Type: "source", RefID: "cdc-src", Config: map[string]any{"label": "CDC"}},
			{ID: "n-lookup", Type: "transformation", Config: map[string]any{
				"transType":   "db_lookup",
				"label":       "customer name",
				"sourceId":    "lookup-src",
				"table":       "flow_customers",
				"keyColumn":   "code",
				"keyField":    "customer_id",
				"valueColumn": "name",
				"targetField": "customer_name",
			}},
			{ID: "n-amount", Type: "transformation", Config: map[string]any{
				"transType":  "data_conversion",
				"label":      "amount to float",
				"field":      "amount",
				"targetType": "float",
			}},
			{ID: "n-qty", Type: "transformation", Config: map[string]any{
				"transType":  "data_conversion",
				"label":      "qty to int",
				"field":      "qty",
				"targetType": "int",
			}},
			{ID: "n-sink", Type: "sink", RefID: "pg-sink", Config: map[string]any{
				"label": "Postgres", "sequential": sequential,
			}},
		},
		Edges: []storage.WorkflowEdge{
			{SourceID: "n-src", TargetID: "n-lookup"},
			{SourceID: "n-lookup", TargetID: "n-amount"},
			{SourceID: "n-amount", TargetID: "n-qty"},
			{SourceID: "n-qty", TargetID: "n-sink"},
		},
	}

	if err := reg.StartWorkflow(wf.ID, wf); err != nil {
		t.Fatalf("start workflow: %v", err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			_ = reg.StopEngine(context.Background(), wf.ID)
		}
	}
	t.Cleanup(stop)

	// Let the CDC source create its slot and begin streaming before the writes,
	// otherwise the changes land before the slot exists and are never sent.
	time.Sleep(3 * time.Second)

	mustExec(t, srcDB, `INSERT INTO flow_orders (id,customer_id,amount,qty) VALUES ('O-1','C-1','10.50','3')`)
	time.Sleep(1500 * time.Millisecond)
	mustExec(t, srcDB, `INSERT INTO flow_orders (id,customer_id,amount,qty) VALUES ('O-2','C-2','99.99','7')`)

	// Bulk, so WAL retention is measurable rather than lost in rounding.
	const bulk = 200
	mustExec(t, srcDB, `INSERT INTO flow_orders (id,customer_id,amount,qty)
		SELECT 'B-'||g, 'C-1', '1.25', '2' FROM generate_series(1,$1) g`, bulk)

	want := 2 + bulk
	deadline := time.Now().Add(60 * time.Second)
	var count int
	for time.Now().Before(deadline) {
		if err := sinkDB.QueryRowContext(t.Context(),
			"SELECT count(*) FROM flow_orders_enriched").Scan(&count); err != nil {
			t.Fatalf("count sink rows: %v", err)
		}
		if count >= want {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Measured while the engine is still running: flushPosition() pins the slot
	// at the acknowledged LSN whenever something delivered is unacknowledged, so
	// a workflow that never acknowledges cannot let the slot advance.
	t.Logf("SLOT %s (engine running, immediately): retained_wal_bytes=%d",
		slot, slotRetainedBytes(t, srcDB, slot))

	// The standby status update is periodic, so an immediate reading shows the
	// same backlog either way. Wait past that interval: by now an acknowledged
	// stream has confirmed its position and released the WAL, while an
	// unacknowledged one is still pinned at the last acknowledgement.
	// The source reports its position on a periodic standby status update, so a
	// single reading lands at an arbitrary point in that cadence and says
	// nothing. Poll for the drain instead. Unacknowledged delivery pins the slot
	// at the last acknowledgement (flushPosition, postgres.go:1015), so before
	// the fix this sat at ~108 KB and never moved.
	const drainBound = 16 << 10
	var retainedLive int64 = -1
	drainDeadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(drainDeadline) {
		time.Sleep(3 * time.Second)
		retainedLive = slotRetainedBytes(t, srcDB, slot)
		if retainedLive >= 0 && retainedLive < drainBound {
			break
		}
	}
	t.Logf("SLOT %s: retained_wal_bytes drained to %d", slot, retainedLive)
	if retainedLive < 0 || retainedLive >= drainBound {
		t.Errorf("SOURCE: the replication slot still holds %d bytes of WAL after every row "+
			"reached the destination; the workflow is not acknowledging what it delivers, so "+
			"the slot never advances and the backlog replays on the next start", retainedLive)
	}

	dumpSink(t, sinkDB)

	// The "delivered nowhere" log is throttled to one line per 30s, so its
	// dropped_total is only the count at the first drop. Read the counter.
	dropped := promtestutil.ToFloat64(telemetry.MessagesDroppedNoTarget.WithLabelValues(wf.ID))
	t.Logf("ROUTER: messages delivered nowhere = %v", dropped)

	if count < want {
		t.Fatalf("SOURCE/SINK: only %d of %d rows reached the sink", count, want)
	}

	var (
		customerID   sql.NullString
		customerName sql.NullString
		amount       sql.NullFloat64
		qty          sql.NullInt64
	)
	err = sinkDB.QueryRowContext(t.Context(),
		"SELECT customer_id, customer_name, amount, qty FROM flow_orders_enriched WHERE order_id='O-1'").
		Scan(&customerID, &customerName, &amount, &qty)
	if err != nil {
		t.Fatalf("read O-1 back: %v", err)
	}

	if customerID.String != "C-1" {
		t.Errorf("SOURCE: customer_id = %q, want \"C-1\" (the CDC after-image did not reach the sink)", customerID.String)
	}
	if customerName.String != "ACME Corp" {
		t.Errorf("DB_LOOKUP: customer_name = %q, want \"ACME Corp\" (enrichment did not reach the sink)", customerName.String)
	}
	if amount.Float64 != 10.50 {
		t.Errorf("DATA_CONVERSION(amount): amount = %v, want 10.50", amount.Float64)
	}
	if qty.Int64 != 3 {
		t.Errorf("DATA_CONVERSION(qty): qty = %v, want 3", qty.Int64)
	}

	// Stop the engine so the source flushes its final position, then ask the
	// server how much WAL this slot is still holding. An acknowledged stream
	// lets the slot advance; an unacknowledged one pins WAL for ever.
	stop()
	time.Sleep(2 * time.Second)

	t.Logf("SLOT %s (engine stopped): retained_wal_bytes=%d", slot, slotRetainedBytes(t, srcDB, slot))

	if dropped > 0 {
		t.Errorf("ROUTER: %v messages counted as delivered nowhere, but %d rows are in the destination; "+
			"the engine did not acknowledge data it in fact delivered", dropped, count)
	}
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func dumpSink(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(t.Context(),
		"SELECT order_id, customer_id, customer_name, amount, qty FROM flow_orders_enriched ORDER BY order_id")
	if err != nil {
		t.Logf("dump sink: %v", err)
		return
	}
	defer func() { _ = rows.Close() }()
	t.Log("--- flow_orders_enriched ---")
	for rows.Next() {
		var id string
		var cid, cname sql.NullString
		var amt sql.NullFloat64
		var q sql.NullInt64
		if err := rows.Scan(&id, &cid, &cname, &amt, &q); err != nil {
			t.Logf("scan: %v", err)
			return
		}
		t.Logf("  order_id=%s customer_id=%v customer_name=%v amount=%v qty=%v",
			id, cid.String, cname.String, amt.Float64, q.Int64)
	}
}

func slotRetainedBytes(t *testing.T, db *sql.DB, slot string) int64 {
	t.Helper()
	var n sql.NullInt64
	err := db.QueryRowContext(context.Background(), `
		SELECT pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn)::bigint
		FROM pg_replication_slots WHERE slot_name=$1`, slot).Scan(&n)
	if err != nil {
		t.Logf("read slot position: %v", err)
		return -1
	}
	return n.Int64
}
