# Per-database activity and connection budget (pkg/infra/dbactivity)

One `dbactivity.Database` per physical database, keyed by `Key(driver, host, port, db, user)`
(`pgxutil.Database(connString)` builds it for Postgres, after stripping Hermod's own keys).
Pools register with `Bind(pool, d)` / `Unbind`; steps find it with `OfPool(pool)`.

- **Tracks** every Hermod query by Kind: `SourcePoll` (Postgres source `gatedPollBatch`),
  `SQLStep` (execute_sql `beginStatement`), `Lookup` (db_lookup `poolUse.track`).
  `Longest`/`LongestBulk` feed the lookup timeout error, which names the workflows holding the
  database.
- **Gates** bulk kinds (everything but Lookup). The limit is min(ceiling, learned bulk):
  `SetLimit` is the configured ceiling (pool_max_conns less the reserve, 0 = none); `Learn(share,
  reserve)` is the learned server-connection share. `BulkLimit(n, reserve)`: reserve < 0 means a
  quarter (at least 1). `pool_lookup_reserve` sets it; stripped like the pooler markers, since
  PgBouncer and Postgres refuse unknown startup parameters.
- **Tuner** (`dbactivity.Tuner`): started per registry pgx pool in `bindSQLPool`, stopped and
  waited for in `closeSQLPool`. Canary `SELECT 1` on its own one-connection pool (pooler-safe exec
  mode) every 5s. A canary slower than 1s or refused (53300/57P03) means no server connection was
  free: `Learn(running)` and `MarkQueued`. Queries queued inside PgBouncer count as running, so it
  converges over a few queues (one multiplicative step each). Gives back one connection per 10 min.
  The canary must not use the shared pool: when that is full it queues in Hermod and learns nothing.
- **Busy, not failed**: a lookup timeout is `hermod.Busy` when `QueuedSince(asked)` or long bulk
  holders exist (it waited for a connection, and a lookup is a read); with neither it stays an error
  because the query itself may be slow. An execute_sql step that timed out waiting for its slot is
  Busy (it never ran); one that timed out while running is not.
- The scale plan's adaptive/cross-worker budget should drive `SetLimit`/`Learn` rather than add a
  second gate.

Integration check: `PGBOUNCER12_DSN=… go test -tags integration -run
TestSQLPool_LookupsGetThroughWhenLongQueriesFillPgBouncer ./internal/engine/registry/` (PgBouncer
default_pool_size 12): learned 12, limit 9, lookups ~1 ms.
