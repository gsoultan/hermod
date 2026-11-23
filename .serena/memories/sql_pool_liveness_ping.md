# SQL source pools: the liveness ping is bounded

`Registry.GetOrOpenDB` / `GetOrOpenDBByID` (`internal/engine/registry/registry.go`)
cache one `*sql.DB` per database source and ping it before every hand-out. Every
per-message SQL step goes through it: `execute_sql`, `db_lookup`, `batch_sql`, ...

- **The ping is bounded** (`poolPingTimeout`, 5s) and, through
  `GetOrOpenDBByID`, runs on the caller's context. It used to run on the
  registry's lifetime context with no deadline: one pooled connection whose peer
  vanished without a FIN/RST (NAT or firewall idle drop, failover) hung the step
  before its statement was sent. Symptom: the advisor's "<step> looks stuck",
  N messages in flight, and `pg_stat_activity` shows nothing running or blocked.
  A workflow restart could not free it. Reported on an Exec SQL DELETE in
  Nov 2026; tests in `db_pool_ping_test.go` use a TCP server that never answers.
- **A ping that only timed out keeps the pool.** It means busy or one dead
  connection (pgx discards that connection), not a wrong pool. Only a real
  error closes and reopens it — closing fails every statement in flight on it.
  The slow path pings on the registry context because it is shared by every
  singleflight waiter.
- `pgPoolConnConfig` gives a Postgres pool `connect_timeout` 15s when the DSN
  sets none. pgx v5.11's dialer already uses Go's TCP keepalive defaults (15s
  idle, 15s interval), so no keepalive override is needed.
- **`execute_sql` has a `timeout` setting** (Go duration, empty = none). It wraps
  getting the pool *and* the statement; the error names the duration. The
  editor (`SQLConfig.tsx`, step 5 "Time limit") validates the unit.
- **`db_lookup` has a `timeout` too, defaulting to 30s** -- see
  `db_lookup_time_limit.md`.
