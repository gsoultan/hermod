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

Final, measured end to end through a real `source -> condition -> mapping ->
sink` workflow (`BenchmarkWorkflowThroughput`, benchstat n=7, p=0.001):
**+19.8% throughput, -56.5% bytes, -48.5% allocations** (geomean over 8-, 32-
and 128-column rows).

On the engine benchmark alone — no workflow, so no evaluator — **+17.4% /
-46.0% / -27.7%**. Throughput has 5-18% run-to-run variance on a laptop and the
geomean has read anywhere from +17% to +27% across sessions; the allocation
figures are deterministic (+/-0-1%) and are what to hold a regression against.
See
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

## Also fixed

- **OTel span attributes on the write path.** `trace.WithAttributes(...)` was
  evaluated before `Start`, so three attribute values and a slice were built
  per message for a span nobody records when no TracerProvider is installed —
  the default. Now set under `span.IsRecording()`. Equivalent **only because
  the sampler does not read attributes**: `internal/observability` builds the
  provider with `WithBatcher` + `WithResource` alone, so it gets the default
  `ParentBased(AlwaysSample)`. An attribute-consulting sampler would need them
  back on `Start`; `TestSinkWriteSpanStillCarriesItsAttributes` is what notices.
- **`tracing.messageCarrier.Get` cloned all metadata to read one header**, and
  the propagator asks twice per write (traceparent, tracestate). `Metadata()`
  clones for a reason — handing out the live map races with `SetMetadata` — so
  the fix is `MetadataValue(key)`, a single lookup under the same read lock,
  reached through an optional interface. Same fix in `recordTraceStep`'s
  lineage read.
- **`recordTraceStep` is slot-bounded** (`maxConcurrentTraceRecords`, 256) and
  drops what it cannot place, counted by `TraceStepsDroppedCount()`. The
  5-second `context.WithTimeout` moved *inside* the goroutine so a dropped step
  never arms a timer.

## Still not fixed, ranked

1. **`SetValByPath`** (`pkg/infra/evaluator/evaluator.go`): 44us / 694 allocs
   for one write into a 128-column row — the untouched write-side twin of
   `GetValByPath`. It round-trips the whole map, which **re-normalises every
   untouched field as a side effect**; that may be load-bearing somewhere, so
   it needs its own parity oracle before being touched.
2. **The condition node re-parses its config on every message**
   (`evaluator.ParseConditions` -> `json.Unmarshal` per message when the node
   stores a `conditions` JSON string). Unmeasured. Wants a prepared form, the
   way `MappingTransformer.Prepare` caches `_parsed_mapping`.
3. **75% of engine CPU is scheduler wait, not work** — `runtime.usleep` 35% +
   `pthread_cond_wait` 27%, `runtime.lock2` 35.6% cumulative. The traversal
   spawns a goroutine per node per message
   (`internal/engine/registry/traversal/traversal.go:147,503`). Whether a
   single-successor node can run inline is the open question; it touches the
   fan-out ownership contract, so it is not a small change.
4. **The message pool retains bucket arrays.** `Reset()` uses `clear(m.data)`,
   which keeps the allocated buckets, so one 10,000-field message pins that
   capacity for the pool's lifetime. Bounded by GC's victim cache, so a
   footprint hazard on mixed workloads rather than a true leak.

## Fixture trap

`recordSpans` (`pkg/engine/trace_propagation_test.go`) built a TracerProvider
per test. **otel's global tracer takes its delegate exactly once per process**,
and the engine's tracer is package-level, so the first test to call the helper
bound it and every later one silently recorded nothing — reported as "no
source.receive span was recorded; spans seen: []", which reads like a defect in
trace propagation rather than a broken fixture. Reproduce the old behaviour
with `-count=2` on a single test. Now one provider is installed once per
process and the recorder behind it is swapped.
