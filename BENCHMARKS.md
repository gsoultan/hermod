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

## Not yet measured

Named explicitly so nothing here is mistaken for full coverage:

- **MySQL / MSSQL / Snowflake sinks.** Only Postgres has the bulk path so far. MySQL
  (`LOAD DATA LOCAL INFILE`), MSSQL (`mssql.CopyIn`) and Snowflake (`PUT` + `COPY INTO`) are still
  row-by-row. Snowflake is the most costly of these — row-by-row into a warehouse is pathological.
- **Bulk path over a real network**, where the round-trip saving should be far larger than measured
  here on localhost.
- **Traversal cost per DAG node** — how the goroutine-per-node model scales with DAG width/depth.
- **CDC end-to-end lag** from Postgres commit to sink write.
- **Memory**: the "<80 MB idle RSS" target in `README.md` has no benchmark behind it.
- **UI**: bundle size and canvas frame time on large DAGs.
