package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
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
		t.Skipf("mysql unreachable: %v", err)
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

	// Give the binlog stream a moment to attach.
	time.Sleep(2 * time.Second)
	mustExec(t, db, "INSERT INTO "+table+" (id, name, price) VALUES (?, ?, ?)", 50, "Gadget", 19.99)

	// Find *this* insert. The binlog carries everything the server does, so
	// filtering to the table under test is the whole point.
	deadline := time.After(45 * time.Second)
	for {
		select {
		case r := <-reads:
			if r.err != nil || r.msg == nil {
				continue
			}
			if !strings.EqualFold(r.msg.Table(), table) {
				r.msg.Release()
				continue
			}

			if got := r.msg.Operation(); got != hermod.OpCreate {
				t.Errorf("operation = %q, want %q", got, hermod.OpCreate)
			}
			if got := r.msg.Metadata()["source"]; got != "mysql" {
				t.Errorf("metadata source = %q, want mysql", got)
			}

			var after map[string]any
			if err := json.Unmarshal(r.msg.After(), &after); err != nil {
				t.Fatalf("after-image %q is not JSON: %v", r.msg.After(), err)
			}
			// The row image may carry a number as float64 or as a string
			// depending on the column type, so compare the rendered value
			// rather than assuming a Go type.
			if got := strings.TrimSuffix(fmt.Sprintf("%v", after["id"]), ".0"); got != "50" {
				t.Errorf("after id = %v (%T), want 50 (full row: %v)", after["id"], after["id"], after)
			}
			if s, _ := after["name"].(string); s != "Gadget" {
				t.Errorf("after name = %v, want Gadget (full row: %v)", after["name"], after)
			}
			r.msg.Release()
			return

		case <-deadline:
			t.Fatal("the inserted row never arrived over CDC within 45s; a change " +
				"committed to a watched table is not reaching the pipeline")
		}
	}
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}
