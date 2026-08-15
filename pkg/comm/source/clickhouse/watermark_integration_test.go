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
)

// Where the ClickHouse source resumes after a restart.
//
// This is a watermark poller: it remembers the highest id it has seen per table
// and asks for rows above it. The watermark was advanced inside Read, the moment
// a row was fetched and before the message was handed to anyone — and GetState
// reports that watermark, which the engine persists on every acknowledgement
// (registry_routing.go, statefulSource.Ack).
//
// So the position written down was always one row ahead of what had been
// delivered. A worker that died with a message in flight came back past it, and
// the row was never handed out again: no error, no gap anyone could see, just a
// record that never arrived at the destination.
//
// This is the third connector with this exact shape. MongoDB had it, MySQL had
// it, and the fix is the same in all three: the position that gets persisted
// moves on acknowledgement, not on read.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 CLICKHOUSE_ADDR=127.0.0.1:9000 \
//	go test -tags=integration ./pkg/comm/source/clickhouse/

func requireCH(t *testing.T) (string, string, driver.Conn) {
	t.Helper()
	addr := os.Getenv("CLICKHOUSE_ADDR")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || addr == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q CLICKHOUSE_ADDR=%q in CI", os.Getenv("HERMOD_INTEGRATION"), addr)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and CLICKHOUSE_ADDR to run")
	}

	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{addr},
		Auth: clickhouse.Auth{Database: "default"},
	})
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	if err := conn.Ping(t.Context()); err != nil {
		t.Fatalf("ClickHouse is not reachable: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	table := "src_" + strings.ToLower(t.Name())
	_ = conn.Exec(context.Background(), "DROP TABLE IF EXISTS default."+table)
	if err := conn.Exec(t.Context(), fmt.Sprintf(
		"CREATE TABLE default.%s (id UInt64, name String) ENGINE = MergeTree() ORDER BY id",
		table)); err != nil {
		t.Fatalf("creating %s: %v", table, err)
	}
	t.Cleanup(func() { _ = conn.Exec(context.Background(), "DROP TABLE IF EXISTS default."+table) })

	for i := 1; i <= 4; i++ {
		if err := conn.Exec(t.Context(), fmt.Sprintf(
			"INSERT INTO default.%s (id, name) VALUES (%d, 'row%d')", table, i, i)); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}
	return addr, table, conn
}

func chDSN(addr string) string {
	return "clickhouse://default@" + addr + "/default"
}

// Reading is not delivering. Until a row is acknowledged, the position a restart
// resumes from must not have moved past it.
func TestStateDoesNotAdvancePastUnacknowledgedRows(t *testing.T) {
	addr, table, _ := requireCH(t)

	src := NewClickHouseSource(chDSN(addr), []string{table}, "id", 200*time.Millisecond, true)
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	// Read three rows and acknowledge none of them: a pipeline that has pulled
	// work and not yet finished it.
	for i := range 3 {
		msg, err := src.Read(ctx)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if msg == nil {
			t.Fatalf("read %d returned no message", i)
		}
	}

	if got := src.GetState()["last_id:"+table]; got != "" {
		t.Errorf("after reading 3 rows and acknowledging none, the source reports "+
			"last_id=%s\n"+
			"the engine persists this on every ack, so a restart resumes past rows that "+
			"were read and never delivered, and they are never handed out again", got)
	}
}

// The consequence, which is the part that matters.
func TestUnacknowledgedRowsAreRedeliveredAfterRestart(t *testing.T) {
	addr, table, _ := requireCH(t)

	first := NewClickHouseSource(chDSN(addr), []string{table}, "id", 200*time.Millisecond, true)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	// Acknowledge the first row only. Two more are read and left in flight, as
	// they would be when a worker dies between source and sink.
	msg, err := first.Read(ctx)
	if err != nil {
		t.Fatalf("read 0: %v", err)
	}
	if err := first.Ack(ctx, msg); err != nil {
		t.Fatalf("ack 0: %v", err)
	}
	for i := 1; i < 3; i++ {
		if _, err := first.Read(ctx); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}

	state := first.GetState()
	_ = first.Close()

	second := NewClickHouseSource(chDSN(addr), []string{table}, "id", 200*time.Millisecond, true)
	second.SetState(state)
	t.Cleanup(func() { _ = second.Close() })

	readCtx, readCancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer readCancel()

	var ids []uint64
	for range 2 {
		m, err := second.Read(readCtx)
		if err != nil {
			t.Fatalf("reading from the replacement source: %v", err)
		}
		if m == nil {
			continue
		}
		var id uint64
		if _, err := fmt.Sscanf(strings.TrimPrefix(m.ID(), "clickhouse-"+table+"-"), "%d", &id); err != nil {
			t.Fatalf("cannot read the row id back from %q: %v", m.ID(), err)
		}
		ids = append(ids, id)
	}

	// Rows 2 and 3 were read and never acknowledged, so they are the ones a
	// restart owes.
	if len(ids) != 2 || ids[0] != 2 || ids[1] != 3 {
		t.Errorf("after a restart with one row of three acknowledged, the replacement "+
			"source delivered %v, want [2 3]\n"+
			"the watermark advanced when each row was read rather than when it was "+
			"acknowledged, so the work that was in flight is skipped and lost", ids)
	}
}
