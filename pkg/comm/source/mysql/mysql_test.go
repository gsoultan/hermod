package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/user/hermod"
)

// ---------------------------------------------------------------------------
// MySQL CDC, against a real server.
//
// This test asserted hardcoded values — id mysql-cdc-1, table products, schema
// inventory, an exact JSON body — against a fixture nothing created, and took
// whatever the first binlog event happened to be. With MYSQL_DSN unset it
// skipped, which is how it survived: the first time it actually ran it read
// MySQL's own time_zone system table being populated at server start and
// reported four mismatches.
//
// A binlog is a shared stream. A test that reads one event and asserts it is
// its own is wrong even when it passes, because it passes by luck about what
// else the server was doing.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 \
//	MYSQL_DSN='root:root@tcp(127.0.0.1:3306)/hermod_it?parseTime=true' \
//	go test -tags=integration ./pkg/comm/source/mysql/
// ---------------------------------------------------------------------------

func TestMySQLSource_Read(t *testing.T) {
	if os.Getenv("HERMOD_INTEGRATION") != "1" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 to run")
	}
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		t.Skip("integration: set MYSQL_DSN to run")
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		// A failure, not a skip. MYSQL_DSN being set is a statement that a
		// server should be there, so an unreachable one is a broken environment
		// — and skipping turns that into a green run that tested nothing.
		t.Fatalf("MYSQL_DSN names a server that is not reachable (%s): %v", dsn, err)
	}

	// Binlog CDC needs row-based logging. Skip with the reason rather than
	// failing on assertions that could never hold.
	var name, format string
	if err := db.QueryRowContext(t.Context(),
		"SHOW VARIABLES LIKE 'binlog_format'").Scan(&name, &format); err != nil {
		t.Skipf("cannot read binlog_format: %v", err)
	}
	if !strings.EqualFold(format, "ROW") {
		t.Skipf("binlog_format is %q, CDC needs ROW", format)
	}

	const table = "cdc_products"
	mustExec(t, db, "DROP TABLE IF EXISTS "+table)
	mustExec(t, db, "CREATE TABLE "+table+" (id INT PRIMARY KEY, name VARCHAR(64), price DECIMAL(10,2))")
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table)
	})

	src := NewMySQLSource(dsn, true)
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	// Start reading before writing, so the change cannot be missed between the
	// insert and the stream attaching.
	type read struct {
		msg hermod.Message
		err error
	}
	reads := make(chan read, 64)
	go func() {
		for {
			m, err := src.Read(ctx)
			select {
			case reads <- read{msg: m, err: err}:
			case <-ctx.Done():
				return
			}
			if ctx.Err() != nil {
				return
			}
		}
	}()

	// Wait for the stream to be live, not for the clock.
	//
	// canal.Run() starts from the server's *current* binlog position, so a row
	// written before it attaches is not late — it is not in the stream at all,
	// and no deadline will find it. The two-second sleep that used to stand here
	// was a guess about how long attaching takes; when it was wrong the test
	// failed 45 seconds later saying a committed change never reached the
	// pipeline, which describes a broken pipeline rather than a test that
	// started writing too early.
	//
	// Instead: write sentinel rows until one comes back. That proves the stream
	// is attached and carrying this table, and it cannot be fooled by a slow
	// machine.
	// Read errors were discarded here, which is why the one CI failure of this
	// test said only that a row never arrived. If the canal dies — a dropped
	// replica connection, a server-id collision with another test opening its
	// own stream — Read returns an error for the rest of the run, the loop spins
	// throwing them away, and forty-five seconds later the test reports a
	// symptom with the cause deleted. The first one is kept and reported.
	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil && !errors.Is(err, context.Canceled) &&
			!errors.Is(err, context.DeadlineExceeded) {
			firstErr = err
		}
	}
	because := func() string {
		if firstErr == nil {
			return "no read error was reported, so the stream was silent rather than broken"
		}
		return "the first read error was: " + firstErr.Error()
	}

	const sentinelID = 1
	live := false
	attachDeadline := time.After(45 * time.Second)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	mustExec(t, db, "INSERT INTO "+table+" (id, name, price) VALUES (?, ?, ?)",
		sentinelID, "sentinel", 0.01)

	for !live {
		select {
		case r := <-reads:
			note(r.err)
			if r.err != nil || r.msg == nil {
				continue
			}
			if strings.EqualFold(r.msg.Table(), table) {
				live = true
			}
			r.msg.Release()

		case <-ticker.C:
			// Replace the sentinel rather than accumulating rows, so a slow
			// attach does not leave the table full of them.
			mustExec(t, db, "REPLACE INTO "+table+" (id, name, price) VALUES (?, ?, ?)",
				sentinelID, "sentinel", 0.01)

		case <-attachDeadline:
			t.Fatalf("no change to the watched table arrived within 45s of writing one; "+
				"the binlog stream never attached, so CDC is not running at all — %s", because())
		}
	}

	// The stream is carrying this table, so this row cannot be missed.
	mustExec(t, db, "INSERT INTO "+table+" (id, name, price) VALUES (?, ?, ?)", 50, "Gadget", 19.99)

	// Find *this* insert. The binlog carries everything the server does, so
	// filtering to the row under test is the whole point.
	deadline := time.After(45 * time.Second)
	for {
		select {
		case r := <-reads:
			note(r.err)
			if r.err != nil || r.msg == nil {
				continue
			}
			if !strings.EqualFold(r.msg.Table(), table) {
				r.msg.Release()
				continue
			}

			var after map[string]any
			if err := json.Unmarshal(r.msg.After(), &after); err != nil {
				t.Fatalf("after-image %q is not JSON: %v", r.msg.After(), err)
			}
			// Sentinels share the table; only the row under test is asserted on.
			id := strings.TrimSuffix(fmt.Sprintf("%v", after["id"]), ".0")
			if id != "50" {
				r.msg.Release()
				continue
			}

			if got := r.msg.Operation(); got != hermod.OpCreate {
				t.Errorf("operation = %q, want %q", got, hermod.OpCreate)
			}
			if got := r.msg.Metadata()["source"]; got != "mysql" {
				t.Errorf("metadata source = %q, want mysql", got)
			}
			if s, _ := after["name"].(string); s != "Gadget" {
				t.Errorf("after name = %v, want Gadget (full row: %v)", after["name"], after)
			}
			r.msg.Release()
			return

		case <-deadline:
			t.Fatalf("the inserted row never arrived over CDC within 45s, although the "+
				"stream was already carrying this table; a committed change is being lost — %s",
				because())
		}
	}
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}
