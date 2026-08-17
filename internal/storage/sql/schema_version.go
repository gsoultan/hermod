package sql

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Recording which schema a database is running.
//
// There is no versioned migration system here: Init creates the tables it knows
// about and autoMigrate adds any columns that are missing. That is a deliberate
// simplification, but it left "does this database match this binary?" with no
// answer at all — and a schema left half-applied by a failed migration
// indistinguishable from a complete one.
//
// So a fingerprint of the expected schema is written after a run that fully
// succeeds, and only then. Three questions become answerable from the database
// itself:
//
//		SELECT value FROM settings WHERE key = 'hermod.schema_fingerprint';
//
//	  - what last applied a schema here, by comparing it with SchemaFingerprint()
//	  - whether the binary has been downgraded, because the values differ
//	  - whether the last upgrade finished, because a failed migration leaves the
//	    previous value in place
//
// It is a fingerprint rather than a version number because there is nothing to
// number: no migration files, no ordering, and no down migrations. What it can
// honestly report is "the same schema" or "a different one".
const (
	// SchemaFingerprintKey names the settings row holding the applied schema.
	SchemaFingerprintKey = "hermod.schema_fingerprint"

	// SchemaAppliedAtKey names the settings row holding when it was applied.
	SchemaAppliedAtKey = "hermod.schema_applied_at"

	// SchemaVersionKey names the settings row holding the schema version.
	SchemaVersionKey = "hermod.schema_version"
)

// currentSchemaVersion orders what the fingerprint can only compare.
//
// The fingerprint above answers "same schema or a different one", and
// deliberately stops there — a digest has no direction. But the two directions
// are not equally safe. Running a *newer* binary against an older database is
// what Init is for: autoMigrate adds what is missing. Running an *older* binary
// against a database a newer one already migrated is the dangerous one, and it
// is the one that happens under pressure — a rollback after a bad release, or a
// rolling upgrade briefly running both binaries against one metadata database.
// Every statement in Init is IF NOT EXISTS, so the old binary starts perfectly
// cleanly and then reads and writes a schema whose shape it does not know.
//
// So the version supplies the ordering, and the fingerprint keeps it honest: a
// test fails if the DDL changes without this being reconsidered, which is what
// stops it silently drifting out of date the way a hand-maintained number
// otherwise would.
//
// Bump it when a change would confuse an older release — a new table it does
// not know to populate, a column it would leave empty that a newer one
// requires. Do not bump it for a change an older binary can ignore, such as an
// added index. This exists to block dangerous rollbacks, and a number that
// moves for harmless reasons blocks safe ones.
const currentSchemaVersion = 1

// ErrSchemaFromTheFuture is returned when the database was migrated by a
// release newer than this binary.
var ErrSchemaFromTheFuture = errors.New("metadata schema is newer than this binary understands")

// checkSchemaVersion refuses to continue when the database is ahead of this
// binary. It runs after the CREATE TABLE statements, so the settings table it
// reads exists, and before autoMigrate, so nothing has been altered by the time
// the decision is made.
//
// A database with no version recorded is adopted rather than refused: that is
// every deployment predating this check, and refusing them would turn an
// upgrade into an outage.
func (s *sqlStorage) checkSchemaVersion(ctx context.Context) error {
	found, ok := s.recordedSchemaVersion(ctx)
	if !ok || found <= currentSchemaVersion {
		return nil
	}
	return fmt.Errorf(
		"%w: the database is at schema version %d and this binary understands %d. "+
			"An older release has been started against a database a newer one already "+
			"migrated — a rollback, or a rolling upgrade running both at once. Refusing "+
			"rather than reading and writing a schema shape this binary does not know: "+
			"there are no down migrations, so recovering from that means restoring a "+
			"backup. Run the newer release, or restore a backup taken before the upgrade",
		ErrSchemaFromTheFuture, found, currentSchemaVersion)
}

// recordedSchemaVersion reports the version stored in settings, and whether
// there was one to read.
//
// It returns no error on purpose. A failed read and an unparseable value are
// both "nothing to compare against", not "the database is ahead of us" — and
// refusing to start over either would be an outage caused by the guard rather
// than by the thing it guards against. Returning (int, bool) rather than
// (int, error) puts that decision in the type instead of leaving a discarded
// error for a reader to wonder about.
func (s *sqlStorage) recordedSchemaVersion(ctx context.Context) (int, bool) {
	raw, err := s.GetSetting(ctx, SchemaVersionKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return 0, false
	}
	version, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, false
	}
	return version, true
}

// SchemaFingerprint returns a stable digest of the schema this binary expects.
//
// It covers every CREATE TABLE the code defines, which is the same set
// autoMigrate reconciles, so adding a table or a column changes it. The
// statements are sorted first because Go iterates maps in a random order, and a
// fingerprint that varied between two runs of the same binary would report a
// schema change on every restart.
func SchemaFingerprint() string {
	var stmts []string
	for _, query := range commonQueries {
		q := strings.TrimSpace(query)
		if !strings.HasPrefix(strings.ToUpper(q), "CREATE TABLE") {
			continue
		}
		// Normalise whitespace so reformatting the DDL is not a schema change.
		stmts = append(stmts, strings.Join(strings.Fields(q), " "))
	}
	slices.Sort(stmts)

	sum := sha256.Sum256([]byte(strings.Join(stmts, "\n")))
	return hex.EncodeToString(sum[:])
}

// recordSchemaState stores the applied fingerprint. Init calls it only after
// every table was created and every missing column added, so a failed migration
// leaves the previous value untouched — which is what makes a half-applied
// schema visible rather than silent.
//
// A failure to record is not a failure to start. The schema is correct at this
// point; losing the note about it is worth reporting, not worth refusing to
// serve traffic over.
func (s *sqlStorage) recordSchemaState(ctx context.Context) error {
	if err := s.saveSetting(ctx, SchemaFingerprintKey, SchemaFingerprint()); err != nil {
		return err
	}
	// Written with the fingerprint and under the same rule: only after a run
	// that fully succeeded. Recording a version a half-applied migration never
	// reached would lock out the very binary that could finish the job.
	if err := s.saveSetting(ctx, SchemaVersionKey, strconv.Itoa(currentSchemaVersion)); err != nil {
		return err
	}
	return s.saveSetting(ctx, SchemaAppliedAtKey, time.Now().UTC().Format(time.RFC3339))
}

func (s *sqlStorage) saveSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, s.queries.get(QuerySaveSetting), key, value)
	return err
}
