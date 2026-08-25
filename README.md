# Hermod

**Real-time Postgres CDC to 40+ destinations, with a visual pipeline editor. One Go
binary, self-hosted, running locally in about a minute.**

No JVM, no Kafka cluster required, no per-row cloud bill. `./scripts/dev.sh` brings up
Postgres, the API, a worker and the UI, and completes first-run setup for you.

Where it fits: managed ELT (Fivetran) and open-source ELT (Airbyte) are
batch-scheduled and measure freshness in minutes. Real-time CDC services (Estuary)
are sub-second but not self-hostable. Stream processors (Redpanda Connect, NiFi)
have no visual pipeline editor or need a JVM. Hermod is the self-hosted overlap:
sub-second Postgres CDC, in-pipeline windowing and transforms, and a DAG you can see.

If you need 600 connectors, use Airbyte. If you need durable multi-day workflow
orchestration, use Temporal. If you need one binary that tails a Postgres WAL and
lands it somewhere else, in order and without a cluster, that is this.

### Maturity, up front

Hermod covers a lot of surface, and that surface is **not uniformly deep**. Connectors
are tiered — GA, Beta, Experimental — in
[Connector maturity tiers](#connector-maturity-tiers). Read that table before picking
a connector for production. Anything not listed as GA has not been tested against
live infrastructure.

Delivery is **at-least-once** with sink-side idempotency for duplicate suppression.
Where this document says a guarantee is scoped or unfinished, that is the literal
state of the code, not modesty.

**Initial load is available for PostgreSQL, MySQL and MongoDB, and off by default.**
Set `initial_load: "true"` on one of those CDC sources and the rows already in the
watched tables are carried across before streaming begins.

**PostgreSQL is consistent rather than approximate.** The replication slot is created
over the replication protocol so that it exports a snapshot, the backfill reads at
exactly that snapshot, and streaming starts from the slot's consistent point — so
nothing committed before the boundary is missed and nothing is read twice at it.

**MySQL and MongoDB are gapless but not snapshot-isolated.** Neither offers Postgres's
exported-snapshot handshake, so the boundary is pinned by position instead: the binlog
coordinates (MySQL) or the cluster time (MongoDB) are taken *before* the tables are
read, and streaming resumes from there. Nothing committed during the backfill is lost,
but a row changed while it runs arrives twice — once as it was, once as it became.
That is the ordinary at-least-once bargain and sink-side idempotency collapses it; see
[Delivery guarantee](#delivery-guarantee).

Each source keeps its own record of having run, so the backfill is once-only and
turning the flag on for a workflow that is already streaming does nothing. For
PostgreSQL the record is the replication slot itself — if it exists, changes have
already streamed from it and the rows are downstream — so enabling the flag takes
effect only once the slot is dropped. MySQL and MongoDB leave nothing server-side that
could serve the same purpose, and their stream positions cannot stand in either: a
backfill moves no binlog position and produces no resume token, so a table carried
across and then never written to would be indistinguishable from one that had never
run. They record completion in the source state the engine persists on every ack
(`initial_load: done`), alongside the position.

Off by default deliberately: enabling it for every existing workflow would re-read
every source table the first time a position was reset, which is the opposite of what
an upgrade should do. The remaining CDC sources still have no initial load — for
those, a backfill and a stream have to be sequenced by hand, position first, and the
duplicates that ordering produces are what sink-side idempotency is for.

---

## Enterprise Data Platform Features

Hermod is built for mission-critical enterprise data workloads, providing robust features for governance, reliability, and observability:

- **Two-Phase Commit (2PC) across a transactional sink group**: A group of sinks commits atomically — either every member applied the batch or none did — and the guarantee survives a crash, because the coordinator's decision is durable before any member is told about it. Recovery uses **presumed abort**, and a reaper rolls back anything left in doubt past a deadline, which is what keeps an interrupted round from holding PostgreSQL locks indefinitely. Scope, stated plainly: **only the Postgres sink can participate today**, so the realistic shape is two or more Postgres sinks kept consistent; a group containing a sink that cannot take part is refused at construction rather than silently degraded. Kafka does *not* participate — it previously carried no-op stubs that would have reported a failed rollback as a success. See [Distributed transactions](#distributed-transactions-transactional-sink-groups), including the operational hazard note, before enabling.
- **SSO & OpenID Connect (OIDC)**: Support for centralized identity providers like **Okta**, **Auth0**, and **Azure AD** for platform-wide authentication and RBAC.
- **Vector Database Sinks**: Built-in support for **Pinecone**, **Milvus**, and **pgvector** to power enterprise AI knowledge bases and RAG pipelines.
- **Hermod CLI (`hermodctl`)**: A powerful terminal-based tool for workflow linting, secret management, and real-time remote monitoring.
- **Global Schema Registry**: Enforce data contracts with built-in JSON Schema, Avro, and Protobuf support. Automatically tracks schema versions and ensures backward compatibility.
- **Workflow Versioning & Rollback**: Every change to a workflow is automatically versioned. One-click rollback allows you to quickly revert to a known stable configuration.
- **Distributed State & Coordination**: Native support for **Redis** and **Etcd** backends for consistent state management across multiple worker instances.
- **Enterprise Secret Management**: Securely resolve credentials from **HashiCorp Vault**, **AWS Secrets Manager**, and **Azure Key Vault** using the `secret:key` prefix.
- **Role-Based Access Control (RBAC)**: Granular permissions for Administrators, Editors, and Viewers, including VHost-level isolation.
- **Message Tracing & Visual Journey**: Visualize the exact path of a single message through the DAG, including latency and data mutations at each step.
- **Pipeline Health Heatmaps**: Real-time visualization of throughput and error rates directly on the workflow canvas.
- **WebAssembly (WASM) Transformations**: Run custom business logic written in Go, Rust, or C++ at near-native speed within the pipeline.
- **Automated PII Scanning**: Built-in `mask` transformation detects and redacts sensitive data (PII/PHI) using a sophisticated regex-based discovery engine.
- **Audit Logging**: Complete history of administrative changes and system events for security and compliance audits.
- **At-least-once delivery with exactly-once *effects* at the sink**: Messages are acknowledged only after successful delivery, using the **Transactional Outbox** pattern for SQL sources. Combined with sink-side idempotency keys (see [Idempotency and Exactly-Once Effects](#idempotency-and-exactly-once-effects-sink-side)), a duplicate delivery does not produce a duplicate row. This is *not* end-to-end exactly-once delivery: the transport is at-least-once and duplicates are suppressed where they land, which is the same guarantee most of this category actually ships.
- **Sequential Control Flow**: Explicitly chain sinks and transformations sequentially. Supports "Sinks as Transformers" by returning data from a sink back into the workflow pipeline.
- **Stateful Event Correlation (Join/Zip)**: Wait for and join messages from multiple sources based on a common key before downstream delivery.
- **Circuit Breaker & Failure Recovery**: Protect downstream systems with a built-in Circuit Breaker node. Automatically routes messages to failure branches when error thresholds are exceeded.
- **Interactive Workflow Debugger**: Real-time message pausing, stepping, and inspection directly from the UI. Pause messages at any node to inspect payload state and resume processing when ready.
- **Visual Lineage with Data Diffs**: Enhanced message tracing with "Before and After" snapshots for every transformation node in the DAG. Visually debug exactly how data is mutated at each step.
- **AIOps & Self-Healing Optimization**: AI-driven performance tuning that automatically adjusts concurrency, batch sizes, and retry policies based on real-time throughput and error patterns.
- **Intelligent Data Quality Alerts**: Automated detection of data quality drift using the `governance.Scorer`. Alerts trigger when schema adherence or DQ scores drift from historical averages.
- **Global Mesh Governance (experimental)**: A cluster registry and an HTTP forwarding sink, letting one Hermod instance route messages to another. Scope check: it is roughly 320 lines, `Failover` marks a cluster's status and logs rather than redirecting traffic, and there are no health checks, retries or partition handling. Treat it as a foundation, not a control plane.
- **WASM & Blueprint Marketplace**: Discover, version, and share custom WebAssembly transformers and Go-based workflow blueprints via an integrated Marketplace.
- **Legacy connectivity via incremental polling (Oracle, IBM DB2)**: Sources for **Oracle** and **IBM DB2** that track a monotonically increasing key and emit new rows. To be precise about what this is and is not: it is **watermark polling, not log-based CDC** — there is no LogMiner or journal reader. It therefore sees **inserts only**; updates and deletes to existing rows are invisible, and every message is emitted as a create. Query construction goes through `sqlutil.BuildIncrementalQuery`, which applies each dialect's row limit *after* the sort (Oracle's `ROWNUM` is evaluated before `ORDER BY`, so it is applied to an ordered subquery — getting this wrong silently skips rows).
- **Advanced Database Mapping & Schema Discovery**: Production-ready column mapping for all major databases (**Postgres**, **MySQL**, **MSSQL**, **Oracle**, **Snowflake**, **MongoDB**, etc.). Supports automatic table creation, identity/auto-increment columns, and "Smart Mapping" from both source and sink schemas.
- **Resource-Aware Sharding**: Advanced worker sharding using Rendezvous hashing weighted by real-time CPU/Memory metrics. Ensures optimal workload distribution and prevents workflow flapping with built-in hysteresis.
- **AI-Native Transformations**: Integrated "Cognitive ETL" nodes for **AI Enrichment** and **AI Mapping**, supporting OpenAI and local Ollama models. Includes **AI Mapping Suggestions** that automatically propose fixes for schema mismatches.
- **Auto-Parallelization & Node-Level Backpressure**: The engine automatically detects independent branches in your DAG and executes them in parallel. Built-in node-level backpressure ensures that slow sinks or transformations don't block healthy execution paths.
- **Self-Healing "Safe Mode" & Anomaly Detection**: Automatically detects processing anomalies (e.g., latency spikes) and can enter a "Safe Mode" during critical failures, diverting traffic to a Dead Letter Sink to preserve data and maintain system stability.
- **Stall Detection & Automatic Recovery**: A wedged pipeline is detected and rebuilt without an operator. Two independent watchdogs cover the two ways a pipeline can wedge — work outstanding that never completes, and a CDC stream that stops being served (detected against the source's own `wal_sender_timeout` keepalive cadence). Recovery is a supervised restart with an exponential backoff, jitter, and a bounded restart budget, so a genuinely broken sink escalates to a human instead of looping. Nothing is lost: un-acknowledged changes replay from the replication slot's confirmed position.
- **Generic Protobuf Source**: A universal source that can dynamically load `.proto` files at runtime, providing type-safe ingestion for any Protobuf-based service without recompilation.
- **Stateful Windowing**: Support for **Tumbling**, **Sliding**, and **Session** windows in aggregation nodes, enabling complex real-time analytics and time-series processing directly in the pipeline.
- **Hot-Reloading for Scripts**: Transformation logic in **WASM** or **Lua** can be updated on-the-fly. The engine detects changes and reloads the logic without requiring a workflow restart.
- **OpenTelemetry (OTLP) Native**: Built-in support for exporting internal traces and metrics to standard enterprise observability stacks via the OTLP protocol.
- **Enterprise Connectivity**: Native, optimized connectors for high-scale enterprise sinks like **Snowflake**.

---

Hermod works by reading data from a `Source`, buffering it in a high-performance buffer (in-memory `RingBuffer` or persistent `FileBuffer`), and then writing it to a `Sink`. This architecture allows it to handle peak loads and provide a flexible way to connect different databases to various message streams.

```
[Source] -> [Buffer] -> [Sink]
   ^           |          ^
   |        [Engine]      |
   +-----------+----------+
```

## Usage

### Hermod CLI (`hermodctl`)

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

### As a Library

```go
import (
    "context"
    "github.com/user/hermod/pkg/engine"
    "github.com/user/hermod/pkg/sink/stdout"
    "github.com/user/hermod/pkg/buffer"
    // ... import your source
)

func main() {
    src := // initialize your source
    sinks := []hermod.Sink{stdout.NewStdoutSink(), // ... other sinks}
    buf := buffer.NewRingBuffer(1024)

    eng := engine.NewEngine(src, sinks, buf)
    
    // Optional: Configure engine for production
    eng.SetConfig(engine.Config{
        MaxRetries:    5,
        RetryInterval: 200 * time.Millisecond,
    })
    
    eng.Start(context.Background())
}
```

### Development Quick Start

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
  `HERMOD_DEV_UI_PORT`, `HERMOD_DEV_PG_CONTAINER`, `HERMOD_DEV_PG_PORT`.

#### Managing the database container

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

### As an Application

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

### Multi-Platform Support

Hermod is compiled for high performance and supports the following platforms and architectures:

- **OS**: Linux only (Ubuntu, Debian, RedHat, Alpine).
- **Architecture**: `amd64`, `arm64`.

You can download the latest binaries and packages (`.deb`, `.rpm`, `.apk`) from the [GitHub Releases](https://github.com/user/hermod/releases) page.

#### API Mode (Default)
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

## Production Considerations

- **Logging**: Hermod uses a `Logger` interface and provides a default implementation using `zerolog` for zero-allocation structured logging. You can provide your own implementation via `eng.SetLogger(myLogger)`.
- **Retries**: The `Engine` automatically retries failed `Sink.Write` operations. Configure this via `eng.SetConfig`.
- **Health Checks**: Sources and Sinks implement a `Ping` method. The `Engine` performs pre-flight checks using `Ping` before starting.
- **Startup Resilience**: Hermod is designed to be resilient to initial infrastructure failures. If the primary storage backend is unavailable at startup, the process will continue to run and retry the connection periodically until successful, enabling automatic recovery in orchestrated environments.
- **Persistence**: For production use cases requiring absolute durability, use the `file_buffer` option. This ensures that even if the process crashes, messages read from the source but not yet written to the sink are persisted on disk.
- **Graceful Shutdown**: The `Engine.Start` method respects the provided `context.Context`. When the context is cancelled, the engine will stop reading from the source, signal the buffer to close, and wait for all pending messages in the buffer to be written to the sink before exiting. This ensures no data loss during normal shutdown procedures.

### SQLite busy/locked handling

When using SQLite for the platform database, concurrent writes can occasionally hit `SQLITE_BUSY` ("database is locked"). Hermod mitigates this in two ways:

- API returns HTTP 503 with `Retry-After: 1` for busy errors on sink create/update. Clients should retry the request.
- The storage layer automatically retries transient busy errors with bounded exponential backoff and respects request context deadlines.

You can tune SQLite's busy timeout via an environment variable (milliseconds):

```
HERMOD_SQLITE_BUSY_TIMEOUT_MS=15000
```

### Advanced Logic & Control Flow

Hermod provides a suite of advanced nodes for complex data orchestration:

- **Switch Node**: Branching logic based on message content. Supports multiple cases and a default path.
- **Foreach Node (Fan-out)**: Splits a single message into multiple independent messages based on an array field. Adds `_fanout_group`, `_fanout_index`, and `_fanout_total` metadata.
- **Collect Node (Fan-in)**: Synchronizes parallel branches from a `Foreach` node. Waits for all items in a group to arrive before emitting a single merged message.
- **Deduplicate Node**: High-speed, in-memory deduplication using rotating Bloom filters. Prevents processing of duplicate messages within a rolling window.
- **Wait Node**: Pauses execution for a specified duration (e.g., `10s`, `1h`). Durations > 30s are automatically suspended to the database for reliability.
- **Approval Node (HITL)**: Halts the workflow and creates a manual approval request. Supports custom form definitions (JSON) that users fill out in the Approvals UI.
- **Log Node**: Explicitly sends data or fields to the live logging system, helpful for debugging production workflows.
- **Error Branching**: All nodes support an `error` output branch. If a node fails, the engine automatically routes the message along the `error` edge if configured.

### Execution‑Level Fan‑out (Foreach Node)

Hermod supports an execution‑level Foreach node that splits a single message into multiple independent messages based on an array path in the message data.

- Configure in UI: Add a node of type `Foreach` and set the required "Array Path" (e.g., `items`, `data.rows`).
- Runtime semantics:
  - For each element in the array, the engine emits a new message downstream.
  - Each emitted message includes helper fields in `data`:
    - `_item`: the current array element
    - `_index`: the 0‑based index
  - And correlation metadata for observability/idempotency:
    - `_fanout_group`: original message ID
    - `_fanout_index`: current index (string)
    - `_fanout_total`: total number of items (string)
- Error handling: If the node fails (e.g., path missing or not an array), the engine routes the message along edges labeled `error` from the Foreach node.

Notes:
- This Foreach node is different from the legacy data‑level "foreach transformer" which materializes arrays inside a single message. The execution‑level node produces N downstream executions.

Default is 15000 ms. WAL mode and other safe pragmas are enabled by default.

## Distributed transactions (transactional sink groups)

A **transactional sink group** writes to several sinks inside one distributed
transaction: either every member applied the batch or none did, and that holds across
a crash. It is built on `pkg/engine/twopc` (the coordinator and its durable log) and
`pkg/comm/sink/txgroup` (the group, which presents to the engine as a single sink).

**Currently only the PostgreSQL sink can participate.** It is the one sink implementing
`hermod.TwoPhaseCommit` with real `PREPARE TRANSACTION` semantics, so the realistic
shape today is two or more Postgres sinks kept consistent with each other. A group
containing a sink that cannot participate is **refused at construction**, not silently
degraded.

### Declaring one

A group is a sink of type `txgroup` whose config names its members. It is a
single node in the DAG, which is what lets the engine drive it through one
writer — every sink otherwise gets its own writer goroutine with its own
batching loop, and independent batches cannot share a transaction boundary.

```json
{
  "type": "txgroup",
  "config": {
    "members": "orders-primary,orders-replica",
    "max_prepared_age": "15m"
  }
}
```

| Field | Meaning |
| :--- | :--- |
| `members` | Comma-separated sink IDs, at least two. Each must implement two-phase commit, or the group is refused at construction. |
| `max_prepared_age` | Optional (default 15m). How long a transaction may sit in doubt before the reaper rolls it back. |

**A durable state store is required.** The coordinator's log has to outlive the
process — a crash with an in-memory log strands prepared transactions with
nothing able to resolve them — so a group refuses to start unless distributed
state (Redis or Etcd) is configured.

On start-up the group preflights every member, resolves anything the previous
run left in doubt, and starts its reaper. A member that cannot genuinely
participate stops the workflow starting rather than failing mid-batch.

### Why it is opt-in

The engine gives every sink its own writer goroutine with its own batching loop. That
independence is why it sustains ~100k msgs/s, and it is also why sinks cannot otherwise
agree on a transaction boundary. A group replaces that independence with a barrier: its
members commit in lockstep at the pace of the slowest, and a member that cannot prepare
aborts the batch for all of them. Use one where a partial write is genuinely worse than
a slower pipeline; leave everything else on the fast path.

### ⚠️ Operational hazard — read before enabling on PostgreSQL

`PREPARE TRANSACTION` leaves a transaction **in doubt**: it holds its locks, keeps its
snapshot, and **survives a server restart** until somebody resolves it. An unresolved
prepared transaction therefore **blocks VACUUM cluster-wide**, which ends in transaction
ID wraparound if it is left long enough. This is a database-availability problem, not an
untidy row.

Hermod defends against that in three places, and you should understand all three before
turning it on:

| Defence | What it does |
| :--- | :--- |
| **Preflight** | At start-up, refuses to run if `max_prepared_transactions = 0` (the PostgreSQL default, which makes `PREPARE` fail) or if the sink is behind a transaction pooler (where a prepared transaction cannot be resolved on the backend that created it). |
| **Recovery** | On every start, resolves transactions left in doubt by the previous run. **Presumed abort**: a transaction commits only if the coordinator's decision was durable before the crash. |
| **Reaper** | Rolls back anything prepared for longer than `MaxPreparedAge` (default 15 minutes). Run it continuously with `Sink.StartReaper(ctx, interval)` — `Recover` only covers restarts, and the case that hurts is a coordinator that prepares, dies, and never comes back. It only ever rolls back: committing on a timer would apply a batch nobody decided to commit. |

**Required PostgreSQL configuration.** `max_prepared_transactions` must be at least the
number of concurrent transactional groups. It defaults to `0` and **changing it requires
a server restart**:

```
max_prepared_transactions = 16   # postgresql.conf, then restart
```

**Do not put a pooler in front of a group member.** PgBouncer in transaction or statement
mode cannot guarantee that the commit lands on the backend that prepared it. Hermod's
preflight refuses this outright rather than degrading, because the degraded behaviour —
committing at prepare time — would leave the coordinator believing it could still roll
back.

**Checking for stranded transactions.** Prepared transactions are visible in
`pg_prepared_xacts`, and this is worth alerting on:

```sql
SELECT gid, prepared, owner, database
FROM pg_prepared_xacts
ORDER BY prepared;
```

Anything older than a few minutes that Hermod is not actively working on should be
resolved. If Hermod's own log was lost — the state store was wiped, say — it cannot
resolve them and you must do it by hand:

```sql
ROLLBACK PREPARED '<gid>';
```

Roll back rather than commit unless you have positively established the transaction
should have committed. Presumed abort is the safe direction for the same reason the
coordinator uses it.

### Verified against a real server

The coordinator's unit tests use fakes, which prove the protocol is implemented
but say nothing about whether PostgreSQL behaves the way it assumes. The
integration tests in `pkg/comm/sink/txgroup` close that gap, asserting against
`pg_prepared_xacts` — the same view an operator would check — rather than
trusting a sink's account of itself:

```bash
HERMOD_INTEGRATION=1 POSTGRES_DSN='postgres://...' go test ./pkg/comm/sink/txgroup/
```

They found two defects that every fake-based test had passed over:

- `PreflightTwoPhaseCommit` read `SHOW max_prepared_transactions`, which returns
  **text**. Scanning it into an int failed on every call — and being a preflight,
  that would have blocked 2PC from ever starting.
- The coordinator never called `Begin`, so a real sink refused to prepare with
  "no active transaction". The fakes accepted `Prepare` unconditionally, so the
  missing phase was invisible.

Both are fixed, and both are now covered by tests that would catch a regression.

### Residual risk, stated plainly

There is a window between a participant's `Prepare` returning and its identifier
reaching the log. A crash inside that window leaves a prepared transaction the
coordinator cannot name. The log is written after **every** vote rather than once at
the end, which narrows the window to a single participant, but it does not close it —
closing it needs the participant to accept a coordinator-supplied transaction ID, which
`hermod.TwoPhaseCommit` does not currently offer. Until then, the `pg_prepared_xacts`
check above is the backstop.

## Connector maturity tiers

Hermod ships 41 source and 45 sink connectors. They are **not equally mature**, and a
list that presents a 2,500-line integration-tested Postgres CDC reader next to an
87-line HTTP poller as equals is not telling you anything useful.

Tiers are assigned on evidence, not intent:

| Tier | Criteria |
| :--- | :--- |
| **GA** | Substantial implementation, unit tests, **and** an integration test that runs against live infrastructure. Suitable for production. |
| **Beta** | Substantial implementation with unit tests, but no live-infrastructure test. Expect to validate against your own environment first. |
| **Experimental** | Thin implementation, no tests, or a known semantic limitation. Suitable for prototyping. Do not put data you cannot lose behind one. |

**Every connector** is covered by the contract suite in `pkg/comm/conformance`,
which checks lifecycle, nil-safety and context-deadline behaviour with no live
infrastructure. That suite proves a connector is not obviously broken; it does
not prove the data path, which is what separates GA from Beta.

The one exception is the `txgroup` sink itself, a composite that wraps other
sinks. It has its own suite and its own PostgreSQL integration tests, which
cover more than the generic contract could.

Connectors needing a collaborator — `batchsql` a `DBProvider`, `form` a
submission store, `grpc` a `.proto` on disk — are constructed against a stub.
That is deliberate: what is being checked is the connector's own lifecycle and
context handling, and the stub is only the seam that lets it be built. Excluding
them left six connectors with no coverage at all in order to avoid a theoretical
weakness in coverage they did not have.

Connectors that address a fixed vendor host — Slack, Discord, Twitter/X, LinkedIn,
Facebook, Instagram, TikTok, Pinecone — expose `SetBaseURL` so they can be pointed
at a test server. Without that seam they dialled the live internet on construction,
which is why they were untested; anything new in that shape should provide it too.

### GA

| Connector | Direction | Evidence |
| :--- | :--- | :--- |
| **PostgreSQL** | source + sink | Logical-replication CDC, 2,572 / 1,448 lines, 13 test files, live-DB integration + bulk-load + idempotency + PgBouncer e2e |
| **MySQL** | source + sink | Live-DB integration tests, idempotency coverage (`MYSQL_DSN`) |
| **SQLite** | source + sink | Local-file engine, tested in-process |
| **File** | source + sink | 1,238-line source with tests; used throughout the e2e suite |
| **RabbitMQ** | source + sink | Queue integration tests against a live broker, both directions: the sink's messages are consumed back off the queue |
| **Redis** | sink | Integration test against a live server |
| **MongoDB** | sink | Live-server data-path tests: documents land, a repeated key does not duplicate, updates replace, deletes remove, batches arrive whole |
| **MongoDB** | source | Live replica-set change-stream tests (`MONGODB_RS_URI`): the resume position advances on acknowledgement rather than on read, unacknowledged messages are redelivered after a restart, the initial load carries existing documents once, and a write during the backfill is not lost between it and the tail |
| **SMTP** | sink | Live send against a mail catcher, read back through its API (`SMTP_HOST` + `SMTP_VERIFY_API`) — an SMTP server accepts a message long before anyone can read it, so a nil return is a weaker claim than it looks. Retry and duplicate-suppression covered too |
| **Kafka** | source + sink | Live-broker round trip (`KAFKA_BROKERS`): a record written by the sink comes back out of the source with its key intact, and acknowledging advances the consumer group's offset so a restart is not handed what was already delivered. **At-least-once only** — see the note under Beta about why it cannot do better |
| **ClickHouse** | sink | Live-server tests (`CLICKHOUSE_ADDR`): an insert lands; a delete in a batch that also inserts does not come back, which it used to; mapped columns insert and delete; and a mapped column name cannot break out of its quoting |
| **MSSQL** | sink | Live-server tests against Azure SQL Edge (`MSSQL_DSN`): insert, upsert on redelivery and delete through the mapped path, and a column name that cannot be quoted is refused by name rather than becoming an empty identifier |
| **Elasticsearch** | sink | Live-server tests (`ELASTICSEARCH_URL`): a document is indexed and deleted, and a document id cannot inject its own actions into the bulk stream |
| **pgvector** | sink | Live-server tests (`PGVECTOR_DSN`): a vector is stored, upserted and deleted, and an identifier needing quotes gets them |
| **S3 / S3-Parquet** | sink | Live MinIO tests (`S3_ENDPOINT`): an object is put, distinct messages land separately, the default key keeps every delivery while the idempotent key does not leave a second object, a batch becomes a Parquet object, and one undecodable message is named rather than wedging the batch |
| **Oracle** | sink | Live-server tests against `gvenzl/oracle-free` (`ORACLE_DSN`, local rather than CI — the image wants ~2GB and a slow first boot): insert, upsert on redelivery and delete through the mapped path; a write into an existing, conventionally-named table; and the identifier guard asserted against the server, where an injected table name is refused and no table of that shape appears. Standing the server up is what found that the sink could not write to an ordinary Oracle table at all — identifiers were quoted PostgreSQL-style, but Oracle folds unquoted names to UPPER case, so every statement failed with `ORA-00904` |
| **Cassandra** | sink | Live-node tests (`CASSANDRA_HOSTS`): a row lands and a delete removes it, and a table name arriving on a message is refused rather than interpolated into CQL. The Cassandra **source** is a different matter — see Experimental |
| **MQTT** | source | Live-broker tests (`MQTT_BROKER`): a published message comes out of Read with payload and topic intact, and a 200-message burst arrives whole — the silent drop-oldest this source once had would have eaten its head |

### Beta

Substantial and unit-tested, but unproven against live infrastructure in CI:

**Sources** — MSSQL, gRPC, WebSocket, HTTP, BatchSQL, Excel.
**Sinks** — Snowflake, HTTP, WebSocket, Failover.

The MSSQL **source** is genuinely a CDC source — it reads `CHANGETABLE` and emits
updates and deletes — so what it lacks is coverage, not capability. The polling
sources that were previously listed here are a different case and have moved to
Experimental: see the inserts-only row below.

**Snowflake** carries one caveat the others do not. Its identifiers are validated
like every other SQL sink's, but no warehouse is reachable from CI, so that guard
is the only one in this repository never watched failing against a real server.
It has tests that run without one — they cover the refusal rather than the
resulting SQL. Oracle used to sit here too; standing a real server up moved it to
GA and found a total-failure bug on the way (see its row above).

**Kafka is GA for its data path but at-least-once only**, and that ceiling is not
a coverage gap. There is no transactional producer — `segmentio/kafka-go` exposes
the wire primitives but nothing on its `Writer` — and Kafka cannot join a
transactional sink group at all, because recovery there must be able to *commit*
a transaction an earlier process prepared and Kafka can only abort one. See
[ROADMAP](./ROADMAP.md). At-least-once with sink-side idempotency is Hermod's
documented guarantee everywhere, so this is the same bargain as the rest of the
platform rather than a Kafka-specific caveat.

**TxGroup** stays here despite having live-PostgreSQL tests that cover more than
most GA entries — commit landing in both tables, and rows staying invisible after
a crash between prepare and recovery. Two things hold it back, and both are
semantic rather than a gap in coverage: PostgreSQL is the only sink implementing
`hermod.TwoPhaseCommit`, so a group can span nothing else; and there is a
documented window in which a crash strands a prepared transaction the coordinator
cannot name, which on PostgreSQL holds locks cluster-wide until someone finds it.
See [Residual risk, stated plainly](#residual-risk-stated-plainly). Neither is a
reason to avoid it — they are reasons to know what you are taking on, which is
what a tier is for.

### Experimental

Thin, untested, or semantically limited. Specific caveats where they matter:

| Connector | Caveat |
| :--- | :--- |
| **Oracle**, **DB2**, **ClickHouse**, **MariaDB**, **YugabyteDB** (sources) | Watermark polling, **not** log-based CDC. Inserts only — updates and deletes are invisible. The limitation is structural rather than a gap: these sources can only construct `OpCreate` and `OpSnapshot`, so there is no code path by which an update or a delete could reach a sink. A row changed after it was read is never re-emitted, and a deleted row is never retracted downstream. **MariaDB is the easiest to be caught by**: it has a binlog and Hermod's MySQL source does read one, so the MariaDB source looks like it should do CDC too. It does not. Use the MySQL source against MariaDB if you need updates and deletes. ClickHouse is listed here despite having live-infrastructure tests, because the tier is set by semantics rather than by coverage. |
| **Cassandra**, **ScyllaDB** (sources) | Inserts only, as in the row above — updates and deletes are invisible. On top of that, CQL cannot `ORDER BY` an arbitrary column, so incremental polling returns an *arbitrary* qualifying row and the cursor can skip rows permanently. Sound **only** when the id field is a clustering column inside a restricted partition. The source logs a warning on first use. The Cassandra **sink** is unaffected and is GA. |
| **SAP** | OData polling client (~180 lines). No IDoc, BAPI or delta queues. OData is SAP's sanctioned direction for third parties after Note 3255746, but it is roughly 10× slower than ODP-RFC for bulk extraction. |
| **Mainframe**, **Dynamics 365**, **ServiceNow**, **Salesforce** | Thin REST/OData clients, no tests. |
| **Social / SaaS** — Slack, Discord, Telegram, Twitter/X, LinkedIn, Facebook, Instagram, TikTok, Google Sheets, Google Analytics, Firebase, FCM | Small API wrappers. Contract-tested, but no data-path coverage. Fine for notifications; not for data of record. |
| **Pinecone**, **Milvus**, **Kinesis**, **Pub/Sub**, **Pulsar**, **FTP**, **generic CDC**, **GraphQL**, **cron**, **webhook**, **form**, **batchsql**, **gRPC** | Minimal implementations. Contract-tested; no data-path coverage. |

GA describes the *data path* — that changes are captured and land correctly. It does
not mean a connector carries existing rows across when a workflow starts: only the
PostgreSQL source does, and only when asked. See
[Maturity, up front](#maturity-up-front).

Moving a connector up a tier means adding the missing evidence, not editing this
table. If you depend on one, an integration test is the most useful contribution you
can make.

## Enterprise Features

Hermod is built for scale and reliability, offering enterprise-grade features out of the box:

- **Two-Phase Commit (2PC)**: Atomic multi-sink delivery via a transactional sink group, with a durable coordinator log, presumed-abort crash recovery and a reaper for transactions left in doubt. Postgres participates; Kafka does not.
- **SSO & OIDC**: Centralized authentication via Okta, Azure AD, and Auth0, with automatic RBAC mapping.
- **Vector Database Sinks**: Optimized connectors for **Pinecone**, **Milvus**, and **pgvector** for AI-driven data pipelines.
- **Hermod CLI**: Full lifecycle management (Lint, Secret, Monitor, GitOps) from the command line.
- **Global Schema Registry**: Centralized management of data contracts with versioning and compatibility checks. Supports JSON Schema, Avro, and Protobuf.
- **WebAssembly (WASM) Transformations**: Run custom business logic at near-native speed. WASM nodes allow you to use Go, Rust, or C++ for complex data processing within the Hermod engine.
- **Adaptive Throughput Control**: The engine automatically monitors processing latency and throttles ingestion if downstream sinks are under pressure or if worker resources are constrained.
- **Transactional interfaces**: Optional `Transactional` / `TwoPhaseCommit` interfaces a sink may implement to support atomic processing patterns. Currently implemented with real prepared-transaction semantics by the Postgres sink only, and not yet driven by a cross-sink coordinator.
- **Granular RBAC**: Role-Based Access Control allowing you to restrict access to Admins (full access), Editors (workflow management), and Viewers (dashboards only).
- **Automated PII Discovery & Masking**: Intelligent sensitive data detection during the transformation phase to ensure compliance with GDPR/HIPAA.
- **Distributed Trace Visualization**: Trace any message's journey through the DAG visually, showing latency and data mutations at every node.
- **Global State Store**: Support for distributed backends like **Redis** and **Etcd** for consistent stateful transformations across worker clusters.
- **AI-Native Transformations**: Cognitive ETL nodes for sentiment analysis, entity extraction, and automated mapping using LLMs.
- **OTLP Native Export**: Integrated support for OpenTelemetry traces and metrics to connect with enterprise observability tools like Datadog and Honeycomb.
- **Transactional Outbox**: Guaranteed message delivery and consistency for SQL-based sinks.

## Data Governance and Schema Validation

Hermod allows you to enforce data quality by validating incoming messages against a schema before they are processed or written to sinks.

Supported formats:
- **JSON Schema**: Standard JSON schema validation.
- **Avro**: Binary-friendly JSON-based schema.
- **Protobuf**: Enforce structure using `.proto` definitions.

Configuration:
1.  Open the **Workflow Panel** (right sidebar) in the Editor.
2.  Go to the **Settings** tab.
3.  Scroll to **Data Governance**.
4.  Select a **Schema Type** and provide the **Schema Definition**.

Messages that fail validation are:
- Logged as errors in the live workflow logs.
- Automatically redirected to the **Dead Letter Sink** (if configured).
- Dropped from the pipeline to prevent downstream corruption.

## Audit Logging

Hermod includes a robust audit logging system that tracks all critical administrative actions.

Tracked actions include:
- Workflow lifecycle (Create, Update, Delete, Start, Stop)
- Source and Sink management
- User authentication and role changes
- Dead Letter Sink draining

Audit logs are stored in the primary database (SQL or MongoDB) and can be viewed by Administrators in the **Audit Logs** page in the dashboard.

## Authentication & Account Security

Hermod hardens the login flow to defend against brute-force attacks and information disclosure:

- **Login Lockout**: After **5 consecutive failed login attempts** for a given username/IP combination, that combination is temporarily locked out for **15 minutes**. Locked requests receive an HTTP `429 Too Many Requests` response with a `Retry-After` header, and the event is recorded in the audit log. A successful login immediately clears the failure counter, and stale counters reset automatically after 15 minutes of inactivity.
- **Sanitized Database Errors**: When Hermod cannot connect to its database, error messages returned to API/UI clients are sanitized to strip database host/IP addresses and ports (e.g. `dial tcp 10.0.0.5:5432: connection refused`). This prevents leaking internal infrastructure details during setup, connectivity tests, and login. Internal database errors during login are surfaced only as a generic `internal server error`.

These protections apply automatically and require no configuration. The relevant thresholds are defined as constants (`MaxLoginAttempts`, `LoginLockoutDuration`) in `internal/api/handlers/login_security.go`.

### Two-Factor Authentication (2FA)

Hermod supports TOTP-based Two-Factor Authentication (compatible with Google Authenticator, 1Password, Authy, etc.).

- Enable 2FA (user self-service):
  1. Start setup to get a temporary secret and provisioning URL (QR):
     - `POST /api/auth/2fa/setup` (authenticated)
     - Response: `{ "secret": "...", "url": "otpauth://totp/..." }`
  2. Scan the QR code (or enter the secret) in your authenticator app and generate a 6‑digit code.
  3. Confirm to enable 2FA permanently:
     - `POST /api/auth/2fa/verify` with body `{ "secret": "...", "code": "123456" }`
  4. On success, the server stores the secret and marks the account as 2FA‑enabled.

- Disable 2FA:
  - `POST /api/auth/2fa/disable` (authenticated)

- Login flow with 2FA enabled:
  1. Submit username and password:
     - `POST /api/login` → returns `{ "two_factor_required": true, "user_id": "...", "pending_token": "..." }` when 2FA is enabled.
  2. Complete login by submitting the 6‑digit TOTP code with the pending token:
     - `POST /api/auth/2fa/login` with `{ "user_id": "...", "pending_token": "...", "code": "123456" }`
     - On success, the API returns a session `token` and sets the `hermod_session` cookie.

- First-time enrollment during login (when 2FA is enabled but not registered yet):
  - If an administrator enabled 2FA for a user without completing registration, password login will respond with
    `{ "two_factor_enroll_required": true, "user_id": "...", "pending_token": "..." }`.
  - Use the pending token to enroll without a session:
    1. Start enrollment and get a secret + provisioning URL (QR):
       - `POST /api/auth/2fa/setup/pending` with `{ "user_id": "...", "pending_token": "..." }`
       - Response: `{ "secret": "...", "url": "otpauth://..." }`
       - The login page renders the `url` as a **scannable QR code** (generated locally in the browser) so it can be added by scanning with any authenticator app — Google Authenticator, Authy, Microsoft Authenticator, Apple/iOS Passwords, etc. The plaintext `secret` is also shown as a manual-entry fallback.
    2. Verify and complete enrollment (also completes login):
       - `POST /api/auth/2fa/verify/pending` with `{ "user_id": "...", "pending_token": "...", "secret": "...", "code": "123456" }`
       - On success, the server persists the secret, issues a session token, and sets the `hermod_session` cookie.

Notes:
- 2FA secrets are never returned after verification; responses and cookies omit the secret.
- Audit logs record 2FA enable/disable and successful 2FA logins.
- The `HERMOD_JWT_SECRET` must be set (or present in `~/.hermod/db_config.yaml`) for token issuance.
- The pre-auth endpoints `/api/auth/2fa/login`, `/api/auth/2fa/setup/pending`, and `/api/auth/2fa/verify/pending` do **not** require a session cookie: they are authenticated solely by the short-lived signed `pending_token` issued by `/api/login`. They are intentionally exempt from the session-auth middleware so the OTP-challenge and first-time enrollment steps can complete before a session exists. The UI also avoids redirecting to `/login` on a `401` from these endpoints (e.g. a wrong code), so users can retry without being bounced out.

## Reliability and Data Loss Prevention

Hermod is designed to minimize data loss during operation and shutdown:

- **Graceful Draining**: During shutdown, Hermod drains its internal buffer to ensure all messages already read from the source reach the sink.
- **At-Least-Once Delivery**: The engine acknowledges messages to the source only after they have been successfully written to the sink. This ensures that if the process crashes, the source can re-deliver unacknowledged messages upon restart (depending on source implementation).
- **Retries with Backoff**: Failed writes to the sink are automatically retried with configurable exponential backoff.
- **Circuit Breaker Pattern**: Prevents cascading failures when downstream systems are unhealthy. Automatically opens after N consecutive failures and probes recovery in a half-open state.
- **Adaptive Batching**: Dynamically groups messages to optimize sink throughput and reduce network roundtrips.
- **Memory Safety**: Uses a bounded `RingBuffer` to prevent out-of-memory issues under high pressure.

**Important Note**: Since the default `RingBuffer` is in-memory, sudden process termination (e.g., `SIGKILL` or power failure) can result in the loss of messages currently held in the buffer. For use cases requiring absolute durability, consider implementing a persistent `Producer`/`Consumer` (buffer) interface (e.g., using a file-backed queue or a dedicated message broker).

### Dead Letter Sink (DLQ) Prioritization

In high-reliability scenarios, some messages might fail to be written to the primary sink even after all retry attempts. Hermod can redirect these messages to a **Dead Letter Sink**.

If you want to ensure that historical failures are processed before new data (e.g., during recovery after a downstream outage), enable **DLQ Prioritization**:

1.  **Configure a Dead Letter Sink**: Assign a Sink (e.g., a Postgres table) to the workflow's `dead_letter_sink_id`.
2.  **Enable Prioritize DLQ**: Set `prioritize_dlq: true` in the workflow configuration.
3.  **Automatic Recovery**: When the workflow starts, Hermod will first attempt to "drain" all messages from the Dead Letter Sink before switching to the primary source stream.

**Note**: The Sink assigned as a DLQ must also implement the `hermod.Source` interface (e.g., Postgres, MySQL, NATS, Kafka).

A sample template for this configuration is available at `examples/templates/reliability_recovery_dlq.json`.

## PostgreSQL CDC: Replication Slots & Publications

When using a PostgreSQL source in CDC mode, Hermod relies on a logical **replication slot** and a **publication**. The Source form now helps you reuse existing objects instead of guessing names:

- **Discovery**: Opening the **Slot Name** or **Publication** dropdown queries the connected database (`pg_replication_slots`, `pg_publication`, `pg_publication_tables`) and lists what already exists. Publications that already cover your configured tables are highlighted.
- **Choose or Create**: Pick an existing slot/publication from the list, or type a new name — Hermod will automatically create it on startup if it does not exist.
- **Defaults**: If both fields are left blank, Hermod falls back to the safe defaults `hermod_slot` and `hermod_pub`.

This is backed by the API endpoint `POST /api/sources/discover/replication`, which accepts `{ "type": "postgres", "config": { ... } }` and returns the available `slots` and `publications`.

> **Requirement**: The database must have `wal_level = logical` and the connecting user must have replication privileges for slot creation to succeed.

## Workflow Versioning & Rollback

Every time you save a workflow, Hermod automatically creates an immutable version in the database. This provides a complete audit trail and enables safe, rapid recovery:

- **Immutable History**: View all previous versions of a workflow, including the author, timestamp, and a summary of changes.
- **One-Click Rollback**: Instantly revert a production workflow to any previous stable version via the **History** tab in the Workflow Detail page.
- **GitOps Readiness**: Versioning ensures that workflow configurations can be managed as code and safely promoted across environments.

## Distributed State & Coordination

For large-scale, high-availability deployments, Hermod supports distributed backends for state management and worker coordination:

- **Global State Stores**: Native support for **Redis** and **Etcd** to store workflow state (e.g., aggregation counters, windowed buffers). This ensures consistency when workflows migrate between workers.
- **Worker Leases**: Distributed coordination ensures that each workflow is processed by exactly one worker instance at a time, preventing processing overlaps.
- **Hash-based Sharding**: Automatically and transparently balances workflows across all available worker instances in a cluster.

## Workflow Blueprints & Templates

Hermod provides a library of pre-built "Blueprints" to jumpstart common data integration patterns. These can be imported with a single click and customized to your needs.

Examples include:
- **CDC to Elasticsearch**: Real-time synchronization of database changes to a search index.
- **API Aggregator**: Consolidate data from multiple external APIs into a single stream.
- **GDPR Masking & Routing**: Automatically redact PII and route high-value data to specialized sinks.

Browse the available templates in the `examples/templates/` directory or directly via the **Import Template** button in the Workflow Dashboard.

## Real-time Data Streams (SSE)

Hermod includes a built-in **SSE (Server-Sent Events) Sink** that allows you to stream data directly to web applications or any SSE-compatible client. This is ideal for real-time dashboards, live notifications, or reactive data orchestration without the complexity of a full message broker.

### Security Features
- **Authentication**: Secure your streams with an Auth Token. Clients must provide the token via the `Authorization: Bearer <token>` header or the `token` query parameter.
- **Origin Verification**: Restrict access to specific web origins (CORS) to prevent unauthorized websites from subscribing to your data streams.
- **Interactive Integration Guide**: The Hermod UI provides a real-time, tailored JavaScript snippet for each SSE Sink, including security parameters, to simplify client-side integration.

### Consuming the Stream

Clients can subscribe to a specific stream using the following endpoint:
`GET /streams/sse?stream={stream_name}&token={optional_token}`

The endpoint is separated from the management API to ensure data isolation and high throughput.

### Sample Client

A complete HTML/JavaScript sample demonstrating how to consume an SSE stream is available in the `examples/sse-sink/` directory.

To use it:
1.  Configure an **SSE Sink** in your workflow with a custom stream name and optional security settings.
2.  Open `examples/sse-sink/index.html` in your browser.
3.  Enter the stream name and connect to see live data.

## Observability

Hermod provides built-in Prometheus metrics to monitor your data pipelines. Metrics are exposed via the `/metrics` endpoint on the API server.

Key Metrics:
- `hermod_engine_messages_processed_total`: Total messages successfully processed.
- `hermod_engine_messages_filtered_total`: Messages dropped by filters.
- `hermod_engine_message_errors_total`: Processing errors categorized by stage (read, transform, sink).
- `hermod_engine_sink_writes_total`: Successful writes per sink.
- `hermod_engine_sink_write_errors_total`: Failed writes per sink.
- `hermod_engine_processing_duration_seconds`: End-to-end processing latency.
- `hermod_engine_dead_letter_total`: Messages sent to the Dead Letter Sink.

Workflow Metrics:
- `hermod_workflow_node_processed_total`: Number of messages processed by a specific workflow node.
- `hermod_workflow_node_errors_total`: Number of errors encountered in a specific workflow node.

Worker Metrics:
- `hermod_worker_sync_duration_seconds`: Time taken for a worker synchronization cycle.
- `hermod_worker_active_workflows_total`: Number of active workflows currently managed by the worker.
- `hermod_worker_sync_errors_total`: Total number of worker synchronization errors or workflow start failures.
- `hermod_worker_admission_rejected_total`: Workflows not started because the worker was over its
  CPU/memory admission threshold, labelled by `reason` (`cpu` or `memory`).

### Tracing — following one record end to end

Traces are exported over OTLP when it is configured (see `OTLPConfig`); nothing is emitted
otherwise. A record produces one trace:

```
source.receive          ← where it entered, one per message
└─ RunWorkflowNode      ← one per node it passes through
   └─ sink.write        ← one per sink write
```

**The trace context travels on the message, not in a `context.Context`.** The read loop and
the sink writers are different goroutines joined by a buffer, so a Go context cannot reach
from one to the other. It is carried in message metadata under the W3C `traceparent` key —
which means a sink that forwards metadata as headers or attributes (Pub/Sub does) propagates
the trace to whatever consumes it next, with no Hermod-specific handling required.

A record that arrives without a `traceparent` starts a new trace rather than failing.
Tracing is diagnostics, and must never be the reason a record does not move.

**Batch writes use links, not a parent.** The messages in one batch were read separately and
each carries its own trace, so `sink.write_batch` links to all of them. Promoting one to
parent would claim the batch belonged to that record's trace and orphan every other one.

### Alerting on silent failures

Each of these has a procedure in [`RUNBOOK.md`](./RUNBOOK.md), along with key
rotation, backup and restore, replication-slot WAL retention, and what is known
*not* to be covered.

Most pipeline problems announce themselves as errors. These four do not — they lose or withhold data
while every status stays green — so each one needs an alert rather than a dashboard.

| Metric | What a non-zero value means | Action |
| :--- | :--- | :--- |
| `hermod_message_over_releases_total` | A message was released after its reference count already reached zero, so it returned to the pool while still in use and another message overwrote it. Symptom is duplicated *and* lost payloads with the totals still balancing. | **Page.** This is a code defect, never a capacity issue. Capture the workflow topology and open a bug; the `TestMain` guards in `internal/engine/registry` and `pkg/engine` should have caught it before release. |
| `hermod_engine_messages_dropped_no_target_total` | Messages were acknowledged to the source and then delivered nowhere, because a workflow that has sinks resolved none of them. They are not in a dead-letter queue. | **Page.** Check sink reachability and the workflow's edges. Data already acknowledged is unrecoverable from the source. |
| `hermod_worker_admission_rejected_total` | The worker is shedding load: it is refusing to start new workflows because the host is above its threshold. Affected workflows simply never start. | Investigate host load. The reading is host-wide, so on a shared machine this can fire on load Hermod does not own — raise `HERMOD_ADMISSION_CPU_THRESHOLD` / `HERMOD_ADMISSION_MEM_THRESHOLD`, or give the worker a dedicated node. |
| `hermod_engine_sub_source_backoff_total` | One source inside a multi-source workflow is failing and has been backed off. Its siblings keep streaming, so the workflow still reports healthy while that source delivers nothing. | Check that source's connectivity. Sustained growth means it is not recovering; the backoff caps at 5s between attempts. |
| `hermod_sink_unmapped_field_total` | A message carried a field the sink's column mappings do not cover, so it was not written. Usually the source grew a column: the destination has quietly stopped matching it, and every status stays green. | Add the field to the sink's column mappings, or confirm the omission is intended. Labelled by `table` and `field`, and counted once per field per sink rather than per message — the value is which field, not how many rows. |
| `hermod_txgroup_in_doubt` | A transactional sink group has prepared transactions it has not resolved. On PostgreSQL each one holds locks and blocks `VACUUM` **cluster-wide** — not just for that table, and not just for Hermod — for as long as it lasts. | **Page if it stays above zero.** Brief non-zero values are normal mid-commit. Sustained ones mean the reaper is not resolving them; check the group's members are reachable and cross-check `SELECT * FROM pg_prepared_xacts` on the destination. Republished on every sweep, so it clears on its own once resolved. |
| `hermod_txgroup_reaped_total` | The reaper rolled back transactions left in doubt past their deadline. The backstop working as designed — and evidence something upstream failed to resolve its own transaction. | Not itself an emergency, but growth means commits are being abandoned rather than completed. Look for coordinator crashes or unreachable members around each increment. |

### Schema evolution — when a source grows a column

A CDC source picks up whatever the upstream table has, so adding a column there starts
sending it without anything in Hermod being reconfigured. What happens next depends on how
the sink is configured, and both behaviours are deliberate:

- **Without column mappings**, a SQL sink writes `(id, data)` with the message as a JSON
  document, so the new field arrives on its own. Nothing to do.
- **With column mappings**, the sink writes exactly the columns it was told about. The new
  field is not written — a mapping is a statement about which fields matter — but it is
  reported through `hermod_sink_unmapped_field_total` and a log line naming the field, so
  the loss is visible rather than silent.

Neither mode alters the destination's schema in response to a message. Automatic column
addition exists only as `sync_columns`, which reconciles the table to the *configured
mappings* at start-up, not to what arrives at runtime.

Admission control knobs (both default to `0.85`; set to `1` or higher to disable that dimension):

```bash
HERMOD_ADMISSION_CPU_THRESHOLD=0.85   # refuse new workflows above this host CPU fraction
HERMOD_ADMISSION_MEM_THRESHOLD=0.85   # refuse new workflows above this host memory fraction
```

### Rebalancing and failback

When workers compete for a workflow, the current owner gets a weight bonus so workflows do not flap
between workers on small load differences — every move is a stop and a restart, which for a CDC
source means tearing down a replication connection.

The trade-off is that rebalancing *by load* is slow. At the default `2.0`, a saturated worker still
retains roughly 199 keys in 200 against a completely idle peer, so a worker that has just recovered
may reclaim very little for a long time. **Failback after a worker dies is unaffected and complete**
— a dead owner stops being a candidate at all, and its workflows move immediately.

```bash
HERMOD_LEASE_HYSTERESIS=2.0   # incumbent's weight multiplier; 1.0 disables the bonus
```

Lower it towards `1.0` to trade stability for faster load-following. Measured on a two-worker
cluster with one idle and one saturated worker, an idle worker reclaims 1 key in 200 at `2.0` and
192 in 200 at `1.0` (`TestLeaseHysteresisIsConfigurable`).

### Delivery guarantee

Delivery is **at-least-once**: a message is acknowledged to its source only after every sink write
succeeds, so a crash or an abrupt stop replays whatever was not acknowledged. Duplicates are
therefore possible and expected.

What makes that **exactly-once as observed at the destination** is the sink's upsert. Every SQL sink
writes with `ON CONFLICT` / `ON DUPLICATE KEY` / `MERGE`, keyed on the message id, so a redelivered
message overwrites its own row rather than adding one.

That holds on one condition: **the source must supply a stable identity.** The key is taken from,
in order:

1. `metadata["idempotency_key"]` — set this when the source knows its own natural key (a CDC row's
   primary key, an order number);
2. the message id;
3. a generated UUID, if neither is present.

A message that reaches case 3 gets a *different* key on every delivery, so its duplicates cannot be
collapsed — two deliveries of an identity-less message are genuinely indistinguishable. If a source
of yours produces messages without an id, set `idempotency_key` in a transformation before the sink.

Sinks that are not upserts (webhook, SMTP, queues) deduplicate only where they say so; SMTP computes
its own key from the message and recipient.

**S3 keeps every delivery by default.** Its object key carries a timestamp, so a redelivery lands
beside the delivery before it rather than on top of it. That is what an archive wants — successive
CDC updates to one row share a message id, and keying on the id alone would keep only the newest —
but it does mean a retry leaves a duplicate object, and retries are the mechanism at-least-once
works by. Set `idempotent_key: "true"` on the sink to key on the message id instead, which gives
the upsert behaviour the guarantee above describes, at the cost of keeping only the latest version
of each record.

### Deploying on Kubernetes

A container image and a Helm chart ship with each release:

```bash
helm install hermod oci://ghcr.io/gsoultan/charts/hermod \
  --version <release> \
  --set existingSecret=hermod-master-key \
  --set metrics.prometheusRule.enabled=true
```

Or from a checkout: `helm install hermod deploy/helm/hermod`.

The chart wires the pieces this document describes and that are easy to get
subtly wrong: `/livez` as the liveness probe and `/readyz` as readiness (never
the other way round — pointing liveness at `/readyz` restarts every pod during a
database blip instead of merely routing away from them), a
`terminationGracePeriodSeconds` that exceeds `HERMOD_SHUTDOWN_TIMEOUT`, the
crypto master key as a Secret, and a volume for `db_config.yaml`.

It **refuses to render** four configurations that install cleanly and then lose
data, rather than letting you find out during a rolling restart:

| Refused | Why |
| :--- | :--- |
| `terminationGracePeriodSeconds` ≤ `shutdownTimeout` | Kubernetes kills the pod mid-drain; everything taken from a source and not yet written is discarded. |
| Both `masterKey` and `existingSecret` | Ambiguous which key encrypts stored credentials. |
| `replicaCount > 1` with no shared database | Each replica keeps its own workflows, leases and users, so they cannot coordinate. |
| `replicaCount > 1` with a ReadWriteOnce volume | Replicas after the first stay unschedulable. |

The image is distroless and runs as uid 65532 with a read-only root filesystem.
Hermod serves plain HTTP and has no TLS listener of its own; terminate TLS at
the ingress.

`/metrics` is open by default, as a scrape target normally is — requiring a
session cookie would break every scraper. The metrics do carry `workflow_id`,
`source_id` and `worker_id` labels though, so an unauthenticated read maps the
deployment: how many pipelines exist, what they are called, and which are
failing. Set `HERMOD_METRICS_TOKEN` (or `metrics.token` in the chart, which
wires the ServiceMonitor to match) to require `Authorization: Bearer <token>`.
The health probes stay open either way — a token covering them would make the
kubelet fail every probe and restart the pod on a loop. Either way, keep the
Service `ClusterIP` rather than behind a public LoadBalancer.

Enable `metrics.prometheusRule.enabled` to install the four alerts in
[Alerting on silent failures](#alerting-on-silent-failures). Two of them page.

### Credential encryption and master key rotation

Connector credentials — passwords, API keys, DSNs, GCP service-account documents — are encrypted
with AES-256-GCM before they reach the metadata database, and decrypted on read. Which values count
as credentials is decided by shape rather than by an exact list (`internal/storage/configsecrets`),
so a connector added later with an `smtp_password` is covered without anyone remembering to add it.
Non-credentials that merely look like one — `s3_key` is an object path, `routing_key` is routing —
are listed explicitly as exceptions.

**Set a master key.** Without one, Hermod encrypts with a constant that is published in this
repository, which protects nobody. Set `crypto_master_key` in `db_config.yaml`, or rotate through
`PUT /api/config/crypto` (Admin only, minimum 16 characters).

**Rotation re-encrypts before it switches.** `PUT /api/config/crypto` rewrites every stored
credential under the new key first, in one transaction, and installs the key only once that has
committed. If anything cannot be re-encrypted the request fails and *nothing* is changed — including
the case where a value is already unreadable from an earlier bad rotation, because re-encrypting it
would mean writing back a blank and destroying a credential that the right key could still recover.

A value that cannot be decrypted reads back empty, never as raw ciphertext, and logs which key
failed. Handing ciphertext to a driver as though it were the password is what previously turned a
key mistake into unexplained authentication failures against the *destination* database.

Upgrades are safe: key derivation changed from truncate-and-zero-pad to SHA-256, and `Decrypt` falls
back to the old derivation, so existing data still opens and moves forward as it is rewritten.

### Backup and restore

`GET /api/backup/export` and `POST /api/backup/import` (both Admin only) carry sources, sinks,
workflows, vhosts, workspaces and notification settings.

The export contains **decrypted credentials in plaintext** — necessarily, since a backup that cannot
restore a credential is not a backup, and the target instance may have a different master key. Treat
the file as a secret: it is every credential in the deployment in one document.

Both endpoints fail loudly rather than quietly. An export that cannot read the database returns an
error instead of downloading an empty file with a plausible name, and refuses rather than truncating
if the deployment holds more than 1000 objects of any one kind. A restore reports which objects could
not be written instead of always answering 204; it still attempts every object, because recovering
most of a configuration beats stopping at the first bad row.

### Shutdown budget — must fit inside your orchestrator's grace period

On SIGTERM, Hermod stops accepting new work and then *drains*: it finishes writing the messages it
has already taken from its sources and acknowledges them, so they are neither lost nor replayed.
That takes time, and if the orchestrator kills the process partway through, the undelivered
remainder is discarded — the drain protects nothing.

Every shutdown stage derives from one total, so the stages always nest:

| Stage | Share of total | Default | What it bounds |
| :--- | :--- | :--- | :--- |
| Total | 100% | 25s | The whole stop, including closing storage |
| PerEngine | 80% | 20s | Stopping one workflow, and `StopAll` across all of them |
| Drain | 55% | ~13s | Sink writes once shutdown has begun |
| Grace | 20% | 5s | Writers unwinding after the drain budget expires |

```bash
HERMOD_SHUTDOWN_TIMEOUT=25s   # keep below terminationGracePeriodSeconds
```

**The default is 25s because Kubernetes' default `terminationGracePeriodSeconds` is 30s.** If you
raise `HERMOD_SHUTDOWN_TIMEOUT`, raise the grace period to match — and leave a few seconds of
margin, because the kubelet's timer starts before the signal is delivered and the process still has
to close its database handles after the engines stop:

```yaml
spec:
  terminationGracePeriodSeconds: 60   # must exceed HERMOD_SHUTDOWN_TIMEOUT
  containers:
    - name: hermod
      env:
        - name: HERMOD_SHUTDOWN_TIMEOUT
          value: "45s"
```

A per-sink `drain_timeout` larger than the budget is clamped to it rather than allowed to overrun
the process-wide deadline. `TestShutdownBudgetStagesNest` and
`TestShutdownDefaultFitsKubernetesGracePeriod` hold both properties.

## Performance Tuning Guide

This section summarizes practical knobs to keep Hermod lightweight (low CPU/RAM) while maintaining throughput and reliability.

Engine flags and settings:

- `engine.max_inflight` (default: 128)
  - Caps the number of in‑flight messages across the pipeline to bound memory.
  - Increase for faster sinks, decrease for small instances with tight RSS limits.
  - **Must be ≥ your largest sink `batch_size`.** A batch fills from in‑flight messages, so a
    `batch_size` above `max_inflight` can never complete on count and every flush waits out
    `batch_timeout` instead — measured at 2,557 msgs/s versus 110,829 msgs/s for the same workload.
    Hermod now clamps the effective batch size to `max_inflight` and logs a warning naming both
    values; raise `max_inflight` if you want the larger batch to take effect. See
    [BENCHMARKS.md](BENCHMARKS.md).
- `engine.drain_timeout` (default: 10s)
  - Logs a warning if sink writers take longer than this to drain on shutdown. Set `0` to wait indefinitely.
- `prioritize_dlq` (per‑workflow)
  - When enabled and a DLQ sink is present, Hermod drains DLQ first before consuming the primary source to avoid starvation of historical failures.

Sink batching and backpressure (per sink):

- `batch_size`, `batch_timeout`, `batch_bytes`
  - Batch flush triggers on count OR bytes OR timeout — tune to balance latency and throughput.
  - Typical starters: `batch_size: 100–128`, `batch_bytes: 1_048_576 (1MB)`, `batch_timeout: 100–250ms`.
  - Keep `batch_size` ≤ `engine.max_inflight` (default 128). If you want 200–500, raise
    `max_inflight` to match — otherwise the batch is clamped. Measured best throughput is at
    `batch_size: 100` (124,877 msgs/s); see [BENCHMARKS.md](BENCHMARKS.md).
- Backpressure buffer and strategy
  - `backpressure_buffer`: bounded channel size (e.g., 1000–5000)
  - `backpressure_strategy`: `block` | `drop_oldest` | `drop_newest` | `sampling` | `spill_to_disk`
  - Prefer `block` (default) unless you need lossy behavior under overload.

Ordered concurrency via sharding (per sink):

- `shard_count`: number of internal worker shards per sink writer (e.g., 4–16)
- `shard_key_meta`: metadata key used to shard (falls back to `Message.ID()`)
  - Guarantees per‑key ordering while parallelizing independent keys.

#### High Availability and Failover
Hermod workers are designed for high availability. When a worker is gracefully shut down:
- It releases all active workflow leases.
- It deregisters itself from the cluster.
- Other workers automatically detect the change and re-shard the workflows.
- Workflow execution moves to the remaining workers "smoothly" with minimal interruption.
- For optimal failover speed, you can configure the worker cache TTL.

Idempotency store hygiene (SMTP / SQLite helper):

- `enable_idempotency: true`
- `idempotency_dsn: ~/.hermod/hermod.db`
- `idempotency_namespace: <string>` → isolates keys into a dedicated table (e.g., `smtp_idempotency_marketing`).
- `idempotency_ttl: 72h` → hourly cleanup of stale keys keeps the store fast and small.

Database pooling defaults:

- Non‑SQLite: `MaxOpenConns=20`, `MaxIdleConns=10`, `ConnMaxIdleTime=60s`
- SQLite (embedded): `MaxOpenConns=4`, `MaxIdleConns=1` (WAL mode recommended)

Logging and profiling:

- `HERMOD_LOG_SAMPLE_N=5` → sample warn/error logs (keep 1/n) to reduce noisy hotspots.
- `HERMOD_PPROF=true` → enables `/debug/pprof/*` endpoints for on‑the‑fly CPU/heap profiling under load.

OpenTelemetry (OTEL):

- The engine emits spans for `sink.write` and `sink.write_batch` with attributes `workflow_id`, `sink_id`, `message_id`/`batch_size`.
- Configure OTEL exporter in your environment to collect traces.

Suggested starting targets:

- Idle worker: < 80 MB RSS, ~0% CPU. *(target — not yet benchmarked)*
- Fast sink (e.g., Kafka/NATS): 5–20k msgs/s with `max_inflight=128`, `batch_size=100–128`, p95 < 50ms.
- Postgres sink: **measured 58k–100k rows/s** for insert-only batches of 1k–5k rows via the COPY
  fast path, versus 3–6k rows/s on the ordered per-message path. See [BENCHMARKS.md](BENCHMARKS.md).

### Bulk-load fast path (Postgres)

Insert-only batches are streamed into a TEMP staging table with `COPY` and merged in a single
`INSERT … SELECT … ON CONFLICT`, giving **10–16× the throughput** of the per-message path.

This is automatic and requires no configuration, but it is deliberately conservative: a batch takes
the fast path only when it is insert-only, targets a single table, has column mappings, uses no
soft-delete strategy, and contains at least 50 rows. **Any CDC batch that mixes inserts, updates or
deletes stays on the ordered path**, because the order of a delete followed by an insert on the same
key is observable and must be preserved.

## Benchmarks

Measured baselines live in **[BENCHMARKS.md](BENCHMARKS.md)**, which also documents the host they
were taken on and how to reproduce them. Headline numbers (Apple M5 Pro, in-memory source and sink,
so this is engine overhead only — network and disk cost are excluded):

| Benchmark | Result |
|---|---|
| Engine throughput, 1 KB payload | **118,285 msgs/s** |
| Engine throughput, 64 B payload | 101,249 msgs/s |
| Engine throughput, 16 KB payload | 45,456 msgs/s |
| Message pool `AcquireRelease` | 76.67 ns/op, 0 allocs/op |
| Unpooled equivalent | 184.6 ns/op, 7 allocs/op |

The engine core sustains ~100k msgs/s, so it is not the bottleneck in a Hermod pipeline — sinks
are. Direct tuning effort at the sink.

Numbers not yet backed by a benchmark are marked *(target)* above and listed under "Not yet
measured" in BENCHMARKS.md.

## Health and Readiness Probes

Hermod exposes production-friendly health endpoints on the API server:

- GET /livez — liveness probe. Always 200 once the server is up.
- GET /readyz — readiness probe (v1 schema). Performs bounded checks and returns JSON with per-component status and durations. Only database connectivity failures are gating (HTTP 503). Registry and Workers checks are informational (non-gating) in v1.

Example response:

```
{
  "version": "v1",
  "status": "ok",
  "time": "2026-01-22T20:00:00Z",
  "checks": {
    "db": { "ok": true, "duration_ms": 3 },
    "registry": { "ok": true, "engines_running": 2, "duration_ms": 1 },
    "workers": { "ok": true, "recent": 2, "stale": 0, "ttl_seconds": 60, "duration_ms": 2 }
  }
}
```

Prometheus metrics:

- hermod_readiness_status{component="db|registry|workers"} (gauge: 1=ok, 0=error)
- hermod_readiness_latency_seconds{component="..."} (histogram)

Kubernetes probes (example):

```yaml
readinessProbe:
  httpGet:
    path: /readyz
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 5
  timeoutSeconds: 2
  failureThreshold: 3
livenessProbe:
  httpGet:
    path: /livez
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 10
```

Note: In v1, only the DB check gates readiness. Future versions will incorporate workflow ownership/leases into readiness once lease-based coordination is enabled.


## Idempotency and Exactly-Once Effects (Sink-Side)

Hermod processes messages with at-least-once delivery. To avoid duplicates at sinks, idempotency is implemented end-to-end:

- Engine ensures each message carries a stable idempotency key (defaults to message ID). Metrics are emitted for present/missing keys.
- SQL sinks (Postgres/MySQL/MariaDB) perform UPSERT semantics on the `id` primary key:
  - Postgres/Yugabyte: `INSERT ... ON CONFLICT (id) DO UPDATE SET data = EXCLUDED.data`
  - MySQL/MariaDB: `INSERT ... ON DUPLICATE KEY UPDATE data = VALUES(data)`
- Elasticsearch sink performs UPSERT by using the message `id` as the document `_id`.
- SQLite sink uses `INSERT OR REPLACE` into a table with `id TEXT PRIMARY KEY`.
- Redis sink deduplicates with `SETNX` using a configurable TTL and namespace; duplicates are skipped.

Environment variables:

- HERMOD_IDEMPOTENCY_REQUIRED=true — log warnings when idempotency keys are missing.
- HERMOD_IDEMPOTENCY_TTL=24h — TTL for Redis dedupe keys.
- HERMOD_IDEMPOTENCY_NAMESPACE=hermod:idemp — prefix for Redis keys.

## Modern API Sources (GraphQL & gRPC)

Hermod supports modern API protocols as data sources, allowing you to push data directly into Hermod instead of relying solely on CDC or webhooks.

### GraphQL Source

Hermod exposes a GraphQL endpoint at `/api/graphql/{path}`. You can send standard GraphQL POST requests:

```json
{
  "query": "mutation { publish(table: \"orders\", payload: \"...\") }",
  "variables": {}
}
```

The entire request body is captured as the message payload, and if it's a valid GraphQL JSON, the `query` and `variables` are extracted into the message data.

### gRPC Source

Hermod runs a gRPC server (default port `50051`) that implements the `SourceService`.

**Service Definition:**
```proto
service SourceService {
  rpc Publish(PublishRequest) returns (PublishResponse);
}
```

You can push structured messages directly from your gRPC clients. Use the `path` field in the request to route to a specific Hermod gRPC source configuration.

## Advanced Transformation Nodes

Beyond simple mapping and filtering, Hermod supports complex business logic within the pipeline:

- **WebAssembly (WASM)**: Execute logic compiled from Go, Rust, or C++ at near-native speed. Ideal for CPU-intensive transformations or proprietary algorithms.
- **Lua Scripting**: Embed lightweight, flexible scripts for dynamic data manipulation without external dependencies.
- **PII Masking**: Automatically discover and redact sensitive information (Credit Cards, Emails, SSNs) using a built-in regex-based scanner.
- **Stateful Aggregations**: Maintain running totals, counts, or windowed averages directly in the stream.
- **Database/API Lookups**: Enrich incoming messages by querying external databases or HTTP APIs in real-time.

### SQL Query Builder

The DB Lookup transformation and SQL sources/sinks share an interactive SQL Query Builder in the Workflow Editor (`ui/src/components/forms/SQLQueryBuilder.tsx`). It is optimized for writing and editing long queries:

- **Database Explorer**: Load tables, drill into columns (with types), and filter the table list. Click a table/column or the `+` icon to insert its name at the caret.
- **Caret-aware Quick Insert**: Keyword shortcuts (`SELECT`, `FROM`, `WHERE`, `AND`, `OR`, `JOIN`, `LEFT/INNER JOIN`, `GROUP BY`, `ORDER BY`, `HAVING`, `LIMIT`, `OFFSET`, `DISTINCT`) and the dynamic `{{.last_value}}` variable are inserted at the current cursor position instead of appended at the end.
- **Format Query**: A one-click formatter normalizes whitespace and breaks major clauses onto their own lines for readability.
- **Fullscreen Editor**: Expand the editor into a large modal for comfortable editing of long, multi-line statements.
- **Convenience**: Copy the query to the clipboard, see a live character/line counter, and run with `Cmd/Ctrl + Enter`.

## Leases and Single-Worker Ownership

Workers acquire per-workflow leases backed by storage to ensure only one worker processes a workflow at a time. Key details:

- Schema fields: `owner_id`, `lease_until` on workflows.
- Worker behavior: acquire (steal if expired), renew at TTL/2, stop engine on renew failure, release on stop.
- Metrics: `hermod_lease_acquire_total`, `hermod_lease_steal_total`, `hermod_lease_renew_errors_total`, and `hermod_worker_leases_owned_total`.
- Readiness: `/readyz` includes a non-gating `leases` check. Make it gating with `HERMOD_READY_LEASES_REQUIRED=true`.

## Security Headers and CORS

Production defaults are secure by default:

- CORS allowlist via `HERMOD_CORS_ALLOW_ORIGINS` (comma-separated). In production, no allowlist -> no CORS.
- Security headers: `Content-Security-Policy`, `X-Frame-Options=DENY`, `Referrer-Policy=no-referrer`, `X-Content-Type-Options=nosniff`.
- HSTS can be forced with `HERMOD_HSTS_ENABLE=true` or when `X-Forwarded-Proto: https` is detected.
- Worker registration requires `X-Worker-Registration-Token` when `HERMOD_ENV=production`. Provide the secret via `HERMOD_WORKER_REG_TOKEN`.
- **UI Build & Deployment**:
  - **Development**: If `HERMOD_ENV` is not `production`, Hermod automatically builds the UI on startup if the `ui/` source exists. This requires `bun`, `curl`, and `unzip`.
  - **Production**: Set `HERMOD_ENV=production`. Hermod will skip runtime builds and serve assets from the embedded filesystem. Build the UI during CI/CD using `go run ./cmd/hermod --build-ui` before the final `go build`.

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

## Settings UI Improvements (Current)

- Settings → Database now pre-fills from the backend via a new admin-only endpoint:
  - GET /api/config/database → returns `{ type, conn }`. For non-SQLite DSNs, passwords are masked in the returned connection string.
- Notification Settings page includes a "Send Test Notification" button:
  - POST /api/settings/test (admin-only) sends a test through configured channels in this order: Email → Slack → Discord → Webhook → Telegram. The UI displays per-channel results.

These endpoints require an administrator role and are used by the UI automatically. No additional configuration is needed beyond saving your settings.

## Roadmap

For a detailed view of planned features and future development ideas, please refer to the [ROADMAP.md](ROADMAP.md) file.

## Contributing & Documentation

- Always update `README.md` when you add a new feature or change user‑visible behavior.
  - Include a brief description, how to enable/use it, and any relevant config flags, env vars, API endpoints, or UI locations.
  - If the UI is affected, verify the UI builds (`cd ui && bun run build`) or run the quick verify script on Windows: `pwsh -File scripts/quick-verify.ps1`.
  - Keep examples up to date; add a short note under the relevant section rather than creating separate long docs.