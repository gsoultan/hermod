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

**Initial load is available for PostgreSQL, MySQL, MongoDB and Oracle (LogMiner), and
off by default.** Set `initial_load: "true"` on one of those CDC sources and the rows
already in the watched tables are carried across before streaming begins. How each
source pins the boundary between backfill and stream, and what that means for
duplicates, is in [Initial load](docs/initial-load.md).

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
- **Redis Lookup and Redis Set**: A Redis Lookup step enriches a record from a key, and Redis Set, as a sink or as a step in the middle of a flow, writes one: `SET` of a value or `HSET` of one field or a whole object, with templated keys, an optional TTL and, for `SET`, only-if-absent. A key whose template has no value on the record is refused rather than written to one shared key, and a batch goes out in one `MULTI`.
- **Enterprise Secret Management**: Securely resolve credentials from **HashiCorp Vault**, **AWS Secrets Manager**, and **Azure Key Vault** using the `secret:key` prefix.
- **Role-Based Access Control (RBAC)**: Granular permissions for Administrators, Editors, and Viewers, including VHost-level isolation.
- **Visual workflow editor**: A flow's read-only view and its editor share one layout, on a desktop and on a phone, where the flow is drawn top to bottom and the actions sit in a bottom bar. While a flow runs, both show it live: each step card carries its counts, errors, problems such as stuck or circuit open, and the newest record it passed on, and the lines move only while messages cross them. The **Tuning** tab shows the engine's real throughput, error rate, backpressure (the fullest sink buffer) and latency, with a table per sink and per step, and says when the flow is off instead of showing zeros.
- **Message Tracing & Visual Journey**: Visualize the exact path of a single message through the DAG, including latency and data mutations at each step. The trace list and the journey scroll independently, and on a phone they are shown one at a time at full width.
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
- **Previews that do not write**: Simulate, Run Preview and the SQL builder's preview run Execute SQL, DB Lookup and SCD steps inside one database transaction that is rolled back when the preview ends, so pressing a button in the editor never changes a real table. Statements that would escape the rollback (`COMMIT`, several statements, DDL on databases that commit it implicitly) are refused in a preview, and on ClickHouse, which cannot roll back, a preview runs reads only. See [Previews run in a rolled-back sandbox](docs/workflows.md#previews-run-in-a-rolled-back-sandbox).
- **Visual Lineage with Data Diffs**: Enhanced message tracing with "Before and After" snapshots for every transformation node in the DAG. Visually debug exactly how data is mutated at each step.
- **Adaptive batch tuning**: A rule-based optimizer raises a sink's batch size and batch timeout when its buffer is under pressure and lowers the batch size again when it runs empty. The AI pass of the optimizer is **off by default**: left on, it would send a failing flow's status, payload samples included, to a language model every cycle.
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
- **Self-Healing "Safe Mode" & Anomaly Detection**: Automatically detects processing anomalies (e.g., latency spikes) and can enter a "Safe Mode" during critical failures, keeping failed messages on the Dead Letters page to preserve data and maintain system stability.
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

```bash
docker pull ghcr.io/gsoultan/hermod:1.23.0
```

A Helm chart, Linux packages, building from source and `go get` are covered in
[Installing Hermod](docs/install.md). See [CHANGELOG.md](CHANGELOG.md) for what changed
in each release.

---

## Development Quick Start

```bash
./scripts/dev.sh
```

That brings up Postgres, the API, a worker and the UI, and completes first-run setup.
Open **http://localhost:5175** and sign in with **`admin` / `admin`**. The options, the
ports, and managing the database container are in
[Developing Hermod](docs/development.md#running-hermod-locally).

## Documentation

The README is the overview. Each topic has its own page under [`docs/`](docs/):

- [Installing Hermod](docs/install.md): The container image, the Helm chart, packages, building from source, and `go get`.
- [Connectors](docs/connectors.md): Which connectors are production-ready, the evidence behind each tier, and every connector `type` the factory can build.
- [Using Hermod](docs/usage.md): The command-line client, embedding Hermod as a Go library, running the API and workers, supported platforms, and the admin settings endpoints.
- [Workflows](docs/workflows.md): Control flow, fan-out, versioning and rollback, workflows as MCP tools, blueprints, and the advanced transformation nodes.
- [Environments](docs/environments.md): Linking a staging install to production and promoting workflows between them, with approval and rollback.
- [Reliability and delivery guarantees](docs/reliability.md): What Hermod does to avoid losing data, the delivery guarantee it makes, and sink-side idempotency.
- [Distributed transactions](docs/transactional-sink-groups.md): Transactional sink groups: committing one batch to several sinks atomically.
- [Initial load](docs/initial-load.md): Carrying the rows already in a table across before streaming starts.
- [PostgreSQL CDC](docs/postgres-cdc.md): Replication slots and publications.
- [API sources and streams](docs/api-sources.md): GraphQL, webhook and gRPC sources, synchronous responses, and the real-time SSE stream.
- [Schema registry and data governance](docs/schema-registry.md): Schema validation and Confluent Schema Registry interop.
- [Security](docs/security.md): Authentication, two-factor sign-in, audit logging, security headers and CORS.
- [Encryption](docs/encryption.md): Credential encryption, master key rotation, and the field-level encrypt and decrypt transformations.
- [Observability](docs/observability.md): Metrics, tracing, alerting, schema evolution, rebalancing, and the health and readiness probes.
- [Deploying and running Hermod](docs/deployment.md): Production settings, Kubernetes, worker coordination, backup and shutdown.
- [Performance tuning](docs/performance.md): Tuning guide, the Postgres bulk-load fast path, and benchmarks.
- [Developing Hermod](docs/development.md): The local dev stack, running the integration and UI tests, and what CI runs.
- [Data lineage and impact](docs/lineage.md): The lineage graph and the downstream impact of changing a source.
- [Oracle CDC with LogMiner](docs/oracle-cdc.md): Setting up log-based change capture from Oracle.
- [AI automation](docs/ai-automation.md): The language-model nodes, the agent, retrieval, the MCP server and the workflow builder.
- [Machine learning](docs/ml.md): Training models on your data, connecting models served by mimir-server, KServe, Triton, MLServer or MLflow, and calling them from workflows, REST and gRPC.
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
- When a change alters how a part of Hermod works, update the matching note in
  `.serena/memories/` and its line in `.serena/memories/core.md`, the index, so the next
  reader starts from the current design rather than the one before it.
