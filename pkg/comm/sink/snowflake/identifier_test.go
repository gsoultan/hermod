package snowflake

import (
	"context"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

// Identifier handling in the Snowflake sink.
//
// Snowflake is the one connector in this repository that cannot be exercised
// against a real server from a workstation or from CI: it is cloud-only and
// needs an account. Every other sink's identifier fix in this codebase was
// watched failing against a live server first. This one was not, and that is
// worth knowing when reading it.
//
// What makes it testable at all is where the check sits. The table name and the
// mapped column names are validated *before* anything connects, so a sink
// pointed at an address that does not exist still refuses a bad identifier
// rather than failing to dial. That is deliberate: a rejected identifier is the
// sink's own decision and should not depend on whether the warehouse happens to
// be reachable.
//
// So these run everywhere, with no account and no network, and they cover the
// property that actually matters. What they cannot cover is whether the quoted
// SQL Snowflake finally receives is accepted by Snowflake — that still needs a
// warehouse.

// unroutable is an address in the TEST-NET-1 range (RFC 5737). Nothing answers
// there, so a connection attempt cannot succeed: if these tests see a
// validation error, it was produced before any dialling.
const unroutable = "user:pass@unroutable/192.0.2.1:443/db/schema?account=x"

func snowflakeMsg(t *testing.T, table string) hermod.Message {
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
	sink := NewSink(unroutable, nil, "", nil, true, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	err := sink.WriteBatch(context.Background(), []hermod.Message{
		snowflakeMsg(t, `pwned (id) AS SELECT 1 --`),
	})
	if err == nil {
		t.Fatal("a message carrying an unsafe table name was accepted")
	}
	if !strings.Contains(err.Error(), "refusing to build a statement") {
		t.Errorf("the write failed, but not because the sink refused the name: %v\n"+
			"the check has to run before the connection, or a rejected identifier is "+
			"indistinguishable from an unreachable warehouse", err)
	}
}

// A mapped column name that cannot be quoted must be refused the same way.
func TestAnUnquotableColumnNameIsRefused(t *testing.T) {
	mappings := []sqlutil.ColumnMapping{
		{SourceField: "$.id", TargetColumn: "id", IsPrimaryKey: true},
		{SourceField: "$.name", TargetColumn: `name" , x AS (SELECT 1)) --`},
	}
	sink := NewSink(unroutable, nil, "t", mappings, true, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	err := sink.WriteBatch(context.Background(), []hermod.Message{snowflakeMsg(t, "t")})
	if err == nil {
		t.Fatal("a column name that ends its own quoting was accepted")
	}
	if !strings.Contains(err.Error(), "invalid column name") {
		t.Errorf("the write failed, but not because the sink refused the column: %v", err)
	}
}

// Ordinary names must pass, or the guard above would be indistinguishable from
// a sink that refuses everything.
//
// This asks the resolver directly rather than going through WriteBatch. Driving
// the whole path would mean the Snowflake driver building a login URL out of the
// DSN and attempting it, which is an outbound request from a test that has no
// business making one — and slow.
func TestOrdinaryNamesArePassedThrough(t *testing.T) {
	sink := NewSink(unroutable, nil, "", nil, true, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	for _, name := range []string{"orders", "sales.orders", "Orders_2026"} {
		msg := snowflakeMsg(t, name)
		got, err := sink.resolveTable(msg)
		if err != nil {
			t.Errorf("an ordinary table name was refused: %q: %v", name, err)
			continue
		}
		if got != name {
			t.Errorf("resolveTable(%q) = %q, want it unchanged", name, got)
		}
	}

	for _, col := range []string{"id", "customer_name", "Total"} {
		if _, err := qcol(col); err != nil {
			t.Errorf("an ordinary column name was refused: %q: %v", col, err)
		}
	}
}
