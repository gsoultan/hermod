# A SQL template resolves paths like every other template now

**Fixed on `fix/sql-template-path-resolution`.** Kept because the shape of the
bug explains the shape of the fix, and because the parity oracle at the bottom
is the thing to reach for the next time two layers disagree about a message.

## What was wrong

A `{{ }}` token in a SQL template resolved through `sqlutil.GetFromMapPath` — a
literal walk of the data map. Every *other* template in Hermod (conditions, sink
mappings, `db_lookup`'s own `keyField` two lines above the bug) resolves through
`evaluator.GetMsgValByPath`, which also answers the CDC envelope (`after.`,
`before.`), the virtual fields (`operation`, `table`, `schema`, `id`) and
`meta.`. The data map of a CDC message **is** its after-image, so `after.x` had
nothing to walk.

The editor's SQL Query Builder resolved the same text against the *sample*,
which is `ToMap()`-shaped and still carries the envelope. So one query, two
answers:

| Layer | `{{.after.payload}}` before |
| :--- | :--- |
| SQL Query Builder (`DiscoveryService.ExecuteSQL`) | resolved → rows |
| Run Preview (`populateMessageFromMap` → `msg.Data()`) | nil → NULL |
| live engine (`msg.Data()`) | nil → NULL |

Silent, not wrong: an unresolved token is deliberately bound as NULL, an
`INNER JOIN` on a NULL matches nothing, `lookupSQLWithTemplate` returns
`(nil, nil)` for zero rows, and `db_lookup`'s default `onMiss` is passthrough.
Reported as "the SQL builder shows results but the preview does not show the
target field."

## The fix

1. **`sqlutil.Resolver`** (`pkg/infra/sqlutil/template.go`) — the path rule is
   now supplied by the caller. `ParameterizeTemplateWith` / `TemplateArgsWith`
   take it; the map-taking entry points delegate through `MapResolver`, so
   `batch_sql` (whose variables come from a `parameters` object, not a message)
   is unchanged. sqlutil cannot import the evaluator — it sits below the
   transformer packages on purpose — so the rule is injected, not imported.
2. **`evaluator.MessageResolver(msg)`** returns that rule bound to a message.
   Returns a bare `func(string) any` so the evaluator need not import sqlutil.
3. **`db_lookup`, `execute_sql`** resolve through it. `bindingDigest` uses
   `TemplateArgsWith` with the *same* resolver — a cache key that resolved
   differently from the statement is the defect [[lookup_cache_fast_path]]
   records, and widening only the query path would have reintroduced it.
4. **`message.PopulateFromMap`** (moved out of `internal/workflow/transport/http`
   into `pkg/comm/message/sample.go`) is now the single answer to "what would the
   engine see for this sample", used by both the preview endpoint and the
   builder.
5. **`bindSample`** (`internal/discovery/service/sample_binding.go`) builds a
   real message from the sample and resolves through the evaluator, so the
   builder and the engine agree **by construction** rather than by imitation.

## Two things found on the way

- **`ToMap()` → `PopulateFromMap` was not a round trip.** `ToMap` writes the
  envelope as `json.RawMessage` (`jsonRawOrWrapped`); `PopulateFromMap`
  understood only `map[string]any` or `string`, so an **in-process** round trip
  dropped every row column silently. Over HTTP it worked, because JSON decoding
  turns the raw form into a map first — which is why nothing had caught it.
  `envelopeImage` now takes all three shapes. Note its `isObject` return: an
  empty object must not fall through to `SetAfter`, which *clears the data map*.
- **The seed was a nondeterministic collision.** `ExecuteSQL` merged
  `{"after": {"id": uuid}}` into every sample, so a non-CDC row with its own
  `id` had two candidates and Go's map iteration order picked one. The seed is
  now used only when there is no sample at all.

## The parity oracle

`TestTheBuilderBindsWhatTheEngineBinds` (`internal/discovery/service`) is the
test worth copying: build the message a source would hand the engine, take
`ToMap()` as the editor would, feed it back through `bindSample`, and assert the
**bound arguments** match `TemplateArgsWith` over the original message. Any
future divergence between what the editor shows and what the pipeline runs fails
there rather than in production. Reach for this shape whenever two layers hold
"the same" message.

## Still true, still worth knowing

- The correct spelling for a column is the bare path — `{{.payload}}` — and it
  is the only one that keeps its Go type. `GetMsgRawValByPath` answers a data-map
  path from the live map (so an int64 stays an int64), while an envelope path
  goes through gjson and is JSON-normalised. A bigint key above 2^53 must not be
  spelled `{{.after.id}}`.
- Binding a `map[string]any` to `$1::JSON` works — pgx's JSONCodec marshals it —
  so a `jsonb` column decoded to an object by [[jsonb_shape_differs_by_path]]
  needs no stringify. A trailing `;` and quoted `AS "Alias"` names round-trip
  intact; the aliases become the lookup result's map keys.
- An unresolved token is still bound as NULL. `db_lookup` now **warns** naming
  the token (through an optional `Logger()` on the registry already in the
  context — this package has no logging of its own and the fakes must not have
  to grow one). `execute_sql` additionally offers `onUnresolved: fail`.
- `SQLQueryBuilder`'s DETECTED VARIABLES badge tracked `availableFields`, which
  is built by recursing the sample — a different question from "will this bind".
  It now tracks the resolved value, so "Matched" means what it says.

Related: [[editor_sample_capture_path]] — where the sample comes from at all.
[[message_payload_decoding]] — the other place two representations of one
message disagreed.
