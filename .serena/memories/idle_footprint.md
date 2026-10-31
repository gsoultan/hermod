# What an idle Hermod spends, and the audit that trimmed it (2026-10-31)

An audit of memory, CPU and storage use across the engine and server. Most of
the expensive problems had already been fixed and are documented in
[engine_allocation_profile](engine_allocation_profile.md),
[retention_sweep_and_trace_growth](retention_sweep_and_trace_growth.md) and
[dashboard_history_footprint](dashboard_history_footprint.md). What was left
had one shape: **work whose result was already known**, repeated on a timer or
kept for ever. Six fixes, each with a test that fails on the old behaviour
(`internal/engine/registry/resource_footprint_test.go`,
`internal/storage/sql/suspended_index_test.go`,
`internal/notification/dedupe_window_test.go`).

## The timers

- **`flushStatsToStorage`** (`registry.go`, 5s) wrote `UpdateWorkflowStats`
  and `UpdateWorkflowStatus` for every engine on every tick. Now gated by
  `activeEngine.flushed` (`flushedRows`): a row is written only when the
  counters or status moved, and a failed write is forgotten so the next tick
  retries. Same shape as `statusWriteGate` for the health callback.
- **`purgeRetention`** swept `audit_logs` once per workflow at that workflow's
  own window — N deletes an hour, shortest window winning. Now one
  `PurgeAuditLogs` per sweep at the *longest* window (`auditCutoff`).
- **`startReconciliationLoop`** (5s, every worker) runs
  `SELECT ... FROM suspended_messages WHERE resume_at <= ?`. The table had no
  index on `resume_at`; `idx_suspended_messages_resume_at` was added (SQL) and
  a `resume_at` index on MongoDB. Still open: the query is unscoped, so each
  worker also loads the due messages of workflows it does not run and then
  skips them in `resumeSuspendedMessage`.

## The maps

- `Registry.piiStats`, `supervisorState.records` and `Registry.nodeStates`
  were keyed by workflow ID and never pruned on delete. `Registry.ForgetWorkflow`
  (called by both delete handlers in place of `DeleteWorkflowTraces`) deletes
  the traces and these entries.
- `Registry.dbPool` kept a deleted source's `*sql.DB` — and database/sql's
  opener goroutine — until restart. `Registry.CloseSourcePools(sourceID)` is
  called from `SourceHandler.DeleteSource`.
- `notification.Service.lastSent` enforced a five-minute repeat window with
  entries that were never removed. `sweepDedupeLocked` drops expired entries
  every `dedupeSweepEvery` (256) admissions or at least once a window; the
  clock is `Service.now` so tests can move it.

## Looked at and left alone (with the reason)

- `time.After` in select loops (sources, SSE handler): Go 1.23+ collects
  unreferenced timers, so this is an allocation per iteration, not a leak.
- `workflowWatchers sync.Map` never shrinks: one `*atomic.Int32` per workflow
  ever watched, by design — a router holds the pointer.
- `sse.Hub.WaitUntil` polls at 10ms, but only until the first subscriber or
  the SSE sink's timeout.
- `pkg/engine/outbox.go` lists the first 100 pending items across *all*
  workflows and filters in Go — but `SetOutboxStorage` has no production
  caller, so the relay never runs.
- `workflow_versions` and `approvals` grow per user action with no retention;
  `webhook_requests` keeps the last 50 per path. Bounding versions or
  processed approvals needs a product decision.
- `dashboard_history` already has knobs and a latch
  ([dashboard_history_footprint](dashboard_history_footprint.md)).
