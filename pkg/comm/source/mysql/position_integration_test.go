//go:build integration

package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	hermod "github.com/gsoultan/Hermod"
)

// Where the MySQL CDC source starts reading, and where it comes back to.
//
// The source had no answer to either question. It implemented none of
// hermod.Stateful, so nothing was ever persisted, and canal was handed an empty
// master position. An empty position is not "start here now" — it is a
// COM_BINLOG_DUMP with an empty filename, which MySQL answers with the *oldest
// binlog it still has*. So every start replayed the entire retained binlog, and
// every restart replayed it again, with no way to resume from what had actually
// been delivered.
//
// These clauses hold the three properties that follow from fixing it, and
// each is a distinct failure:
//
//  1. a first run starts from now, not from the beginning of history;
//  2. the position advances when a message is acknowledged, not when it is read;
//  3. a restart resumes from that position, so changes made while the source
//     was down are delivered rather than skipped.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 \
//	MYSQL_DSN='root:root@tcp(127.0.0.1:3306)/hermod_it?parseTime=true' \
//	go test -tags=integration ./pkg/comm/source/mysql/

func requireMySQL(t *testing.T) (string, *sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" || os.Getenv("HERMOD_INTEGRATION") != "1" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q MYSQL_DSN=%q in CI, where a MySQL service is "+
				"started for exactly this; the CDC source would go unexercised and the run "+
				"would still be green",
				os.Getenv("HERMOD_INTEGRATION"), dsn)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and MYSQL_DSN to run")
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("opening MYSQL_DSN (%s): %v", dsn, err)
	}
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("MYSQL_DSN names a server that is not reachable (%s): %v", dsn, err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// A binlog is a shared stream and this server is shared with every other
	// test, so each one gets a table nothing else writes to and filters on it.
	table := fmt.Sprintf("pos_%d", time.Now().UnixNano())
	if _, err := db.ExecContext(t.Context(),
		fmt.Sprintf("CREATE TABLE %s (id INT PRIMARY KEY, note VARCHAR(64))", table)); err != nil {
		t.Fatalf("creating %s: %v", table, err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
	})
	return dsn, db, table
}

func insertRow(t *testing.T, db *sql.DB, table string, id int, note string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(),
		fmt.Sprintf("INSERT INTO %s (id, note) VALUES (?, ?)", table), id, note); err != nil {
		t.Fatalf("inserting id=%d into %s: %v", id, table, err)
	}
}

// readOurRows reads until it has seen want messages for this test's table, or
// the context expires. Everything else on the shared binlog is discarded.
func readOurRows(ctx context.Context, src *MySQLSource, table string, want int) []map[string]any {
	var out []map[string]any
	for len(out) < want {
		msg, err := src.Read(ctx)
		if err != nil {
			return out
		}
		if msg == nil || msg.Table() != table {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal(msg.After(), &row); err != nil {
			continue
		}
		out = append(out, row)
	}
	return out
}

// A source starting for the first time must begin at the server's current
// position. Beginning at the oldest retained binlog replays every change the
// server still has on disk — for a busy server that is days of history
// delivered as if it were new, on every single start.
func TestFirstRunStartsFromCurrentPositionNotTheOldestBinlog(t *testing.T) {
	_, db, table := requireMySQL(t)
	dsn := os.Getenv("MYSQL_DSN")

	// Written before the source exists. It is in the binlog, so a source that
	// starts from the beginning will hand it over as though it were new.
	insertRow(t, db, table, 1, "before the source started")

	src := NewMySQLSource(dsn, true)
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	// Force the reader to start before writing the row it should see.
	if err := src.init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}
	// canal connects asynchronously; give it a moment to register before the
	// write, or the row races the dump connection.
	time.Sleep(2 * time.Second)

	insertRow(t, db, table, 2, "after the source started")

	readCtx, readCancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer readCancel()

	rows := readOurRows(readCtx, src, table, 1)
	if len(rows) == 0 {
		t.Fatal("the source delivered nothing for its own table; it is not streaming at all")
	}
	if id, _ := rows[0]["id"].(float64); int(id) != 2 {
		t.Errorf("the first row delivered was id=%v, want id=2\n"+
			"id=1 was written before the source started, so delivering it means the source "+
			"began at the oldest binlog the server still holds rather than at the current "+
			"position — every start replays the whole retained history",
			rows[0]["id"])
	}
}

// Reading is not delivering. Until a message is acknowledged, the position a
// restart resumes from must not have moved past it.
func TestBinlogPositionAdvancesOnAckNotOnRead(t *testing.T) {
	_, db, table := requireMySQL(t)
	dsn := os.Getenv("MYSQL_DSN")

	src := NewMySQLSource(dsn, true)
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := src.init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}
	time.Sleep(2 * time.Second)

	insertRow(t, db, table, 1, "read but never acknowledged")

	readCtx, readCancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer readCancel()

	before := src.GetState()
	rows := readOurRows(readCtx, src, table, 1)
	if len(rows) == 0 {
		t.Fatal("the source delivered nothing for its own table")
	}
	after := src.GetState()

	if before["binlog_file"] != after["binlog_file"] || before["binlog_pos"] != after["binlog_pos"] {
		t.Errorf("reading a message moved the persisted position from %v to %v\n"+
			"the engine writes this down on every ack, so a restart resumes past messages "+
			"that were read and never delivered and they are never handed out again",
			before, after)
	}
}

// The consequence, which is the part that matters: a change made while the
// source is down has to arrive when it comes back.
func TestChangesDuringDowntimeAreDeliveredAfterRestart(t *testing.T) {
	_, db, table := requireMySQL(t)
	dsn := os.Getenv("MYSQL_DSN")

	first := NewMySQLSource(dsn, true)
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	if err := first.init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}
	time.Sleep(2 * time.Second)

	insertRow(t, db, table, 1, "delivered before the restart")

	readCtx, readCancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer readCancel()
	for {
		msg, err := first.Read(readCtx)
		if err != nil {
			t.Fatalf("reading the first row: %v", err)
		}
		if msg == nil || msg.Table() != table {
			continue
		}
		if err := first.Ack(readCtx, msg); err != nil {
			t.Fatalf("ack: %v", err)
		}
		break
	}

	state := first.GetState()
	_ = first.Close()

	if state["binlog_file"] == "" {
		t.Fatal("the source reports no binlog position after acknowledging a change, so a " +
			"restart has nothing to resume from and begins wherever canal defaults to")
	}

	// The window in which the source is not running. This is the change that
	// gets silently skipped when there is no resume position.
	insertRow(t, db, table, 2, "written while the source was down")

	second := NewMySQLSource(dsn, true)
	second.SetState(state)
	t.Cleanup(func() { _ = second.Close() })

	resumeCtx, resumeCancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer resumeCancel()

	rows := readOurRows(resumeCtx, second, table, 1)
	if len(rows) == 0 {
		t.Fatal("the restarted source delivered nothing at all")
	}
	if id, _ := rows[0]["id"].(float64); int(id) != 2 {
		t.Errorf("after a restart the first row delivered was id=%v, want id=2 — the row "+
			"written while the source was down\n"+
			"the resume position is not being honoured, so a worker restart loses every "+
			"change made in the gap and reports nothing wrong",
			rows[0]["id"])
	}
}

// The rows that were already in the table when the workflow started.
func TestInitialLoadCarriesExistingRows(t *testing.T) {
	_, db, table := requireMySQL(t)
	dsn := os.Getenv("MYSQL_DSN")

	for i := 1; i <= 3; i++ {
		insertRow(t, db, table, i, fmt.Sprintf("existing row %d", i))
	}

	src := NewMySQLSource(dsn, true)
	src.SetInitialLoad(true)
	src.SetTables(table)
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()

	rows := readOurRows(ctx, src, table, 3)
	if len(rows) != 3 {
		t.Fatalf("initial load delivered %d of the 3 rows already in %s\n"+
			"a binlog reader reports only what happens after it starts, so without a backfill "+
			"a workflow started against an existing table moves nothing until somebody writes "+
			"to it",
			len(rows), table)
	}
}

// Once, not on every restart.
func TestInitialLoadRunsOnlyOnce(t *testing.T) {
	_, db, table := requireMySQL(t)
	dsn := os.Getenv("MYSQL_DSN")

	for i := 1; i <= 3; i++ {
		insertRow(t, db, table, i, fmt.Sprintf("existing row %d", i))
	}

	first := NewMySQLSource(dsn, true)
	first.SetInitialLoad(true)
	first.SetTables(table)

	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()

	// Read and acknowledge this table's three rows. Counting loop iterations
	// rather than matching rows would stop early, because the binlog is shared
	// and a message for somebody else's table consumes an iteration too.
	acked := 0
	for acked < 3 {
		msg, err := first.Read(ctx)
		if err != nil {
			t.Fatalf("reading the backfill (%d of 3 acknowledged): %v", acked, err)
		}
		if msg == nil || msg.Table() != table {
			continue
		}
		if err := first.Ack(ctx, msg); err != nil {
			t.Fatalf("ack: %v", err)
		}
		acked++
	}

	// The completion flag is set by the backfill goroutine once every row is
	// handed over, which is deliberately *after* the last one reaches the
	// buffer — recording it earlier would mean a crash mid-backfill left a
	// record saying the table had been carried across when it had not. So the
	// last ack can land a moment before the flag does. The engine persists
	// state on every ack and would pick it up on the next one; here, wait.
	var state map[string]string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		state = first.GetState()
		if state["initial_load"] == "done" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = first.Close()

	if state["initial_load"] != "done" {
		t.Fatalf("after a completed backfill the source reports initial_load=%q, want \"done\"\n"+
			"nothing else distinguishes a first run from a resumed one, so the whole table is "+
			"re-read on every restart",
			state["initial_load"])
	}

	second := NewMySQLSource(dsn, true)
	second.SetInitialLoad(true)
	second.SetTables(table)
	second.SetState(state)
	t.Cleanup(func() { _ = second.Close() })

	resumeCtx, resumeCancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer resumeCancel()

	insertRow(t, db, table, 4, "after the restart")

	for {
		msg, err := second.Read(resumeCtx)
		if err != nil {
			t.Fatal("the restarted source delivered nothing; expected the row written after it " +
				"came back")
		}
		if msg == nil || msg.Table() != table {
			continue
		}
		if msg.Operation() == hermod.OpSnapshot {
			t.Fatalf("the restarted source emitted a snapshot message for %s\n"+
				"the completed backfill was recorded in state and read back, yet the table was "+
				"read again anyway: every restart re-copies the whole table",
				table)
		}
		break
	}
}

// Backpressure, rather than dropping what does not fit.
//
// The row handler offered each message to the buffer and released it when the
// buffer was full, which is a silent delete: no error, no log, no metric, and
// the binlog position moves on regardless. Any burst larger than the 64-message
// buffer lost its tail, and the resume position made it permanent — the dropped
// rows were never acknowledged, but the rows after them were, so the watermark
// advanced straight past them.
//
// A source that cannot keep up must make the reader wait, which is what holding
// the binlog is for.
func TestABurstLargerThanTheBufferIsNotDropped(t *testing.T) {
	_, db, table := requireMySQL(t)
	dsn := os.Getenv("MYSQL_DSN")

	src := NewMySQLSource(dsn, true)
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	if err := src.init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}
	time.Sleep(2 * time.Second)

	// Comfortably more than sourcebuf.DefaultSourceBuffer (64), written before
	// anything reads, so the buffer is full long before the burst ends.
	const burst = 250
	for i := 1; i <= burst; i++ {
		insertRow(t, db, table, i, fmt.Sprintf("burst row %d", i))
	}

	readCtx, readCancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer readCancel()

	seen := make(map[int]bool, burst)
	for len(seen) < burst {
		msg, err := src.Read(readCtx)
		if err != nil {
			break
		}
		if msg == nil || msg.Table() != table {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal(msg.After(), &row); err != nil {
			continue
		}
		id, ok := row["id"].(float64)
		if !ok {
			continue
		}
		seen[int(id)] = true
	}

	if len(seen) != burst {
		var missing []int
		for i := 1; i <= burst && len(missing) < 10; i++ {
			if !seen[i] {
				missing = append(missing, i)
			}
		}
		t.Errorf("%d of %d rows written in one burst arrived; %d were dropped (first missing ids: %v)\n"+
			"the row handler discards a message when the buffer is full instead of waiting for "+
			"room, so nothing reports the loss and the resume position moves past the rows that "+
			"were thrown away",
			len(seen), burst, burst-len(seen), missing)
	}
}
