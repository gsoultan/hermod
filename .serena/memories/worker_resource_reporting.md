# Worker resource reporting

What a worker says about the machine it runs on, and which half may be published
when.

## The shape

`storage.WorkerResources` (internal/storage/storage.go) is embedded in
`storage.Worker` with `bson:",inline"`:

- `CPUUsage`, `MemoryUsage` — fractions 0..1, pre-existing, read by the
  scheduler.
- `CPUCores`, `MemoryTotalBytes`, `MemoryUsedBytes`, `StorageTotalBytes`,
  `StorageUsedBytes` — capacity. Bytes, signed `int64` (BSON has no unsigned
  64-bit type and the SQL drivers reject one).

**Zero means "did not say", never "none".** Old workers leave the columns NULL.
Aggregates skip a worker whose capacity is zero (`ReportsCapacity()`), and the
UI renders an absent reading as an em-dash, never as `0`.

## The rule registration has to obey

`register()` persists `currentHostResources().Capacity()` — capacity with the
load fractions zeroed — and does **not** call `SetResources`.

A real load figure written at registration is asymmetric: `filterOnlineWorkers`
builds this worker's own entry from the *local* reading (empty until the first
health check) and every peer's from *storage*. So each worker scores itself as
idle and its peers as loaded, and each claims everything it can.
Seeding the local view instead feeds a start-up CPU spike to admission control,
which then refuses the worker's first workflows.

Static facts at registration; anything the scheduler reads only from
`checkHealth`.

## Where storage measures

`hostResources(dataDir)` measures the filesystem holding
`config.GetConfigDir()` — where the metadata database, WAL files and trace
payloads live — not every mount. Each probe degrades independently: a bad data
directory zeroes the storage fields and keeps CPU and memory.

Container caveat: gopsutil reads `/proc/meminfo`, so a memory-capped container
reports the host, not the cgroup limit.

## Cluster totals

`readClusterResources` in both the SQL and Mongo backends, over the same
two-minute window `ActiveWorkers` uses. Sums, not averages. The CPU fraction is
weighted by core count — `SUM(cpu_usage * cpu_cores) / SUM(cpu_cores)` — so a
busy 2-core box beside an idle 30-core one reads 6%, not 50%. One worker per
host is assumed; two on one machine double-count its memory and disk.

`DashboardSample` deliberately does **not** carry capacity: it is a projection
of what moves over time, and cores and installed memory do not.

## Tests

- `internal/storage/sql/worker_resources_test.go` — round trip, heartbeat,
  online-only totals, core weighting, silent workers, empty cluster.
- `internal/engine/worker/resources_test.go` — host probe bounds, the
  registration rule.
- `internal/storage/mongodb/worker_bson_test.go` — the bson keys the writes use
  are the keys the struct reads (they were not; see below).
- `ui/src/__tests__/resourceReadings.test.tsx`, `metricFormat.test.ts`.

## Three screens describe a worker, and they drift

`/workers` (WorkersPage), `/` (dashboard cluster cards) and `/health`
(GlobalHealthPage, fed by `/api/infra/mesh-health`). The third is the one that
gets forgotten: it had its own `cpu`/`memory` field names, and its `memory` held
a *fraction* that the page rendered as `{node.memory.toFixed(1)} MB` — 78%
memory displayed as "0.8 MB". It also threw outright on the first mesh cluster
registered, because a cluster carries no resource fields and `undefined.toFixed`
is a TypeError.

All three now read `storage.WorkerResources` under one set of names and share
`ResourceGauge` + `@/utils/metricFormat`. Anything added to what a worker
reports has to land on all three, or the third silently keeps the old shape.

Mesh health also has to tell *reporting* from *offline*: capacity survives a
node going away (the machine has the cores it had) but utilisation does not, and
a filled ring beside an OFFLINE badge reads as live. Fleet totals exclude
offline nodes, matching the dashboard's two-minute window.

Still hardcoded on that page and untouched here: the "System Health: Optimal"
card and the "Intelligent Data Quality Alerts" panel, which names a workflow and
a score that come from nowhere.

## Fixed on the way through

On MongoDB, `storage.Worker` had no bson tags, so the driver decoded
`lastseen`/`cpuusage`/`memoryusage` while every write set
`last_seen`/`cpu_usage`/`memory_usage`. Every worker read back with a nil
`LastSeen`, which the UI renders as permanently offline.
