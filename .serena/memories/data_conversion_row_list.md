# `data_conversion` holds a list of rows, not one field

A `data_conversion` node used to read one `field` and one `targetType`. It now reads
`conversions`: a list of rows, each with its own `field`, `targetType`, `format`,
`separator`, `elementType`, `targetField` and optional `errorBehavior`.
`pkg/comm/transformer/core/conversion.go` — `parseConversions` + `conversionRow`.

Three decisions worth keeping:

1. **Presence of `conversions` is authoritative, not its length.** An empty list means
   "convert nothing". Falling back to the pre-list `field`/`targetType` keys on an empty
   list would resurrect the conversion an operator deleted the last row to remove, because
   the editor leaves those keys in place until it migrates the node.

2. **Writes are staged, then applied together.** Each row reads the message as it arrived,
   and nothing is written until every row has resolved. `applyTransformation` forwards the
   *input* message when a workflow sets `onError: continue`
   (`internal/engine/registry/registry.go`), so converting in place would hand a
   half-converted row to the sink — some columns retyped, the failed one raw — with nothing
   downstream able to tell. Row order therefore decides write order only, never what a row
   sees.

3. **`errorBehavior` is a node-level default a row may override.** Empty on a row means
   inherit. The editor's per-row control uses an `inherit` sentinel that it must write back
   as `""` — `parseConversions` would treat the literal `"inherit"` as an unknown behaviour
   and silently fall through to `fail`.

## The Date Format is a hint, not the only shape allowed

`toDate` tries the configured layout first — so any node that converts today
converts to the same instant and zone — and falls back to `dateLayouts`, the
ISO-8601 shapes a source actually produces (RFC3339 with or without a fraction
or zone, PostgreSQL `timestamptz`/`timestamp` text including the short `+07`
offset, Go's `time.Time.String()`, a bare date). Three decisions hold it up:

1. **The sweep is safe only because every layout opens with `YYYY-MM-DD`.**
   `looksLikeISODate` screens for that before any parse, so `03/01/2026` is never
   guessed at — it still fails under the operator's own layout rather than
   converting to a plausible wrong date. Never add a `D/M` or `M/D` layout here.
2. **The instant is kept, never truncated to the layout's precision.** A
   deadline at 07:26 silently becoming midnight is worse than the error this
   replaces; a `date` column truncates on write and a template's `.Format`
   renders, so precision is the sink's decision.
3. **Native shapes bypass text entirely.** `%v` on a driver's `time.Time` gives
   Go's `String()` form, which no configured layout describes, and on `[]byte`
   gives decimal bytes — the values needing no conversion were the ones that
   failed. Note that `evaluator.EvaluateField` JSON-normalises, so a `time.Time`
   in the data map usually reaches the node as RFC3339Nano text anyway; both
   paths have to land on one instant.

The editor's placeholder used to be `2006-01-02`, which is how a date-only
layout over a timestamp column became the configuration operators land on first.

Rows arrive from JSON as `[]any` of `map[string]any`; tests that build typed rows exercise
a shape the engine never sees. `Prepare` caches them under `_parsed_conversions` and the
editor's preview endpoint takes the uncached path, so both paths must agree —
`TestDataConversion_MultiField_PreparedAndUnpreparedAgree` pins that.

Fastest live check for any transformer change, without touching a running dev stack:
build to a temp dir, run `--mode=standalone --port=4105 --grpc-port=50151 --db-type=sqlite`
with its own `HERMOD_CONFIG_DIR`, POST `/api/config/setup` then `/api/login`, and drive
`POST /api/transformations/test` with `{transformation:{type,config}, message:{...}}`.
