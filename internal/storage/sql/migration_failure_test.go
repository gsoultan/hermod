package sql

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// A migration that fails must say so.
//
// autoMigrate returned nothing, and inside it every ALTER error that was not
// "column already exists" was dropped on the floor — the line that would have
// logged it was commented out. So a column that could not be added left the
// database missing it while Init reported success, and the failure resurfaced
// later as a query error against a column that was never created, far from the
// cause and with nothing linking the two.
//
// This is not hypothetical. Adding a NOT NULL column without a default to a
// table that already has rows is rejected by SQLite and by PostgreSQL alike,
// and it is an ordinary thing for a schema change to want.
//
// Every deployment upgrade runs this code.
// ---------------------------------------------------------------------------

// withProbeTable adds a CREATE TABLE to the set autoMigrate reconciles, and
// removes it afterwards. autoMigrate walks commonQueries, so this is how a
// schema change is simulated without inventing a whole fake driver.
func withProbeTable(t *testing.T, ddl string) {
	t.Helper()
	const key = "InitMigrationProbeTable"
	commonQueries[key] = ddl
	t.Cleanup(func() { delete(commonQueries, key) })
}

// probeWithRow creates the probe table as it exists *before* the schema change,
// and puts a row in it. The row is what makes a NOT NULL addition impossible.
func probeWithRow(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(),
		`CREATE TABLE IF NOT EXISTS mig_probe (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create probe: %v", err)
	}
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO mig_probe (id) VALUES ('row-1')`); err != nil {
		t.Fatalf("seed probe: %v", err)
	}
}

func TestAFailedMigrationIsReported(t *testing.T) {
	ctx := t.Context()
	db := newMigrationDB(t)
	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	if err := s.Init(ctx); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	probeWithRow(t, db)

	// The schema now wants a NOT NULL column on a table that has rows. The
	// database will refuse.
	withProbeTable(t, `CREATE TABLE IF NOT EXISTS mig_probe (
		id TEXT PRIMARY KEY,
		required TEXT NOT NULL
	)`)

	err := s.Init(ctx)
	if err == nil {
		t.Fatal("Init reported success while a column it wanted could not be added; " +
			"the service starts against a schema it believes is correct, and the " +
			"failure surfaces later as a query error on a missing column")
	}
	for _, want := range []string{"mig_probe", "required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should name %q so an operator can act on it, got: %v", want, err)
		}
	}

	// And the column really is absent — the report is not a false alarm.
	if cols := columnsOf(t, db, "mig_probe"); len(cols) != 1 {
		t.Errorf("probe table has columns %v; expected the addition to have failed", cols)
	}
}

// TestAnAddableColumnStillGetsAdded is the other half. Reporting failures is
// only useful if the ordinary case still works, and every start-up depends on
// it.
func TestAnAddableColumnStillGetsAdded(t *testing.T) {
	ctx := t.Context()
	db := newMigrationDB(t)
	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	if err := s.Init(ctx); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	probeWithRow(t, db)

	withProbeTable(t, `CREATE TABLE IF NOT EXISTS mig_probe (
		id TEXT PRIMARY KEY,
		note TEXT
	)`)

	if err := s.Init(ctx); err != nil {
		t.Fatalf("Init failed on an ordinary nullable column: %v", err)
	}
	if _, ok := columnsOf(t, db, "mig_probe")["note"]; !ok {
		t.Error("the nullable column was not added")
	}
}

// TestOneFailureDoesNotHideTheRest: an operator fixing a broken upgrade wants
// the whole list, not one item per restart.
func TestOneFailureDoesNotHideTheRest(t *testing.T) {
	ctx := t.Context()
	db := newMigrationDB(t)
	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	if err := s.Init(ctx); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	probeWithRow(t, db)

	withProbeTable(t, `CREATE TABLE IF NOT EXISTS mig_probe (
		id TEXT PRIMARY KEY,
		first_required TEXT NOT NULL,
		second_required TEXT NOT NULL
	)`)

	err := s.Init(ctx)
	if err == nil {
		t.Fatal("Init reported success despite two impossible columns")
	}
	for _, want := range []string{"first_required", "second_required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should list %q too; fixing an upgrade one restart at a "+
				"time is how a short outage becomes a long one. Got: %v", want, err)
		}
	}
}

// TestEveryDefinedTableIsCreated.
//
// autoMigrate reconciles columns for every CREATE TABLE in the query set, but
// Init creates tables from a separate hand-maintained list. suspended_messages
// was in the first and not the second, so on every SQL backend it did not
// exist — and the Wait node, which writes to it and then drops the message from
// the pipeline expecting to resume it later, destroyed everything that passed
// through a wait longer than thirty seconds.
//
// Nothing connected the two lists, so nothing noticed. This connects them.
func TestEveryDefinedTableIsCreated(t *testing.T) {
	ctx := t.Context()
	db := newMigrationDB(t)
	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	if err := s.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	live := map[string]bool{}
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		t.Fatalf("listing tables: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		live[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	for _, query := range commonQueries {
		q := strings.Join(strings.Fields(strings.ReplaceAll(query, "\n", " ")), " ")
		if !strings.HasPrefix(strings.ToUpper(q), "CREATE TABLE") {
			continue
		}
		before, _, ok := strings.Cut(q, "(")
		if !ok {
			continue
		}
		parts := strings.Fields(strings.TrimSpace(before))
		table := parts[len(parts)-1]

		if !live[table] {
			t.Errorf("%s is defined in the schema but Init never creates it; "+
				"every write to it fails against a table that does not exist", table)
		}
	}
}
