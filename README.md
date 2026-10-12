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
[Connector maturity tiers](docs/connectors.md#connector-maturity-tiers). Read that table before picking
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
[Delivery guarantee](docs/reliability.md#delivery-guarantee).

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

- **Two-Phase Commit (2PC) across a transactional sink group**: A group of sinks commits atomically — either every member applied the batch or none did — and the guarantee survives a crash, because the coordinator's decision is durable before any member is told about it. Recovery uses **presumed abort**, and a reaper rolls back anything left in doubt past a deadline, which is what keeps an interrupted round from holding PostgreSQL locks indefinitely. Scope, stated plainly: **the PostgreSQL (and YugabyteDB) sink and the MySQL (and MariaDB) sink can participate**, so a group can keep PostgreSQL and MySQL destinations consistent with each other; MySQL needs 5.7.7+ and MariaDB 10.5.2+. A group containing a sink that cannot take part is refused at construction rather than silently degraded. Kafka does *not* participate — it previously carried no-op stubs that would have reported a failed rollback as a success. See [Distributed transactions](docs/transactional-sink-groups.md#distributed-transactions-transactional-sink-groups), including the operational hazard note, before enabling.
- **SSO & OpenID Connect (OIDC)**: Support for centralized identity providers like **Okta**, **Auth0**, and **Azure AD** for platform-wide authentication and RBAC.
- **AI automation**: Prompt, extract, classify, embed and retrieve with Claude, ChatGPT, Gemini, DeepSeek, Ollama or any OpenAI-compatible model; an **AI Agent** node whose writes wait for human approval; an **MCP server** so Claude and other MCP clients can run Hermod workflows; run history with replay. See [AI automation](docs/ai-automation.md).
- **Data lineage & impact**: One graph of the tables, topics, files, endpoints and ML models each vhost's workflows read and write, built from their saved configuration. Pick a source to see every workflow and asset downstream of it — what breaks if it changes. Scoped to the vhosts you may see. See [Data lineage](docs/lineage.md).
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
- **Field-Level Encryption**: `encrypt` and `decrypt` transformations seal named fields, so sensitive columns stay unreadable in the sink and are recovered only where a downstream pipeline is configured with the same key. Fourteen algorithms are selectable — AES-128/192/256 in GCM, CBC, CTR and CFB, plus ChaCha20-Poly1305 and XChaCha20-Poly1305 — with AES-256-GCM the default. Keys can be a hashed passphrase (the default), raw/hex/base64 bytes, or derived with PBKDF2 or scrypt, and the payload written as base64, base64url or hex. Each value gets a fresh random nonce, so an encrypted field cannot be used as a join key; in the default envelope format values are written as `enc:v1:…`/`enc:v2:…` and already-encrypted values are left alone, making a re-run idempotent. A `raw` format drops the envelope so a column encrypted by **another system** can be read and written — see [Decrypting data Hermod did not encrypt](docs/encryption.md#decrypting-data-hermod-did-not-encrypt). Decryption failures fail the message by default (`onError` can relax this to `skip` or `null`). The key is configured on the node, which means it is stored with the workflow definition.
- **Audit Logging**: Complete history of administrative changes and system events for security and compliance audits.
- **At-least-once delivery with exactly-once *effects* at the sink**: Messages are acknowledged only after successful delivery, using the **Transactional Outbox** pattern for SQL sources. Combined with sink-side idempotency keys (see [Idempotency and Exactly-Once Effects](docs/reliability.md#idempotency-and-exactly-once-effects-sink-side)), a duplicate delivery does not produce a duplicate row. This is *not* end-to-end exactly-once delivery: the transport is at-least-once and duplicates are suppressed where they land, which is the same guarantee most of this category actually ships.
- **Sequential Control Flow**: Explicitly chain sinks and transformations sequentially. Supports "Sinks as Transformers" by returning data from a sink back into the workflow pipeline.
- **Stateful Event Correlation (Join/Zip)**: Wait for and join messages from multiple sources based on a common key before downstream delivery.
- **Circuit Breaker & Failure Recovery**: Protect downstream systems with a built-in Circuit Breaker node. Automatically routes messages to failure branches when error thresholds are exceeded.
- **Interactive Workflow Debugger**: Real-time message pausing, stepping, and inspection directly from the UI. Pause messages at any node to inspect payload state and resume processing when ready.
- **Visual Lineage with Data Diffs**: Enhanced message tracing with "Before and After" snapshots for every transformation node in the DAG. Visually debug exactly how data is mutated at each step.
- **AIOps & Self-Healing Optimization**: AI-driven performance tuning that automatically adjusts concurrency, batch sizes, and retry policies based on real-time throughput and error patterns.
- **Intelligent Data Quality Alerts**: Automated detection of data quality drift using the `governance.Scorer`. Alerts trigger when schema adherence or DQ scores drift from historical averages.
- **Global Mesh Governance (experimental)**: A cluster registry and an HTTP forwarding sink, letting one Hermod instance route messages to another. Scope check: it is roughly 320 lines, `Failover` marks a cluster's status and logs rather than redirecting traffic, and there are no health checks, retries or partition handling. Treat it as a foundation, not a control plane.
- **WASM & Blueprint Marketplace**: Discover, version, and share custom WebAssembly transformers and Go-based workflow blueprints via an integrated Marketplace.
- **Oracle CDC with LogMiner**: Set the Oracle source's change capture method to **LogMiner** and it reads inserts, updates and deletes from the redo logs, with before and after images, resuming from the last acknowledged SCN after a restart. It needs archive logging and supplemental logging; see [Oracle CDC](docs/oracle-cdc.md). The default method is still **incremental polling**, which tracks a monotonically increasing key and so sees **inserts only**. Query construction for polling goes through `sqlutil.BuildIncrementalQuery`, which applies each dialect's row limit *after* the sort (Oracle's `ROWNUM` is evaluated before `ORDER BY`, so it is applied to an ordered subquery — getting this wrong silently skips rows).
- **IBM DB2 source (no driver in this build)**: The DB2 source opens the `go_ibm_db` driver, which needs CGO and IBM's native client library. Release builds use `CGO_ENABLED=0` and include neither, so a DB2 source cannot connect.
- **Advanced Database Mapping & Schema Discovery**: Production-ready column mapping for all major databases (**Postgres**, **MySQL**, **MSSQL**, **Oracle**, **Snowflake**, **MongoDB**, etc.). Supports automatic table creation, identity/auto-increment columns, and "Smart Mapping" from both source and sink schemas.
- **Resource-Aware Sharding**: Advanced worker sharding using Rendezvous hashing weighted by real-time CPU/Memory metrics. Ensures optimal workload distribution and prevents workflow flapping with built-in hysteresis.
- **AI-Native Transformations**: Integrated "Cognitive ETL" nodes for **AI Enrichment** and **AI Mapping**, supporting OpenAI and local Ollama models. Includes **AI Mapping Suggestions** that automatically propose fixes for schema mismatches.
- **Auto-Parallelization & Node-Level Backpressure**: The engine automatically detects independent branches in your DAG and executes them in parallel. Built-in node-level backpressure ensures that slow sinks or transformations don't block healthy execution paths.
- **Adaptive Throughput Control**: The engine automatically monitors processing latency and throttles ingestion if downstream sinks are under pressure or if worker resources are constrained.
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

## Install

The current release is **`v1.2.0`**. See [CHANGELOG.md](CHANGELOG.md) for what
changed, what breaks, and the known gaps.

- **Pin an explicit version** for both the image and the chart. It is what makes
  a deployment reproducible, and it is what the examples below do.
- **Go consumers are still unserved.** `v1.2.0` falls inside the retracted range
  `[v1.0.0, v1.8.0]`, exactly as `v1.0.0` does, so `go get` will not select it.
  See [`go get` does not work, and is not expected
  to](#go-get-does-not-work-and-is-not-expected-to) — nothing changed here, the
  release line simply continues below the range.

### Container image

```bash
docker pull ghcr.io/gsoultan/hermod:1.2.0
docker run --rm ghcr.io/gsoultan/hermod:1.2.0 -version
# hermod v1.2.0 (Enterprise Edition)
```

`linux/amd64` and `linux/arm64` are both published under that one tag.

### Helm

```bash
helm install hermod oci://ghcr.io/gsoultan/charts/hermod \
  --version 1.2.0 \
  --set existingSecret=hermod-master-key
```

### Binaries

`.deb`, `.rpm`, `.apk` and tarballs for Linux `amd64`/`arm64` are attached to
the [release](https://github.com/gsoultan/hermod/releases).

### From source

```bash
git clone https://github.com/gsoultan/hermod && cd hermod
go build ./cmd/hermod
```

Go 1.27 or later is required.

### `go get` does not work, and is not expected to

> `go get github.com/gsoultan/hermod` and
> `go install github.com/gsoultan/hermod/cmd/hermod@…` **fail at this version.**
> Use the image, the chart, a packaged binary, or a checkout.
>
> Every version the Go module proxy can serve for this path is withdrawn and
> retracted — the range `[v1.0.0, v1.8.0]` plus `v1.0.0-rc.1` by name. No
> tombstone tag carries that block any more; it lives in `go.mod` on main, so
> every release tag cut from main carries it. The proxy is immutable, so the
> releases deleted in this reset still
> answer there — and `v1.0.0` in particular is permanently bound to a commit
> from February whose `go.mod` declared `github.com/user/hermod`, a path
> matching no repository. Re-tagging cannot displace it.
>
> Nothing is lost that previously worked: the module path was a placeholder
> until 2026-09-02, so no published release was ever importable. Restarting the
> numbering at `1.0.0` was chosen over a `go get` path that has never
> functioned. It starts working once the line passes `v1.7.4`.

---

## Development Quick Start

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

## Documentation

The README is the overview. Each topic has its own page under [`docs/`](docs/):

- [Connectors](docs/connectors.md): Which connectors are production-ready, and the evidence behind each tier.
- [Using Hermod](docs/usage.md): The command-line client, embedding Hermod as a Go library, running the API and workers, supported platforms, and the admin settings endpoints.
- [Workflows](docs/workflows.md): Control flow, fan-out, versioning and rollback, workflows as MCP tools, blueprints, and the advanced transformation nodes.
- [Reliability and delivery guarantees](docs/reliability.md): What Hermod does to avoid losing data, the delivery guarantee it makes, and sink-side idempotency.
- [Distributed transactions](docs/transactional-sink-groups.md): Transactional sink groups: committing one batch to several sinks atomically.
- [PostgreSQL CDC](docs/postgres-cdc.md): Replication slots and publications.
- [API sources and streams](docs/api-sources.md): GraphQL, webhook and gRPC sources, synchronous responses, and the real-time SSE stream.
- [Schema registry and data governance](docs/schema-registry.md): Schema validation and Confluent Schema Registry interop.
- [Security](docs/security.md): Authentication, two-factor sign-in, audit logging, security headers and CORS.
- [Encryption](docs/encryption.md): Credential encryption, master key rotation, and the field-level encrypt and decrypt transformations.
- [Observability](docs/observability.md): Metrics, tracing, alerting, schema evolution, rebalancing, and the health and readiness probes.
- [Deploying and running Hermod](docs/deployment.md): Production settings, Kubernetes, worker coordination, backup and shutdown.
- [Performance tuning](docs/performance.md): Tuning guide, the Postgres bulk-load fast path, and benchmarks.
- [Developing Hermod](docs/development.md): Running the integration and UI tests, and what CI runs.
- [Data lineage and impact](docs/lineage.md): The lineage graph and the downstream impact of changing a source.
- [Oracle CDC with LogMiner](docs/oracle-cdc.md): Setting up log-based change capture from Oracle.
- [AI automation](docs/ai-automation.md): The language-model nodes, the agent, retrieval, the MCP server and the workflow builder.
- [Machine learning](docs/ml.md): Training models on your data and calling them from workflows, REST and gRPC.
- [Expression functions](docs/expression-functions.md): The functions you can call in expressions, conditions and templates.
- [Structural nodes](docs/structural-nodes.md): Nodes that reshape a record, parse text inside it, enrich it from a file, or do geometry on it.

Also at the top level: [BENCHMARKS.md](BENCHMARKS.md), [RUNBOOK.md](RUNBOOK.md),
[ROADMAP.md](ROADMAP.md) and [CHANGELOG.md](CHANGELOG.md).

## Roadmap

For a detailed view of planned features and future development ideas, please refer to the [ROADMAP.md](ROADMAP.md) file.

## Contributing & Documentation

- Always update the documentation when you add a new feature or change user‑visible behavior: the
  page under `docs/` that covers the topic, and the feature list in `README.md` if the feature is new.
  - Include a brief description, how to enable/use it, and any relevant config flags, env vars, API endpoints, or UI locations.
  - If the UI is affected, verify the UI builds (`cd ui && bun run build`) or run the quick verify script on Windows: `pwsh -File scripts/quick-verify.ps1`.
  - Keep examples up to date. Add to the page that covers the topic; a new page is for a new topic,
    and gets a line in the [Documentation](#documentation) list.
