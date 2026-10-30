# Deploying and running Hermod

Production settings, Kubernetes, worker coordination, backup and shutdown.

Running separate staging and production installs? See [Environments](environments.md) for linking
them and promoting workflows from one to the other.

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

## Distributed State & Coordination

For large-scale, high-availability deployments, Hermod supports distributed backends for state management and worker coordination:

- **Global State Stores**: Native support for **Redis** and **Etcd** to store workflow state (e.g., aggregation counters, windowed buffers). This ensures consistency when workflows migrate between workers.
- **Worker Leases**: Distributed coordination ensures that each workflow is processed by exactly one worker instance at a time, preventing processing overlaps.
- **Hash-based Sharding**: Automatically and transparently balances workflows across all available worker instances in a cluster.

## Deploying on Kubernetes

A container image and a Helm chart ship with each release:

```bash
helm install hermod oci://ghcr.io/gsoultan/charts/hermod \
  --version 1.21.0 \
  --set existingSecret=hermod-master-key \
  --set metrics.prometheusRule.enabled=true
```

`--version` is not optional while the current release is a candidate: without
it Helm reports `could not locate a version matching provided version string`.

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
[Alerting on silent failures](observability.md#alerting-on-silent-failures). Two of them page.

## Backup and restore

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

## Shutdown budget — must fit inside your orchestrator's grace period

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

## Leases and Single-Worker Ownership

Workers acquire per-workflow leases backed by storage to ensure only one worker processes a workflow at a time. Key details:

- Schema fields: `owner_id`, `lease_until` on workflows.
- Worker behavior: acquire (steal if expired), renew at TTL/2, stop engine on renew failure, release on stop.
- Metrics: `hermod_lease_acquire_total`, `hermod_lease_steal_total`, `hermod_lease_renew_errors_total`, and `hermod_worker_leases_owned_total`.
- Readiness: `/readyz` includes a non-gating `leases` check. Make it gating with `HERMOD_READY_LEASES_REQUIRED=true`.
