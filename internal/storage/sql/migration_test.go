package sql

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Schema migration.
//
// Hermod has no versioned migration system and no down migrations: Init runs
// autoMigrate, which parses the embedded CREATE TABLE statements, compares them
// against the live schema and adds any missing columns. That is a deliberate
// simplification, but it puts two properties on the critical path of every
// single start-up, and neither was covered.
//
//  1. It must be idempotent. It runs on every boot, so a second run has to be a
//     no-op rather than an error or a duplicate column.
//  2. It must be additive only. Adding a column is safe; dropping or retyping
//     one destroys data on a schema that has drifted, and there is no down
//     migration to undo it.
//
// A rollback test cannot be written because there is nothing to roll back to.
// That absence is worth knowing about rather than papering over: recovering
// from a bad schema change today means restoring a backup.
// ---------------------------------------------------------------------------

func newMigrationDB(t *testing.T) *sql.DB {
	t.Helper()
	// A distinct in-memory database per test, so migrations do not collide with
	// the shared handle the other tests in this package use.
	dsn := fmt.Sprintf("file:mig_%s?mode=memory&cache=shared&_pragma=foreign_keys(ON)", t.Name())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func columnsOf(t *testing.T, db *sql.DB, table string) map[string]string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer rows.Close()

	cols := map[string]string{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		cols[name] = ctype
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info rows: %v", err)
	}
	return cols
}

// TestInitIsIdempotent covers the property every restart depends on.
func TestInitIsIdempotent(t *testing.T) {
	ctx := t.Context()
	db := newMigrationDB(t)
	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	if err := s.Init(ctx); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	before := columnsOf(t, db, "workflows")
	if len(before) == 0 {
		t.Fatal("workflows table has no columns after Init; the migration did nothing")
	}

	// Restarts.
	for i := range 3 {
		if err := s.Init(ctx); err != nil {
			t.Fatalf("Init run %d failed; the service would not restart: %v", i+2, err)
		}
	}

	// A second table, so this covers more than one shape of CREATE TABLE.
	if cols := columnsOf(t, db, "sources"); len(cols) == 0 {
		t.Error("sources table has no columns after Init")
	}

	after := columnsOf(t, db, "workflows")
	if len(after) != len(before) {
		t.Errorf("workflows went from %d columns to %d across repeated Init calls; "+
			"the migration is not idempotent", len(before), len(after))
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			t.Errorf("column %q disappeared across repeated Init calls", name)
		}
	}
}

// TestMigrationPreservesExistingData is the one that matters on an upgrade:
// rows written by the previous version must still be there afterwards.
func TestMigrationPreservesExistingData(t *testing.T) {
	ctx := t.Context()
	db := newMigrationDB(t)
	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	if err := s.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	want := storage.Workflow{ID: "wf-upgrade", Name: "before upgrade", Active: true}
	if err := s.CreateWorkflow(ctx, want); err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	// The upgrade: the binary restarts and migrates again.
	if err := s.Init(ctx); err != nil {
		t.Fatalf("Init after data was written: %v", err)
	}

	got, err := s.GetWorkflow(ctx, want.ID)
	if err != nil {
		t.Fatalf("workflow is gone after a migration run: %v", err)
	}
	if got.Name != want.Name || got.Active != want.Active {
		t.Errorf("workflow changed across migration: got %+v, want name=%q active=%v",
			got, want.Name, want.Active)
	}
}

// TestMigrationIsAdditiveOnly holds the safety property that stands in for the
// missing down migration.
//
// A column the running schema has but the embedded DDL does not — left over
// from a newer version during a rollback, or added by an operator — must be
// left alone. Dropping it would destroy data with no way back.
func TestMigrationIsAdditiveOnly(t *testing.T) {
	ctx := t.Context()
	db := newMigrationDB(t)
	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	if err := s.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// A column this version knows nothing about, holding data.
	if _, err := db.ExecContext(ctx, `ALTER TABLE workflows ADD COLUMN future_field TEXT`); err != nil {
		t.Fatalf("adding the unknown column: %v", err)
	}
	if err := s.CreateWorkflow(ctx, storage.Workflow{ID: "wf-1", Name: "n"}); err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE workflows SET future_field = 'precious' WHERE id = 'wf-1'`); err != nil {
		t.Fatalf("writing to the unknown column: %v", err)
	}

	if err := s.Init(ctx); err != nil {
		t.Fatalf("Init with an unknown column present: %v", err)
	}

	cols := columnsOf(t, db, "workflows")
	if _, ok := cols["future_field"]; !ok {
		t.Fatal("migration dropped a column it did not recognise; on a rollback that is " +
			"unrecoverable data loss, and there is no down migration to restore it")
	}

	var v sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT future_field FROM workflows WHERE id = 'wf-1'`).Scan(&v); err != nil {
		t.Fatalf("reading back the unknown column: %v", err)
	}
	if v.String != "precious" {
		t.Errorf("data in the unrecognised column is %q, want %q", v.String, "precious")
	}
}

// TestMigrationAddsAMissingColumn is the forward case the whole mechanism
// exists for: an older database gains the columns a newer binary expects.
func TestMigrationAddsAMissingColumn(t *testing.T) {
	ctx := t.Context()
	db := newMigrationDB(t)
	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	if err := s.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	full := columnsOf(t, db, "workflows")

	// Simulate an older schema by rebuilding the table with a single column,
	// then letting the migration bring it up to date.
	if _, err := db.ExecContext(ctx, `DROP TABLE workflows`); err != nil {
		t.Fatalf("dropping workflows: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE workflows (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("creating the old schema: %v", err)
	}

	if err := s.Init(ctx); err != nil {
		t.Fatalf("Init against an older schema: %v", err)
	}

	got := columnsOf(t, db, "workflows")
	var missing []string
	for name := range full {
		if _, ok := got[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("migration left %d expected column(s) missing on an older schema: %v; "+
			"the binary would fail on its first query", len(missing), missing)
	}
}
