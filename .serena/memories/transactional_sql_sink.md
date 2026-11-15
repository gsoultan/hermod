# Transactional SQL sink (`txsql`)

`pkg/comm/sink/txsql`, built by `internal/factory/txsql.go`. Runs an ordered list
of statements against ONE database in one transaction per batch; a later statement
reads an earlier one's first returned row as `{{name.column}}`. Docs:
`docs/transactional-sql-sink.md`. Design doc (Claude Docs) has the plan: PR 1 is
the sink core (this), PR 2 the editor form with Guided (no-SQL) and SQL modes plus
`hermod.SimulatingSink`, PR 3 Oracle and two-phase commit.

Decisions that are easy to undo by accident:

- **`on_unresolved` defaults to `fail`**, the opposite of Execute SQL. A NULL
  foreign key is the failure the sink exists to prevent. Gembit decided this.
- **A batch is one transaction, with no savepoints.** Any data error rolls the
  whole batch back and wraps `hermod.ErrPermanent`; the engine
  (`writeBatchToSink` -> `isolateFailedBatch`) then writes each message alone, so
  only the bad one is parked. `pkg/engine/txsql_batch_test.go` holds that contract.
- **Error classification** (`classify.go`): constraint / bad value / missing
  object = permanent; deadlock, serialization, lock timeout, connection,
  context = transient; anything unrecognised = transient.
- **Query vs exec is decided from the text** (`returnsRows`): RETURNING / OUTPUT
  anywhere, or a leading SELECT / VALUES / WITH-without-DML, runs as a query. Only
  a query can return rows; only exec gives RowsAffected.
- **Default `expect`**: `one` for a statement that returns rows, `any` otherwise.
  `one` is what refuses `ON CONFLICT DO NOTHING RETURNING id` returning no row.
- **MySQL has no RETURNING**: after an exec INSERT/REPLACE the driver's
  LastInsertId is bound as `{{name.id}}`. Upsert recipe that keeps the id on a
  duplicate: `ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id), ...`.
- **Token resolution order**: forEach alias, then earlier statement results, then
  the message (`evaluator.MessageResolver`). Statement names shadow message fields.
- **Refused at save** (`checkSinkConfig` in `internal/sink/transport/http/sink.go`)
  and at build: forward/self references, references into a forEach statement,
  DDL as first keyword, more than one statement per entry, > 50 statements.
- Config keys are snake_case on the sink (`driver`, `statements`,
  `on_unresolved`, `isolation`, `statement_timeout`) plus the database's usual
  connection fields; statement keys inside the JSON are camelCase (`forEach`,
  `returnId`).
- Live tests: `txsql_integration_test.go` (`-tags=integration`, `POSTGRES_DSN`,
  `MYSQL_DSN`, `MSSQL_DSN`). SQL Server upsert uses `MERGE ... OUTPUT inserted.id;`.

## PR 2: editor form, Guided mode, simulation

- **Modes**: `mode` = `sql` (default when `statements` is set and no mode: every
  sink saved before PR 2) or `guided`. Guided `steps` JSON is compiled server-side
  by `CompileGuided` (`guided.go`) into the same `[]Statement`, then the same
  `validateStatements` checks run. Identifiers must match `^[A-Za-z_][A-Za-z0-9_$]*$`
  (one optional `schema.` prefix) and are quoted per driver; every value must be a
  single `{{token}}`. Upsert is the default action; `DO NOTHING` is never emitted.
  MySQL: at most one `returning` column, read back via `LastInsertId` under
  `Statement.ReturnIDAs` (json `returnIdAs`), with `col = LAST_INSERT_ID(col)` on
  the duplicate path; give-back on MySQL update/delete is refused.
- **`hermod.SimulatingSink`** (`simulating_sink.go`): `Simulate(ctx, msg)` runs and
  rolls back. txsql runs it through `sqlutil.Use`, so inside a preview sandbox it
  nests under the run's transaction. `hermod.SimulatorOf` looks through decorators
  that expose `Unwrap() hermod.Sink` (TracingSink and RetrySink do).
- **Dry run**: `traversal.simulateSink` calls it on sink nodes only when
  `Eng.IsDryRun()`, under `sqlutil.EnterNode`, recovering panics. The log line's
  data drops `Args` and `Rows` (logs are stored); the Test endpoint keeps them.
- **Endpoints**: `POST /api/sinks/txsql/test` (EditorOnly + `AllowConnectorVHost`,
  own sandbox, 60s) and `POST /api/sinks/txsql/compile` (EditorOnly, pure).
- **UI**: `ui/src/components/workflow/Sink/TxSqlSinkConfig.tsx`, logic in
  `ui/src/utils/txsql.ts` (client checks mirror the server's). Guided table and
  column discovery calls the ordinary discovery endpoints with the driver's own
  sink type (`discoveryType`). SQLite connection key is `path`.
- Known unrelated bug found while building this: the SQLite *sink* form
  (DatabaseSinkConfig) writes `db_path`, but `BuildConnectionString` reads `path`.
