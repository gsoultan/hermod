//go:build integration

package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	_ "github.com/microsoft/go-mssqldb"
	"github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	"github.com/gsoultan/Hermod/pkg/infra/sqlutil"
)

// The MSSQL sink, against a real server.
//
// MSSQL was Beta and had never been shown to write a row. It was also the
// connector I had written off as untestable on an arm64 workstation — SQL Server
// proper has no arm64 image. Azure SQL Edge does, and runs natively under
// Apple's container runtime, so the claim that this could not be exercised here
// was simply wrong.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 \
//	MSSQL_DSN='sqlserver://sa:Hermod%21Passw0rd@127.0.0.1:1433?database=master&encrypt=disable' \
//	go test -tags=integration ./pkg/comm/sink/mssql/

func requireMSSQL(t *testing.T) (string, *sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("MSSQL_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q MSSQL_DSN=%q in CI, where a server is started "+
				"for exactly this", os.Getenv("HERMOD_INTEGRATION"), dsn)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and MSSQL_DSN to run")
	}

	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("MSSQL_DSN names a server that is not reachable: %v", err)
	}

	table := "mssql_" + strings.ToLower(t.Name())
	drop := func() {
		_, _ = db.ExecContext(context.Background(),
			fmt.Sprintf("IF OBJECT_ID('%s','U') IS NOT NULL DROP TABLE %s", table, table))
	}
	drop()
	t.Cleanup(drop)
	return dsn, db, table
}

func msg(t *testing.T, id, name string, op hermod.Operation) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(m.Release)
	m.SetID(id)
	m.SetOperation(op)
	m.SetData("id", id)
	m.SetData("name", name)
	return m
}

// Insert, upsert on redelivery, delete — through the mapped path, which is what
// a real destination schema uses.
func TestMSSQLMappedInsertUpsertAndDelete(t *testing.T) {
	dsn, db, table := requireMSSQL(t)

	if _, err := db.ExecContext(t.Context(), fmt.Sprintf(
		"CREATE TABLE %s (id NVARCHAR(64) PRIMARY KEY, name NVARCHAR(64))", table)); err != nil {
		t.Fatalf("create: %v", err)
	}

	mappings := []sqlutil.ColumnMapping{
		{SourceField: "$.id", TargetColumn: "id", DataType: "NVARCHAR(64)", IsPrimaryKey: true},
		{SourceField: "$.name", TargetColumn: "name", DataType: "NVARCHAR(64)"},
	}
	sink := NewMSSQLSink(dsn, table, mappings, true, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	ctx := t.Context()
	if err := sink.Write(ctx, msg(t, "a", "ada", hermod.OpCreate)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var name string
	q := fmt.Sprintf("SELECT name FROM %s WHERE id = @p1", table)
	if err := db.QueryRowContext(ctx, q, "a").Scan(&name); err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if name != "ada" {
		t.Errorf("row landed with name=%q, want ada", name)
	}

	// At-least-once: the same record again must update in place, not duplicate.
	if err := sink.Write(ctx, msg(t, "a", "grace", hermod.OpUpdate)); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT count(*) FROM %s WHERE id = @p1", table), "a").Scan(&n); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if n != 1 {
		t.Errorf("after a redelivery there are %d rows, want 1", n)
	}
	if err := db.QueryRowContext(ctx, q, "a").Scan(&name); err != nil {
		t.Fatalf("reading back after upsert: %v", err)
	}
	if name != "grace" {
		t.Errorf("the upsert did not update the row: name=%q, want grace", name)
	}

	if err := sink.Write(ctx, msg(t, "a", "grace", hermod.OpDelete)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT count(*) FROM %s WHERE id = @p1", table), "a").Scan(&n); err != nil {
		t.Fatalf("counting after delete: %v", err)
	}
	if n != 0 {
		t.Errorf("the deleted row is still present %d time(s)", n)
	}
}

// A column name that cannot be quoted must be refused, not silently emptied.
//
// Every QuoteIdent call in this sink discards its error — `qCol, _ := ...` — and
// QuoteIdent returns "" when it rejects a name. So an unquotable name does not
// stop the write; it becomes an empty identifier inside the statement. That
// fails, but as a parse error naming nothing rather than as a refusal naming the
// column.
func TestAnUnquotableColumnNameIsRefusedByName(t *testing.T) {
	dsn, db, table := requireMSSQL(t)

	if _, err := db.ExecContext(t.Context(), fmt.Sprintf(
		"CREATE TABLE %s (id NVARCHAR(64) PRIMARY KEY, name NVARCHAR(64))", table)); err != nil {
		t.Fatalf("create: %v", err)
	}

	mappings := []sqlutil.ColumnMapping{
		{SourceField: "$.id", TargetColumn: "id", DataType: "NVARCHAR(64)", IsPrimaryKey: true},
		{SourceField: "$.name", TargetColumn: "name] , x AS (SELECT 1)) --", DataType: "NVARCHAR(64)"},
	}
	sink := NewMSSQLSink(dsn, table, mappings, true, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	err := sink.Write(t.Context(), msg(t, "a", "ada", hermod.OpCreate))
	if err == nil {
		t.Fatal("a column name QuoteIdent rejects was accepted")
	}
	if !strings.Contains(err.Error(), "invalid column name") {
		t.Errorf("the write failed, but not because the sink refused the name: %v\n"+
			"QuoteIdent's error is discarded, so the rejected name becomes an empty "+
			"identifier and the statement fails somewhere that names neither the column "+
			"nor the reason", err)
	}
	_ = db
}
