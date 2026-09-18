package conformance_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	sourcefile "github.com/gsoultan/hermod/pkg/comm/source/file"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	"github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/writer"

	sinksqlite "github.com/gsoultan/hermod/pkg/comm/sink/sqlite"
	_ "modernc.org/sqlite"
)

const parquetCDCSchema = `{"Tag":"name=parquet_go_root, repetitiontype=REQUIRED","Fields":[` +
	`{"Tag":"name=id, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=REQUIRED"},` +
	`{"Tag":"name=name, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=REQUIRED"},` +
	`{"Tag":"name=operation, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=REQUIRED"}]}`

// The question this connector exists to answer, asked of the real thing: does a
// parquet file carrying operations actually insert, update and delete rows in a
// target, or only look like it does?
//
// The unit tests prove the source labels each message correctly. Only running it
// into a real sink proves a delete deletes — a DELETE that matches nothing
// reports success just as loudly as one that removes a row.
func TestAParquetFileAppliesInsertUpdateAndDeleteToASink(t *testing.T) {
	dir := t.TempDir()
	parquetPath := filepath.Join(dir, "changes.parquet")

	fw, err := local.NewLocalFileWriter(parquetPath)
	if err != nil {
		t.Fatalf("local writer: %v", err)
	}
	pw, err := writer.NewJSONWriter(parquetCDCSchema, fw, 1)
	if err != nil {
		t.Fatalf("parquet writer: %v", err)
	}
	for _, row := range []string{
		`{"id":"a1","name":"ada","operation":"create"}`,
		`{"id":"a2","name":"bob","operation":"create"}`,
		`{"id":"a1","name":"ada lovelace","operation":"update"}`,
		`{"id":"a2","name":"bob","operation":"delete"}`,
	} {
		if err := pw.Write(row); err != nil {
			t.Fatalf("write %s: %v", row, err)
		}
	}
	if err := pw.WriteStop(); err != nil {
		t.Fatalf("write stop: %v", err)
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	src := sourcefile.NewGenericFileSource(sourcefile.GenericConfig{
		Backend:   sourcefile.BackendLocal,
		LocalPath: dir,
		Pattern:   "*.parquet",
		Format:    sourcefile.FormatParquet,
		Table:     "people",
		KeyField:  "id",
		OpField:   "operation",
	})
	defer src.Close()

	dbPath := filepath.Join(dir, "target.db")
	sink := sinksqlite.NewSQLiteSink(
		dbPath, "people",
		[]sqlutil.ColumnMapping{
			{SourceField: "id", TargetColumn: "id", DataType: "TEXT", IsPrimaryKey: true},
			{SourceField: "name", TargetColumn: "name", DataType: "TEXT"},
		},
		false, "", "", "", "auto", false, false,
	)
	defer sink.Close()

	ctx := t.Context()
	for i := range 4 {
		msg, err := src.Read(ctx)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if msg == nil {
			t.Fatalf("read %d: no message", i)
		}
		if err := sink.Write(ctx, msg); err != nil {
			t.Fatalf("write %d (%s %s): %v", i, msg.Operation(), msg.ID(), err)
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	defer db.Close()

	var name string
	if err := db.QueryRowContext(ctx, `SELECT name FROM people WHERE id = 'a1'`).Scan(&name); err != nil {
		t.Fatalf("a1 should still be there, updated: %v", err)
	}
	if name != "ada lovelace" {
		t.Errorf("a1 name = %q, want the updated %q", name, "ada lovelace")
	}

	var gone int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM people WHERE id = 'a2'`).Scan(&gone); err != nil {
		t.Fatalf("count a2: %v", err)
	}
	if gone != 0 {
		t.Errorf("a2 is still in the table after a delete row: count = %d", gone)
	}

	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM people`).Scan(&total); err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 1 {
		t.Errorf("table holds %d rows, want 1 — an update that inserted instead would show here", total)
	}
}
