# Avro: hamba's decoder is forbidden, Hermod has its own

`github.com/hamba/avro/v2` carries three unfixed denial-of-service advisories —
GO-2026-5046 / 5047 / 5048, CVE-2026-46385. The array and map **decoders** loop
over an attacker-controlled block count without re-checking the reader's error
state, so a record declaring up to `math.MaxInt64` elements followed by a
truncated body pins a CPU core until the process is killed.

**The module is archived upstream.** Every published version through v2.31.0 is
affected and no fix is coming (verified with `go list -m -versions`). The
advisory's suggested remediation is a third-party fork,
`github.com/iskorotkov/avro/v2` >= v2.33.0.

## What Hermod does instead

Hermod **does** decode Avro — reading a Confluent-framed topic needs it — but
not with hamba. `pkg/infra/avrodecode` is Hermod's own decoder, written rather
than taking the fork as a dependency. hamba is still used for `avro.Parse`
(schema compilation) and `avro.Marshal` (encoding); neither is the affected
path, so the govulncheck exemptions in `scripts/govulncheck.sh` still hold and
their comment explains exactly this.

Why it is not the same bug, structurally rather than by patch:

- It reads a **byte slice already fully in memory**, not a stream. There is no
  deferred reader error state for a loop body to ignore; running out of bytes
  fails at the read.
- **No allocation is sized from a declared count** — collections grow as
  elements actually decode.
- Collection bounds are **cumulative across blocks**, so splitting a large
  collection into many small ones evades nothing.
- Nesting is depth-limited; value size and total values per record are bounded.

**The zero-width trap:** a count cannot exceed the bytes remaining *unless the
item type is `null`*, which encodes to nothing. An array of `null` is
arithmetically consistent with an empty body at any count, so the
"count <= remaining" check is vacuous there and only the absolute bound stops
it. There is a test for exactly this.

Verification: `pkg/infra/avrodecode/decode_abuse_test.go` (advisory's own
`math.MaxInt64` case under a wall-clock budget, truncation at every offset,
out-of-range union/enum indices, `math.MinInt64` block count, 200k nesting
bomb), differential correctness against hamba's encoder, and fuzzing — 11M
executions clean.

## The guards, and the hole that was in them

- `pkg/infra/schema/avro_no_decode_test.go` — `TestSchemaPackageNeverDecodesAvro`
- `pkg/infra/schema/avro_exposure_test.go` — `TestNoUntrustedAvroDecoding`
- `avro_decode_guard_test.go` (repo root) — `TestNoPackageDecodesAvro`

The first two glob `*.go` **in their own directory only**, which was sufficient
while `pkg/infra/schema` was the only importer. Measured, not assumed: with a
live `avro.Unmarshal` planted in `pkg/comm/formatter/schemaregistry`, *both
existing guards pass*. The root guard walks the whole module and closes it; it
also fails if the `hamba/avro` import disappears entirely, so it cannot quietly
stop testing anything.

All three still ban hamba's decoder specifically. `pkg/infra/avrodecode` does
not trip them because it never calls `avro.Unmarshal`/`NewDecoder`/`NewReader`
— it decodes bytes itself and only walks hamba's schema AST.

Related: [connector_conformance_suite](connector_conformance_suite.md) for the
"a guard is only as good as watching it fail" pattern this follows.
