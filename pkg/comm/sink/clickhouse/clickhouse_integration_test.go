//go:build integration

package clickhouse

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	"github.com/gsoultan/Hermod/pkg/infra/sqlutil"
)

// The ClickHouse sink, against a real server.
//
// ClickHouse was Beta: substantial and unit-tested, never shown to move a
// record. The properties worth a server are the ordinary ones — an insert
// lands, a delete removes — and one that only shows up with a server, because
// ClickHouse deletes are asynchronous mutations rather than statements that
// take effect when they return.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 CLICKHOUSE_ADDR=127.0.0.1:9000 \
//	go test -tags=integration ./pkg/comm/sink/clickhouse/

func requireClickHouse(t *testing.T) (string, string, driver.Conn) {
	t.Helper()
	addr := os.Getenv("CLICKHOUSE_ADDR")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || addr == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q CLICKHOUSE_ADDR=%q in CI, where a server is "+
				"started for exactly this", os.Getenv("HERMOD_INTEGRATION"), addr)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and CLICKHOUSE_ADDR to run")
	}

	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{addr},
		Auth: clickhouse.Auth{Database: "default"},
	})
	if err != nil {
		t.Fatalf("connecting to %s: %v", addr, err)
	}
	if err := conn.Ping(t.Context()); err != nil {
		t.Fatalf("ClickHouse at %s is not reachable: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	table := "ch_" + strings.ToLower(t.Name())
	_ = conn.Exec(context.Background(), "DROP TABLE IF EXISTS default."+table)
	t.Cleanup(func() {
		_ = conn.Exec(context.Background(), "DROP TABLE IF EXISTS default."+table)
	})
	return addr, table, conn
}

func rowCount(t *testing.T, conn driver.Conn, table, id string) uint64 {
	t.Helper()
	var n uint64
	q := fmt.Sprintf("SELECT count() FROM default.%s WHERE id = ?", table)
	if err := conn.QueryRow(context.Background(), q, id).Scan(&n); err != nil {
		t.Fatalf("counting %s id=%s: %v", table, id, err)
	}
	return n
}

// waitForMutations blocks until ClickHouse has applied outstanding mutations
// for the table. Deletes are asynchronous, so asserting immediately after
// WriteBatch returns would test the queue rather than the outcome.
func waitForMutations(t *testing.T, conn driver.Conn, table string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var pending uint64
		err := conn.QueryRow(context.Background(),
			"SELECT count() FROM system.mutations WHERE database = 'default' AND table = ? AND is_done = 0",
			table).Scan(&pending)
		if err == nil && pending == 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("mutations on %s did not finish within 30s", table)
}

func newMsg(t *testing.T, id string, op hermod.Operation, payload string) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(m.Release)
	m.SetID(id)
	m.SetOperation(op)
	m.SetPayload([]byte(payload))
	return m
}

// The ordinary path: an insert lands and can be read back.
func TestAnInsertLands(t *testing.T) {
	addr, table, conn := requireClickHouse(t)
	sink := NewClickHouseSink(addr, "default", table, nil, false, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	ctx := t.Context()
	if err := sink.WriteBatch(ctx, []hermod.Message{
		newMsg(t, "a", hermod.OpCreate, `{"v":1}`),
		newMsg(t, "b", hermod.OpCreate, `{"v":2}`),
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	if n := rowCount(t, conn, table, "a"); n != 1 {
		t.Errorf("row a landed %d times, want 1", n)
	}
	if n := rowCount(t, conn, table, "b"); n != 1 {
		t.Errorf("row b landed %d times, want 1", n)
	}
}

// A delete in the same batch as inserts must remove its row and leave it
// removed.
//
// The batch loop iterated every message rather than only the inserts, so a
// delete was issued *and then the same row appended right back* in the same
// call. ClickHouse's ALTER TABLE ... DELETE is an asynchronous mutation over
// the parts that exist when it is created, and the re-insert lands in a new
// part the mutation never covers — so the row is not merely deleted late, it
// is permanently back.
func TestADeleteInAMixedBatchDoesNotComeBack(t *testing.T) {
	addr, table, conn := requireClickHouse(t)
	sink := NewClickHouseSink(addr, "default", table, nil, false, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	ctx := t.Context()

	// Seed, so the delete has something to remove.
	if err := sink.WriteBatch(ctx, []hermod.Message{
		newMsg(t, "keep", hermod.OpCreate, `{"v":1}`),
		newMsg(t, "gone", hermod.OpCreate, `{"v":2}`),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A CDC batch carrying an insert and a delete together, which is what a
	// change stream produces all the time.
	if err := sink.WriteBatch(ctx, []hermod.Message{
		newMsg(t, "fresh", hermod.OpCreate, `{"v":3}`),
		newMsg(t, "gone", hermod.OpDelete, `{"v":2}`),
	}); err != nil {
		t.Fatalf("mixed batch: %v", err)
	}
	waitForMutations(t, conn, table)

	if n := rowCount(t, conn, table, "gone"); n != 0 {
		t.Errorf("the deleted row is present %d time(s) after the batch that deleted it\n"+
			"the insert loop walks every message rather than only the inserts, so the "+
			"delete is issued and the same row appended straight back — and because the "+
			"re-insert lands in a part the asynchronous mutation never covers, it stays",
			n)
	}
	if n := rowCount(t, conn, table, "keep"); n != 1 {
		t.Errorf("an unrelated row was disturbed: keep present %d times, want 1", n)
	}
	if n := rowCount(t, conn, table, "fresh"); n != 1 {
		t.Errorf("the insert in the mixed batch landed %d times, want 1", n)
	}
}

// The table name can come from the message, and nothing checked it.
//
// When the sink is not pinned to a table it takes one from msgs[0].Table(), and
// that name is interpolated into INSERT, ALTER TABLE ... DELETE and CREATE
// TABLE with fmt.Sprintf. A message's table originates upstream — for a webhook
// or a generic source, on the wire — so this is an identifier from outside
// being pasted into SQL.
//
// The PostgreSQL sink already solved exactly this and says why in its own
// comment: sink config comes from an authenticated editor, but the fallback
// comes from the message, so the name is validated and an unsafe one fails the
// write instead of executing it. This sink had no such check.
func TestAnUnsafeTableNameFromAMessageIsRefused(t *testing.T) {
	addr, _, conn := requireClickHouse(t)

	// Not pinned to a table, so the name comes from the message.
	sink := NewClickHouseSink(addr, "default", "", nil, false, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("a")
	msg.SetOperation(hermod.OpCreate)
	msg.SetPayload([]byte(`{"v":1}`))
	// Deliberately a single statement. A version with a semicolon is rejected
	// by ClickHouse itself ("Multi-statements are not allowed") — the server
	// saving us rather than the sink being safe, and a test that used one
	// would pass without proving anything. Without the semicolon the injected
	// text is part of one perfectly legal CREATE TABLE, and before this was
	// fixed it ran: a table named `pwned` appeared in the destination with the
	// schema and the ENGINE the message asked for.
	msg.SetTable(`pwned (id String) ENGINE = Memory --`)

	err := sink.WriteBatch(t.Context(), []hermod.Message{msg})
	if err == nil {
		t.Fatal("a message carrying an unsafe table name was written without complaint\n" +
			"the name is interpolated into CREATE TABLE, INSERT and ALTER TABLE, and a " +
			"message's table comes from upstream — the PostgreSQL sink validates exactly " +
			"this and refuses, and this one did not")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "table") {
		t.Errorf("the refusal does not mention the table name: %v", err)
	}

	// The part that matters: no table of the injected shape may exist.
	var n uint64
	if err := conn.QueryRow(context.Background(),
		"SELECT count() FROM system.tables WHERE database = 'default' AND name = 'pwned'").
		Scan(&n); err != nil {
		t.Fatalf("checking system.tables: %v", err)
	}
	if n != 0 {
		t.Errorf("a message's table name created the table `pwned` in the destination\n" +
			"the name is interpolated into CREATE TABLE, so a message chose the table, its " +
			"schema and its engine — the PostgreSQL sink validates this exact input and " +
			"refuses; this one did not")
		_ = conn.Exec(context.Background(), "DROP TABLE IF EXISTS default.pwned")
	}
}

// The mapped-column path, which is a different construction from the (id, data)
// one every other test here uses — a different INSERT, a different DELETE, and
// its own CREATE TABLE. It is also the path anyone with a real destination
// schema is on, and none of it had been exercised.
func TestMappedColumnsInsertAndDelete(t *testing.T) {
	addr, table, conn := requireClickHouse(t)

	mappings := []sqlutil.ColumnMapping{
		{SourceField: "$.id", TargetColumn: "id", DataType: "String", IsPrimaryKey: true},
		{SourceField: "$.name", TargetColumn: "name", DataType: "String"},
	}
	sink := NewClickHouseSink(addr, "default", table, mappings, false, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("a")
	msg.SetOperation(hermod.OpCreate)
	msg.SetData("id", "a")
	msg.SetData("name", "ada")

	if err := sink.WriteBatch(t.Context(), []hermod.Message{msg}); err != nil {
		t.Fatalf("mapped insert: %v", err)
	}

	var name string
	if err := conn.QueryRow(context.Background(),
		fmt.Sprintf("SELECT name FROM default.%s WHERE id = ?", table), "a").Scan(&name); err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if name != "ada" {
		t.Errorf("mapped column landed as %q, want ada", name)
	}

	del := message.AcquireMessage()
	t.Cleanup(del.Release)
	del.SetID("a")
	del.SetOperation(hermod.OpDelete)
	del.SetData("id", "a")
	if err := sink.WriteBatch(t.Context(), []hermod.Message{del}); err != nil {
		t.Fatalf("mapped delete: %v", err)
	}
	waitForMutations(t, conn, table)
	if n := rowCount(t, conn, table, "a"); n != 0 {
		t.Errorf("the mapped delete left %d row(s)", n)
	}
}

// A mapped column name that would break out of its own quoting.
//
// The mapped path pasted TargetColumn straight into INSERT, DELETE, CREATE
// TABLE and ALTER TABLE with no quoting at all — not even the manual backticks
// the MySQL sink used. ClickHouse quotes identifiers with double quotes or
// backticks, so a name containing one ends its own identifier and the rest is
// read as SQL.
//
// These names come from sink configuration rather than from a message, so this
// is hardening rather than an incident. The assertion is that the sink refuses
// the name, not merely that the write fails: a server rejecting one payload is
// a different property from the name never reaching a statement.
func TestAMappedColumnNameCannotBreakOutOfItsQuoting(t *testing.T) {
	addr, table, _ := requireClickHouse(t)

	mappings := []sqlutil.ColumnMapping{
		{SourceField: "$.id", TargetColumn: "id", DataType: "String", IsPrimaryKey: true},
		{SourceField: "$.name", TargetColumn: `name" , x String) ENGINE = Memory --`, DataType: "String"},
	}
	sink := NewClickHouseSink(addr, "default", table, mappings, false, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("a")
	msg.SetOperation(hermod.OpCreate)
	msg.SetData("id", "a")
	msg.SetData("name", "ada")

	err := sink.WriteBatch(t.Context(), []hermod.Message{msg})
	if err == nil {
		t.Fatal("a column name that ends its own quoting was accepted")
	}
	if !strings.Contains(err.Error(), "invalid column name") {
		t.Errorf("the write failed, but not because the sink refused the name: %v\n"+
			"that is the server rejecting one payload rather than the name being kept out "+
			"of the statement, and a name chosen to parse would have run", err)
	}
}
