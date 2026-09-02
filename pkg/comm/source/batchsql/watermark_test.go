package batchsql

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/engine/telemetry"
	_ "modernc.org/sqlite"
)

// The BatchSQL watermark, against a real database.
//
// Two defects live here, and the existing test could see neither because its
// table held three rows with single-digit ids.
//
// First: the watermark maximum was computed on strings — fmt.Sprintf("%v") and
// a string compare — so on a numeric column, "10" < "9". Once the cursor
// reached 9 nothing between 10 and 89999… could ever raise it: WHERE id > 9
// re-selected every later row on every scheduled run, forever. Not data loss,
// but an unbounded duplicate storm that never heals and grows with the table.
//
// Second: the watermark advanced when a row was read — written into state
// before the message was even buffered, with Ack a no-op. GetState/SetState
// are the engine's persistence contract, so a crash after the state advanced
// and before downstream wrote the rows lost them for good. That is the fourth
// occurrence of this class in this repository; the position must move on Ack.

func numberedSource(t *testing.T, rows int) (*BatchSQLSource, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE TABLE nums (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= rows; i++ {
		if _, err := db.ExecContext(ctx, "INSERT INTO nums (name) VALUES (?)", fmt.Sprintf("row-%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	cfg := Config{
		SourceID:          "wm-test",
		Cron:              "* * * * * *",
		Queries:           "SELECT * FROM nums WHERE ('{{.last_value}}' = '' OR id > '{{.last_value}}') ORDER BY id",
		IncrementalColumn: "id",
	}
	src := NewBatchSQLSource(&mockDBProvider{db: db}, cfg)
	src.SetLogger(telemetry.NewDefaultLogger())
	t.Cleanup(func() { _ = src.Close() })
	return src, db
}

// Twelve rows: the lexicographic maximum of "1".."12" is "9", the numeric one
// is 12. After reading and acknowledging everything, the persisted cursor must
// be 12 — or the next scheduled run re-selects rows 10, 11 and 12, and every
// run after that does too.
func TestTheWatermarkIsNumericNotLexicographic(t *testing.T) {
	src, _ := numberedSource(t, 12)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	for i := 1; i <= 12; i++ {
		msg, err := src.Read(ctx)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if err := src.Ack(ctx, msg); err != nil {
			t.Fatalf("ack %d: %v", i, err)
		}
	}

	if got := src.GetState()["last_value"]; got != "12" {
		t.Errorf("after acknowledging rows 1..12 the cursor is %q, want 12\n"+
			"the maximum is taken on strings, and \"10\" < \"9\": every row past 9 "+
			"is re-selected by every future run", got)
	}
}

// The cursor must not run ahead of what was acknowledged. GetState is what the
// engine persists; if it already names the last row read, a crash before the
// sinks wrote the earlier rows erases them from the resume.
func TestTheCursorDoesNotAdvancePastUnacknowledgedRows(t *testing.T) {
	src, _ := numberedSource(t, 3)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	first, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Two more rows are read — in flight, not acknowledged.
	if _, err := src.Read(ctx); err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := src.Read(ctx); err != nil {
		t.Fatalf("read: %v", err)
	}

	if err := src.Ack(ctx, first); err != nil {
		t.Fatalf("ack: %v", err)
	}

	if got := src.GetState()["last_value"]; got != "1" {
		t.Errorf("one row is acknowledged and the cursor names %q, want 1\n"+
			"rows 2 and 3 are still in flight; a restart resuming from %q would "+
			"never deliver them", got, got)
	}
}

// What the cursor is for: a restart resuming from persisted state re-delivers
// what was never acknowledged and does not re-deliver what was.
func TestARestartResumesFromTheAcknowledgedRow(t *testing.T) {
	src, db := numberedSource(t, 3)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	first, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := src.Ack(ctx, first); err != nil {
		t.Fatalf("ack: %v", err)
	}
	carried := src.GetState()

	// A new instance — the restart — resumes from what was persisted.
	cfg := Config{
		SourceID:          "wm-test",
		Cron:              "* * * * * *",
		Queries:           "SELECT * FROM nums WHERE ('{{.last_value}}' = '' OR id > '{{.last_value}}') ORDER BY id",
		IncrementalColumn: "id",
	}
	resumed := NewBatchSQLSource(&mockDBProvider{db: db}, cfg)
	resumed.SetLogger(telemetry.NewDefaultLogger())
	t.Cleanup(func() { _ = resumed.Close() })
	resumed.SetState(carried)

	var names []string
	for range 2 {
		msg, err := resumed.Read(ctx)
		if err != nil {
			t.Fatalf("read after restart: %v", err)
		}
		names = append(names, fmt.Sprintf("%v", msg.Data()["name"]))
		if err := resumed.Ack(ctx, msg); err != nil {
			t.Fatalf("ack after restart: %v", err)
		}
	}
	if names[0] != "row-2" || names[1] != "row-3" {
		t.Errorf("the restart delivered %v, want the two unacknowledged rows row-2, row-3", names)
	}
}

// The messages carry snapshot semantics and their own watermark, so an ack can
// move the cursor without the source guessing which read it belonged to.
func TestRowsCarryTheirWatermark(t *testing.T) {
	src, _ := numberedSource(t, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	msg, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg.Operation() != hermod.OpSnapshot {
		t.Errorf("operation is %q, want %q", msg.Operation(), hermod.OpSnapshot)
	}
	if got := msg.Metadata()["batchsql_last_value"]; got != "1" {
		t.Errorf("the row carries watermark %q, want 1", got)
	}
}
