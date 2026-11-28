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

- HTTP answers: `httpclient.MarkBusy(resp, err)` (`pkg/infra/httpclient/busy.go`)
  is the one rule, 429 or 503 with `Retry-After` (seconds or HTTP date) as
  the wait. Used by the HTTP sink, `api_lookup`, the AI step (OpenAI, Ollama)
  and `ai_retrieve`'s Pinecone query. `api_lookup` returns a busy answer at
  once rather than spending its own retries, and a busy answer bypasses its
  error policy and default value: writing the default would pass "no room"
  off as the API's answer.

- `pkg/llm`: `APIError` matches `ErrBusy` (`Is`) and yields a `*hermod.Busy`
  (`As`, so `hermod.RetryAfter` reads it) on 429/503/529, after `WithRetry`
  has given up. Exception by code, not text: `insufficient_quota` (OpenAI's
  out-of-credit 429) is neither retryable nor busy -- waiting never ends it.
  `llm.OutOfQuota(body)` is the same rule for callers reading bodies
  themselves (the AI step's direct OpenAI call).

- A waiting step logs at INFO, once per step while any message waits on it
  (`busyWaits.begin` reports the first). Not WARN: waiting is normal under
  load, and the branching load test fails on any WARN Hermod logs.

Not yet covered: the batched lookup path, and the UI word for `waiting`.
