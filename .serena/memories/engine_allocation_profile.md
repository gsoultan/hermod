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

## Second pass: the logger had no level

`DatabaseLogger` (`internal/engine/registry/logger.go`) had **no level filter at
all** — only sampling, off by default. So moving the per-write success line from
`Info` to `Debug` silenced zerolog and changed nothing there: a healthy pipeline
still wrote **one row to the `logs` table per message**.

And because it could not report a level, the engine's `debugEnabled()` guard —
which defaults to "yes, log", deliberately, since dropping output because you
could not ask is worse — let the payload measurement run, marshalling every
message's data map to JSON. At 32 columns: 754k allocations for the marshal plus
295k for the log entry, ~30% of everything the workflow allocated. **One bug,
two hats.** It now honours `HERMOD_LOG_LEVEL` and implements `DebugEnabled()`.

Also fixed: `ParseConditions` re-unmarshalled its JSON per message (1126ns / 24
allocs -> 117ns / 5, cached per config, bounded, result copied out); and a
pooled message kept whatever it grew to — `clear()` keeps a map's buckets,
`[:0]` keeps a buffer's capacity, so one 8 MB payload pinned that for the life
of the process (`maxPooledBufferBytes` 1 MiB, `maxPooledMapEntries` 512).

Cumulative end-to-end: **+30% throughput, -69% bytes, -69% allocations** over
the b0703d7 baseline (geomean; +71% / -75% at 128 columns).

## The profile reading that was wrong

I blamed the traversal's goroutine-per-node model for "75% of CPU in
`runtime.usleep` + `pthread_cond_wait`". Wrong twice:

1. That profile was **`pkg/engine`'s BenchmarkEngineThroughput, which runs no
   workflow** — it never exercised the traversal at all. Profile the thing you
   are about to change.
2. In the *workflow* profile those symbols are mostly **idle threads**, not
   contention. 1.46s of samples over 401ms of wall time at 363% is ~3.6 of 15
   cores busy. High `usleep`/`cond_wait` on a mostly-idle machine means *not
   CPU-bound*, not *contended*. Check `samples / duration` before reading
   scheduler symbols as overhead.

Measured properly, `resolveEdge` is 0.68% of CPU flat and the whole traversal
~2%. A workflow's cost is allocation, not scheduling. The goroutine-per-node
model was left alone: it owns the fan-out ownership contract
([[fanout_traversal_and_two_foreaches]]) and is where the expensive bugs have
been. Inlining a downstream node would also hold the current node's
`AcquireNode` slot for the whole walk below it, which is the deadlock
`forkFanout`'s comment already warns about.

## Reading an allocation profile without fooling yourself

Three separate times a top-of-profile entry turned out not to be a production
cost. Check these before acting on any figure:

1. **Is the benchmark exercising the thing?** "75% of CPU in `runtime.usleep`"
   came from `BenchmarkEngineThroughput`, which **runs no workflow**, and was
   used to blame the traversal. It never ran the traversal.
2. **Is it idle or contended?** Compare `Total samples` against `Duration`.
   1.46s of samples over 401ms at 363% is ~3.6 of 15 cores busy — those
   `usleep`/`pthread_cond_wait` samples are idle threads, not contention.
3. **Is it the harness?** `fmt.Sprintf` was 16% of the workflow profile and
   **100% of it was the benchmark's own source fixture** formatting column
   names per message. Fixed, the per-message budget fell from 158 to 101 at 32
   columns with no production change at all.
4. **Is it amortised startup?** `GetStatus` under `notifyStatusChange` was 7.8%
   — engine start and stop, which the benchmark pays once per iteration and
   divides across 20,000 messages. Zero per-message cost in production.

`pprof -list='<func>'`, not `-top`. The top view names the allocator
(`bytes.Clone`, `encoding/json`, `reflect.unsafe_New`), which is true and
almost never the useful answer.

## The CI allocation gate

`TestEngineAllocationBudget` and `TestWorkflowAllocationBudget` budget
allocations per message and fail past +25%; CI runs them in their own step.
Allocations, not time: counts held within 0-2% across sessions while throughput
swung 5-56% with machine load.

Both are **mutation-tested** — a logger that claims Debug is on fails them at
every size. A gate that cannot fail is decoration. Note the mutation test
itself nearly passed for a bad reason: `sed` missed because gofmt had aligned
the target line with extra spaces, so the mutation never applied. Verify the
mutation landed before believing the gate caught it.

`TestEveryShortGuardedTestIsRunSomewhere` read only the *first* `-run`
expression in `ci.yml`, so a second step running short-guarded tests would have
been reported as running nowhere. It reads all of them now.

## Still not fixed, ranked

1. **OTel span object + `context.WithValue` per sink write and per node** —
   ~480k of 2.24M allocations at 32 columns (21%). The attributes are gone; the
   span itself and putting it in the context remain. Skipping span creation
   needs a reliable "is any real provider installed" test, and getting that
   wrong breaks tracing silently.
2. **`maps.clone`, ~11%** — `Data()` and `Metadata()` clone by contract,
   because handing out the live map races with a concurrent write.
   `MetadataValue(key)` is the pattern that fixed it for single-key reads;
   `DataValue(key)` would do the same for the rest.
3. **`ParseConditions` triple form, ~4%** — the single field/operator/value
   shape builds a slice and a map per message. Deliberately left: avoiding it
   means handing callers a cached slice they could mutate, and one workflow's
   filter silently rewriting another's is worse than the allocation.
4. **`SetEngineStatusUnless` returns true on *set*, not on *change***
   (`pkg/engine/telemetry/status.go`), so `checkHealth` calls
   `notifyStatusChange` on every 1s tick even when nothing moved — and the
   registry's callback writes workflow, source and sink status rows to storage.
   Not a per-message cost, but a steady pointless write. Same shape the
   reliability commit warned about for dead-lettering.
