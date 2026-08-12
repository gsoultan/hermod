package sql

import (
	"testing"

	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Which schema a database is actually running.
//
// There is no versioned migration system: Init creates the tables it knows
// about and adds any columns that are missing. That works, but nothing recorded
// what had been applied, so "does this database match this binary?" had no
// answer — and a schema left half-applied by a failed migration was
// indistinguishable from a complete one.
//
// A fingerprint of the expected schema is written after a run that fully
// succeeds, and only then. That is what makes the three questions answerable:
// what last touched this database, has it been downgraded, and did the last
// upgrade finish.
// ---------------------------------------------------------------------------

func TestInitRecordsTheSchemaFingerprint(t *testing.T) {
	ctx := t.Context()
	db := newMigrationDB(t)
	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	if err := s.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	got, err := s.GetSetting(ctx, SchemaFingerprintKey)
	if err != nil {
		t.Fatalf("reading the fingerprint: %v", err)
	}
	if got == "" {
		t.Fatal("nothing was recorded, so the database cannot say which schema it is running")
	}
	if want := SchemaFingerprint(); got != want {
		t.Errorf("recorded %q, expected %q", got, want)
	}

	if applied, _ := s.GetSetting(ctx, SchemaAppliedAtKey); applied == "" {
		t.Error("no timestamp was recorded, so nothing says when the schema was applied")
	}
}

// TestTheFingerprintIsStableAcrossRestarts. It runs on every boot, so a value
// that changed each time would report a schema change that never happened.
func TestTheFingerprintIsStableAcrossRestarts(t *testing.T) {
	ctx := t.Context()
	db := newMigrationDB(t)
	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	if err := s.Init(ctx); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	first, _ := s.GetSetting(ctx, SchemaFingerprintKey)

	for range 3 {
		if err := s.Init(ctx); err != nil {
			t.Fatalf("restart: %v", err)
		}
	}
	again, _ := s.GetSetting(ctx, SchemaFingerprintKey)

	if first != again {
		t.Errorf("the fingerprint changed across restarts (%q then %q); every boot would "+
			"look like a schema change", first, again)
	}
}

// TestTheFingerprintIsDeterministic. It is derived from a map, and Go iterates
// maps in a random order — a fingerprint that depended on that order would
// differ between two runs of the same binary.
func TestTheFingerprintIsDeterministic(t *testing.T) {
	first := SchemaFingerprint()
	for range 20 {
		if got := SchemaFingerprint(); got != first {
			t.Fatalf("the fingerprint is not stable within one binary: %q then %q", first, got)
		}
	}
}

// TestTheFingerprintChangesWithTheSchema. If it did not, it could not report a
// version change, which is the only thing it is for.
func TestTheFingerprintChangesWithTheSchema(t *testing.T) {
	before := SchemaFingerprint()

	withProbeTable(t, `CREATE TABLE IF NOT EXISTS mig_probe (
		id TEXT PRIMARY KEY,
		note TEXT
	)`)

	if after := SchemaFingerprint(); after == before {
		t.Error("adding a table did not change the fingerprint, so a schema change is invisible")
	}
}

// TestAFailedMigrationLeavesTheFingerprintAlone is the property that makes a
// half-applied schema detectable. Recording the new fingerprint after a
// migration that did not finish would claim the upgrade completed.
func TestAFailedMigrationLeavesTheFingerprintAlone(t *testing.T) {
	ctx := t.Context()
	db := newMigrationDB(t)
	s := NewSQLStorage(db, "sqlite").(*sqlStorage)

	if err := s.Init(ctx); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	before, _ := s.GetSetting(ctx, SchemaFingerprintKey)
	probeWithRow(t, db)

	// A column the database will refuse to add.
	withProbeTable(t, `CREATE TABLE IF NOT EXISTS mig_probe (
		id TEXT PRIMARY KEY,
		required TEXT NOT NULL
	)`)

	if err := s.Init(ctx); err == nil {
		t.Fatal("Init succeeded despite an impossible column")
	}

	after, _ := s.GetSetting(ctx, SchemaFingerprintKey)
	if after != before {
		t.Errorf("the fingerprint was updated after a migration that failed (%q -> %q); "+
			"the database would claim to be running a schema it never applied", before, after)
	}
	if after == SchemaFingerprint() {
		t.Error("the recorded fingerprint matches the expected one after a failed migration, " +
			"so a half-applied schema is indistinguishable from a complete one")
	}
}
