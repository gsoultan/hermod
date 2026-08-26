//go:build integration

package oracle

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/sijms/go-ora/v2"
	"github.com/user/hermod"
)

// The Oracle source, against a real server.
//
// Two reasons this exists. The source builds its polling SQL through the same
// sqlutil helpers whose identifier casing changed when the sink's ORA-00904
// was fixed — that change was verified against the sink and merely inherited
// here, which is not the same as tested. And the watermark is advanced inside
// the read loop while Ack does nothing, which is the shape that has already
// cost this project four separate sources their unacknowledged rows.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 \
//	ORACLE_DSN='oracle://system:hermod_test@127.0.0.1:1521/FREEPDB1' \
//	go test -tags=integration ./pkg/comm/source/oracle/
//
// Not wired into CI, for the same reason as the sink suite: Oracle Free wants
// ~2GB and a slow first boot, and the integration job already runs at the edge
// of a 7GB runner.
func requireOracle(t *testing.T) (string, *sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("ORACLE_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and ORACLE_DSN to run (not run in CI)")
	}

	db, err := sql.Open("oracle", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("ORACLE_DSN names a server that is not reachable: %v", err)
	}

	table := "HSRC_" + strings.ToUpper(strings.ReplaceAll(t.Name(), "/", "_"))
	if len(table) > 28 {
		table = table[:28]
	}
	drop := func() {
		_, _ = db.ExecContext(context.Background(),
			fmt.Sprintf("BEGIN EXECUTE IMMEDIATE 'DROP TABLE %s'; EXCEPTION WHEN OTHERS THEN NULL; END;", table))
	}
	drop()
	t.Cleanup(drop)

	// Ordinary unquoted DDL, so Oracle folds the column names to upper case —
	// the shape a real schema has.
	if _, err := db.ExecContext(t.Context(), fmt.Sprintf(
		"CREATE TABLE %s (id NUMBER PRIMARY KEY, name VARCHAR2(64))", table)); err != nil {
		t.Fatalf("create: %v", err)
	}
	return dsn, db, table
}

func insertRow(t *testing.T, db *sql.DB, table string, id int, name string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(),
		fmt.Sprintf("INSERT INTO %s (id, name) VALUES (:1, :2)", table), id, name); err != nil {
		t.Fatalf("insert %d: %v", id, err)
	}
}

// Rows come back from a conventionally-named table, in id order. This is the
// casing change exercised through the source's own polling SQL: before it, the
// generated WHERE/ORDER BY named a lower-case column that ordinary DDL never
// creates, and every poll failed with ORA-00904.
func TestOracleSourceReadsFromAConventionallyNamedTable(t *testing.T) {
	dsn, db, table := requireOracle(t)
	for i := 1; i <= 3; i++ {
		insertRow(t, db, table, i, fmt.Sprintf("row-%d", i))
	}

	src := NewOracleSource(dsn, []string{table}, "ID", 100*time.Millisecond, true)
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	var names []string
	for i := 0; i < 3; i++ {
		msg, err := src.Read(ctx)
		if err != nil {
			t.Fatalf("read %d: %v", i+1, err)
		}
		if msg.Operation() != hermod.OpCreate {
			t.Errorf("operation is %q, want %q", msg.Operation(), hermod.OpCreate)
		}
		names = append(names, string(msg.After()))
		if err := src.Ack(ctx, msg); err != nil {
			t.Fatalf("ack %d: %v", i+1, err)
		}
	}

	for i, want := range []string{"row-1", "row-2", "row-3"} {
		if !strings.Contains(names[i], want) {
			t.Errorf("row %d is %s, want it to contain %s", i+1, names[i], want)
		}
	}
}

// The persisted cursor must not run ahead of what was acknowledged.
//
// GetState is what the engine writes down; if it already names the last row
// *read*, a crash before the sinks wrote the earlier ones erases them from the
// resume. This source advanced the watermark inside the read loop with Ack as
// a no-op — the same defect already fixed in the ClickHouse, MongoDB, MySQL
// and BatchSQL sources.
func TestTheCursorDoesNotAdvancePastUnacknowledgedRows(t *testing.T) {
	dsn, db, table := requireOracle(t)
	for i := 1; i <= 3; i++ {
		insertRow(t, db, table, i, fmt.Sprintf("row-%d", i))
	}

	src := NewOracleSource(dsn, []string{table}, "ID", 100*time.Millisecond, true)
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	first, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Two more read and left in flight, unacknowledged.
	if _, err := src.Read(ctx); err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := src.Read(ctx); err != nil {
		t.Fatalf("read: %v", err)
	}

	if err := src.Ack(ctx, first); err != nil {
		t.Fatalf("ack: %v", err)
	}

	got := src.GetState()["last_id:"+table]
	if got != "1" {
		t.Errorf("one row is acknowledged and the cursor names %q, want 1\n"+
			"rows 2 and 3 are still in flight; a restart resuming from %q would "+
			"never deliver them", got, got)
	}
}

// What the cursor is for: a restart resuming from persisted state re-delivers
// exactly what was never acknowledged.
func TestARestartResumesFromTheAcknowledgedRow(t *testing.T) {
	dsn, db, table := requireOracle(t)
	for i := 1; i <= 3; i++ {
		insertRow(t, db, table, i, fmt.Sprintf("row-%d", i))
	}

	src := NewOracleSource(dsn, []string{table}, "ID", 100*time.Millisecond, true)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	first, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := src.Ack(ctx, first); err != nil {
		t.Fatalf("ack: %v", err)
	}
	carried := src.GetState()
	_ = src.Close()

	resumed := NewOracleSource(dsn, []string{table}, "ID", 100*time.Millisecond, true)
	t.Cleanup(func() { _ = resumed.Close() })
	resumed.SetState(carried)

	var names []string
	for i := 0; i < 2; i++ {
		msg, err := resumed.Read(ctx)
		if err != nil {
			t.Fatalf("read after restart %d: %v", i+1, err)
		}
		names = append(names, string(msg.After()))
		if err := resumed.Ack(ctx, msg); err != nil {
			t.Fatalf("ack after restart: %v", err)
		}
	}
	if !strings.Contains(names[0], "row-2") || !strings.Contains(names[1], "row-3") {
		t.Errorf("the restart delivered %v, want the two unacknowledged rows row-2 and row-3", names)
	}
}
