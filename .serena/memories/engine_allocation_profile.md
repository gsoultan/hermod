# What the engine actually allocates per message

`BenchmarkEngineThroughput` (`pkg/engine/bench_test.go`) drives 50k messages
through an in-memory source and sink with **no workflow and no
transformations**. That bare pass-through allocated **69 objects and 93 KB per
message** at a 16 KB payload — 5.6x the payload, in garbage, to move it.

Profile it, do not guess:

```bash
go test ./pkg/engine -bench='BenchmarkEngineThroughput$' -benchtime=1x -run='^$' \
  -benchmem -cpuprofile=/tmp/cpu.prof -memprofile=/tmp/mem.prof -o /tmp/engine.test
go tool pprof -sample_index=alloc_space -list='Engine..writeToSink$' /tmp/engine.test /tmp/mem.prof
```

`-list` is the one that pays: the top-level view blamed `bytes.Clone`, which is
true and useless. Line attribution named the actual callers.

## What it was (measured, 150k messages across three payload sizes, 4.9 GB)

| site | share | what it was doing |
|---|---|---|
| `writer.go` batch loop | 1.86 GB / 38% | cloning the whole payload to add `len()` to `batchBytes` — which nothing reads unless `BatchBytes > 0`, and that is **not** the default |
| `writer.go` write-success log | 0.94 GB / 19% | `payload_len` argument of a **`Debug`** line, at the default `Info` level |
| `TryFixJSON` | 0.83 GB / 16% | `string(data)` — a full copy of the body — to look at its first character |

All three are the same shape: **a cheap number computed the expensive way, then
discarded.** Two were invisible because the *consumer* was disabled (a log
level, an unset config), not the producer.

## The traps

- **A log level does not stop argument evaluation.** `logger.Debug("...",
  "payload_len", payloadLen(msg))` runs `payloadLen` whatever the level is.
  zerolog is zero-allocation *after* the call, which is exactly why this hid.
  Guard with `debugEnabled(e.logger)` (`pkg/engine/writer.go`); the logger side
  is `DefaultLogger.DebugEnabled()`.
- **`Payload()` clones, by contract** (`bytes.Clone`), and there is no
  `PayloadRef` on purpose — a message can be `Reset` under you. Use
  `PayloadLen()` when a size is all you need; it materialises the data map the
  same way `Payload()` does so the two can never disagree.
- **`decodePayloadFields` runs on every non-JSON body**, which for a file, CSV,
  text or queue source is every message. It also runs on the **first `SetData`**
  of any message that has a payload. `canBeJSONObject` now short-circuits it,
  but note the surviving cost: a non-JSON body is still copied once, because the
  decoded map has to own it as a string. That is inherent, not a bug.
  `null` must keep reaching the object attempt — it unmarshals into a nil
  `map[string]any` *without error*, which is why the guard is "can be an object"
  and not "starts with `{`".

## Result

+27% throughput, -42% bytes, -21% allocations (geomean, benchstat n=6,
p=0.002) — before any transformation is involved. See
[`field_access_cost.md`](field_access_cost.md) for the transformation path,
which was far worse.

## Fixed since

**`regexp.Compile` per message per condition** (`evaluator.go`,
`EvaluateConditions` `regex`/`not_regex`): 1931ns / 5958 B / 69 allocs ->
**294ns / 104 B / 4 allocs**, against a floor of 200ns / 1 alloc for a match
against an already-compiled pattern. `compilePattern` holds a bounded cache
(512 entries, batch eviction over Go's randomised map order). It is bounded
because **the pattern is not necessarily static**: `EvaluateConditions`
resolves a value containing `{{ }}` against the message's own data first, so
the pattern can be derived from message content. Failed compiles are cached
too, so a stream carrying the same malformed pattern pays once.

> Still open, and a correctness bug rather than a performance one: `match` is
> initialised to `false`, so an **invalid pattern rejects every message**, with
> nothing logged. A typo in a router filter silently drops 100% of traffic.
> Same shape as [`retention_sweep_and_trace_growth.md`](retention_sweep_and_trace_growth.md)
> — a parse failure that silently disables something.

## Not fixed, ranked

1. **OTel span per sink write** (`writer.go`, `tracer.Start`): ~7.7 allocs per
   message through `global.(*tracer).newSpan` even with **no** TracerProvider
   installed — 15% of remaining allocation count.
2. **`recordTraceStep` spawns an unbounded goroutine + a 5s
   `context.WithTimeout` per node per message** (`pkg/engine/telemetry_methods.go:88`).
   Harmless at the default `TraceSampleRate: 0`, a memory incident at 1.0.
   `internal/engine/registry` already has the right shape for this: a
   `backgroundTasks` semaphore that drops under pressure.
