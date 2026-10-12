# Developing Hermod

Running the integration and UI tests, and what CI runs.

## Running Integration Tests

Hermod ships with env-gated integration tests.

- Two-worker lease failover (no external deps):
  - Set `HERMOD_INTEGRATION=1`
  - Run: `go test ./internal/engine -run TwoWorkerLeaseFailover -v -tags=integration`
- SQL sink idempotency:
  - MySQL: set `HERMOD_INTEGRATION=1` and `MYSQL_DSN` (e.g., `user:pass@tcp(host:3306)/dbname`)
  - Postgres: set `HERMOD_INTEGRATION=1` and `POSTGRES_DSN` (e.g., `postgres://user:pass@host:5432/db?sslmode=disable`)
  - Run: `go test ./pkg/sink/mysql -tags=integration` and `go test ./pkg/sink/postgres -tags=integration`

### UI Testing

Hermod uses Vitest for unit/component testing and Cypress for End-to-End (E2E) testing.

#### Unit & Component Tests
```bash
cd ui
bun run test
```

#### End-to-End Tests (Cypress)
1. Ensure the development server is running (default: `http://localhost:5173`).
2. Run Cypress:
```bash
cd ui
bun run cypress:run   # Headless mode
bun run cypress:open  # Interactive mode
```

## Continuous Integration (CI)

This repository includes a GitHub Actions workflow that:

- Runs a quick Go build + focused tests via `scripts/quick-verify.ps1` on push/PR.
- Builds the UI (`bun run build`) as a separate job.

The workflow file is at `.github/workflows/ci.yml`. To enable optional SQL integration tests, add secrets with DSNs and create an additional job based on your environment.
