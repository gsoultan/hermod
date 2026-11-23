# Where a workflow says it succeeded

Reported 2026-11-23 on a cron → set → 5 × (db_lookup / execute_sql chain) →
stdout workflow: "it is running, but neither the traces nor the logs show any
indicator of success". Nothing was broken; success was simply never said.

## Logs: one summary line, not one per message

The per-write line is DEBUG on purpose (`write_logging_test.go`), and
`DatabaseLogger` drops DEBUG unless `HERMOD_LOG_LEVEL=debug`. So a workflow log
had no INFO line saying anything was delivered.

`pkg/engine/outcome_report.go` now writes `action=processed`:
"Processed 3 messages: 2 delivered, 1 filtered out". The first message after a
quiet minute is reported at once (a cron run shows up as it happens); the rest
inside the minute are counted and written when the window ends (status ticker,
`flushDue`) or at stop (`flush`). Ceiling: one line per workflow per minute.

The counts come from `replyState.status`, which `conclude` now sets whether or
not a sync caller is waiting. A walk interrupted by a stop is `uncounted` — the
message stays on the source. A panic counts as failed.

## Traces: the write is the delivery

- A sink write that failed for good used to leave **no** step: `writeToSink`
  recorded only after a successful attempt, so the trace ended green at the
  router. It now records a step under the sink connection id carrying the error.
- The UI derives the verdict (`ui/src/pages/workflows/traces/traceOutcome.ts`):
  any step with an error → Failed; a sink *node* step plus a successful step
  under that node's `ref_id` → Delivered (names from the node labels, since
  several sink nodes can share one connection); otherwise "Did not reach a sink".
  Reaching the sink node is not a delivery; the traversal records that step
  before the write.
- The trace list shows `error_count` as a badge ("No errors" / "N errors").
  It already came back from `ListMessageTraces`; nothing displayed it.

## Not changed

The stdout sink logs the raw payload at INFO via the workflow logger; that is
its "print". Tracing still needs `trace_sample_rate > 0`.

Tests: `pkg/engine/success_visibility_test.go`, `ui/src/__tests__/traceOutcome.test.tsx`.
