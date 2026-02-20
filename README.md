# Hermod: Enterprise-Grade Data Integration & Streaming Platform

Hermod is a high-performance, distributed data integration platform designed to bridge the gap between various data sources and sinks. Built with Go and React, it provides mission-critical features for data governance, reliability, and real-time observability.

---

## Enterprise Data Platform Features

Hermod is built for mission-critical enterprise data workloads, providing robust features for governance, reliability, and observability:

- **Two-Phase Commit (2PC)**: Guaranteed atomic delivery for mission-critical sinks like **Postgres** and **Kafka**, ensuring no data loss or duplication in distributed environments.
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
- **Exactly-Once Semantics (EOS)**: Guarantees 100% data consistency using the **Transactional Outbox** pattern for SQL sources, ensuring messages are only acknowledged after successful delivery.
- **Advanced Database Mapping & Schema Discovery**: Production-ready column mapping for all major databases (**Postgres**, **MySQL**, **MSSQL**, **Oracle**, **Snowflake**, **MongoDB**, etc.). Supports automatic table creation, identity/auto-increment columns, and "Smart Mapping" from both source and sink schemas.
- **Resource-Aware Sharding**: Advanced worker sharding using Rendezvous hashing weighted by real-time CPU/Memory metrics. Ensures optimal workload distribution and prevents workflow flapping with built-in hysteresis.
- **AI-Native Transformations**: Integrated "Cognitive ETL" nodes for **AI Enrichment** and **AI Mapping**, supporting OpenAI and local Ollama models.
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

### As an Application

Hermod can be run as a standalone application. By default, it starts in **API Mode**, which includes a web-based management platform for configuring sources, sinks, and engines.

1. Run the application:
   ```bash
   go run cmd/hermod/main.go
   ```

   This will automatically build the UI (if not already built) and start the Go backend. The UI assets are served from the binary (or disk in dev mode).

   If you want to force a UI rebuild:
   ```bash
   go run cmd/hermod/main.go --build-ui
   ```

The UI will be available at `http://localhost:4000`.

### Multi-Platform Support

Hermod is compiled for high performance and supports the following platforms and architectures:

- **OS**: Linux (Ubuntu, Debian, RedHat, Alpine), macOS, Windows, FreeBSD, OpenBSD, NetBSD.
- **Architecture**: `amd64`, `arm64`.

You can download the latest binaries and packages (`.deb`, `.rpm`, `.apk`) from the [GitHub Releases](https://github.com/user/hermod/releases) page.

#### API Mode (Default)
   To start Hermod in API mode (which also serves the UI):
   ```bash
   go run cmd/hermod/main.go
   ```

   The UI will be available at `http://localhost:4000`.

   You can customize the port and database for storing state:
   ```bash
   go run cmd/hermod/main.go --port=8080 --db-type=sqlite --db-conn=hermod.db
   ```

   Hermod supports multiple databases for storing its state (Sources, Sinks, Workflows):
   - **SQLite**: `--db-type=sqlite --db-conn=hermod.db`
   - **PostgreSQL**: `--db-type=postgres --db-conn="postgres://user:pass@localhost:5432/hermod?sslmode=disable"`
   - **MySQL/MariaDB**: `--db-type=mysql --db-conn="user:pass@tcp(localhost:3306)/hermod"`

   When running in API mode, Hermod saves its database configuration to `db_config.yaml` after the first successful setup or when updated via the UI. Subsequent starts will automatically use this configuration.

   #### Standalone Mode
   In Standalone mode, both the API/UI and a worker are started in the same process:
   ```bash
   go run cmd/hermod/main.go --mode=standalone
   ```

   #### Worker Scaling and Sharding
   Hermod supports horizontal scaling of workers. You can run multiple worker processes that share the same platform and automatically shard connections between them.

   To start a worker-only process connected to the platform:
   ```bash
   go run cmd/hermod/main.go --mode=worker --platform-url="http://localhost:4000" --worker-id=0 --total-workers=2
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
      go run cmd/hermod/main.go --mode=worker --worker-guid="my-server-1" --platform-url="http://localhost:4000"
      ```

   #### Worker Self-Registration
   Instead of manually registering a worker in the UI, you can let the worker register itself upon its first run by providing additional flags:

   ```bash
   go run cmd/hermod/main.go --mode=worker --worker-guid="my-server-1" --platform-url="http://localhost:4000" --worker-host="192.168.1.10"
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

   Example using env vars:
   ```bash
   export HERMOD_MODE=worker
   export HERMOD_PLATFORM_URL=http://localhost:4000
   export HERMOD_WORKER_GUID=my-server-1
   export HERMOD_WORKER_TOKEN=secret-token
   hermod
   ```

   #### Background OS Service Integration

   Hermod can be integrated with OS-level service managers (systemd on Linux, Windows Services, macOS Launchd) directly from the binary. This ensures that Hermod starts automatically on boot and can be managed using standard system tools.

   **Service commands:**
   - `hermod --service install` - Install the service (requires administrative privileges).
   - `hermod --service uninstall` - Remove the service.
   - `hermod --service start` - Start the service.
   - `hermod --service stop` - Stop the service.
   - `hermod --service restart` - Restart the service.
   - `hermod --service status` - Check the current service status.

   **Examples: Install as a service**
   ```bash
   # Windows (Run PowerShell as Administrator) — Worker mode
   hermod.exe --mode=worker --platform-url="http://localhost:4000" --worker-guid="worker-1" --service install
   hermod.exe --service start

   # Linux (systemd) — Worker mode
   ./hermod --mode=worker --platform-url="http://localhost:4000" --worker-guid="worker-1" --service install
   ./hermod --service start

   # Windows — Standalone (API + Worker in one process)
   hermod.exe --mode=standalone --service install
   hermod.exe --service start

   # Linux — Standalone
   ./hermod --mode=standalone --service install
   ./hermod --service start
   ```

   Or use the helper scripts provided in `scripts/`:
   ```bash
   # Windows (PowerShell)
   pwsh -File scripts/install-service.ps1 -Mode standalone

   # Linux
   bash scripts/install-service.sh standalone
   ```

   The service will be configured to run with all the flags provided during the `install` command (except for `--service` itself`).

## Production Considerations

- **Logging**: Hermod uses a `Logger` interface and provides a default implementation using `zerolog` for zero-allocation structured logging. You can provide your own implementation via `eng.SetLogger(myLogger)`.
- **Retries**: The `Engine` automatically retries failed `Sink.Write` operations. Configure this via `eng.SetConfig`.
- **Health Checks**: Sources and Sinks implement a `Ping` method. The `Engine` performs pre-flight checks using `Ping` before starting.
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

Default is 15000 ms. WAL mode and other safe pragmas are enabled by default.

## Enterprise Features

Hermod is built for scale and reliability, offering enterprise-grade features out of the box:

- **Two-Phase Commit (2PC)**: Support for atomic multi-sink delivery, ensuring that data is either committed to both the internal state and external systems (Kafka, Postgres) or not at all.
- **SSO & OIDC**: Centralized authentication via Okta, Azure AD, and Auth0, with automatic RBAC mapping.
- **Vector Database Sinks**: Optimized connectors for **Pinecone**, **Milvus**, and **pgvector** for AI-driven data pipelines.
- **Hermod CLI**: Full lifecycle management (Lint, Secret, Monitor, GitOps) from the command line.
- **Global Schema Registry**: Centralized management of data contracts with versioning and compatibility checks. Supports JSON Schema, Avro, and Protobuf.
- **WebAssembly (WASM) Transformations**: Run custom business logic at near-native speed. WASM nodes allow you to use Go, Rust, or C++ for complex data processing within the Hermod engine.
- **Adaptive Throughput Control**: The engine automatically monitors processing latency and throttles ingestion if downstream sinks are under pressure or if worker resources are constrained.
- **Exactly-Once Semantics (EOS)**: Infrastructure for atomic processing patterns using Transactional interfaces, minimizing message duplication in high-integrity use cases.
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

## Performance Tuning Guide

This section summarizes practical knobs to keep Hermod lightweight (low CPU/RAM) while maintaining throughput and reliability.

Engine flags and settings:

- `engine.max_inflight` (default: 128)
  - Caps the number of in‑flight messages across the pipeline to bound memory.
  - Increase for faster sinks, decrease for small instances with tight RSS limits.
- `engine.drain_timeout` (default: 10s)
  - Logs a warning if sink writers take longer than this to drain on shutdown. Set `0` to wait indefinitely.
- `prioritize_dlq` (per‑workflow)
  - When enabled and a DLQ sink is present, Hermod drains DLQ first before consuming the primary source to avoid starvation of historical failures.

Sink batching and backpressure (per sink):

- `batch_size`, `batch_timeout`, `batch_bytes`
  - Batch flush triggers on count OR bytes OR timeout — tune to balance latency and throughput.
  - Typical starters: `batch_size: 200–500`, `batch_bytes: 1_048_576 (1MB)`, `batch_timeout: 100–250ms`.
- Backpressure buffer and strategy
  - `backpressure_buffer`: bounded channel size (e.g., 1000–5000)
  - `backpressure_strategy`: `block` | `drop_oldest` | `drop_newest` | `sampling` | `spill_to_disk`
  - Prefer `block` (default) unless you need lossy behavior under overload.

Ordered concurrency via sharding (per sink):

- `shard_count`: number of internal worker shards per sink writer (e.g., 4–16)
- `shard_key_meta`: metadata key used to shard (falls back to `Message.ID()`)
  - Guarantees per‑key ordering while parallelizing independent keys.

Idempotency store hygiene (SMTP / SQLite helper):

- `enable_idempotency: true`
- `idempotency_dsn: hermod.db`
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

- Idle worker: < 80 MB RSS, ~0% CPU.
- Fast sink (e.g., Kafka/NATS): 5–20k msgs/s with `max_inflight=128`, `batch_size=200–500`, p95 < 50ms.
- SQL sink: 1–5k rows/s with 200–500 row batches, p95 < 200ms.

## Benchmarks

```
BenchmarkAcquireRelease-12      56807960                22.36 ns/op            0 B/op          0 allocs/op
```

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

## Running Integration Tests

Hermod ships with env-gated integration tests.

- Two-worker lease failover (no external deps):
  - Set `HERMOD_INTEGRATION=1`
  - Run: `go test ./internal/engine -run TwoWorkerLeaseFailover -v -tags=integration`
- SQL sink idempotency:
  - MySQL: set `HERMOD_INTEGRATION=1` and `MYSQL_DSN` (e.g., `user:pass@tcp(host:3306)/dbname`)
  - Postgres: set `HERMOD_INTEGRATION=1` and `POSTGRES_DSN` (e.g., `postgres://user:pass@host:5432/db?sslmode=disable`)
  - Run: `go test ./pkg/sink/mysql -tags=integration` and `go test ./pkg/sink/postgres -tags=integration`

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

## Contributing & Documentation

- Always update `README.md` when you add a new feature or change user‑visible behavior.
  - Include a brief description, how to enable/use it, and any relevant config flags, env vars, API endpoints, or UI locations.
  - If the UI is affected, verify the UI builds (`cd ui && bun run build`) or run the quick verify script on Windows: `pwsh -File scripts/quick-verify.ps1`.
  - Keep examples up to date; add a short note under the relevant section rather than creating separate long docs.