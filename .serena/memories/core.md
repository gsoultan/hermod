### Hermod — memory index

Read this first, then only the memories a task actually needs.

**What Hermod is.** A self-hosted data integration and streaming platform: Go
backend, React 19 UI, single binary. Its defensible position is real-time
Postgres CDC with a visual DAG editor, self-hosted, no JVM and no Kafka cluster
required — not connector count. README.md leads with that.

**Maturity is tiered, and the tiers are load-bearing.** 42 source and 46 sink
connectors are *not* equally deep. README.md's connector table assigns GA / Beta /
Experimental on evidence (GA requires a test against live infrastructure). Moving
a connector up means adding the evidence, not editing the table. Before
recommending a connector for production, check the tier.

**Claims must match code.** The most expensive problem this codebase has had is
documentation ahead of implementation — a README promising LogMiner CDC and Kafka
2PC that the code did not implement. Delivery is **at-least-once** with sink-side
idempotency. 2PC has a real coordinator (`pkg/engine/twopc`) driven by the
transactional sink group, with a durable log, start-up recovery and a reaper —
but PostgreSQL is still the only sink implementing `hermod.TwoPhaseCommit`, so a
group can only span PostgreSQL destinations.
Oracle and DB2 are watermark polling, not log-based CDC — inserts only. If you
find a claim that outruns the code, fix the claim.

### Memories

- [List variables in SQL templates](sql_template_list_variables.md) — why
  `IN ({{.ids}})` expands but `= ANY({{.ids}})` must not, the 65535 cap, and how
  a list gets built in the first place.
- [sqlutil owns dialect differences](sqlutil_owns_dialect_differences.md) —
  row limits, placeholders and quoting live in one place; the Oracle `ROWNUM`
  row-skipping bug is why.
- [Connector conformance suite](connector_conformance_suite.md) — the contract
  every source and sink must pass, the defects it found, and the two traps
  (vendor hostnames, unroutable addresses) that make it slow.
- [Session auth is cookie-only](session_auth_cookie_only.md) — the JWT is not
  reachable from JavaScript; streams authenticate by cookie; logout is real.
- [Postgres sink and polling](postgres_sink_and_polling_updates.md) — singleflight
  connection dedup, non-CDC polling, PgBouncer compatibility.
- [Field encryption transformations](field_encryption_transformations.md) —
  `encrypt`/`decrypt`, the `enc:v1:` tag that makes re-runs idempotent, why
  validation cannot live in `Prepare`, and the inline-key risk.
- [Message payload decoding](message_payload_decoding.md) — a body that is not a
  JSON object is exposed under `payload`; `SetAfter` is a no-op alias for
  `SetPayload`, and the real data loss was an ignored unmarshal error in both
  `MarshalJSON` and `ToMap`.
- [Reachability tests](reachability_tests.md) — a feature configured through storage
  needs one test that starts from storage; three shipped bugs had full unit and
  integration coverage of the parts and none of the assembly.
- [dashboard_history footprint](dashboard_history_footprint.md) — the only
  append-only table; measured 21 -> 14.08 MB/week/series, the env knobs that
  shrink it, and why ErrNotSupported is a storage decision.
- [Retention sweeps and trace growth](retention_sweep_and_trace_growth.md) —
  `time.ParseDuration` cannot read the UI's default `7d`, so the trace purge
  silently never ran and PostgreSQL grew 50 GB in hours.
- [The metis connectors](metis_connectors.md) — a BPMN engine source and sink;
  why a 5xx is an *unknown* outcome here but a refusal in panmail, the
  time-plus-ties cursor, and the unpushed SDK the `go.mod` replace depends on.
- [How the editor captures a source sample](editor_sample_capture_path.md) — an
  empty "Available Fields" list is an upstream sample problem; Test Connection is
  what fires sampling, `SamplePanel` is dead code, and a failed sample is only a
  toast.
- [Sink form fall-through, and the panmail sink](sink_form_fallthrough_and_panmail.md)
  — `configComponents[type] || 'database'` silently rendered the database form
  for twelve sink types, making two of them unconfigurable; and why the panmail
  sink keeps its idempotency claim when a send's outcome is unknown.
- [panmail: templated routing fields](panmail_templated_routing_fields.md) — why
  templating `base_url`/`api_key` turns the SDK client into a per-message
  resource and its cache into a map keyed by row data, the mandatory
  `allowed_hosts` bound, and why a *static* gateway must stay out of the derived
  idempotency key.
- [The FCM sink](fcm_sink.md) — FCM's one-destination and 4096-byte rules, why
  batching is opt-in when there is no idempotency key, and the `option.WithEndpoint`
  seam that makes the wire format assertable.
- [A jsonb column has two shapes](jsonb_shape_differs_by_path.md) — pgx decodes
  it to an object on the snapshot path, pgoutput hands it over as a string on the
  CDC path, and an unchanged TOASTed one is dropped from the UPDATE image
  entirely.
- [The lookup cache is a second write path](lookup_cache_fast_path.md) — a
  cache hit skipped `flattenInto`, and with no TTL set that meant every message
  after the first; the test fake that "caches" nothing could never catch it.
  The key itself was worse: built from *unresolved* template text, so in query
  mode every message in a workflow shared one entry. Same class in `api_lookup`
  (headers and the credential applied after the key) and in the batcher closure.
  When auditing a cache key, list every input applied *below* where it is built.
- [Reading a message trace](message_trace_shape.md) — `workflow_start` and
  `router` are engine pseudo-nodes, the `router` *is* the whole traversal, and
  `before_data` is not stored but reconstructed from the previous step's
  `after`. Query `message_trace_steps` before theorising about stale data.
- [Workflow dependency references](workflow_dependency_references.md) — a
  workflow names its sources in four places, not one; walking nodes for
  `type == "source"` is the wrong answer, and the source delete guard still
  gives it.
- [SMTP template time helpers](smtp_template_time_helpers.md) — `.Format` and
  `.In "Asia/Jakarta"` on a column, and the two shapes (CDC text vs pgx
  `time.Time`) both of them have to read.
- [`use_cdc` is opt-out](use_cdc_is_opt_out.md) — a source with no key is a CDC
  source; one definition (`hermod.SourceAllowsDirectQueries`) now gates both
  `db_lookup`'s `sourceId` and a `batch_sql` source's `source_id` delegate.
- [A `column.<path>` key is identity *and* order](column_map_is_identity_and_order.md)
  — one string names the target, identifies the row and carries the list order,
  which is three bugs: renaming re-appended (so typing scattered characters
  across rows), renaming onto a taken path ate a row, and ranging the Go map
  gave a different apply order per message. Blur fires *before* the click that
  caused it, so a pending edit cannot be flushed there.
- [The workflow Reliability Policy](reliability_policy.md) — dry-run means read
  normally, write nowhere (the DLQ included) and acknowledge nothing, with a
  resumed message the one exception; the DLQ threshold is edge-triggered from
  the engine because dead-lettering changes no status; `hasConfigChanged` is the
  only path a setting reaches a running engine, and `DrainDLQ` swaps the source
  under the read loop.
- [Fan-out, and the two foreaches](fanout_traversal_and_two_foreaches.md) — the
  traversal carried one message per node, so a foreach node delivered its first
  item and dropped the rest (and with a `collect` downstream, delivered nothing
  at all). Cloning per item also copied the whole array onto each clone, so the
  cost was O(N²): 5.64 GB for a 4000-line order, now 8.41 MB. Also: two different nodes are called foreach, `transType` cannot tell
  them apart, and the settings modal's hand-written node-type list left nine node
  types with no editor at all.
- [Reading one field cost a whole row](field_access_cost.md) — `GetValByPath`
  marshalled the entire data map to JSON per field access, so a 6-placeholder
  sink mapping on a 128-column row cost 106us and 1763 allocations; the JSON
  round trip is also what normalises int -> float64, so the fast path needs a
  parity oracle.
- [What the engine allocates per message](engine_allocation_profile.md) — 69
  allocations per message with no transformations at all; a `Debug` line's
  arguments and a `batchBytes` sum nothing read were 57% of it, and how to
  profile it (`pprof -list`, not `-top`).
- [`data_conversion` holds a list of rows](data_conversion_row_list.md) — one node
  now retypes several fields, each to its own target type, with `On Error` a
  node default any row may override. The list is authoritative by *presence*,
  not length (an empty list converts nothing), and writes are staged so a failing
  row cannot hand a half-converted message to a sink on `onError: continue`.

### Gates

`go build ./...` · `go test -race ./...` · `golangci-lint run ./...` (ratchets on
new code only; backlog is clear) · `govulncheck ./...` (3 accepted `hamba/avro`
findings, reachability-analysed in SECURITY.md) · `bun run typecheck` ·
`bunx vitest run` · `bunx playwright test` against `./scripts/dev.sh --sqlite`.

`golangci-lint` and `govulncheck` live in `$(go env GOPATH)/bin`, which is not on
the agent shell's PATH — export it, and never read a bare exit 0 from a gate as
proof it ran.
