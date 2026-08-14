//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Closing a source while its replication stream is still reading.
//
// Close cancelled the stream context and then closed the replication
// connection immediately, waiting for streamLoop only afterwards. streamLoop
// spends nearly all its time inside PgConn.ReceiveMessage on that same
// connection, holding its own reference to it — so the close and the read
// happen on one connection from two goroutines.
//
// The mutex Close holds does not help: consumeStream is handed the connection
// as a parameter and reads it without the lock. And the close was redundant in
// the normal path anyway, because streamLoop already closes the connection on
// its way out through teardownStream. What it was there for is stated in its
// own comment — "unblock ReceiveMessage if context cancel doesn't" — which is a
// real hazard, just one that should be a bounded last resort rather than the
// first thing every shutdown does.
//
// A race detector finds this only when the two land in the same window, which
// is why it surfaced as an occasional failure in an unrelated registry test
// rather than here. Closing repeatedly, immediately after the stream is live,
// makes the window easy to hit.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 \
//	POSTGRES_DSN='postgres://postgres:postgres@127.0.0.1:5432/hermod_it?sslmode=disable' \
//	go test -tags=integration -race -run TestCloseDoesNotRaceTheReplicationStream ./pkg/comm/source/postgres/
func TestCloseDoesNotRaceTheReplicationStream(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q POSTGRES_DSN=%q in CI, where PostgreSQL is "+
				"started for exactly this", os.Getenv("HERMOD_INTEGRATION"), dsn)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}
	if lvl := scalar(t, db, "SELECT current_setting('wal_level')"); lvl != "logical" {
		t.Skipf("wal_level is %q, CDC needs 'logical'", lvl)
	}

	suffix := strings.ToLower(strings.NewReplacer("/", "_", " ", "_", "-", "_").Replace(t.Name()))
	table := "closerace_" + suffix

	mustExec(t, db, "DROP TABLE IF EXISTS "+table)
	mustExec(t, db, fmt.Sprintf(`CREATE TABLE %s (id SERIAL PRIMARY KEY, name TEXT)`, table))
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table) })

	// Several rounds, because the race needs the close to land while the read
	// is in flight. Each round is a whole source lifecycle: create, stream,
	// close.
	const rounds = 5
	for i := range rounds {
		slot := fmt.Sprintf("slot_%s_%d", suffix, i)
		pub := fmt.Sprintf("pub_%s_%d", suffix, i)

		// A leaked logical slot pins WAL on the server forever, so these are
		// dropped whatever happens below.
		dropObjects := func() {
			_, _ = db.ExecContext(context.Background(),
				"SELECT pg_drop_replication_slot($1) WHERE EXISTS "+
					"(SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)", slot)
			_, _ = db.ExecContext(context.Background(), "DROP PUBLICATION IF EXISTS "+pub)
		}
		dropObjects()
		t.Cleanup(dropObjects)

		src := NewPostgresSource(dsn, slot, pub, []string{table}, true, "", time.Second)

		readCtx, cancelRead := context.WithTimeout(t.Context(), 20*time.Second)
		// One read is enough to get the stream established and sitting in
		// ReceiveMessage, which is the state Close has to handle.
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = src.Read(readCtx)
		}()

		// Give the stream a moment to attach, then write something so it has
		// work in flight rather than sitting idle.
		time.Sleep(300 * time.Millisecond)
		mustExec(t, db, fmt.Sprintf("INSERT INTO %s (name) VALUES ($1)", table), "row")
		time.Sleep(100 * time.Millisecond)

		if err := src.Close(); err != nil {
			t.Errorf("round %d: close: %v", i, err)
		}
		cancelRead()
		<-done
	}
}
