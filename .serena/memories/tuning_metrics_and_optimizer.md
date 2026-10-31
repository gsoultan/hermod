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

1. Rule-based batch tuning from `SinkBufferFill`: fill > 0.8 raises batch size
   x1.2 (max 10000) and batch timeout x1.1 (max 2s); fill < 0.1 after 1000
   processed lowers batch size x0.8 (min 10). It does not touch concurrency or
   retry policy, whatever older README text said.
2. Self-correction gates (`gate.Analyze`).
3. AI pass — **`EnableAI: false` by default**, not exposed as a setting. Its
   triggers read ErrorRate/Backpressure, which were always 0, so it had never
   run. Left on with real values it would send a failing flow's status (payload
   samples included) to an LLM every cycle and discard the reply, and raise a
   busy flow's concurrency without a ceiling. Guarded by
   `TestNewOptimizerLeavesTheAIPassOff`.

**Open hazard, not fixed as of v1.23.0:** `optimizeAll` always calls
`runChaosMonkey`, which with probability 0.001 per cycle picks a random registered
engine and calls `SimulateFailure(10s)`; for those 10s `writer.go` returns
`simulated engine failure` from every sink write. That is roughly one injected
10-second outage every ~8 hours in production, and it now shows up in ErrorRate.
