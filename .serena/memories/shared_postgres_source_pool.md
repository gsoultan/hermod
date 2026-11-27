### Many workflows on one PostgreSQL source

All Postgres sources and sinks get their pool from `pgxutil.DefaultPooler`, keyed by the
DSN with the pooler markers stripped. Every workflow on one source node therefore shares one
pool per worker process, capped at `defaultMaxConns` (50) unless the DSN sets
`pool_max_conns`; the source form's **Max connections** field writes `pool_max_conns`, and
`factory.BuildConnectionString` copies a positive value into the URL.

- **Never deliver rows while a pooled result is open.** `poll()` reads the whole batch
  (`fetchPollBatch`), releases the connection, then delivers (`deliverPolledRow`). The
  watermark moves only after a row is delivered. Holding the rows open was what timed out
  ~1000 workflows on one source: a slow sink pinned a pooled connection.
- **First poll is jittered** (`pollStartDelay`: random in `[0, min(interval, 10s))`).
- **Proxy detection** (`pgxutil/proxy.go`): if the DSN has no `pgbouncer`/`pool_mode` key,
  `Pooler.Get` runs `SELECT current_setting('port')` (simple protocol) and compares it with
  the dialled TCP port. A mismatch rebuilds the pool with `ApplyPoolerSafety`.
  `Pooler.Pooled(dsn)` reports declared-or-detected; sources and sinks read `pooled` from
  it, not from `IsPooledConnString`. An explicit marker, `true` or `false`, disables
  detection. A forwarded port is a false positive that only costs the statement cache.
- **CDC through a pooler is tried, not refused.** PgBouncer ≥1.23 passes replication
  through; an older one answers 08P01, which `explainReplicationRefusal` turns into
  "upgrade to 1.23 or use a direct DSN".
- **CDC workflows on one slot share a `slotHub`** (`slot_hub.go`, `hub_member.go`), keyed by
  DSN + slot, per process. Every CDC `PostgresSource` joins one in `init`; the hub owns an
  inner `PostgresSource` (`hubOwned`) that streams as before. Fan-out by table
  (`tableMatcher`), publication = union (`setTables`, `tablesMu`), inner acked per change
  only when every target acked (`pending`/`settleLocked`). Stopped workflows (named by
  `SetSharedSlotMember(workflowID)`, from `factory.SourceConfig.WorkflowID`) are held for:
  `setHold` caps the confirmed LSN at the freeze point for `sharedSlotHoldLimit` (24h);
  returning rewinds (`rewind`, per-connection ctx in `streamLoop`) and each member skips
  transactions with `commit_lsn` ≤ what it acknowledged (`ackWindow` keyed by commit).
  `buildReplicationAppName` keeps PID+session when truncating — cutting them caused a
  mutual-kill loop between two sources on one long slot name.
- Next step planned: move this pool onto gpool (caduceus driver); gpool PR 3 is merged.
