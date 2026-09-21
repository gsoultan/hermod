package sql

import (
	"errors"
	"strconv"
	"testing"

	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Refusing to run against a schema from the future.
//
// The fingerprint next door answers "same schema or a different one" and stops
// there, because a digest has no direction. The two directions are not equally
// safe. A newer binary on an older database is the ordinary upgrade and
// autoMigrate handles it. An older binary on a database a newer one already
// migrated is the dangerous one — and it is the one that happens under
// pressure, during a rollback or a rolling upgrade running both at once.
//
// Every statement in Init is IF NOT EXISTS, so the old binary used to start
// perfectly cleanly and then read and write a schema whose shape it did not
// know, with no down migration to undo the result.
// ---------------------------------------------------------------------------

// knownSchemaFingerprint pins the DDL that currentSchemaVersion describes.
//
// This is what stops the version quietly rotting. A hand-maintained number that
// nobody remembers to change is a gate that never fires; here the fingerprint
// is computed from the DDL itself, so changing the schema fails this test and
// forces the question to be asked out loud.
//
// Last moved by: adding after_hash and after_blob to message_trace_steps, so a
// payload is stored once per message and compressed instead of once per step in
// plain JSON. currentSchemaVersion was left alone deliberately, and this one is
// closer to the line than the others below, so the reasoning is worth spelling
// out.
//
// The previous release names its columns in both directions. Its
// RecordTraceStep inserts a fixed list that does not mention the two new
// columns, both of which are nullable, so it keeps writing after_data and this
// release reads that path unchanged — there is no backfill and no rewrite of
// the largest table in the database. Its GetMessageTrace selects a fixed list
// that does not mention them either, so it still runs.
//
// What it does not do is see the payloads this release wrote: they are in
// after_blob, and the old binary reads after_data, which is NULL on those rows.
// So a rollback shows blank payloads for the traces the newer binary recorded,
// and they come back when it returns — the same shape as the dashboard_history
// note below, a gap in a diagnostic view that heals on roll-forward, not a
// misread of live data. Nothing else reads this table, no foreign key points at
// it, and tracing is off by default (TraceSampleRate 0), so the gap only exists
// where someone deliberately turned it on.
//
// That is not the direction the version exists to block. The bump to 2 below
// was for a release that would *fail* every trace read and write; refusing
// start-up is right for that and wrong for this, because it would refuse
// exactly the rollback it is meant to make safe.
//
// The note before that: adding created_at to workflows, sources, sinks, users, vhosts,
// workers and plugins — the key each of those lists sorts and pages on.
// currentSchemaVersion was left alone deliberately. The previous release names
// its columns in both directions on all seven tables — every Create inserts a
// fixed list that does not include created_at, and the matching List and Get
// queries select a fixed list that does not read it — so it writes rows with a
// NULL there and is otherwise unaffected. Nothing existing changed shape, and
// autoMigrate only ever adds columns, so the columns survive the rollback and
// this release's Init backfills the NULLs the older one left when it returns. A
// bump here would refuse exactly the rollback it is meant to keep safe.
//
// The note before that: narrowing message_trace_steps (dropping the write-only
// id and the duplicated before_data) and adding the message_traces parent
// table. currentSchemaVersion was bumped to 2 for that one, because it is the
// dangerous direction the version exists to block. That release's
// RecordTraceStep inserts id and before_data by name, and its GetMessageTrace
// selects before_data. Both columns are gone after that migration, so an older
// binary rolled back onto the database would fail every trace write and every
// trace read — not degrade, fail. Refusing to start is the correct outcome.
//
// The earlier note, still true of the change it describes: adding the
// dashboard_history table, then narrowing it to drop the surrogate id column
// before it shipped, left currentSchemaVersion alone deliberately. The question this gate asks is whether the previous
// release would *misread* the result, and it would not: dashboard_history is a
// standalone table no earlier code path reads or writes, nothing existing
// changed shape, and no foreign key points at it. A rollback leaves the table
// unpopulated — a gap in a chart that fills itself in when the newer binary
// returns — so bumping the version would buy nothing and cost a refused
// start-up during exactly the rollback it was supposed to make safe.
const knownSchemaFingerprint = "3311dcff267f2991b7bd6e57eee794b9cf43c3fd51cee172c0b688f4c0af3931"

func TestSchemaVersionIsReconsideredWhenTheSchemaChanges(t *testing.T) {
	if got := SchemaFingerprint(); got != knownSchemaFingerprint {
		t.Fatalf("the schema changed.\n"+
			"  fingerprint now: %s\n"+
			"  pinned here:     %s\n"+
			"  currentSchemaVersion is %d\n\n"+
			"Decide which kind of change this is, then update this test:\n"+
			"  - Could the previous release still read and write the result correctly?\n"+
			"    Then leave currentSchemaVersion alone and update knownSchemaFingerprint.\n"+
			"  - Would it misread it — a new table it does not populate, a column it\n"+
			"    would leave empty that this release requires? Then bump\n"+
			"    currentSchemaVersion as well, so a rollback onto this database is\n"+
			"    refused instead of silently corrupting it.",
			got, knownSchemaFingerprint, currentSchemaVersion)
	}
}

func TestInitRecordsTheSchemaVersion(t *testing.T) {
	ctx := t.Context()
	db := newMigrationDB(t)
	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	if err := s.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	got, err := s.GetSetting(ctx, SchemaVersionKey)
	if err != nil {
		t.Fatalf("reading the recorded version: %v", err)
	}
	if got != strconv.Itoa(currentSchemaVersion) {
		t.Errorf("recorded schema version is %q, want %q", got, strconv.Itoa(currentSchemaVersion))
	}
}

// The property this exists for.
func TestInitRefusesADatabaseMigratedByANewerRelease(t *testing.T) {
	ctx := t.Context()
	db := newMigrationDB(t)
	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	if err := s.Init(ctx); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	// A newer release has been here.
	if err := s.saveSetting(ctx, SchemaVersionKey, strconv.Itoa(currentSchemaVersion+1)); err != nil {
		t.Fatalf("simulating a newer schema: %v", err)
	}

	err := s.Init(ctx)
	if err == nil {
		t.Fatal("Init succeeded against a database a newer release had migrated\n" +
			"every statement it runs is IF NOT EXISTS, so it starts cleanly and then reads " +
			"and writes a schema it does not know the shape of — with no down migration to " +
			"undo whatever it does")
	}
	if !errors.Is(err, ErrSchemaFromTheFuture) {
		t.Errorf("refused with %v, which does not wrap ErrSchemaFromTheFuture; a caller "+
			"cannot tell this apart from a corrupt database", err)
	}
}

// Every deployment upgrading to this release has a database with tables and no
// recorded version. Refusing them would turn an upgrade into an outage.
func TestADatabasePredatingTheVersionCheckIsAdopted(t *testing.T) {
	ctx := t.Context()
	db := newMigrationDB(t)
	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	if err := s.Init(ctx); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	// Put it back the way a release before this check would have left it: a
	// fingerprint recorded, but no version.
	if err := s.saveSetting(ctx, SchemaVersionKey, ""); err != nil {
		t.Fatalf("clearing the version: %v", err)
	}

	if err := s.Init(ctx); err != nil {
		t.Fatalf("Init refused a database that predates the version check: %v", err)
	}
	if got, _ := s.GetSetting(ctx, SchemaVersionKey); got != strconv.Itoa(currentSchemaVersion) {
		t.Errorf("the adopted database records version %q, want %q", got,
			strconv.Itoa(currentSchemaVersion))
	}
}

// An unreadable version is not evidence of anything. Refusing to start over a
// value nobody can parse would be an outage caused by the guard rather than by
// the thing it guards against.
func TestAnUnparseableVersionDoesNotStopStartup(t *testing.T) {
	ctx := t.Context()
	db := newMigrationDB(t)
	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	if err := s.Init(ctx); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	if err := s.saveSetting(ctx, SchemaVersionKey, "not-a-number"); err != nil {
		t.Fatalf("writing a bad version: %v", err)
	}

	if err := s.Init(ctx); err != nil {
		t.Errorf("Init refused to start over an unparseable version: %v", err)
	}
}

// A failed migration must not record a version it never reached — that would
// lock out the very binary able to finish the job.
func TestAFailedMigrationLeavesTheVersionAlone(t *testing.T) {
	ctx := t.Context()
	db := newMigrationDB(t)
	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	if err := s.Init(ctx); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	if err := s.saveSetting(ctx, SchemaVersionKey, ""); err != nil {
		t.Fatalf("clearing the version: %v", err)
	}
	probeWithRow(t, db)

	// A column the database will refuse to add.
	withProbeTable(t, `CREATE TABLE IF NOT EXISTS mig_probe (
		id TEXT PRIMARY KEY,
		required TEXT NOT NULL
	)`)

	if err := s.Init(ctx); err == nil {
		t.Fatal("Init succeeded despite an impossible column")
	}

	if got, _ := s.GetSetting(ctx, SchemaVersionKey); got != "" {
		t.Errorf("a version (%q) was recorded after a migration that failed; the database "+
			"would claim a schema it never applied, and an older binary would be refused "+
			"for an upgrade that never happened", got)
	}
}
