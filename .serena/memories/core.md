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
idempotency. 2PC is Postgres-only and single-sink (no coordinator drives it).
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
- [Reachability tests](reachability_tests.md) — a feature configured through storage
  needs one test that starts from storage; three shipped bugs had full unit and
  integration coverage of the parts and none of the assembly.

### Gates

`go build ./...` · `go test -race ./...` · `golangci-lint run ./...` (ratchets on
new code only; backlog is clear) · `govulncheck ./...` (3 accepted `hamba/avro`
findings, reachability-analysed in SECURITY.md) · `bun run typecheck` ·
`bunx vitest run` · `bunx playwright test` against `./scripts/dev.sh --sqlite`.

`golangci-lint` and `govulncheck` live in `$(go env GOPATH)/bin`, which is not on
the agent shell's PATH — export it, and never read a bare exit 0 from a gate as
proof it ran.
