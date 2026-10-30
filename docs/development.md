# Developing Hermod

The local dev stack, running the integration and UI tests, and what CI runs.

## Running Hermod locally

One command brings up the whole stack — Postgres, the API + worker, and the UI dev server — and
completes Hermod's first-run wizard for you, so all that is left is logging in:

```bash
./scripts/dev.sh
```

Then open **http://localhost:5175** and sign in with **`admin` / `admin`**.

| Option | Effect |
| :--- | :--- |
| `./scripts/dev.sh` | Start everything (PostgreSQL via Apple `container`) |
| `./scripts/dev.sh --sqlite` | Use SQLite instead — no container needed |
| `./scripts/dev.sh --reset` | Wipe the dev database and re-seed from scratch |
| `./scripts/dev.sh --build-ui` | Refresh the UI bundle the API serves, then start |
| `./scripts/dev.sh --stop` | Stop a running stack |

**Work against port 5175.** It runs Vite, so edits under `ui/src` appear in the browser immediately
via hot reload — no restart needed. Port 4005 also serves a UI, but that is the pre-built bundle in
`internal/api/static`; it will **not** show your edits until you run `--build-ui`. It exists for
checking what production actually ships.

**Ports are chosen, not assumed.** The stack prefers 4005 (API), 50051 (gRPC) and 5175 (UI), and
steps up to the next free number for any of them already taken — so a second stack, or an unrelated
service, never produces `address already in use`. The banner prints the numbers it settled on, and
`./scripts/dev.sh --print-ports` shows them without starting anything. Note that 50051 is the
conventional gRPC port and therefore the likeliest to collide, even when both documented ports are
free; a failed bind on it takes the whole backend down.

Notes:

- **PostgreSQL is created for you.** On first run the script builds a `postgres-dev` container with
  Apple's `container` CLI (macOS 26+), configured with `wal_level=logical` for CDC, and creates the
  `hermod_metadata`, `hermod_test_source` and `hermod_test_sink` databases. Nothing to set up by hand.
- Re-running is safe: an existing container is started rather than rebuilt, its data survives
  `container stop`, and an existing admin user is kept rather than recreated.
- **Your real configuration is never touched.** The dev stack writes to `.dev/` inside the repo via
  `HERMOD_CONFIG_DIR`, not `~/.hermod`, so it cannot overwrite another Hermod instance's setup.
- Logs stream to the terminal and are kept in `.dev/logs/{backend,ui}.log`.
- Overridable: `HERMOD_DEV_ADMIN_USER`, `HERMOD_DEV_ADMIN_PASS`, `HERMOD_DEV_API_PORT`,
  `HERMOD_DEV_GRPC_PORT`, `HERMOD_DEV_UI_PORT`, `HERMOD_DEV_PG_CONTAINER`, `HERMOD_DEV_PG_PORT`.
  A port set explicitly is used as given — if it is busy the run fails and names the holder, rather
  than silently moving to another port you did not ask for.

### Managing the database container

```bash
./scripts/create-postgres.sh              # create it (no-op if it already exists)
./scripts/create-postgres.sh --recreate   # destroy and rebuild from scratch
container exec -it postgres-dev psql -U postgres
container stop postgres-dev               # data is preserved
```

Overrides: `HERMOD_DEV_PG_IMAGE` (default `postgres:18-alpine`), `HERMOD_DEV_PG_PASSWORD`.

Data lives in the container's own filesystem — it survives `stop`/`start` but not
`container delete`. PGDATA is deliberately not bind-mounted, because PostgreSQL's strict ownership
requirements do not survive the macOS→Linux filesystem boundary reliably.

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
