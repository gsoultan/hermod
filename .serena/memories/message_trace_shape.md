# Reading a message trace: three things that are not obvious

Asked "why is the same data on every trace step", check
`message_trace_steps` directly before theorising:

```sql
-- The payload is zstd in after_blob now, not JSON in after_data, so a bare
-- SELECT shows bytes. after_hash is what you actually want to eyeball: steps
-- sharing a hash share a payload, which answers the question directly.
SELECT message_id, node_id, timestamp,
       encode(after_hash, 'hex') AS payload,
       after_blob IS NOT NULL    AS carries_bytes
FROM message_trace_steps
WHERE workflow_id = '<id>'
ORDER BY message_id, timestamp;
```

`HERMOD_TRACE_COMPRESSION=off` makes new rows plain JSON again (after a codec
byte) if you need to read them by eye. Rows written before 2026-09-21 still have
their JSON in `after_data`.

in `hermod_metadata`. That one query separates "the viewer is confusing" from "a
node really is returning stale data" — it is what found the `db_lookup` cache-key
collapse in [`lookup_cache_fast_path.md`](lookup_cache_fast_path.md): the
enriched block was byte-identical across messages while the input field differed.

## 1. `workflow_start` and `router` are not nodes in anyone's graph

The engine records them in `pkg/engine/runner.go` (`processMessage`).
`nodeNameById` in `WorkflowDetailPage.tsx` cannot name them, so they render as
raw ids beside properly-named nodes. `workflow_start` is the message as the
source handed it over — the only record of the untransformed input.

## 2. For a node-graph workflow the `router` **is** the traversal

`setupWorkflowRouter` (`internal/engine/registry/registry_workflow.go`) walks the
entire DAG inside the `RouterFunc`. So the `router` step covers the whole
pipeline and its duration is end-to-end latency, not routing time.

This caused a bug: the step was timestamped *before* the traversal and
snapshotted *after* it. Fixed 2026-09-17 by snapshotting before routing
(`Engine.WillTrace` + `Engine.RecordTraceStepSnapshot`). A router decides where a
message goes; it is not a transformation, and its step records the message it was
handed.

## 3. `before_data` is not stored — it is reconstructed

Nor, since 2026-09-21, is `after_data` stored per step: a payload is stored once
per *message*, in `after_blob`, compressed, and every other step carrying the
same content stores `after_hash` alone. `GetMessageTrace` resolves references
against the rows it is already reading. So "the same data on every trace step"
is now literally one copy — and 66.6% of payload bytes were duplicates before
that. See [retention_sweep_and_trace_growth](retention_sweep_and_trace_growth.md).

A reference whose carrier is gone reads as a nil `After`, deliberately: the
retention sweep cuts on timestamp and a message spans milliseconds, so a cutoff
can land inside one. Absent is honest; inheriting the neighbour's payload would
show one node's data under another node's name.

`RecordTraceStep` writes no `Before`; `GetMessageTrace` sets each step's
`Before` to the *previous* step's `After`, in `timestamp ASC` order. The first
step therefore has a nil `Before`, honestly.

The consequence is the one that bites: **a step whose timestamp and whose payload
come from different moments corrupts its neighbour's before-image too.** That is
exactly what the router step did — the finished payload sorted second, between
the message arriving and the source node emitting it, and the source node then
looked as though it had deleted every enriched field.

## Expected, not a bug

A transformation node produces **two** steps with identical payloads:
`traversal.runNode` records under `node.ID`, and `doApplyTransformation` records
again under the transformation *type*. The second is what gives pipeline
sub-steps an identity (a pipeline's steps have no node id of their own); only the
first can be named in the UI. It does mean a transformation node costs two rows
per message in the table [`retention_sweep_and_trace_growth.md`](retention_sweep_and_trace_growth.md)
is about.

Tracing is off unless a workflow sets a sample rate — `DefaultConfig` leaves
`TraceSampleRate` at 0, deliberately. Sampling is deterministic on message ID, so
a message is traced at every step or at none.
