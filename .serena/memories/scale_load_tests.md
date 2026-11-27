# Scale load tests (internal/test/scale)

What one more workflow costs, and what thousands of workflows on shared sources
and sinks do to delivery. Both tests skip under `-short`; CI runs them small in
the "Test (scale)" step. Full-size numbers and commands are in BENCHMARKS.md
("Many workflows on shared sources and sinks"); the plan they measure is the
scaling plan (phases 0-6: load test, per-database connection budget, budget
across workers, shared readers/writers, idle workflows cost ~nothing,
backpressure/priority, caduceus driver).

- **TestScaleIdleFootprint** — N workflows whose source never produces; reports
  goroutines and live heap per workflow. Needs no database. Budget constants
  (`idleGoroutineBudget`, `idleHeapBudget`) are today's measurement plus
  headroom; the change that makes idle workflows cheap lowers them.
- **TestScaleSharedDatabase** — real Postgres via `HERMOD_SCALE_DSN` (point it
  at PgBouncer for the production path). Real polling source, `set` step, DB
  Lookup on every 2nd workflow, real Postgres sink with column mappings in
  insert mode. Success = distinct (wf, src_id) rows across the sink tables
  (view `hermod_scale.deliveries`); delay = `received_at - created_at`, read
  in SQL. Peak connections sampled from `pg_stat_activity`, harness's own
  connections excluded by `application_name`.

Traps found building it:

- **Node executors and transformers register by import.** A test that builds a
  registry without `_ ".../registry/nodes/core"` and the transformer packages
  runs `transformation` nodes as silent pass-throughs: no error, no log, the
  sink just gets the unchanged message. Mirror cmd/hermod's imports.
- `set` takes `column.<path>` keys whose values are expressions: a literal
  string needs quotes, `"'value'"`.
- The Postgres sink without mappings upserts `(id, data)` on the message id,
  which is the source row id: many workflows writing one table then contend on
  the same rows. Use mappings + `operation_mode=insert` for load tests.

Baseline (2026-11-27, 4 vCPU): an idle workflow is 139 goroutines and ~172 KiB,
128 of them `runBufferToSink`'s per-engine message workers. 1,000 -> 3,000
workflows on one database took delay p50 0.5 s -> 12.9 s at 100 of 300
connections: the worker, not the database, is the bottleneck at that size.
