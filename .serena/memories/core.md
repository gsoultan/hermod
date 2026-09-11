### Hermod — memory index

Read this first, then only the memories a task actually needs.

**What Hermod is.** A self-hosted data integration and streaming platform: Go
backend, React 19 UI, single binary. Its defensible position is real-time
Postgres CDC with a visual DAG editor, self-hosted, no JVM and no Kafka cluster
required — not connector count. README.md leads with that.

**Maturity is tiered, and the tiers are load-bearing.** 41 source and 45 sink
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

### Gates

`go build ./...` · `go test -race ./...` · `golangci-lint run ./...` (ratchets on
new code only; backlog is clear) · `govulncheck ./...` (3 accepted `hamba/avro`
findings, reachability-analysed in SECURITY.md) · `bun run typecheck` ·
`bunx vitest run` · `bunx playwright test` against `./scripts/dev.sh --sqlite`.

`golangci-lint` and `govulncheck` live in `$(go env GOPATH)/bin`, which is not on
the agent shell's PATH — export it, and never read a bare exit 0 from a gate as
proof it ran.
