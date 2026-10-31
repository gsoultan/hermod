# SQL previews run in a rolled-back sandbox

Since #339 (v1.23.0). Simulate, Run Preview and the SQL builder's preview used to
run an Execute SQL node's INSERT/UPDATE on the connection pool, which commits: a
button in the editor changed the real table, and a second preview of the same
INSERT failed on the row the first left behind.

## The seam

`pkg/infra/sqlutil/sandbox.go`. A preview run's context carries a `*Sandbox`
(`WithSandbox`, `Sandboxed`). Nodes get their database through
`sqlutil.Use(ctx, db, driver)`:

- outside a preview it returns the pool unchanged (`poolDB`);
- inside one it returns the run's single transaction on that pool, opened on
  first use and rolled back by `Sandbox.Rollback` when the run ends.

Execute SQL, DB Lookup and SCD go through `Use`. **Any new node that runs SQL must
too**, or its preview commits for real. Later nodes in the run see earlier nodes'
writes because they share the transaction.

## Rules inside a preview

- PostgreSQL: every statement runs behind `SAVEPOINT hermod_stmt`, so one failed
  node does not abort the transaction for every node after it.
- A node's own `BeginTx` (SCD) becomes a named savepoint (`nestedTx`), so its
  "commit" stays inside the preview.
- Refused: COMMIT/ROLLBACK, more than one statement, opening a transaction, and DDL
  or procedure calls on MySQL, Oracle and Snowflake (they commit implicitly).
- ClickHouse cannot roll back: a preview runs reads only and refuses writes.
- Lock waits capped at `lockWaitSeconds = 5`: `SET LOCAL lock_timeout` on
  PostgreSQL; MySQL and SQL Server session settings are reset before the
  connection returns to the pool (`resetSession`; a connection that cannot be
  reset is discarded).
- DB Lookup skips the shared lookup cache and the batcher in a preview: the cache
  could answer with a value the run has changed, and must not keep a row that is
  about to be rolled back.
- The SQL builder preview (Available Fields) runs a query that may write in a
  one-shot sandbox, never on a component's own SQL path.

Docs: `docs/workflows.md#previews-run-in-a-rolled-back-sandbox`. See also
[execute_sql returned rows](execute_sql_returned_rows.md), whose "Live Preview
executes the statement for real" now means "inside the sandbox".
