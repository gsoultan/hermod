# The registry's database/sql pool (DB Lookup, Execute SQL)

Two pool families talk to one PostgreSQL source. Do not confuse them:

| Pool | Who | Where | Size |
| :--- | :--- | :--- | :--- |
| pgxutil `DefaultPooler` (pgxpool) | Postgres sources and sinks | `pkg/infra/pgxutil` | 50, or `pool_max_conns` |
| registry `dbPool` (`*sql.DB`, pgx stdlib) | `db_lookup`, `execute_sql`, scd, discovery — anything via `GetOrOpenDB` | `internal/engine/registry/sql_pool.go` | 20 (`defaultSQLPoolMaxConns`), or `pool_max_conns` |

Both are per source per worker process. A fix to one does not reach the other: PR 436's
PgBouncer detection and Max connections field reached only the pgxpool until the follow-up.

- **`pool_max_conns` must be stripped from `RuntimeParams`.** Only `pgxpool.ParseConfig`
  removes `pool_*` keys; `pgx.ParseConfig` forwards unknown keys as startup parameters, and
  PostgreSQL ("unrecognized configuration parameter") and PgBouncer ("unsupported startup
  parameter", 08P01) refuse the connection. `pgPoolConnConfig` reads it and deletes every
  `pool_*` key. Any new code that parses a DSN with `pgx.ParseConfig` needs the same.
- **Proxy detection:** `openPostgresPool` probes one connection (`pgxutil.ProbeConn` via
  `sql.Conn.Raw`) unless `pgxutil.DeclaresPooler`, and reopens with `ApplyPoolerSafety` when
  the server's port differs from the dialled one. Without it, transaction-mode PgBouncer with
  `max_prepared_statements = 0` fails with `42P05 prepared statement ... already exists`.
- **No ping per call.** `cachedPool` pings at most every `poolCheckInterval` (30s) per pool;
  `pooledDB.checked` records the last good-or-busy ping. A ping per lookup doubled connection
  acquisitions and PgBouncer server assignments, and queued behind a saturated pool for up to
  `poolPingTimeout`. Entries built without `newPooledDB` (tests) always ping.
- Tests: `sql_pool_size_test.go`; `sql_pool_pgbouncer_integration_test.go`
  (`-tags integration`, `PGBOUNCER_DSN` in transaction mode with no marker; set
  `pgbouncer=false` in it to watch the 42P05 failure come back).
