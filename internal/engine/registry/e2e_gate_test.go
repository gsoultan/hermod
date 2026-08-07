package registry

import (
	"os"
	"testing"
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
