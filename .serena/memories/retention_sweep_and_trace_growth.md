# Retention sweeps, and why message_trace_steps ate 50 GB

`message_trace_steps` is the fastest-growing table Hermod owns: the whole message
payload, per node, per message. Nothing bounds it except `Registry.purgeRetention`.

> **Superseded 2026-09-21 for the payload columns.** `after_data` is no longer
> written either: see "One payload per message" below. The note below is still
> the right history.
>
> **Corrected 2026-09-17.** This used to say the table stores `before_data` *and*
> `after_data` — the payload twice. It no longer does. There is no `before_data`
> column (`QueryInitMessageTraceStepsTable` in `internal/storage/sql/queries.go`);
> only `after_data` is written, and `GetMessageTrace` reconstructs each step's
> `Before` from the previous step's `After`. That reconstruction has a
> consequence worth knowing before reading a trace — see
> [`message_trace_shape.md`](message_trace_shape.md). A single payload is also
> capped at 32 KB by `capTracePayload` (`HERMOD_TRACE_MAX_PAYLOAD_BYTES`).

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

## One payload per message (2026-09-21)

The redesign above took the table from two copies of the payload chain to one.
This takes it from one copy *per step* to one copy per distinct payload, and
compresses it.

**Why there was anything left to win.** Most nodes do not change the message.
`workflow_start`, the source ingest step, the validator and the router each
record it verbatim, and a transformation node records its output twice — once
under `node.ID` from the traversal, once under its `transType` from
`doApplyTransformation`. Measured across a realistic nine-step workflow, **66.6%
of every payload byte in the table was byte-identical to another step of the
same message**. And nothing compressed it: a trace payload is well under
PostgreSQL's ~2 KB TOAST threshold, so it sat in the heap as raw JSON —
**measured 0.01 MB of TOAST against a 127.84 MB heap**. Do not assume TOAST is
handling this; it is not, and that is the whole reason compression was worth
adding.

**The shape.** Two nullable columns, so `autoMigrate` adds them as a catalogue
change rather than rewriting the largest table in the database:
`after_hash BLOB` (128-bit content hash, scoped to one message) and
`after_blob BLOB` (zstd, with a leading codec byte). The first step carrying a
payload stores the bytes; the rest store the hash alone and `GetMessageTrace`
resolves them in a second pass over the rows it already read — no join, no
second query. `after_data` survives read-only, so pre-upgrade rows need no
backfill.

**Measured**, 20k traces x 9 steps, realistic incompressible CDC payloads:

| | before | after |
|---|---|---|
| SQLite, whole database | 171.33 MB | **80.20 MB** (2.14x) |
| bytes per step | 998.1 | 467.2 |
| PostgreSQL 18, the table | 135.20 MB | **59.45 MB** (2.27x) |
| bytes per step | 787.6 | 346.3 |
| payload bytes on disk | 94.85 MB | **20.45 MB** (4.64x) |

**Dedup beat compression** — 2.05x against 1.42x alone on PostgreSQL. That is
the opposite of the usual intuition and the reason both were measured. The
measuring harness is `TestMessageTraceStepsFootprint`, which is now a gate: it
asserts carriers == distinct payloads, that nothing writes `after_data`, a 3x
compression floor and a 700 B/step ceiling.

**Two design points worth not re-litigating.**

- The dedup bookkeeping is a bounded in-process cache, consulted
  *pessimistically*: an entry is added only after an insert succeeded, and a
  miss stores another copy. A cold start, a second replica, an eviction or a
  race between two steps of one message therefore costs a duplicate payload,
  never a reference with nothing behind it. Getting that direction wrong turns a
  full table into a blank trace viewer.
- The hash is 128-bit, not 64. The bytes hashed are the message's own data,
  which is whatever an upstream database or an HTTP caller supplied, and a
  64-bit content address over input someone else chooses is a 2^32 search away
  from showing one node's payload under another node's name.

`currentSchemaVersion` was **not** bumped: an older binary still reads and
writes, it just sees blank payloads for traces the newer one recorded, which
heal on roll-forward. Reasoning in full at `knownSchemaFingerprint`.

## What was measured and deliberately not changed

- **An INTEGER micros `timestamp`** saves ~8 MB per 180k steps on SQLite (the
  driver stores `time.Time` as 36-byte text). Rejected: PostgreSQL RANGE
  partitioning is declared on that column, and `QueryPurgeMessageTraces` binds a
  `time.Time` against it — a silent type mismatch there stops the purge, which
  is the 50 GB bug at the top of this file.
- **Dropping `idx_trace_ts`** saves 2.91 MB per 180k steps. Rejected: it is what
  keeps the purge off a sequential scan, and it was added for that reason.
- **A `WITHOUT ROWID` clustered PK on (workflow_id, message_id, timestamp)** was
  the single biggest remaining win on SQLite — 60.26 MB -> 43.96 MB, because it
  folds `idx_trace_msg` into the table. Rejected: two steps can share a
  timestamp, so the PK silently drops one, and the win is SQLite-only
  (PostgreSQL has no clustered index).

## Still open after this

- `PurgeMessageTraces` still has no `workflow_id` predicate (see above). Unchanged.
- **MongoDB and Pebble still store the payload twice, uncompressed.**
  `hermod.TraceStep` has no bson tags, so Mongo's `$push` persists `Before` *and*
  `After`; Pebble JSON-marshals the whole `MessageTrace`. Both are the shape SQL
  left behind in 2026-09-17, plus no dedup. Mongo additionally pushes every step
  into one document, so a long trace can reach the 16 MB BSON limit.
