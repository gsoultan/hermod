package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/infra/sqlutil"
)

func bulkITDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}
	return dsn
}

func bulkITMappings() []sqlutil.ColumnMapping {
	return []sqlutil.ColumnMapping{
		{SourceField: "id", TargetColumn: "id", DataType: "TEXT", IsPrimaryKey: true},
		{SourceField: "name", TargetColumn: "name", DataType: "TEXT", IsNullable: true},
		{SourceField: "amount", TargetColumn: "amount", DataType: "INTEGER", IsNullable: true},
	}
}

func bulkITMsg(id, name string, amount int) hermod.Message {
	m := message.AcquireMessage()
	m.SetID(id)
	m.SetOperation(hermod.OpCreate)
	payload, _ := json.Marshal(map[string]any{"id": id, "name": name, "amount": amount})
	m.SetAfter(payload)
	return m
}

type bulkRow struct {
	id     string
	name   string
	amount int
}

func dumpTable(t *testing.T, dsn, table string) []bulkRow {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, "SELECT id, name, amount FROM "+table+" ORDER BY id")
	if err != nil {
		t.Fatalf("query %s: %v", table, err)
	}
	defer rows.Close()

	var out []bulkRow
	for rows.Next() {
		var r bulkRow
		if err := rows.Scan(&r.id, &r.name, &r.amount); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func dropTable(t *testing.T, dsn, table string) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
		t.Fatalf("drop %s: %v", table, err)
	}
}

// The bulk path is only safe if it is indistinguishable from the ordered path.
// This writes the same batch both ways into two identical tables and requires
// the resulting table contents to match exactly.
func TestBulkCopyMatchesOrderedPath(t *testing.T) {
	dsn := bulkITDSN(t)
	ctx := context.Background()

	const n = bulkMinRows * 4
	build := func() []hermod.Message {
		msgs := make([]hermod.Message, 0, n)
		for i := 0; i < n; i++ {
			msgs = append(msgs, bulkITMsg(fmt.Sprintf("k%04d", i), fmt.Sprintf("name %d", i), i*3))
		}
		// Duplicate keys inside one batch: the ordered path applies them in
		// sequence so the last wins, and the bulk path must agree.
		msgs = append(msgs, bulkITMsg("k0000", "OVERWRITTEN", 999))
		return msgs
	}

	fastTable := "hermod_bulk_fast"
	slowTable := "hermod_bulk_slow"
	dropTable(t, dsn, fastTable)
	dropTable(t, dsn, slowTable)

	// Fast path: large insert-only batch, classifier should pick COPY.
	fastMsgs := build()
	fastSink := NewPostgresSink(dsn, fastTable, bulkITMappings(), false, "hard_delete", "", "", "", false, false)
	defer fastSink.Close()
	if got := fastSink.classifyBatch(fastMsgs); got != bulkModeCopy {
		t.Fatalf("expected the batch to take the copy path, got %v", got)
	}
	if err := fastSink.WriteBatch(ctx, fastMsgs); err != nil {
		t.Fatalf("fast WriteBatch: %v", err)
	}
	for _, m := range fastMsgs {
		m.Release()
	}

	// Slow path: identical data written one message at a time.
	slowMsgs := build()
	slowSink := NewPostgresSink(dsn, slowTable, bulkITMappings(), false, "hard_delete", "", "", "", false, false)
	defer slowSink.Close()
	for _, m := range slowMsgs {
		if err := slowSink.Write(ctx, m); err != nil {
			t.Fatalf("slow Write: %v", err)
		}
	}
	for _, m := range slowMsgs {
		m.Release()
	}

	fast := dumpTable(t, dsn, fastTable)
	slow := dumpTable(t, dsn, slowTable)

	if len(fast) != len(slow) {
		t.Fatalf("row count differs: copy=%d ordered=%d", len(fast), len(slow))
	}
	for i := range fast {
		if fast[i] != slow[i] {
			t.Fatalf("row %d differs:\n  copy    = %+v\n  ordered = %+v", i, fast[i], slow[i])
		}
	}
	if len(fast) != n {
		t.Errorf("expected %d distinct rows after dedupe, got %d", n, len(fast))
	}
	// Last write must win for the duplicated key.
	if fast[0].name != "OVERWRITTEN" || fast[0].amount != 999 {
		t.Errorf("duplicate key did not resolve last-wins: %+v", fast[0])
	}
}

// A batch that mixes operations must never take the bulk path, because the
// ordering between a delete and a later insert on the same key is observable.
func TestMixedOperationBatchStaysOrdered(t *testing.T) {
	dsn := bulkITDSN(t)
	ctx := context.Background()

	table := "hermod_bulk_mixed"
	dropTable(t, dsn, table)

	snk := NewPostgresSink(dsn, table, bulkITMappings(), false, "hard_delete", "", "", "", false, false)
	defer snk.Close()

	msgs := make([]hermod.Message, 0, bulkMinRows+2)
	for i := 0; i < bulkMinRows; i++ {
		msgs = append(msgs, bulkITMsg(fmt.Sprintf("m%04d", i), "keep", i))
	}
	// Delete then re-insert the same key: only the ordered path gets this right.
	del := message.AcquireMessage()
	del.SetID("m0000")
	del.SetOperation(hermod.OpDelete)
	del.SetAfter([]byte(`{"id":"m0000"}`))
	msgs = append(msgs, del)
	msgs = append(msgs, bulkITMsg("m0000", "reinserted", 42))

	if got := snk.classifyBatch(msgs); got != bulkModeNone {
		t.Fatalf("mixed-operation batch must not take the copy path, got %v", got)
	}
	if err := snk.WriteBatch(ctx, msgs); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	for _, m := range msgs {
		m.Release()
	}

	rows := dumpTable(t, dsn, table)
	var found *bulkRow
	for i := range rows {
		if rows[i].id == "m0000" {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatal("delete-then-insert lost the row entirely")
	}
	if found.name != "reinserted" || found.amount != 42 {
		t.Errorf("delete-then-insert produced %+v; want the re-inserted values", *found)
	}
}
