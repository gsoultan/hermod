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

## When a later node fails

The run ends at the failing node: `SimulateWorkflow` defers `end()` (the
sandbox's `Rollback`) so the write is rolled back whatever happened after it,
the connection goes back to the pool (`db.Stats().InUse == 0`), the failure is
on that node's step (`Error`), the nodes after it are `Skipped`, and the same
preview can run again at once (the UNIQUE code is free again). Run Preview
(`TestTransformationPipeline`) returns the error and rolls back the same way.
Pinned by `TestSimulationRollsBackSQLWritesWhenALaterNodeFails` and
`TestRunPreviewRollsBackSQLWritesWhenALaterStepFails`
(`internal/engine/registry/preview_sandbox_failure_test.go`). The simulation
used to forward whatever a failed node handed back; see
[simulation_path_contract](simulation_path_contract.md).

Docs: `docs/workflows.md#previews-run-in-a-rolled-back-sandbox`. See also
[execute_sql returned rows](execute_sql_returned_rows.md), whose "Live Preview
executes the statement for real" now means "inside the sandbox".
