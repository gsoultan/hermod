# Performance tuning

Tuning guide, the Postgres bulk-load fast path, and benchmarks.

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
    [BENCHMARKS.md](../BENCHMARKS.md).
- `HERMOD_LOG_LEVEL` (default: `info`)
  - Set to `debug` to keep DEBUG lines. **This is a storage knob, not only a verbosity one.**
    A successful sink write logs at DEBUG, and that is the single most frequent event the
    platform produces — at DEBUG the database logger persists one `logs` row per message, and
    the engine also measures each message's payload to report its size, which for a message
    carrying a data map means marshalling it to JSON.
  - Leave it unset in production. Turn it on for one workflow while debugging, not as a default.
  - `HERMOD_DB_LOG_SAMPLE_RATE` thins what reaches the log table when you do need DEBUG on.
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
    `batch_size: 100` (124,877 msgs/s); see [BENCHMARKS.md](../BENCHMARKS.md).
- Backpressure buffer and strategy
  - `backpressure_buffer`: bounded channel size (e.g., 1000–5000)
  - `backpressure_strategy`: `block` | `drop_oldest` | `drop_newest` | `sampling` | `spill_to_disk`
  - Prefer `block` (default) unless you need lossy behavior under overload.

#### Ordering

Hermod orders messages **per row**, end to end. A source that knows its row
identity stamps `_hermod_order_key` (for PostgreSQL CDC: `schema.table` plus the
replica-identity columns, on both the initial load and the stream). The engine
pins a key to one processing worker and one sink shard, so two changes to the
same row reach the sink in the order the source produced them, while different
rows run fully in parallel.

Messages with no ordering key — a queue message, a cron tick, a batch row — have
no row order to keep and are spread across workers as before.

A table with no primary key or `REPLICA IDENTITY NOTHING` cannot identify a row,
so its changes carry no key and are not ordered against each other. Set a replica
identity if you need that ordering.

- `shard_count`: number of internal worker shards per sink writer (e.g., 4–16).
  Off by default. Sharding splits the sink's queue, so each shard batches
  independently — a `batch_size` of 100 across 8 shards forms batches of ~12 or
  waits out `batch_timeout`. Turn it on for sink parallelism, not for ordering:
  ordering already holds without it.
- `shard_key_meta`: metadata key used to shard. Defaults to the row key
  (`_hermod_order_key`) when sharding is enabled; name your own only for a source
  that carries a different notion of "same entity".

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
  fast path, versus 3–6k rows/s on the ordered per-message path. See [BENCHMARKS.md](../BENCHMARKS.md).

### Bulk-load fast path (Postgres)

Insert-only batches are streamed into a TEMP staging table with `COPY` and merged in a single
`INSERT … SELECT … ON CONFLICT`, giving **10–16× the throughput** of the per-message path.

This is automatic and requires no configuration, but it is deliberately conservative: a batch takes
the fast path only when it is insert-only, targets a single table, has column mappings, uses no
soft-delete strategy, and contains at least 50 rows. **Any CDC batch that mixes inserts, updates or
deletes stays on the ordered path**, because the order of a delete followed by an insert on the same
key is observable and must be preserved.

## Benchmarks

Measured baselines live in **[BENCHMARKS.md](../BENCHMARKS.md)**, which also documents the host they
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
