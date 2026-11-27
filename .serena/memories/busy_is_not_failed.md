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

Not yet covered: transformation steps (`db_lookup`, `execute_sql`) and pool
acquisition waits, which still surface as timeouts.
