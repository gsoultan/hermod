//go:build integration

package oracle

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	_ "github.com/sijms/go-ora/v2"
	"github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	"github.com/gsoultan/Hermod/pkg/infra/sqlutil"
)

// The Oracle sink, against a real server.
//
// Oracle is the second of two connectors whose identifier fix was written
// blind — no server was reachable from a workstation or CI when it landed, so
// unlike every other SQL sink its guard was never watched failing against a
// live database. This test closes that gap when an ORACLE_DSN is provided,
// which is what the CI service and a local gvenzl/oracle-free container both
// supply. It proves two things a network-free test cannot: that the data path
// round-trips, and that the SQL the identifier guard builds is actually
// accepted by Oracle rather than merely well-formed to our eyes.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 \
//	ORACLE_DSN='oracle://system:hermod_test@127.0.0.1:1521/FREEPDB1' \
//	go test -tags=integration ./pkg/comm/sink/oracle/
func requireOracle(t *testing.T) (string, *sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("ORACLE_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		// Unlike the other SQL sinks, this one is NOT fatal-in-CI. Oracle Free
		// is a ~2GB image needing ~2GB of RAM and a slow first boot, and the
		// integration job already runs at the edge of a 7GB runner's memory
		// and disk — adding it reintroduces the OOM class that took six
		// commits to clear. The evidence for this connector therefore comes
		// from a local run against a gvenzl/oracle-free container, cited in
		// SECURITY.md, in the same spirit as Snowflake. When a larger runner
		// or a hosted Oracle is available, wire ORACLE_DSN and this becomes a
		// standing gate.
		t.Skip("integration: set HERMOD_INTEGRATION=1 and ORACLE_DSN to run (not run in CI; see comment)")
	}

	db, err := sql.Open("oracle", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("ORACLE_DSN names a server that is not reachable: %v", err)
	}

	// Oracle upper-cases unquoted identifiers; keep the fixture name short and
	// unquoted so it matches what the sink creates.
	table := "HERMOD_" + strings.ToUpper(strings.ReplaceAll(t.Name(), "/", "_"))
	if len(table) > 30 {
		table = table[:30]
	}
	drop := func() {
		_, _ = db.ExecContext(context.Background(),
			fmt.Sprintf("BEGIN EXECUTE IMMEDIATE 'DROP TABLE %s'; EXCEPTION WHEN OTHERS THEN NULL; END;", table))
	}
	drop()
	t.Cleanup(drop)
	return dsn, db, table
}

func omsg(t *testing.T, id, name string, op hermod.Operation) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(m.Release)
	m.SetID(id)
	m.SetOperation(op)
	m.SetData("id", id)
	m.SetData("name", name)
	return m
}

// Insert, upsert on redelivery, delete — through the mapped path a real
// destination schema uses.
func TestOracleMappedInsertUpsertAndDelete(t *testing.T) {
	dsn, db, table := requireOracle(t)

	mappings := []sqlutil.ColumnMapping{
		{SourceField: "$.id", TargetColumn: "id", DataType: "VARCHAR2(64)", IsPrimaryKey: true},
		{SourceField: "$.name", TargetColumn: "name", DataType: "VARCHAR2(64)"},
	}
	sink := NewOracleSink(dsn, table, mappings, false, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	ctx := t.Context()
	if err := sink.Write(ctx, omsg(t, "a", "ada", hermod.OpCreate)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var name string
	q := fmt.Sprintf("SELECT name FROM %s WHERE id = :1", table)
	if err := db.QueryRowContext(ctx, q, "a").Scan(&name); err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if name != "ada" {
		t.Errorf("row landed with name=%q, want ada", name)
	}

	// At-least-once: the same record again updates in place, not duplicates.
	if err := sink.Write(ctx, omsg(t, "a", "grace", hermod.OpUpdate)); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT count(*) FROM %s WHERE id = :1", table), "a").Scan(&n); err != nil {
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

	if err := sink.Write(ctx, omsg(t, "a", "grace", hermod.OpDelete)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT count(*) FROM %s WHERE id = :1", table), "a").Scan(&n); err != nil {
		t.Fatalf("counting after delete: %v", err)
	}
	if n != 0 {
		t.Errorf("the deleted row is still present %d time(s)", n)
	}
}

// The identifier guard, against a real server: a table name arriving on a
// message is refused before it can reach a statement, and Oracle never sees a
// table of that shape. This is the assertion the blind fix could not make.
func TestAnUnsafeTableNameFromAMessageIsRefusedLive(t *testing.T) {
	dsn, db, _ := requireOracle(t)

	// Not pinned, so the name comes from the message.
	sink := NewOracleSink(dsn, "", nil, false, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("a")
	msg.SetOperation(hermod.OpCreate)
	msg.SetData("id", "a")
	msg.SetTable(`PWNED (id) AS SELECT banner FROM v$version --`)

	err := sink.WriteBatch(t.Context(), []hermod.Message{msg})
	if err == nil {
		t.Fatal("a message carrying an unsafe table name was accepted")
	}
	if !strings.Contains(err.Error(), "refusing to build a statement") {
		t.Errorf("the write failed, but not because the sink refused the name: %v", err)
	}

	var count int
	_ = db.QueryRowContext(t.Context(),
		"SELECT count(*) FROM user_tables WHERE table_name = 'PWNED'").Scan(&count)
	if count != 0 {
		t.Errorf("a table matching the injected name exists")
	}
}

// Writing into an existing, conventionally-named Oracle table.
//
// This is the case a real deployment starts from: the table already exists,
// created by a DBA with ordinary unquoted DDL, so Oracle folded its column
// names to upper case — ID, NAME. The user then maps to them the way every
// example and every other connector in this project spells them, in lower
// case.
//
// QuoteIdent quotes for Oracle exactly as it does for PostgreSQL, producing
// "id". But the two databases fold in opposite directions: PostgreSQL folds
// unquoted identifiers to lower case, so "id" matches the natural column,
// while Oracle folds to UPPER case, so "id" names a different column that
// does not exist. Every write to a conventional Oracle table therefore fails
// with ORA-00904, and the failure names the column rather than the cause.
func TestWritingToAnExistingConventionallyNamedTable(t *testing.T) {
	dsn, db, table := requireOracle(t)

	// Ordinary DDL, the way a DBA writes it: unquoted, so Oracle stores ID/NAME.
	if _, err := db.ExecContext(t.Context(), fmt.Sprintf(
		"CREATE TABLE %s (id VARCHAR2(64) PRIMARY KEY, name VARCHAR2(64))", table)); err != nil {
		t.Fatalf("create: %v", err)
	}

	mappings := []sqlutil.ColumnMapping{
		{SourceField: "$.id", TargetColumn: "id", IsPrimaryKey: true},
		{SourceField: "$.name", TargetColumn: "name"},
	}
	// useExistingTable: the sink must write into the table as it stands.
	sink := NewOracleSink(dsn, table, mappings, true, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	if err := sink.Write(t.Context(), omsg(t, "a", "ada", hermod.OpCreate)); err != nil {
		t.Fatalf("writing to an existing table with conventional column names: %v\n"+
			"the mapping names id/name in lower case, which is how every example "+
			"spells them; Oracle folds unquoted DDL to upper case, so quoting the "+
			"lower-case form names a column that does not exist", err)
	}

	var name string
	if err := db.QueryRowContext(t.Context(),
		fmt.Sprintf("SELECT name FROM %s WHERE id = :1", table), "a").Scan(&name); err != nil {
		t.Fatalf("reading back with ordinary SQL: %v", err)
	}
	if name != "ada" {
		t.Errorf("row landed with name=%q, want ada", name)
	}
}
