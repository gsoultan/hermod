//go:build integration
// +build integration

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/user/hermod"
)

// ---------------------------------------------------------------------------
// Postgres logical-replication CDC, against a real server.
//
// This is the product's flagship claim — a row changes in the source database
// and the change arrives as a message — and it was covered only by a Playwright
// spec that drove the source wizard through a browser. That spec rotted as soon
// as the wizard was redesigned, and repairing it locator-by-locator never
// converged, because none of what it was really testing lives in the DOM.
//
// Driving the source directly tests the same behaviour without a browser: it is
// faster, it survives UI changes, and when it fails it points at replication
// rather than at a button.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 \
//	POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/hermod_it?sslmode=disable' \
//	go test -tags=integration ./pkg/comm/source/postgres/
//
// The server must run with wal_level=logical.
// ---------------------------------------------------------------------------

// cdcFixture is one isolated CDC setup: its own table, publication and
// replication slot, all named after the test.
type cdcFixture struct {
	dsn    string
	db     *sql.DB
	table  string
	slot   string
	pub    string
	source *PostgresSource

	// msgs carries everything the reader goroutine has pulled off the stream.
	// A single long-lived reader is essential: Read is what drives the
	// replication stream, and calling it only when a test wants a message means
	// changes that arrive in between are read by nobody. Buffered well past
	// what any test produces, so the reader never blocks.
	msgs   chan hermod.Message
	cancel context.CancelFunc

	// lastErr is the most recent Read error. A test that fails with "no message
	// arrived" is much harder to act on than one that can say why the read was
	// failing — "replication slot is active for PID ..." points straight at a
	// connection that has not let go.
	mu      sync.Mutex
	lastErr error
}

func (f *cdcFixture) setErr(err error) {
	f.mu.Lock()
	f.lastErr = err
	f.mu.Unlock()
}

func (f *cdcFixture) why() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lastErr == nil {
		return "no read error was recorded"
	}
	return "last read error: " + f.lastErr.Error()
}

// newCDCFixture skips unless the integration environment is present.
//
// Cleanup drops the replication slot unconditionally. A leaked slot is not a
// tidiness problem: Postgres retains every WAL segment a slot has not consumed,
// so one forgotten slot fills the disk of whatever server this ran against.
func newCDCFixture(t *testing.T) *cdcFixture {
	t.Helper()

	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	ctx := t.Context()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}

	if lvl := scalar(t, db, "SELECT current_setting('wal_level')"); lvl != "logical" {
		t.Skipf("wal_level is %q, CDC needs 'logical'", lvl)
	}

	// Names derived from the test, so parallel packages cannot collide and a
	// leftover object is traceable to what created it.
	suffix := strings.ToLower(strings.NewReplacer("/", "_", " ", "_", "-", "_").Replace(t.Name()))
	f := &cdcFixture{
		dsn:   dsn,
		db:    db,
		table: "cdc_" + suffix,
		slot:  "slot_" + suffix,
		pub:   "pub_" + suffix,
	}

	f.dropReplicationObjects(t)
	mustExec(t, db, "DROP TABLE IF EXISTS "+f.table)
	mustExec(t, db, fmt.Sprintf(
		`CREATE TABLE %s (id SERIAL PRIMARY KEY, name TEXT NOT NULL, email TEXT)`, f.table))
	// REPLICA IDENTITY FULL so UPDATE and DELETE carry the previous row; the
	// default only sends the primary key, and the before-image assertions below
	// are the point of the test.
	mustExec(t, db, fmt.Sprintf(`ALTER TABLE %s REPLICA IDENTITY FULL`, f.table))

	t.Cleanup(func() {
		// Stop the stream before dropping the slot and publication underneath
		// it, or the reconnect logic races the teardown and logs confusing
		// "publication does not exist" errors on the way out.
		f.stop()
		f.dropReplicationObjects(t)
		mustExec(t, db, "DROP TABLE IF EXISTS "+f.table)
		_ = db.Close()
	})

	return f
}

func (f *cdcFixture) dropReplicationObjects(t *testing.T) {
	t.Helper()
	// pg_drop_replication_slot errors if the slot is missing or still active,
	// so this is best-effort and deliberately does not fail the test.
	_, _ = f.db.ExecContext(context.Background(),
		`SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name = $1`, f.slot)
	_, _ = f.db.ExecContext(context.Background(), "DROP PUBLICATION IF EXISTS "+f.pub)
}

// start brings the source up on its own reader goroutine and waits until the
// replication slot exists, so a change made after this returns is captured.
func (f *cdcFixture) start(t *testing.T, ctx context.Context, persistent bool) {
	t.Helper()

	f.source = NewPostgresSource(f.dsn, f.slot, f.pub, []string{f.table}, true, "", time.Second)
	f.source.SetPersistentSlot(persistent)
	f.msgs = make(chan hermod.Message, 256)

	readCtx, cancel := context.WithCancel(ctx)
	f.cancel = cancel

	go func() {
		for {
			msg, err := f.source.Read(readCtx)
			if readCtx.Err() != nil {
				return
			}
			if err != nil {
				f.setErr(err)
				// Do not spin on a persistent failure; the slot being held by a
				// connection that has not closed yet is the common case.
				time.Sleep(200 * time.Millisecond)
				continue
			}
			if msg == nil {
				continue
			}
			select {
			case f.msgs <- msg:
			case <-readCtx.Done():
				msg.Release()
				return
			}
		}
	}()

	// The first Read creates the publication and the slot; wait for the slot
	// rather than for a message, since no change has been made yet.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if scalar(t, f.db,
			fmt.Sprintf("SELECT count(*) FROM pg_replication_slots WHERE slot_name = '%s'", f.slot)) == "1" {
			// The slot exists; give the stream a moment to attach to it.
			time.Sleep(time.Second)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("replication slot %q never appeared; the source did not start streaming", f.slot)
}

// stop shuts the source down the way a worker restart does.
//
// Close, not teardownStream: teardownStream only drops the client socket and
// returns, leaving streamLoop running and the server-side walsender attached.
// The next source then finds its own slot "active", and — correctly — backs off
// rather than terminating what looks like a live Hermod backend, so the stream
// never resumes. Close cancels the loop and waits for it.
func (f *cdcFixture) stop() {
	if f.cancel != nil {
		f.cancel()
	}
	if f.source != nil {
		_ = f.source.Close()
		f.source = nil
	}
}

// collect takes up to n messages the reader has already pulled off the stream.
func (f *cdcFixture) collect(t *testing.T, n int, within time.Duration) []hermod.Message {
	t.Helper()
	var got []hermod.Message
	t.Cleanup(func() {
		for _, m := range got {
			m.Release()
		}
	})

	deadline := time.After(within)
	for len(got) < n {
		select {
		case m := <-f.msgs:
			got = append(got, m)
		case <-deadline:
			return got
		}
	}
	return got
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func scalar(t *testing.T, db *sql.DB, q string) string {
	t.Helper()
	var v sql.NullString
	if err := db.QueryRowContext(context.Background(), q).Scan(&v); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return v.String
}

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decoding row image %q: %v", raw, err)
	}
	return m
}

// TestCDCCapturesInsertUpdateDelete is the core guarantee.
func TestCDCCapturesInsertUpdateDelete(t *testing.T) {
	f := newCDCFixture(t)
	ctx := t.Context()
	f.start(t, ctx, false)

	mustExec(t, f.db, fmt.Sprintf("INSERT INTO %s (name, email) VALUES ($1, $2)", f.table),
		"Ada", "ada@example.com")
	mustExec(t, f.db, fmt.Sprintf("UPDATE %s SET email = $1 WHERE name = $2", f.table),
		"ada.lovelace@example.com", "Ada")
	mustExec(t, f.db, fmt.Sprintf("DELETE FROM %s WHERE name = $1", f.table), "Ada")

	msgs := f.collect(t, 3, 60*time.Second)
	if len(msgs) < 3 {
		t.Fatalf("captured %d change(s), want 3 (insert, update, delete); "+
			"changes committed to the source table are not reaching the pipeline (%s)", len(msgs), f.why())
	}

	ops := make([]string, 0, 3)
	for _, m := range msgs {
		ops = append(ops, strings.ToLower(string(m.Operation())))
	}
	// Hermod normalises the replication operation to its own vocabulary; an
	// INSERT arrives as OpCreate.
	want := []string{string(hermod.OpCreate), string(hermod.OpUpdate), string(hermod.OpDelete)}
	for i, w := range want {
		if ops[i] != w {
			t.Errorf("change %d is %q, want %q (order matters: replication replays "+
				"the commit order, and a sink applying them out of order diverges)", i, ops[i], w)
		}
	}

	// The insert carries the new row.
	after := decode(t, msgs[0].After())
	if after["name"] != "Ada" {
		t.Errorf("insert after-image is %v, want name=Ada", after)
	}

	// The update carries both images, which is what a sink needs to apply a
	// keyed change and what REPLICA IDENTITY FULL is set for.
	updBefore, updAfter := decode(t, msgs[1].Before()), decode(t, msgs[1].After())
	if updBefore["email"] != "ada@example.com" {
		t.Errorf("update before-image is %v, want the original email", updBefore)
	}
	if updAfter["email"] != "ada.lovelace@example.com" {
		t.Errorf("update after-image is %v, want the new email", updAfter)
	}

	// The delete carries the row that went away.
	delBefore := decode(t, msgs[2].Before())
	if delBefore["name"] != "Ada" {
		t.Errorf("delete before-image is %v, want the deleted row; without it a sink "+
			"cannot tell which row to remove", delBefore)
	}
}

// TestCDCOnlyCapturesPublishedTables checks the table filter.
//
// A source configured for one table must not stream another. Getting this wrong
// leaks rows from tables the operator never selected — a data-governance
// failure, not just extra traffic.
func TestCDCOnlyCapturesPublishedTables(t *testing.T) {
	f := newCDCFixture(t)
	ctx := t.Context()

	other := f.table + "_other"
	mustExec(t, f.db, "DROP TABLE IF EXISTS "+other)
	mustExec(t, f.db, fmt.Sprintf("CREATE TABLE %s (id SERIAL PRIMARY KEY, secret TEXT)", other))
	t.Cleanup(func() { mustExec(t, f.db, "DROP TABLE IF EXISTS "+other) })

	f.start(t, ctx, false)

	mustExec(t, f.db, fmt.Sprintf("INSERT INTO %s (secret) VALUES ($1)", other), "must-not-appear")
	mustExec(t, f.db, fmt.Sprintf("INSERT INTO %s (name) VALUES ($1)", f.table), "published")

	msgs := f.collect(t, 1, 45*time.Second)
	if len(msgs) == 0 {
		t.Fatalf("nothing captured from the published table (%s)", f.why())
	}
	for _, m := range msgs {
		if strings.Contains(m.Table(), "_other") {
			t.Errorf("captured a change from %q, which is not in the publication; "+
				"rows from unselected tables are leaking into the pipeline", m.Table())
		}
		if strings.Contains(string(m.After()), "must-not-appear") {
			t.Error("an unpublished table's row reached the pipeline")
		}
	}
}

// TestCDCResumesFromSlotAfterRestart is the durability property.
//
// A slot exists so a consumer that goes away does not lose the changes made
// while it was gone. If a restart silently skipped them, a worker redeploy
// would drop data with nothing to show for it.
func TestCDCResumesFromSlotAfterRestart(t *testing.T) {
	f := newCDCFixture(t)
	ctx := t.Context()

	// Persistent, so the slot outlives the source. A temporary slot is dropped
	// on disconnect and cannot resume by construction.
	f.start(t, ctx, true)

	mustExec(t, f.db, fmt.Sprintf("INSERT INTO %s (name) VALUES ($1)", f.table), "before-restart")
	if got := f.collect(t, 1, 45*time.Second); len(got) == 0 {
		t.Fatalf("the first change was never captured (%s)", f.why())
	}

	// The consumer goes away.
	f.stop()

	// Changes happen while nothing is listening.
	mustExec(t, f.db, fmt.Sprintf("INSERT INTO %s (name) VALUES ($1)", f.table), "during-downtime")

	// It comes back on the same slot.
	f.start(t, ctx, true)

	// Two, because nothing was acknowledged before the restart: the slot
	// replays from its confirmed_flush_lsn, so the pre-restart change is
	// redelivered alongside the one made during downtime. That redelivery is
	// correct — delivery is at-least-once and sinks upsert — and the change
	// that must not be missing is the one committed while nobody was listening.
	msgs := f.collect(t, 2, 45*time.Second)
	if len(msgs) == 0 {
		t.Fatalf("no change captured after restart; the row committed while the consumer "+
			"was down was lost, which is the failure the replication slot exists to prevent (%s)", f.why())
	}

	found := false
	for _, m := range msgs {
		if strings.Contains(string(m.After()), "during-downtime") {
			found = true
		}
	}
	if !found {
		seen := make([]string, 0, len(msgs))
		for _, m := range msgs {
			seen = append(seen, string(m.After()))
		}
		t.Errorf("the change made during downtime was not replayed after restart; "+
			"captured %v. A worker redeploy would drop every change committed while it "+
			"was down, with nothing to show for it", seen)
	}
}
