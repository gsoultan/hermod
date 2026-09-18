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

Rows arrive from JSON as `[]any` of `map[string]any`; tests that build typed rows exercise
a shape the engine never sees. `Prepare` caches them under `_parsed_conversions` and the
editor's preview endpoint takes the uncached path, so both paths must agree —
`TestDataConversion_MultiField_PreparedAndUnpreparedAgree` pins that.

Fastest live check for any transformer change, without touching a running dev stack:
build to a temp dir, run `--mode=standalone --port=4105 --grpc-port=50151 --db-type=sqlite`
with its own `HERMOD_CONFIG_DIR`, POST `/api/config/setup` then `/api/login`, and drive
`POST /api/transformations/test` with `{transformation:{type,config}, message:{...}}`.
