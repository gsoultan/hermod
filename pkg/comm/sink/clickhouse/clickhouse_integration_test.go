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
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
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
