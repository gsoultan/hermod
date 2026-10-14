# Using Hermod

The command-line client, embedding Hermod as a Go library, running the API and workers, supported platforms, and the admin settings endpoints.

## Hermod CLI (`hermodctl`)

Hermod provides a professional CLI tool for developers and operators to manage the platform from the terminal.

1.  **Workflow Linting**: Validate DAGs and schema mappings locally before deployment.
    ```bash
    hermodctl workflow lint path/to/workflow.json
    ```
2.  **Secret Management**: Manage enterprise secrets directly from the CLI.
    ```bash
    hermodctl secret set vault my-secret-key "my-value"
    ```
3.  **Real-time Monitoring**: Monitor worker health and cluster throughput in the terminal.
    ```bash
    hermodctl monitor
    ```
4.  **GitOps Support**: Export and import workflows as code for CI/CD pipelines.
    ```bash
    hermodctl workflow export --all > workflows.json
    ```

## As a Library

Compiled as `Example` in [`pkg/engine/example_test.go`](../pkg/engine/example_test.go),
so it cannot drift from the packages it names.

Note that `go get` cannot fetch these packages at the current version — see
[`go get` does not work](../README.md#go-get-does-not-work-and-is-not-expected-to). Building
against a checkout, or a `replace` directive pointing at one, does work.

```go
import (
    "context"
    "log"
    "time"

    "github.com/gsoultan/hermod"
    "github.com/gsoultan/hermod/pkg/comm/buffer"
    "github.com/gsoultan/hermod/pkg/comm/formatter/json"
    "github.com/gsoultan/hermod/pkg/comm/sink/stdout"
    "github.com/gsoultan/hermod/pkg/engine"
    "github.com/gsoultan/hermod/pkg/engine/config"
)

func main() {
    source := mySource{} // anything implementing hermod.Source
    sinks := []hermod.Sink{stdout.NewStdoutSink(json.NewJSONFormatter())}
    buf := buffer.NewRingBuffer(1024)

    eng := engine.NewEngine(source, sinks, buf)
    eng.SetConfig(config.Config{
        MaxRetries:    5,
        RetryInterval: 200 * time.Millisecond,
    })

    if err := eng.Start(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

`Start` blocks until the context is cancelled or the source is drained.

## As an Application

Hermod can be run as a standalone application. By default, it starts in **API Mode**, which includes a web-based management platform for configuring sources, sinks, and engines.

1. Run the application:
   ```bash
   go run ./cmd/hermod
   ```

   This will automatically build the UI (if not already built) and start the Go backend. The UI assets are served from the binary (or disk in dev mode).

   If you want to force a UI rebuild:
   ```bash
   go run ./cmd/hermod --build-ui
   ```

The UI will be available at `http://localhost:4000`.

## Multi-Platform Support

Hermod is compiled for high performance and supports the following platforms and architectures:

- **OS**: Linux only (Ubuntu, Debian, RedHat, Alpine).
- **Architecture**: `amd64`, `arm64`.

You can download the latest binaries and packages (`.deb`, `.rpm`, `.apk`) from the [GitHub Releases](https://github.com/gsoultan/hermod/releases) page.

### API Mode (Default)
   To start Hermod in API mode (which also serves the UI):
   ```bash
   go run ./cmd/hermod
   ```

   The UI will be available at `http://localhost:4000`.

   You can customize the port and database for storing state:
   ```bash
   go run ./cmd/hermod --port=8080 --db-type=sqlite --db-conn=~/.hermod/hermod.db
   ```

   Hermod supports multiple databases for storing its state (Sources, Sinks, Workflows):
   - **SQLite**: `--db-type=sqlite --db-conn=~/.hermod/hermod.db`
   - **PostgreSQL**: `--db-type=postgres --db-conn="postgres://user:pass@localhost:5432/hermod?sslmode=disable"`
   - **MySQL/MariaDB**: `--db-type=mysql --db-conn="user:pass@tcp(localhost:3306)/hermod"`

   When running in API mode, Hermod saves its database configuration to `~/.hermod/db_config.yaml` after the first successful setup or when updated via the UI. Subsequent starts will automatically use this configuration.

   #### Standalone Mode
   In Standalone mode, both the API/UI and a worker are started in the same process:
   ```bash
   go run ./cmd/hermod --mode=standalone
   ```

   #### Worker Scaling and Sharding
   Hermod supports horizontal scaling of workers. You can run multiple worker processes that share the same platform and automatically shard connections between them.

   To start a worker-only process connected to the platform:
   ```bash
   go run ./cmd/hermod --mode=worker --platform-url="http://localhost:4000" --worker-id=0 --total-workers=2
   ```

   - `--mode=worker`: Runs only the engine worker (no API/UI).
   - `--platform-url`: The URL of the Hermod platform API.
   - `--worker-id`: The unique ID of this worker (starting from 0).
   - `--total-workers`: The total number of workers in the cluster.

   Workflows are automatically assigned to workers based on a hash of their ID. If the number of workers changes, the workflows will be re-sharded across the available workers.

   #### Explicit Worker Assignment
   You can also register workers in the Hermod platform and explicitly assign Sources, Sinks, and Workflows to a specific worker. This is useful when workers are running on different servers or in different vhosts.

   1. Register a worker via the API or UI. Each worker should have a unique GUID.
   2. Start the worker process with the `--worker-guid` and `--platform-url` flags:
      ```bash
      go run ./cmd/hermod --mode=worker --worker-guid="my-server-1" --platform-url="http://localhost:4000"
      ```

   #### Worker Self-Registration
   Instead of manually registering a worker in the UI, you can let the worker register itself upon its first run by providing additional flags:

   ```bash
   go run ./cmd/hermod --mode=worker --worker-guid="my-server-1" --platform-url="http://localhost:4000" --worker-host="192.168.1.10"
   ```

   - `--worker-host`: The hostname or IP address where the worker is running.
   - `--worker-port`: The port the worker is using.
   - `--worker-description`: Optional description of the worker.

   If a worker with the provided `--worker-guid` is not found in the database, it will be automatically created using the provided information. Name will default to the GUID.

   3. When creating or updating a Source, Sink, or Workflow, specify the `worker_id` to pin it to that worker.

   If a component has a `worker_id` assigned, only the worker with the matching `--worker-guid` will process it. If no `worker_id` is assigned, the component is subject to the default hash-based sharding.

   #### Worker Registration & Tokens (Security)
   - When you create a worker via the API/UI, Hermod generates a secret `token` for that worker.
   - For security, the worker `token` is returned only in the Create response. Subsequent GET/LIST responses do not include it.
   - Store the token securely and pass it to the worker process using the `--worker-token` flag (or environment variable).

   Example single‑line command (shown in the UI after creation):
   ```bash
   hermod --mode=worker --platform-url="http://localhost:4000" --worker-guid="<GUID>" --worker-token="<TOKEN>"
   ```

   If you prefer, the worker can self‑register with `--worker-host/--worker-port`, but you still need to provide the `--worker-token` obtained at creation time for authenticated API calls.

   #### Worker CLI via Environment Variables
   To simplify production deployments (containers, systemd), Hermod supports environment variables as fallbacks when flags are not provided (or left at defaults):

   - `HERMOD_MODE` → `--mode`
   - `HERMOD_PLATFORM_URL` → `--platform-url`
   - `HERMOD_WORKER_GUID` → `--worker-guid`
   - `HERMOD_WORKER_TOKEN` → `--worker-token`
   - `HERMOD_WORKER_HOST` → `--worker-host`
   - `HERMOD_WORKER_PORT` → `--worker-port`
   - `HERMOD_WORKER_DESCRIPTION` → `--worker-description`
   - `HERMOD_WORKER_ID` → `--worker-id`
   - `HERMOD_TOTAL_WORKERS` → `--total-workers`

   #### Reference Lookup Files

   | Variable | Default | What it does |
   | :--- | :--- | :--- |
   | `HERMOD_REFERENCE_DIRS` | none | Comma-separated absolute directories the Reference Lookup node may read files from, besides the upload directory (`file_storage.local_dir`, default `uploads`). Any other path, including a `../` walk or a symlink out of these directories, is refused. See [docs/structural-nodes.md](structural-nodes.md#reference-lookup). |

   #### Stall Detection & Recovery Tuning

   These control when a workflow is declared wedged and rebuilt. The defaults suit most
   pipelines; a malformed or non-positive value is ignored in favour of the default, so a typo
   in a deployment variable can never switch a safety mechanism off.

   | Variable | Default | What it does |
   | :--- | :--- | :--- |
   | `HERMOD_STALL_THRESHOLD` | `60s` | How long a pipeline may hold outstanding work without completing any of it before it is treated as wedged. Raise it for a workflow whose sink is legitimately slow; lower it to detect faster during an incident. |
   | `HERMOD_STREAM_SILENCE_INTERVAL` | `10s` | How often a CDC source's stream is sampled for silence. The deadline itself is not configurable — it is derived from the source server's own `wal_sender_timeout`, because that is what sets the keepalive cadence a healthy stream is held to. |
   | `HERMOD_LAG_WARN_BYTES` | `256MB` | How much un-acknowledged WAL a source may retain before it is reported. This guards the **source** database's disk. |

   **Runbook — what the recovery log lines mean:**

   - `Pipeline stalled: work is outstanding but nothing has completed` — the pipeline is holding
     work it has stopped finishing. Usually a sink. The workflow is being rebuilt; no data is
     lost, because un-acknowledged changes replay from the replication slot.
   - `Source stream has gone silent: not even a keepalive has arrived` — the replication
     connection is open and the slot still reports it attached, but the server has stopped
     serving it. Look at the source database, not the sinks.
   - `Workflow stalled again while the last automatic restart was still settling` — informational.
     The previous rebuild is being given its chance; backoff widens with each attempt.
   - `Workflow stalled but automatic recovery is exhausted; manual intervention required` — the
     restart budget for the window is spent. Fix the underlying fault, then **stop and start** the
     workflow: an operator stop is what restores its supervision budget.
   - `Workflow logs could not be written to storage and were dropped` — the log *store* is
     failing, not the pipeline. The lines themselves are still in the process log.

   Example using env vars:
   ```bash
   export HERMOD_MODE=worker
   export HERMOD_PLATFORM_URL=http://localhost:4000
   export HERMOD_WORKER_GUID=my-server-1
   export HERMOD_WORKER_TOKEN=secret-token
   hermod
   ```

   #### Background OS Service Integration

   Hermod can be integrated with the OS-level service manager (systemd on Linux) directly from the binary. This ensures that Hermod starts automatically on boot and can be managed using standard system tools.

   **Service commands:**
   - `hermod --service install` - Install the service (requires administrative privileges).
   - `hermod --service uninstall` - Remove the service.
   - `hermod --service start` - Start the service.
   - `hermod --service stop` - Stop the service.
   - `hermod --service restart` - Restart the service.
   - `hermod --service status` - Check the current service status.

   **Examples: Install as a service**
   ```bash
   # Linux (systemd) — Worker mode
   ./hermod --mode=worker --platform-url="http://localhost:4000" --worker-guid="worker-1" --service install
   ./hermod --service start

   # Linux — Standalone (API + Worker in one process)
   ./hermod --mode=standalone --service install
   ./hermod --service start
   ```

   Or use the helper script provided in `scripts/`:
   ```bash
   # Linux
   bash scripts/install-service.sh standalone
   ```

   The service will be configured to run with all the flags provided during the `install` command (except for `--service` itself`).

## Settings UI Improvements (Current)

- Settings → Database now pre-fills from the backend via a new admin-only endpoint:
  - GET /api/config/database → returns `{ type, conn }`. For non-SQLite DSNs, passwords are masked in the returned connection string.
- Notification Settings page includes a "Send Test Notification" button:
  - POST /api/settings/test (admin-only) sends a test through configured channels in this order: Email → Slack → Discord → Webhook → Telegram → Panmail. The UI displays per-channel results.
- Settings → Connectivity → Panmail sends alerts through a Panmail gateway. With **Mail every dead-lettered message** on, each workflow's dead letters are gathered for a minute and mailed as one digest: the workflow, the node or sink that gave up, the error, up to 10 of the messages (4 KB each), and a link to the Dead letters page filtered to the workflow (`/dead-letters?workflow_id=<id>`, which needs the Base URL). **Leave message contents out** sends everything but the messages. Every Panmail field takes `{{secret.NAME}}` and `{{env.NAME}}`; they resolve from the global secret manager and `HERMOD_ENV_*`, never from a vhost's secrets.
- **Dead Letters** (sidebar) lists every message a workflow parked, whatever its dead-letter sink: Hermod keeps a copy of each (workflow, failing node or sink, error, payload up to 256 KB, metadata, before-image) for `HERMOD_DEAD_LETTER_RETENTION` (default `720h`). From the page an Editor can replay a message from the node that failed it, edit the payload and replay it (optionally from the workflow's start, optionally tested first without writing anything), or discard it; anyone with access to the vhost can read and export them as CSV. The API is `GET /api/dead-letters` (filters `workflow_id`, `where`, `status`, `search`, `since`, `until`, `vhost`, `limit`, `offset`), `GET /api/dead-letters/stats`, `GET /api/dead-letters/{id}`, `GET /api/dead-letters/export`, `POST /api/dead-letters/{id}/replay` (`payload`, `from_start`, `test`), `POST /api/dead-letters/replay` and `POST /api/dead-letters/discard` (`ids`).

These endpoints require an administrator role and are used by the UI automatically. No additional configuration is needed beyond saving your settings.
