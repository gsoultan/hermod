# Observability

Metrics, tracing, alerting, schema evolution, rebalancing, and the health and readiness probes.

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

Each of these has a procedure in [`RUNBOOK.md`](../RUNBOOK.md), along with key
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
