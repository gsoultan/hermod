package registry

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// requireIntegrationInfra gates the e2e tests in this package that need a real
// Postgres (and, for the PgBouncer cases, a real pooler) on localhost.
//
// Without this gate `go test ./...` fails on any machine — including CI —
// that has no local Postgres, because the tests t.Fatalf on a connection
// refused rather than skipping. That makes the "test suite green" gate in
// AGENTS.md unreachable and trains everyone to ignore a red suite.
//
// This matches the convention already used across pkg/comm (see
// pkg/comm/sink/postgres/postgres_idempotency_test.go) and documented in
// README.md under "Running Integration Tests".
func requireIntegrationInfra(t *testing.T) {
	t.Helper()
	if os.Getenv("HERMOD_INTEGRATION") != "1" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 (and run a local Postgres) to enable")
	}
}

// requireIntegrationDB returns a handle to dsn, skipping the test when the
// database is not reachable.
//
// These tests hardcode localhost DSNs and used to assume the databases and
// their tables had been prepared by hand. On a machine where HERMOD_INTEGRATION
// was set but that fixture was absent they did not skip -- they failed, on a
// missing relation, which reads like a product defect and is not one. A test
// that cannot tell "the feature is broken" from "my fixture is missing" is
// worse than no test.
func requireIntegrationDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Skipf("integration: cannot open %s: %v", redactDSN(dsn), err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		t.Skipf("integration: %s is not reachable (%v); create it to run this test",
			redactDSN(dsn), err)
	}
	return db
}

// seedTestData creates the table these tests read from, so a run does not
// depend on whatever a previous one happened to leave behind.
func seedTestData(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := t.Context()
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS test_data (
		id SERIAL PRIMARY KEY,
		name TEXT NOT NULL,
		value INTEGER
	)`); err != nil {
		t.Fatalf("creating test_data: %v", err)
	}
	if _, err := db.ExecContext(ctx, `TRUNCATE test_data RESTART IDENTITY`); err != nil {
		t.Fatalf("truncating test_data: %v", err)
	}
}

// redactDSN strips the password so a skip message is safe to paste anywhere.
func redactDSN(dsn string) string {
	if at := strings.LastIndex(dsn, "@"); at > 0 {
		if slash := strings.Index(dsn, "//"); slash > 0 && slash+2 < at {
			return dsn[:slash+2] + "***" + dsn[at:]
		}
	}
	return dsn
}
