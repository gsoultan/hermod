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
   replaces. Reading never renders: a `date` column truncates on write, and a row
   that wants text says so with an `outputFormat` (below).
3. **Native shapes bypass text entirely.** `%v` on a driver's `time.Time` gives
   Go's `String()` form, which no configured layout describes, and on `[]byte`
   gives decimal bytes — the values needing no conversion were the ones that
   failed. Note that `evaluator.EvaluateField` JSON-normalises, so a `time.Time`
   in the data map usually reaches the node as RFC3339Nano text anyway; both
   paths have to land on one instant.

The editor's placeholder used to be `2006-01-02`, which is how a date-only
layout over a timestamp column became the configuration operators land on first.

## Reading and writing are two formats

`format` is the layout a value is **read** with; `outputFormat` is the layout it
is **written** with (`convertDate`). Empty `outputFormat` keeps the `time.Time`
— every row stored before it existed, and the right shape for a date or
timestamp column. The row used to have only `format`, labelled "Date Format" in
the editor, so an operator who set it to "02 January 2006" to get
"18 September 2026" got `2026-09-18T04:30:57.333046Z` back: the layout missed,
the ISO sweep read the value, and nothing ever wrote with it. Never reinterpret
`format` as the output — stored rows read non-ISO text with it
(`flow_dateconv_integration_test.go` reads `16-09-2026 08:30`).

- **Text is rendered in the value's own zone** — no `.In()`. There is no zone
  option yet; a UTC timestamp written date-only for a +07 business is a day early
  for seven hours of every day. The SMTP sink's `dateInZone` is the precedent if
  one is added.
- **An output layout that prints no part of a date is a config fault**, refused
  whatever `errorBehavior` says (else `null` turns a typo into a column of nulls).
  `checkOutputFormat` formats two probe instants that differ in every element Go
  can print; equal text means no element. It runs in `parseConversions`, once per
  config — `Prepare` errors are ignored by the registry
  (`registry_workflow.go`), so it cannot live there.
- **The editor picks layouts from a list** shown as the text each produces
  (`ui/.../data/dateFormat/dateFormatOptions.ts`). Every example is a claim about
  the engine, so `TestDataConversion_Date_EditorFormatsDoWhatTheyShow` reads that
  file and runs each one through the node. A custom layout is still allowed;
  `goLayoutProblem` flags the two mistakes Go accepts silently — a real year
  where 2006 belongs ("02 January 2026" prints "18 September 18186") and letter
  codes (`DD/MM/YYYY` prints itself) — and offers the fix only when there is
  exactly one reading of it.

Rows arrive from JSON as `[]any` of `map[string]any`; tests that build typed rows exercise
a shape the engine never sees. `Prepare` caches them under `_parsed_conversions` and the
editor's preview endpoint takes the uncached path, so both paths must agree —
`TestDataConversion_MultiField_PreparedAndUnpreparedAgree` pins that.

Fastest live check for any transformer change, without touching a running dev stack:
build to a temp dir, run `--mode=standalone --port=4105 --grpc-port=50151 --db-type=sqlite`
with its own `HERMOD_CONFIG_DIR`, POST `/api/config/setup` then `/api/login`, and drive
`POST /api/transformations/test` with `{transformation:{type,config}, message:{...}}`.
