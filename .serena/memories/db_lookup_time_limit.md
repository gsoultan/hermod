# DB Lookup has a time limit (default 30s)

`db_lookup` (`pkg/comm/transformer/lookup/db_lookup.go`, helpers in `timeout.go`)
bounds everything after the cache check with `context.WithTimeout`: GetSourceConfig,
the pool, the query (table, query-template and Mongo paths) and the batcher's shared
query. Setting: `timeout` (Go duration). Empty = `defaultDBLookupTimeout` (30s, a var
so tests can shorten it). A bad value fails before any query is sent.

- **Why 30s:** under the engine stall watchdog's 60s (`pkg/engine/stall.go`,
  `DefaultStallThreshold`). Before this, a lookup with no answer (PgBouncer queue
  `cl_waiting`, a lock, a half-dead connection) held its message until the watchdog
  restarted the workflow; the restart's cancel surfaced as
  `failed to execute lookup query: context canceled`. Reported Nov 2026 on a lookup
  behind PgBouncer; same chain without PgBouncer.
- **Batcher:** `pkg/infra/batcher` runs `batchFn` on `context.Background()`, so the
  limit is applied again inside the closure in `getOrCreateBatcher`, and `timeout`
  is part of `batcherFingerprint`.
- **Stop vs timeout:** `lookupTimeoutError` only rewrites a DeadlineExceeded when the
  message's own context is still alive, so a stop stays `context.Canceled` and the
  traversal still treats it as an interruption (redelivered, not dead-lettered).
- **Execute SQL vs DB Lookup:** separate transformers (`advanced/execute_sql.go` has a
  `timeout` with no default); both get pools from the same registry cache.
- **Pooler safety and pool size:** see [`registry_sql_pool.md`](registry_sql_pool.md). The
  pool is the registry's database/sql one, not pgxutil's shared pgxpool; it now detects
  PgBouncer itself and honours `pool_max_conns`.
- **Where the wait was:** `poolUse` (timeout.go) reads the pool's InUse/MaxOpen *before* the
  query and appends "all N of Hermod's connections ... were busy" (queued in Hermod, invisible
  to SHOW POOLS) or "M of N ... so the wait was in the database or PgBouncer". Skipped in a
  preview (sandbox connection, not the pool).
- Tests: `db_lookup_timeout_test.go` (hanging database/sql driver), `db_lookup_pool_wait_test.go`,
  `db_lookup_timeout_integration_test.go` (`-tags=integration`, `POSTGRES_DSN` and
  optional `PGBOUNCER_DSN` with `default_pool_size = 1`: lock wait direct and via
  PgBouncer, and PgBouncer pool exhausted). UI: `dbLookupTimeLimit.test.tsx`.
