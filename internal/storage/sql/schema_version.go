package sql

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
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
)

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
	return s.saveSetting(ctx, SchemaAppliedAtKey, time.Now().UTC().Format(time.RFC3339))
}

func (s *sqlStorage) saveSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, s.queries.get(QuerySaveSetting), key, value)
	return err
}
