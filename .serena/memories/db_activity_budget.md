# Per-database activity and connection budget (pkg/infra/dbactivity)

One `dbactivity.Database` per physical database, keyed by `Key(driver, host, port, db, user)`
(`pgxutil.Database(connString)` builds it for Postgres, after stripping Hermod's own keys).
Pools register with `Bind(pool, d)` / `Unbind`; steps find it with `OfPool(pool)`.

- **Tracks** every Hermod query by Kind: `SourcePoll` (Postgres source `gatedPollBatch`),
  `SQLStep` (execute_sql `beginStatement`), `Lookup` (db_lookup `poolUse.track`).
  `Longest`/`LongestBulk` feed the lookup timeout error, which names the workflows holding the
  database.
- **Gates** bulk kinds (everything but Lookup). The limit is min(ceiling, learned bulk):
  `SetLimit` is the configured ceiling (pool_max_conns less the reserve; with Max connections
  blank, `bindSQLPool` uses the pool's real size, 20, unless another source set one); `Learn(share,
  reserve)` is the learned server-connection share. `BulkLimit(n, reserve)`: reserve < 0 means a
  quarter (at least 1). `pool_lookup_reserve` sets it; stripped like the pooler markers, since
  PgBouncer and Postgres refuse unknown startup parameters.
- **Tuner** (`dbactivity.Tuner`): started per registry pgx pool in `bindSQLPool`, stopped and
  waited for in `closeSQLPool`. Canary `SELECT 1` on its own one-connection pool (pooler-safe exec
  mode) every 5s. A canary slower than 1s or refused (53300/57P03) means no server connection was
  free: `Learn(min(running, learned))` and `MarkQueued`. A further queue never raises the share
  (running counts lookups queued in PgBouncer, so it grows with the queue). `MarkQueued` fires
  from a timer once the canary has waited QueuedAfter, not when it returns, so a lookup timing out
  on the same queue sees it; a Skipped canary takes its early mark back. Gives back one connection
  per 10 min. With a PgBouncer reserve pool the share is not fixed (reserve connections open after
  reserve_pool_timeout), so tests assert limit < learned, never a value.
  The canary must not use the shared pool: when that is full it queues in Hermod and learns nothing.
- **Busy answers carry holders** (`hermod.Busy.Holders`, built by `dbactivity.Holders`): the
  lookup's long bulk queries, or every bulk-slot holder for execute_sql. Steps are named by
  `hermod.StepWorkflowKey` (set on every transformation in nodes/core; WorkflowIDKey stays
  ml_predict-only because rolling/aggregate/sampling key state by it).
- **Busy, not failed**: a lookup timeout is `hermod.Busy` when `QueuedSince(asked)` or long bulk
  holders exist (it waited for a connection, and a lookup is a read); with neither it stays an error
  because the query itself may be slow. An execute_sql step that timed out waiting for its slot is
  Busy (it never ran); one that timed out while running is not.
- The scale plan's adaptive/cross-worker budget should drive `SetLimit`/`Learn` rather than add a
  second gate.

- **A waited-out busy attempt is not a trace step**: `doApplyTransformation` skips recording it
  (`waitedOut`), and TransformationNode does not broadcast it as an error. v1.27.1 recorded every
  attempt, so a lookup that waited and succeeded showed as a failed run on the Runs tab.
- **The wait is recorded instead**: `waitOutBusyStep` calls `hermod.AddStepWait`; traversal (traced
  messages only) and recorded manual runs put a slot on the step's ctx with `WithStepWait`;
  `RecordCompletedTraceStep`/`manualRun.record` read `StepWaitFrom`. Stored as `TraceStep.Wait`
  (SQL `wait_data` JSON, documents `wait`), served as `executions.Step.Wait` with holders named by
  `NameHolders` (only workflows in the caller's vhosts), `Execution.WaitMs`.

Reproduction: `PGBOUNCER_SMALL_POOL_DSN=… go test -tags integration -run
TestLookupsNeverFailBehindSQLStepsOnASmallPgBouncerPool ./internal/engine/registry/` against
PgBouncer default_pool_size 5, reserve_pool_size 50 (the reported one); `REPRO_REAL_TIMINGS=1`
for 25s statements and the 30s lookup limit. On v1.27.1 lookups fail with the reported error.

Integration check: `PGBOUNCER12_DSN=… go test -tags integration -run
TestSQLPool_LookupsGetThroughWhenLongQueriesFillPgBouncer ./internal/engine/registry/` (PgBouncer
default_pool_size 12): learned 12, limit 9, lookups ~1 ms.

## Tuning "Databases" cards
- `dbactivity.Database.Snapshot()` — one locked read (Ceiling, Learned, Limit, Running, InBulk,
  QueuedAt, Queries oldest first).
- `Registry.DatabaseSnapshot(ctx, src)` → SourceConnString + `pgxutil.Database(connStr)` (same key
  as the pools); memory only; false for non-pgx.
- `advisor.ReadDatabase` (internal/advisor/database.go): Fixed = learned_share, kept_lookups_moving
  (QueuedAt within 1h and Limit>0); NeedsYou = long_query (this flow's bulk ≥5 min, NodeID),
  other_flows (other flows' bulk ≥5 min, named), pool_smaller_than_set (Learned>0, Limit<Ceiling).
  No card invented for a quiet database. Times are "N min ago", never server clock times.
- `GET /api/workflows/{id}/insights/databases` (vhost access; flowSourceIDs = source nodes' RefID +
  transformations' `sourceId`). UI `pages/workflows/tuning/DatabaseCards.tsx`.
