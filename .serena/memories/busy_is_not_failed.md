# Busy is not failed: hermod.ErrBusy

A destination that is up but has no room (no free connection, too many
clients, 429, 503) is a different thing from a failure. A sink says so by
wrapping `hermod.ErrBusy`, or returning `&hermod.Busy{Err, RetryAfter}` when the
destination named a wait. `hermod.RetryAfter(err)` reads it.

- `Engine.waitOutBusy` (`pkg/engine/busy.go`) wraps every sink write in the
  three engine retry loops (`writeToSink`, `retryBatch`, `writeEventToSink`). It
  repeats the attempt until the answer is not busy, with backoff starting at the
  sink's retry interval (floor 50 ms), doubling to 30 s, with jitter, honouring
  Retry-After up to 5 min. The retry loop, and so `MaxRetries` and the circuit
  breaker's observers, only see what comes after.
- Sink status is `waiting` while a write waits; the health tick keeps it that
  way (`sinkStatusWhenUp`) instead of resetting it to `running`.
- The stall watchdog treats a pipeline with any waiting write as having no
  stuck work (`progressSample`). Accepted cost: a second, wedged sink on the
  same engine is not reported while another sink is being waited for.
- `sink.RetrySink` returns a busy answer at once rather than spending its own
  three attempts.
- Classified so far: HTTP sink 429/503 (`pkg/comm/sink/http/status.go`),
  PostgreSQL sink SQLSTATE 53300 and 57P03 (`pkg/comm/sink/postgres/busy.go`).
  Classify by code, never by message text.

Steps (transformation nodes) too:

- The engine offers itself to the steps it runs as a `hermod.BusyWaiter`
  (`hermod.WithBusyWaiter` on the runner's base context, `busy_waiter.go`).
  `Engine.WaitOutBusy(ctx, "step:<node id>", attempt)` shares the loop
  (`waitOut`) and the stall exemption with sinks, without a sink status.
- `Registry.runWorkflowNode` runs every node through `waitOutBusyStep` before
  the node's retry policy. No waiter in the context (preview, test run) means
  the busy answer is returned as it is.
- A step answers busy only when nothing was done, so repeating it is safe:
  `sqlutil.MarkBusy` (SQLSTATE 53300/57P03 via a `SQLState()` interface, no
  driver import) on `db_lookup` and `execute_sql`, and for `db_lookup` only a
  deadline hit while all of Hermod's connections to the source were in use
  (`poolUse.explain`). Never mark a write busy after it may have run.

Not yet covered: API steps (429), the batched lookup path, and the UI word
for `waiting`.
