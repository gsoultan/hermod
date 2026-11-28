# Source database Diagnosis (`internal/advisor/dbdiag`)

`POST /api/sources/{id}/diagnosis` (EditorOnly, audit SOURCE_DIAGNOSIS) → `dbdiag.Run(ctx, db, connStr)`.
UI: source edit page, Diagnosis tab (`?tab=diagnosis`), `components/connections/SourceDiagnosis.tsx`.

- collect.go: one READ ONLY tx, `SET LOCAL statement_timeout='5s'`; limits + pg_stat_activity
  (left(query,120), pg_blocking_pids). Pooler detected by server `port` != dialled port.
- pooler.go: PgBouncer console = db `pgbouncer`, simple protocol, SHOW POOLS / SHOW DATABASES.
  Refusal is 08P01; report only the SQLSTATE, never the DSN.
- command.go: only up to 4 leading allow-listed SQL keywords are ever reported (PII), leading
  BEGIN dropped.
- assess.go: pure checks (locks, long_queries >=300s, waits, idle_in_transaction >=60s, limits
  80% warn / 95% err, pooler). Hermod sessions recognised by application_name `hermod*` (#447).
- Without pg_read_all_stats / pg_monitor other users' state shows as hidden; Report.Limited names
  the grant.
- Registry.SourceConnString(ctx, src) gives conn string + driver (batch_sql resolved).
- Integration test: build tag `integration`, env DIAG_PG_DSN, DIAG_PGB_DSN, DIAG_PGB_PLAIN_DSN.
