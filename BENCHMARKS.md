# Hermod Benchmarks

Measured baselines. **Every performance number Hermod publishes must trace back to a benchmark in
this file.** Before this file existed, all throughput figures in `README.md` were unverified targets.

## Host

| | |
|---|---|
| CPU | Apple M5 Pro (15 usable cores reported by Go) |
| GOOS / GOARCH | darwin / arm64 |
| Date | 2026-08-05 |

Numbers are hardware-specific. Re-measure on your own host before quoting them; what transfers
between machines is the *ratio* between configurations, not the absolute rate.

## Reproducing

```bash
# Engine end-to-end throughput (in-memory source and sink)
go test ./pkg/engine -bench=. -benchtime=1x -run='^$' -timeout=600s

# Message pooling and payload microbenchmarks
go test ./pkg/comm/message -bench=. -benchmem -run='^$'

# End-to-end workflow throughput (source -> condition -> mapping -> sink)
go test ./internal/engine/registry -bench=BenchmarkWorkflowThroughput -benchtime=1x -run='^$' -benchmem

# Field-access microbenchmarks (every transformation, condition and mapping)
go test ./pkg/infra/evaluator -bench=. -benchmem -run='^$'

# Sink integration benchmarks (require real infrastructure)
HERMOD_INTEGRATION=1 POSTGRES_DSN='postgres://...' go test ./pkg/comm/sink/postgres -bench=. -run='^$'
```

`-benchtime=1x` is intentional for the engine benchmarks: each iteration drives 50,000 messages
through a full engine start/drain cycle, so `b.N` scaling multiplies wall time without improving
signal.

---

## Engine throughput

In-memory source and sink, so these isolate engine overhead — traversal, message pooling,
backpressure, buffer handoff — from network and disk cost. **This is the engine's ceiling.**

### By payload size

| Payload | Throughput | ns/op (50k msgs) |
|---|---|---|
| 64 B | 101,249 msgs/s | 493,939,625 |
| 1 KB | 118,285 msgs/s | 422,742,292 |
| 16 KB | 45,456 msgs/s | 1,100,060,666 |

**The engine sustains ~100k msgs/s at 1 KB payloads** — roughly 5–20× higher than the
"5–20k msgs/s" figure previously stated in `README.md`. The engine is not the bottleneck in a
Hermod pipeline; sinks are. Tuning effort belongs at the sink.

### By in-flight cap

| `max_inflight` | Throughput |
|---|---|
| 16 | 112,053 msgs/s |
| 128 (default) | 123,758 msgs/s |
| 512 | 101,503 msgs/s |

The default of 128 is well chosen. Raising it to 512 *reduces* throughput ~18%, so "increase
max_inflight for more speed" is not sound advice on its own — see the interaction below.

### Batching

| `batch_size` | Throughput |
|---|---|
| 1 | 109,025 msgs/s |
| 100 | 124,877 msgs/s |
| 500 | 110,829 msgs/s |

---

## Regression guard: `batch_size` vs `max_inflight`

`BenchmarkBatchVsInflight` exists because the first run of this harness found a **43× throughput
cliff** in the configuration `README.md` recommended.

A batch fills from messages that are currently in flight. When `batch_size` exceeds `max_inflight`
the batch can never complete on count, so every flush falls through to the `batch_timeout` path.
With 50,000 messages, a 128-message in-flight cap and a 50 ms timeout, that is
50,000 ÷ 128 × 50 ms ≈ 19.5 s — matching the measured 19.55 s exactly.

| Configuration | Before fix | After fix |
|---|---|---|
| `batch=500, inflight=128` (README's recommended combo) | **2,557 msgs/s** | **110,829 msgs/s** |
| `batch=500, inflight=1024` | 110,848 msgs/s | 110,848 msgs/s |
| `batch=100, inflight=128` | 110,852 msgs/s | 110,847 msgs/s |

**Fix**: `effectiveBatchSize` (`pkg/engine/batch_sizing.go`) clamps the batch size to
`max_inflight` and logs a warning naming both values. Clamping down rather than raising
`max_inflight` is deliberate — that cap exists to bound memory, so raising it silently would trade
an invisible latency problem for an invisible memory one.

Covered by `TestEffectiveBatchSize` (`pkg/engine/batch_sizing_test.go`) and
`BenchmarkBatchVsInflight` (`pkg/engine/bench_test.go`).

---

## Message pooling

`-benchtime=100x`, from `pkg/comm/message`.

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| `AcquireRelease` (pooled) | 76.67 | 23 | 0 |
| `NoPool` | 184.6 | 848 | 7 |
| `MessagePayload` first call (marshal) | 1,073 | 356 | 9 |
| `MessagePayload` cached | 16.25 | 48 | 1 |
| `MessageSetData` simple key | 82.50 | 2 | 0 |
| `MessageSetData` nested key | 225.0 | 54 | 1 |
| `SanitizeValue` string | 20.00 | 16 | 1 |
| `SanitizeValue` uuid | 463.3 | 80 | 3 |
| `SanitizeValue` ptr string | 722.5 | 16 | 1 |

Pooling is worth **2.4× on time and 37× on bytes allocated**, and takes the steady-state path to
zero allocations. Payload caching is worth 66×, so repeated `Payload()` calls are cheap — but the
first call is not, which matters for transforms that touch the payload once per node.

`SanitizeValue` on a pointer-to-string is 36× the cost of a plain string, and UUID handling
allocates three times. Both are candidates if `SanitizeValue` shows up in a profile.

---

## Postgres sink: bulk-load fast path

Measured against PostgreSQL 18.4 in a local Podman container, so round-trip latency is near zero —
a real network will lower every figure here, and lower the ordered path far more than the COPY path
because that one is round-trip bound.

`WriteBatch` originally applied one statement per message inside a single transaction. An
insert-only batch has no observable ordering, so it now streams into a TEMP staging table via
`pgx.CopyFrom` and merges with a single `INSERT … SELECT … ON CONFLICT` (`bulk.go`).

| Batch size | Ordered path | COPY fast path | Speedup |
|---|---|---|---|
| 100 rows | 3,316 rows/s | 8,240 rows/s | 2.5× |
| 1,000 rows | 5,814 rows/s | 58,327 rows/s | **10.0×** |
| 5,000 rows | 6,223 rows/s | **100,881 rows/s** | **16.2×** |

For reference, SSIS OLE DB Destination with Fast Load lands around 50k–150k rows/s. The 10–50×
deficit identified in the competitive review is closed for insert-only batches.

**The fast path is opt-out by construction.** `classifyBatch` returns `bulkModeCopy` only when every
safety condition is positively established — insert-only, one target table, mappings present, no
soft-delete rewriting, at least `bulkMinRows` (50) rows. Anything else, and anything uncertain,
falls back to the ordered path that preserves CDC semantics.

Guarded by:
- `TestBulkCopyMatchesOrderedPath` — differential: the same batch written both ways must produce
  byte-identical table contents, including last-wins on duplicate keys within a batch.
- `TestMixedOperationBatchStaysOrdered` — a delete followed by a re-insert of the same key must not
  take the fast path, and must still produce the re-inserted row.
- `TestClassifyBatch` — 9 cases covering each disqualifying condition.

## Workflow throughput

Measured 2026-09-19 on the host above, `BenchmarkWorkflowThroughput`
(`internal/engine/registry/workflow_bench_test.go`). 20,000 messages per
iteration through a real `source -> condition -> mapping -> sink` graph.

**This is the number to quote for a pipeline.** `## Engine throughput` above
measures the engine with no workflow at all — in-memory source straight to
in-memory sink — so it never touches the evaluator, and the evaluator is where a
real pipeline spends its time. The two differ by roughly 2x for that reason.

Row width is the axis that matters, because reading a field used to cost O(row).

| Columns | Throughput | B/op | allocs/op |
|---|---|---|---|
| 8 | 87,300 msgs/s | 172.2 MiB | 3.240 M |
| 32 | 75,140 msgs/s | 204.7 MiB | 5.178 M |
| 128 | 42,270 msgs/s | 340.6 MiB | 12.86 M |

### Against the 2026-09-18 baseline

benchstat, n=7 each side, all p=0.001. Baseline is commit `b0703d7`.

| | Throughput | Bytes allocated | Allocations |
|---|---|---|---|
| 8 columns | +25.8% | −52.8% | −45.6% |
| 32 columns | +15.8% | −55.5% | −48.5% |
| 128 columns | +18.0% | −60.7% | −51.3% |
| **geomean** | **+19.8%** | **−56.5%** | **−48.5%** |

The same change measured on the engine benchmark (no workflow, so no evaluator):
**+17.4% throughput, −46.0% bytes, −27.7% allocations**, geomean over the three
payload sizes. At a 16 KB payload the engine's garbage per message went from
92.7 KB — 5.7x the payload — to 38.7 KB.

Throughput has run-to-run variance of 5–18% on a laptop and the geomean has been
observed between +17% and +27% across sessions; the allocation figures are
deterministic (±0–1%) and are the ones to hold a regression against.

### Where it went

Profile with `-list`, not `-top`: the top view blamed `bytes.Clone` and
`encoding/json`, which is true and useless.

```bash
go test ./pkg/engine -bench='BenchmarkEngineThroughput$' -benchtime=1x -run='^$' \
  -memprofile=/tmp/mem.prof -o /tmp/engine.test
go tool pprof -sample_index=alloc_space -list='Engine..writeToSink$' /tmp/engine.test /tmp/mem.prof
```

| Site | Was | Cause |
|---|---|---|
| `evaluator.GetValByPath` | O(row) per field read | marshalled the whole row to JSON, then gjson-parsed one field back out |
| `writer.go` batch loop | 1.86 GB / 38% | cloned every payload to sum `batchBytes`, which nothing reads unless `BatchBytes > 0` — not the default |
| `writer.go` write-success log | 0.94 GB / 19% | the `payload_len` argument of a **`Debug`** line, at the default `Info` level |
| `message.TryFixJSON` | 0.83 GB / 16% | `string(data)` — a full copy of the body — to look at its first character |
| `tracing.messageCarrier.Get` | 2 map clones/message | cloned all metadata to read one propagation header |
| `writer.go` span attributes | ~7.7 allocs/message | built eagerly for a span nobody records when no TracerProvider is installed |

## Workflow throughput: second pass

Measured 2026-09-19, same benchmark, against the same `b0703d7` baseline — so
these are **cumulative** with the figures above, not additional to them.

| | Throughput | Bytes allocated | Allocations |
|---|---|---|---|
| 8 columns | +10.1% | −61.3% | −61.1% |
| 32 columns | +17.8% | −66.8% | −68.6% |
| 128 columns | **+70.7%** | **−77.5%** | **−75.3%** |
| **geomean** | **+30.3%** | **−69.0%** | **−68.9%** |

benchstat n=7+8, p<=0.021. Throughput geomean has read between +30% and +50%
across sessions depending on machine load; the allocation figures are
deterministic (±0–2% across three separate runs) and are what a regression
should be held against. 128 columns gains most because the dominant cost
scaled with row width.

### What it was

| Site | Was | Cause |
|---|---|---|
| `DatabaseLogger.log` | 295k allocs + a DB row per message | **no level filter at all** — every `Debug` line was built and persisted |
| `writeToSink` payload measure | 754k allocs (32/message at 32 columns) | the engine's `debugEnabled()` guard assumes "yes" for a logger that cannot report a level, so the marshal it exists to prevent ran anyway |
| `evaluator.ParseConditions` | 1,126 ns / 24 allocs per message | re-unmarshalled the same conditions JSON for every message |
| pooled message footprint | unbounded | `clear()` keeps a map's buckets and `[:0]` keeps a buffer's capacity, so one outlier message pinned its size in the pool for the life of the process |

The first two are one bug wearing two hats. Moving the per-write success line
from `Info` to `Debug` silenced zerolog but not the database logger, which had
no notion of level — so a healthy pipeline still wrote one log row per message,
*and* still marshalled each message's data map to report a size for it.

### What the profile said not to do

The traversal spawns a goroutine per node per message, and an earlier reading of
the **engine** profile put 75% of CPU in `runtime.usleep` and
`pthread_cond_wait`. That reading was wrong twice over: the engine benchmark
runs no workflow, so it never exercised the traversal at all; and in the
workflow profile those same symbols are mostly **idle** threads — 1.46 s of
samples over 401 ms of wall time at 363% means ~3.6 of 15 cores busy, not
contention.

Profiled properly, `resolveEdge` is 0.68% of CPU flat and the whole traversal
about 2%. A workflow's cost is allocation, not scheduling, so the
goroutine-per-node model was left alone rather than restructured — that code
owns the fan-out ownership contract and is where the expensive bugs have been.
The one spawn removed is `Traverse`'s, which created a goroutine and
immediately waited for it.

## Allocation budgets (CI gate)

`TestEngineAllocationBudget` (`pkg/engine`) and `TestWorkflowAllocationBudget`
(`internal/engine/registry`) measure allocations per message against a recorded
figure and fail past +25%. CI runs them in their own step.

They budget **allocations, not time**. Across three measurement sessions the
allocation counts held within 0–2% while throughput swung 5–56% with machine
load, so a time gate on shared CI hardware would be a flake generator and this
is not.

| Budget | Recorded allocations/message |
|---|---|
| engine, 64 B / 1 KB / 16 KB payload | 39 / 39 / 40 |
| workflow, 8 / 32 / 128 columns | 116 / 158 / 326 |

Both are **mutation-tested**: reintroducing the regression they exist for (a
logger that claims Debug is on, so the per-write line marshals the message)
fails them at every payload size and row width, and restoring it passes them.
A gate that cannot fail is decoration.

They exist because the same bug class landed twice — a log line whose arguments
are evaluated whatever the level does with them, costing 19% of engine
allocations the first time and ~30% of workflow allocations the second.
Invisible to a test, a lint and a review; visible only in a profile somebody
happened to take.

### The engine benchmark was measuring a configuration nothing runs in

`benchLogger` did not implement `DebugEnabled()`, and a logger that cannot
report a level is assumed to want the line. So the per-write debug line's
payload measurement — a full JSON marshal of the message's data map — ran for
every message in the benchmark and, since `DefaultLogger` and `DatabaseLogger`
both report their level now, for no message in production.

With the benchmark corrected to match, the engine's figures are:

| Payload | Throughput | B/op (50k msgs) | allocs/op | Garbage per message |
|---|---|---|---|---|
| 64 B | 128,624 msgs/s | 110.0 MB | 1.934 M | 2.2 KB |
| 1 KB | 115,510 msgs/s | 160.9 MB | 1.935 M | 3.2 KB |
| 16 KB | 79,906 msgs/s | 1.013 GB | 1.958 M | 20.3 KB |

Against the original `b0703d7` baseline that is **−44% allocations** and, at a
16 KB payload, **92.7 KB of garbage per message down to 20.3 KB** — from 5.7x
the payload to 1.24x.

## Field access

Measured 2026-09-19, `pkg/infra/evaluator/path_bench_test.go`. Every
transformation, router condition and sink column mapping goes through these.

| Benchmark | Before | After |
|---|---|---|
| one field read, 8-column row | 1,035 ns / 24 allocs | 29 ns / 1 alloc |
| one field read, 32-column row | 3,925 ns / 78 allocs | 33 ns / 1 alloc |
| one field read, 128-column row | 17,890 ns / 294 allocs | 32 ns / 1 alloc |
| 6-placeholder template, 8-column | 6,706 ns / 143 allocs | 490 ns / 9 allocs |
| 6-placeholder template, 128-column | 106,110 ns / 1,763 allocs | 576 ns / 9 allocs |
| 2-condition filter, 32-column | 8,215 ns / 158 allocs | 250 ns / 5 allocs |
| regex condition | 1,931 ns / 69 allocs | 294 ns / 4 allocs |
| one field **write**, 128-column row | 44,121 ns / 33,088 B / 694 allocs | 731 ns / 32 B / 2 allocs |

Reading one field is now flat in row width, where it used to be linear — so a
sink with N mapped columns over a row of W fields went from O(N x W) to O(N).

The write side (`SetValByPath`) is the same shape and was slower still — it
marshalled the map, `sjson`-set one field, unmarshalled it all back, then
cleared the map and refilled it. It gets a narrower fast path, because the
round trip there has a second effect: it JSON-normalises every **untouched**
value as well. So the targeted write is taken only when the map is already
all-JSON-native — exactly when that side effect would have been a no-op — and
anything else still goes the long way (`BenchmarkSetValByPathRoundTrip`
measures that path). Allocations are then flat in row width; wall time is still
O(row), because checking the map is native is itself a scan, but ~60x lower.

> Note: `SetValByPath` has **no production caller** — its only caller is a
> test-only wrapper in `internal/engine/registry/registry_routing.go`. It is
> exported from `pkg/`, so it was made fast and kept exactly equivalent, but
> nothing in a running pipeline pays either cost.

The JSON round trip was not pure overhead: it is what normalises `int` to
`float64` and `[]byte` to a base64 string, and everything downstream is written
against that shape. `TestGetValByPathMatchesJSONRoundTrip` and
`TestSetValByPathMatchesJSONRoundTrip` keep the old implementations verbatim as
oracles and diff the two over a matrix of rows x paths x values; each fast path
deliberately falls back for anything it cannot reproduce exactly.

That matrix earns its keep: it caught **sjson and `json.Marshal` disagreeing on
`[]byte`** — sjson writes the literal string, `json.Marshal` writes base64 — so
the write fast path handles only value types it has been proved equivalent for
and hands the rest to sjson.

## Not yet measured

Named explicitly so nothing here is mistaken for full coverage:

- **MySQL / MSSQL / Snowflake sinks.** Only Postgres has the bulk path so far. MySQL
  (`LOAD DATA LOCAL INFILE`), MSSQL (`mssql.CopyIn`) and Snowflake (`PUT` + `COPY INTO`) are still
  row-by-row. Snowflake is the most costly of these — row-by-row into a warehouse is pathological.
- **Bulk path over a real network**, where the round-trip saving should be far larger than measured
  here on localhost.
- **Traversal cost per DAG node** — how the goroutine-per-node model scales with DAG width/depth.
  `BenchmarkWorkflowThroughput` measures a fixed 4-node graph, so it prices the model but does
  not vary width or depth. 75% of engine CPU sits in scheduler wait (`runtime.usleep` +
  `pthread_cond_wait`), which is where that measurement should start.
- **CDC end-to-end lag** from Postgres commit to sink write.
- **Memory**: the "<80 MB idle RSS" target in `README.md` has no benchmark behind it.
- **UI**: bundle size and canvas frame time on large DAGs.
