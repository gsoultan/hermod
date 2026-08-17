package oracle

import (
	"context"
	"strings"
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/infra/sqlutil"
)

// Identifier handling in the Oracle sink.
//
// Like Snowflake, Oracle is not reachable from a workstation or from CI, so
// this fix was not watched failing against a real server first. What makes it
// testable anyway is where the checks sit: the table name and the mapped
// column names are validated before anything connects, so a sink pointed at an
// address nothing answers on still refuses a bad identifier rather than
// failing to dial. The identical layout in the Snowflake sink is what these
// tests are copied from.
//
// What they cannot cover is whether the SQL Oracle finally receives is
// accepted by Oracle — that still needs a server.

// unroutable is an address in the TEST-NET-1 range (RFC 5737). Nothing answers
// there, so a connection attempt cannot succeed: if these tests see a
// validation error, it was produced before any dialling.
const unroutable = "oracle://user:pass@192.0.2.1:1521/orcl"

func oracleMsg(t *testing.T, table string) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(m.Release)
	m.SetID("a")
	m.SetOperation(hermod.OpCreate)
	m.SetTable(table)
	m.SetPayload([]byte(`{"v":1}`))
	return m
}

// A table name from a message must be refused before it reaches a statement.
func TestAnUnsafeTableNameFromAMessageIsRefused(t *testing.T) {
	// Not pinned to a table, so the name comes from the message.
	sink := NewOracleSink(unroutable, "", nil, true, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	err := sink.WriteBatch(context.Background(), []hermod.Message{
		oracleMsg(t, `t (id) AS SELECT banner FROM v$version --`),
	})
	if err == nil {
		t.Fatal("a message carrying an unsafe table name was accepted")
	}
	if !strings.Contains(err.Error(), "refusing to build a statement") {
		t.Errorf("the write failed, but not because the sink refused the name: %v\n"+
			"the check has to run before the connection, or a rejected identifier is "+
			"indistinguishable from an unreachable server", err)
	}
}

// A mapped column name that cannot be quoted must be refused the same way —
// and by name. The QuoteIdent errors used to be discarded, which turned a
// rejected column into an empty identifier inside the statement.
func TestAnUnquotableColumnNameIsRefused(t *testing.T) {
	mappings := []sqlutil.ColumnMapping{
		{SourceField: "$.id", TargetColumn: "id", IsPrimaryKey: true},
		{SourceField: "$.name", TargetColumn: `name" , x AS (SELECT 1 FROM DUAL)) --`},
	}
	sink := NewOracleSink(unroutable, "t", mappings, true, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	err := sink.WriteBatch(context.Background(), []hermod.Message{oracleMsg(t, "t")})
	if err == nil {
		t.Fatal("a column name that ends its own quoting was accepted")
	}
	if !strings.Contains(err.Error(), "invalid column name") {
		t.Errorf("the write failed, but not because the sink refused the column: %v", err)
	}
}

// Ordinary names must pass, or the guards above would be indistinguishable
// from a sink that refuses everything. This asks the resolver directly rather
// than driving WriteBatch, which would dial the unroutable address.
func TestOrdinaryNamesArePassedThrough(t *testing.T) {
	sink := NewOracleSink(unroutable, "", nil, true, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	for _, name := range []string{"orders", "hr.orders", "ORDERS_2026"} {
		msg := oracleMsg(t, name)
		got, err := sink.resolveTable(msg)
		if err != nil {
			t.Errorf("an ordinary table name was refused: %q: %v", name, err)
			continue
		}
		if got != name {
			t.Errorf("resolveTable(%q) = %q, want it unchanged", name, got)
		}
	}

	for _, col := range []string{"id", "customer_name", "TOTAL"} {
		if _, err := qcol(col); err != nil {
			t.Errorf("an ordinary column name was refused: %q: %v", col, err)
		}
	}
}
