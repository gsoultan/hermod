//go:build integration

package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

// The MySQL sink's mapped-column path, against a real server.
//
// MySQL is GA, and its idempotency coverage runs against a live server — but
// only through the unmapped path, where the sink writes (id, data). Column
// mappings are a different construction entirely: different INSERT, a different
// ON DUPLICATE KEY clause, a different DELETE. It is also the path anyone with
// a real destination schema is using, and none of it had been exercised.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 MYSQL_DSN='root:root@tcp(127.0.0.1:3306)/hermod_it?parseTime=true' \
//	go test -tags=integration ./pkg/comm/sink/mysql/

func requireMappedMySQL(t *testing.T) (string, *sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("MYSQL_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q MYSQL_DSN=%q in CI", os.Getenv("HERMOD_INTEGRATION"), dsn)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and MYSQL_DSN to run")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("MYSQL_DSN is not reachable: %v", err)
	}

	table := "mapped_" + strings.ToLower(t.Name())
	drop := func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table) }
	drop()
	t.Cleanup(drop)
	return dsn, db, table
}

func mappedMsg(t *testing.T, id, name, city string, op hermod.Operation) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(m.Release)
	m.SetID(id)
	m.SetOperation(op)
	m.SetData("id", id)
	m.SetData("name", name)
	m.SetData("city", city)
	return m
}

// Insert, upsert on redelivery, delete — through the mapped path.
func TestMappedColumnsInsertUpsertAndDelete(t *testing.T) {
	dsn, db, table := requireMappedMySQL(t)

	if _, err := db.ExecContext(t.Context(), fmt.Sprintf(
		"CREATE TABLE %s (id VARCHAR(64) PRIMARY KEY, name VARCHAR(64), city VARCHAR(64))",
		table)); err != nil {
		t.Fatalf("create: %v", err)
	}

	mappings := []sqlutil.ColumnMapping{
		{SourceField: "$.id", TargetColumn: "id", DataType: "VARCHAR(64)", IsPrimaryKey: true},
		{SourceField: "$.name", TargetColumn: "name", DataType: "VARCHAR(64)"},
		{SourceField: "$.city", TargetColumn: "city", DataType: "VARCHAR(64)"},
	}
	sink := NewMySQLSink(dsn, table, mappings, true, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	ctx := t.Context()
	if err := sink.Write(ctx, mappedMsg(t, "a", "ada", "London", hermod.OpCreate)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var name, city string
	q := fmt.Sprintf("SELECT name, city FROM %s WHERE id = ?", table)
	if err := db.QueryRowContext(ctx, q, "a").Scan(&name, &city); err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if name != "ada" || city != "London" {
		t.Errorf("mapped columns landed as name=%q city=%q, want ada/London", name, city)
	}

	// At-least-once: the same record again must update in place.
	if err := sink.Write(ctx, mappedMsg(t, "a", "ada", "Paris", hermod.OpUpdate)); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT count(*) FROM %s WHERE id = ?", table), "a").Scan(&n); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if n != 1 {
		t.Errorf("after a redelivery there are %d rows, want 1", n)
	}
	if err := db.QueryRowContext(ctx, q, "a").Scan(&name, &city); err != nil {
		t.Fatalf("reading back after upsert: %v", err)
	}
	if city != "Paris" {
		t.Errorf("the upsert did not update city: got %q, want Paris", city)
	}

	if err := sink.Write(ctx, mappedMsg(t, "a", "ada", "Paris", hermod.OpDelete)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT count(*) FROM %s WHERE id = ?", table), "a").Scan(&n); err != nil {
		t.Fatalf("counting after delete: %v", err)
	}
	if n != 0 {
		t.Errorf("the deleted row is still present %d time(s)", n)
	}
}

// A mapped column name that would break out of its own quoting.
//
// The sink wrapped names in backticks with fmt.Sprintf, which neither validates
// the name nor escapes a backtick inside it, so one carrying a backtick ended
// its own identifier and the rest became statement text. That was confirmed
// against a real server before this was fixed — MySQL echoed
// "`name`, (SELECT 1)) -- `" straight back — and it failed only because that
// particular text was invalid SQL. A name chosen to parse would have run.
//
// So the assertion is that the *sink* refuses it, not merely that the write
// fails: a server rejecting one payload is not the same as the name never
// reaching a statement. These names come from sink configuration rather than
// from a message, which is why this is a hardening fix rather than an incident,
// but the rule in SECURITY.md exists so that stays true when the code is reused
// somewhere it is not.
func TestAMappedColumnNameCannotBreakOutOfItsQuoting(t *testing.T) {
	dsn, db, table := requireMappedMySQL(t)

	if _, err := db.ExecContext(t.Context(), fmt.Sprintf(
		"CREATE TABLE %s (id VARCHAR(64) PRIMARY KEY, name VARCHAR(64))", table)); err != nil {
		t.Fatalf("create: %v", err)
	}

	mappings := []sqlutil.ColumnMapping{
		{SourceField: "$.id", TargetColumn: "id", DataType: "VARCHAR(64)", IsPrimaryKey: true},
		// A backtick inside the name. Unescaped, this ends the identifier and
		// the rest becomes statement text.
		{SourceField: "$.name", TargetColumn: "name`, (SELECT 1)) -- ", DataType: "VARCHAR(64)"},
	}
	sink := NewMySQLSink(dsn, table, mappings, true, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	err := sink.Write(t.Context(), mappedMsg(t, "a", "ada", "London", hermod.OpCreate))
	if err == nil {
		t.Fatal("a column name containing a backtick was accepted\n" +
			"the name ends its own quoting and the rest is read as SQL")
	}
	// Refused here, not by the server. A syntax error from MySQL would mean the
	// name still reached a statement and simply failed to parse.
	if !strings.Contains(err.Error(), "invalid column name") {
		t.Errorf("the write failed, but not because the sink refused the name: %v\n"+
			"that is the server rejecting one payload rather than the name being kept "+
			"out of the statement, and a name chosen to parse would have run", err)
	}
	_ = db
}
