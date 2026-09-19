//go:build integration

package mysql

// The bulk path is only allowed to exist if it writes exactly what the ordered
// path writes. Unit tests can check which batches it admits; only a real
// server can check that the rows come out the same.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 \
//	MYSQL_DSN='root:hermod@tcp(192.168.64.23:3306)/hermod_it?parseTime=true' \
//	go test ./pkg/comm/sink/mysql -tags=integration -run TestBulk -v

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

func bulkDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("MYSQL_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and MYSQL_DSN to run")
	}
	return dsn
}

// freshTable creates a table for one test and drops it afterwards, so a rerun
// never inherits the previous run's rows.
func freshTable(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+name); err != nil {
		t.Fatalf("drop %s: %v", name, err)
	}
	ddl := fmt.Sprintf(`CREATE TABLE %s (
		id BIGINT NOT NULL PRIMARY KEY,
		name VARCHAR(255),
		amount DOUBLE
	)`, name)
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+name) })
	return name
}

func bulkIntegrationMappings() []sqlutil.ColumnMapping {
	return []sqlutil.ColumnMapping{
		{SourceField: "id", TargetColumn: "id", IsPrimaryKey: true, DataType: "BIGINT"},
		{SourceField: "name", TargetColumn: "name", DataType: "VARCHAR(255)"},
		{SourceField: "amount", TargetColumn: "amount", DataType: "DOUBLE"},
	}
}

func sinkFor(dsn, table string) *MySQLSink {
	return NewMySQLSink(dsn, table, bulkIntegrationMappings(), true, "", "", "", "auto", false, false)
}

// rowsOf reads the whole table ordered by key, as comparable text.
func rowsOf(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rs, err := db.QueryContext(context.Background(),
		"SELECT id, COALESCE(name,'<null>'), COALESCE(amount,-1) FROM "+table+" ORDER BY id")
	if err != nil {
		t.Fatalf("select from %s: %v", table, err)
	}
	defer rs.Close()

	var out []string
	for rs.Next() {
		var id int64
		var name string
		var amount float64
		if err := rs.Scan(&id, &name, &amount); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, fmt.Sprintf("%d|%s|%g", id, name, amount))
	}
	if err := rs.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func compare(t *testing.T, bulk, ordered []string) {
	t.Helper()
	if len(bulk) != len(ordered) {
		t.Fatalf("bulk wrote %d rows, ordered wrote %d", len(bulk), len(ordered))
	}
	for i := range bulk {
		if bulk[i] != ordered[i] {
			t.Fatalf("row %d differs:\n bulk: %s\n  ord: %s", i, bulk[i], ordered[i])
		}
	}
}

func insertMsgs(t *testing.T, ids []int64, label string) []hermod.Message {
	t.Helper()
	msgs := make([]hermod.Message, 0, len(ids))
	for _, id := range ids {
		m := message.AcquireMessage()
		t.Cleanup(func() { message.ReleaseMessage(m) })
		m.SetOperation(hermod.OpCreate)
		m.SetData("id", id)
		m.SetData("name", label+"-"+strconv.FormatInt(id, 10))
		m.SetData("amount", float64(id)*1.5)
		msgs = append(msgs, m)
	}
	return msgs
}

// writeOrdered applies msgs one at a time, which is the reference: a single
// message is always below bulkMinRows, so it can only take the ordered path.
func writeOrdered(t *testing.T, s *MySQLSink, msgs []hermod.Message) {
	t.Helper()
	for _, m := range msgs {
		if err := s.Write(context.Background(), m); err != nil {
			t.Fatalf("ordered write: %v", err)
		}
	}
}

// TestBulkMatchesOrderedPath is the contract: the same batch, both ways,
// byte-identical table contents.
func TestBulkMatchesOrderedPath(t *testing.T) {
	dsn := bulkDSN(t)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	cases := []struct {
		name string
		ids  func() []int64
	}{
		{
			name: "distinct keys",
			ids: func() []int64 {
				ids := make([]int64, 0, 200)
				for i := range 200 {
					ids = append(ids, int64(i))
				}
				return ids
			},
		},
		{
			// A repeated key inside one batch: the multi-row statement must
			// land on the same final value the ordered path would.
			name: "duplicate keys, last wins",
			ids: func() []int64 {
				ids := make([]int64, 0, 200)
				for i := range 200 {
					ids = append(ids, int64(i%25))
				}
				return ids
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bulkTable := freshTable(t, db, "bulk_target")
			ordTable := freshTable(t, db, "ordered_target")
			ids := tc.ids()

			bs := sinkFor(dsn, bulkTable)
			defer bs.Close()
			if mode := bs.classifyBatch(insertMsgs(t, ids, "v")); mode != bulkModeMultiValues {
				t.Fatalf("precondition: batch classified as %v, so this test is not exercising the bulk path", mode)
			}
			if err := bs.WriteBatch(context.Background(), insertMsgs(t, ids, "v")); err != nil {
				t.Fatalf("bulk WriteBatch: %v", err)
			}

			os := sinkFor(dsn, ordTable)
			defer os.Close()
			writeOrdered(t, os, insertMsgs(t, ids, "v"))

			compare(t, rowsOf(t, db, bulkTable), rowsOf(t, db, ordTable))
		})
	}
}

// Chunking must not change the result either. maxBulkPlaceholders is lowered
// so a modest batch actually crosses a boundary; at its real value that would
// take 20,000 rows.
func TestBulkChunkingMatchesOrderedPath(t *testing.T) {
	dsn := bulkDSN(t)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	original := maxBulkPlaceholders
	maxBulkPlaceholders = 30 // 3 columns -> 10 rows per statement
	defer func() { maxBulkPlaceholders = original }()

	bulkTable := freshTable(t, db, "bulk_target")
	ordTable := freshTable(t, db, "ordered_target")

	ids := make([]int64, 0, 95) // 9 full chunks and a partial one
	for i := range 95 {
		ids = append(ids, int64(i))
	}

	bs := sinkFor(dsn, bulkTable)
	defer bs.Close()
	if err := bs.WriteBatch(context.Background(), insertMsgs(t, ids, "chunk")); err != nil {
		t.Fatalf("bulk WriteBatch: %v", err)
	}

	os := sinkFor(dsn, ordTable)
	defer os.Close()
	writeOrdered(t, os, insertMsgs(t, ids, "chunk"))

	got := rowsOf(t, db, bulkTable)
	if len(got) != 95 {
		t.Fatalf("chunked write produced %d rows, want 95", len(got))
	}
	compare(t, got, rowsOf(t, db, ordTable))
}

// A batch the classifier refuses must still be written, by the ordered path,
// correctly — the fallback is not a failure mode.
func TestBatchWithAnUpdateStillWritesCorrectly(t *testing.T) {
	dsn := bulkDSN(t)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	table := freshTable(t, db, "bulk_target")

	ids := make([]int64, 0, 60)
	for i := range 60 {
		ids = append(ids, int64(i))
	}
	msgs := insertMsgs(t, ids, "mixed")
	msgs[30].(*message.DefaultMessage).SetOperation(hermod.OpUpdate)

	s := sinkFor(dsn, table)
	defer s.Close()
	if mode := s.classifyBatch(msgs); mode != bulkModeNone {
		t.Fatalf("a batch containing an update classified as %v; ordering is observable there", mode)
	}
	if err := s.WriteBatch(context.Background(), msgs); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if got := rowsOf(t, db, table); len(got) != 60 {
		t.Fatalf("wrote %d rows, want 60", len(got))
	}
}
