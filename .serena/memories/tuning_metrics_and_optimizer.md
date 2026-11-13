# The Tuning tab's numbers, and the optimizer

## What the status frame carries (#342)

`Engine.GetStatus` (`pkg/engine/engine.go`) now fills two fields it used to leave
at 0 on every flow:

- `ErrorRate` = (node failures + dead-lettered) / (processed + node failures),
  capped at 1. A node failure is never acked so it is not in `processed`; a
  dead-lettered message *is* acked, so it is already in `processed` and only adds
  to the numerator. See [ack is not always a success](ack_is_not_always_a_success.md).
- `Backpressure` = the **fullest** sink buffer, not the average: one jammed sink
  among idle ones is the reading to act on.
- Average latency is recorded whether or not adaptive throughput is on (it was
  only recorded with it on, and it is off by default).

UI: `ui/src/pages/workflows/tuning/TuningMetrics.tsx` — tiles (throughput, error
rate, backpressure, latency, processed, dead-lettered), a per-sink table (buffer
fill, breaker) and a per-step table (avg time, runs, failures). It says when the
flow is off instead of showing zeros. The feed is `useWorkflowWebSockets`'
`onStatus` callback.

## The optimizer (`internal/optimizer/optimizer.go`)

Every 30s per registered engine:

1. ~~Rule-based batch tuning~~ **removed (2026-11-13)**. It rewrote a running
   sink's batch size from `SinkBufferFill`, but `runOn` in `pkg/engine/writer.go`
   reads the batch size once into a local when the writer starts, so the change
   never reached the writer: it only mutated the engine's copy of the config.
   A full sink is now a recommendation, see
   [the performance advisor](performance_advisor.md).
   `TestOptimizerLeavesSinkSettingsAsTheOperatorSavedThem` holds it.
2. Self-correction gates (`gate.Analyze`).
3. AI pass — **`EnableAI: false` by default**, not exposed as a setting. Its
   triggers read ErrorRate/Backpressure, which were always 0, so it had never
   run. Left on with real values it would send a failing flow's status (payload
   samples included) to an LLM every cycle and discard the reply, and raise a
   busy flow's concurrency without a ceiling. Guarded by
   `TestNewOptimizerLeavesTheAIPassOff`.

**Fixed (2026-11-13):** `optimizeAll` used to call `runChaosMonkey`, which with
probability 0.001 per 30s cycle made every sink write of a random workflow fail
with `simulated engine failure` for 10s, about one outage every ~8 hours in
production. It was removed with `Engine.SimulateFailure` and `isFailing` (a lock
and a clock read per sink write). `TestOptimizerNeverInjectsFailures` runs 20,000
cycles and fails on any injected failure. Fault injection belongs in tests.

## Sink buffer fill was wrong for a stuck sink (fixed 2026-11-13, #383)

Fill was `len(ch)/cap(ch)` with a 1000-slot channel, but at most `MaxInflight`
(128) messages can be waiting, so a fully stuck sink read 12.7% (2.8% when
batching) and the 80% backpressure warning could not fire. `sinkWriter` now
counts what it holds (`held`) and `fill(inflight)` measures against
min(channel cap, in-flight limit). `pkg/engine/sink_buffer_fill_test.go`.
