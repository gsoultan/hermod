package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/user/hermod/pkg/comm/message"
	_ "modernc.org/sqlite"
)

// The SQLite source's resume position, against a real database file.
//
// SQLite is GA — the tier that says "suitable for production" — and it carried
// the watermark-on-read defect that ten other sources in this repository have
// now been fixed for. The cursor advanced inside the read loop, before the row
// had been handed back, while Ack did nothing at all.
//
// GetState is what the engine writes down. A cursor moved at read time is
// already past rows still in flight, so a crash between reading and the sinks
// writing erases them from the resume — silently, because on restart the
// source begins after them.
//
// Nothing here needs infrastructure: SQLite is a file, so the whole data path
// is exercisable, which makes this the strongest version of the assertion
// rather than the weakest.

func numberedDB(t *testing.T, rows int) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "src.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE TABLE items (name TEXT)"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= rows; i++ {
		if _, err := db.ExecContext(ctx,
			"INSERT INTO items (name) VALUES (?)", fmt.Sprintf("row-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	return path, "items"
}

// Every row arrives once, in order — the behaviour the cursor exists to
// provide, pinned before the assertions about when it moves.
func TestEveryRowArrivesOnceInOrder(t *testing.T) {
	path, table := numberedDB(t, 3)
	src := NewSQLiteSource(path, []string{table}, true)
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	var got []string
	for i := 0; i < 3; i++ {
		msg, err := src.Read(ctx)
		if err != nil {
			t.Fatalf("read %d: %v", i+1, err)
		}
		got = append(got, fmt.Sprintf("%v", msg.Data()["name"]))
		if err := src.Ack(ctx, msg); err != nil {
			t.Fatalf("ack %d: %v", i+1, err)
		}
	}
	for i, want := range []string{"row-1", "row-2", "row-3"} {
		if got[i] != want {
			t.Errorf("row %d is %q, want %q (all: %v)", i+1, got[i], want, got)
		}
	}
}

// The persisted cursor must not run ahead of what was acknowledged.
func TestTheCursorDoesNotAdvancePastUnacknowledgedRows(t *testing.T) {
	path, table := numberedDB(t, 3)
	src := NewSQLiteSource(path, []string{table}, true)
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	first, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Two more read and left in flight, unacknowledged.
	for i := 0; i < 2; i++ {
		if _, err := src.Read(ctx); err != nil {
			t.Fatalf("read: %v", err)
		}
	}

	if err := src.Ack(ctx, first); err != nil {
		t.Fatalf("ack: %v", err)
	}

	if got := src.GetState()[table]; got != "1" {
		t.Errorf("one row is acknowledged and the cursor names %q, want 1\n"+
			"rows 2 and 3 are still in flight; a restart resuming from %q would "+
			"never deliver them", got, got)
	}
}

// What the cursor is for: a restart resuming from persisted state re-delivers
// exactly what was never acknowledged.
func TestARestartResumesFromTheAcknowledgedRow(t *testing.T) {
	path, table := numberedDB(t, 3)
	src := NewSQLiteSource(path, []string{table}, true)

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	first, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Read two more without acknowledging them, then crash.
	for i := 0; i < 2; i++ {
		if _, err := src.Read(ctx); err != nil {
			t.Fatalf("read: %v", err)
		}
	}
	if err := src.Ack(ctx, first); err != nil {
		t.Fatalf("ack: %v", err)
	}
	carried := src.GetState()
	_ = src.Close()

	resumed := NewSQLiteSource(path, []string{table}, true)
	t.Cleanup(func() { _ = resumed.Close() })
	resumed.SetState(carried)

	var got []string
	for i := 0; i < 2; i++ {
		msg, err := resumed.Read(ctx)
		if err != nil {
			t.Fatalf("read after restart %d: %v", i+1, err)
		}
		got = append(got, fmt.Sprintf("%v", msg.Data()["name"]))
		if err := resumed.Ack(ctx, msg); err != nil {
			t.Fatalf("ack after restart: %v", err)
		}
	}
	if got[0] != "row-2" || got[1] != "row-3" {
		t.Errorf("the restart delivered %v, want the two unacknowledged rows row-2 and row-3", got)
	}
}

// A nil acknowledgement must not panic: the conformance suite feeds every
// source one, because a worker goroutine that dereferences it takes the engine
// down.
func TestAckOfNilDoesNotPanic(t *testing.T) {
	path, table := numberedDB(t, 1)
	src := NewSQLiteSource(path, []string{table}, true)
	t.Cleanup(func() { _ = src.Close() })

	if err := src.Ack(t.Context(), nil); err != nil {
		t.Fatalf("ack(nil): %v", err)
	}
}

// A watermark that cannot be parsed must leave the cursor alone rather than
// clearing it or advancing past the row.
//
// This source writes the value itself with FormatInt, so an unparseable one
// means the metadata was rewritten or dropped downstream. Leaving the cursor
// put redelivers the row, which is the safe direction; skipping it would lose
// it, and returning an error would tell the engine the acknowledgement failed
// and have it present the same message forever.
func TestAnUnparseableWatermarkLeavesTheCursorAlone(t *testing.T) {
	path, table := numberedDB(t, 2)
	src := NewSQLiteSource(path, []string{table}, true)
	t.Cleanup(func() { _ = src.Close() })

	src.SetState(map[string]string{table: "1"})

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("corrupt")
	msg.SetTable(table)
	msg.SetMetadata(watermarkKey, "not-a-rowid")

	if err := src.Ack(t.Context(), msg); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if got := src.GetState()[table]; got != "1" {
		t.Errorf("an unparseable watermark moved the cursor to %q, want it left at 1", got)
	}
}
