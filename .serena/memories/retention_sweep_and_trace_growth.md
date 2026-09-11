# Retention sweeps, and why message_trace_steps ate 50 GB

`message_trace_steps` is the fastest-growing table Hermod owns. It stores
`before_data` **and** `after_data` — the whole message payload, twice, per node,
per message (`internal/storage/sql/queries.go`, `QueryInitMessageTraceStepsTable`).
Nothing bounds it except `Registry.purgeRetention`.

## The bug

`purgeRetention` parsed `wf.TraceRetention` with `time.ParseDuration`, which has
no `d` unit. The workflow editor defaults the field to `'7d'`
(`ui/src/pages/workflows/WorkflowEditor/store/useWorkflowStore.ts`). So the parse
failed **on the default value**, the call site was `if err == nil`, and the sweep
was skipped silently for every workflow, forever. Same code, same bug, for
`AuditRetention`.

A day-aware `parseDuration` already existed in the same file, ~1,400 lines below
the call site. Found in production: PostgreSQL +50 GB in a couple of hours.

**The lesson is the `if err == nil` shape.** A parse failure on operator-supplied
config that silently disables a safety mechanism is invisible until the disk
fills. Retention, quota and expiry parses must log on failure.

## Related traps

- **Index the column the sweep filters.** The purge is
  `DELETE ... WHERE timestamp < ?`; the only index was `(workflow_id, message_id)`.
  Added `idx_trace_ts`. `audit_logs` already had `idx_audit_ts`.
- **Fixing a broken purge is itself dangerous.** The first sweep that actually
  runs bulk-deletes everything past the window: tens of GB of WAL, dead tuples
  until `VACUUM FULL`, and `CREATE INDEX` blocking startup. Truncate or
  copy-and-swap *before* deploying the fix.
- **`SchemaFingerprint` hashes only `CREATE TABLE`**, so adding an index does not
  move it — see [dashboard_history_footprint](dashboard_history_footprint.md)
  for when it does.

## The redesign (measured on PostgreSQL 17, 250k step rows)

| | before | after |
|---|---|---|
| list traces, page 1 | 58.5 ms, Seq Scan, ~390 MB I/O | **0.071 ms**, index scan |
| storage (realistic payloads) | 262 MB | **123 MB** |

- **`message_traces` parent table**, one row per traced message, written by
  `RecordTraceStep` alongside the steps. Listing used to be
  `SELECT DISTINCT message_id, MIN(timestamp) ... GROUP BY` over the step table,
  which no index can satisfy. The parent row makes the page an index scan whose
  cost is the page size, not the table size.
- **`before_data` is gone.** It is the previous step's `after_data`;
  `GetMessageTrace` reconstructs it. Half the table.
- **The write-only UUID `id` is gone** — same defect as `dashboard_history`.
- **Keyset paging.** `TraceFilter{Before, Limit, Offset}`; the HTTP layer takes
  `before`, the viewer keeps a cursor stack. `offset` still works.
- **`HERMOD_TRACE_MAX_PAYLOAD_BYTES`** (32 KiB) caps one step; the marker is
  valid JSON so the viewer still renders it.
- **PostgreSQL daily partitioning**, `HERMOD_TRACE_PARTITIONING=off` to opt out.
  Retention becomes `DROP TABLE`. A DEFAULT partition means maintenance can lag
  without failing an insert; a week of lookahead keeps DEFAULT empty so
  attaching never waits on a validation scan. New tables only — a table cannot
  be altered into a partitioned one.
- **`DefaultConfig().TraceSampleRate` is now 0.** It was 1.0, so any engine
  missing the per-workflow override traced everything.

`currentSchemaVersion` went to **2**: an older binary inserts `id`/`before_data`
by name, so a rollback onto this schema fails every trace write. Refusing to
start is correct.

Two traps this left: SQLite cannot drop a primary key, so `id` survives there
and `traceStepsKeepsLegacyID` makes the insert keep supplying one; and traces
written before the upgrade have no parent row, so they do not appear in the list
until backfilled (SQL is in the CHANGELOG — deliberately not automatic, it would
block start-up).

## Still open

`PurgeMessageTraces` runs `DELETE FROM message_trace_steps WHERE timestamp < ?`
with **no `workflow_id` predicate**, but `purgeRetention` loops over workflows
and derives the cutoff from each one's own setting. A workflow set to `7d`
deletes the traces of one set to `365d`, and the same global delete runs once per
workflow per hour. Fixing needs a decision about traces of deleted workflows.

Tracing is off unless `trace_sample_rate > 0`
(`registry_workflow.go` overwrites the engine default) — but
`pkg/engine/config/config.go` defaults `TraceSampleRate: 1.0`, so any engine path
that skips that override traces 100% of messages.
