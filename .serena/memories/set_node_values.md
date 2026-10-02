# What a `set` / `advanced` column value means

A column value (`column.<path>: <value>`) is evaluated by
`evaluator.EvaluateAdvancedExpression` (`pkg/infra/evaluator/evaluator.go`).
Its type decides how it is read:

- **A string** is an expression: `source.x` reads a path, a number or boolean
  literal becomes that value, `'text'` is text, `fn(args)` calls a function, and
  anything else is literal text.
- **A string holding `{{`** is a template instead: every token is read with
  `fieldToken` (= `EvaluateField` after the leading dot), so `{{source.a}}`,
  `{{.a}}`, `{{a}}` and `source.a` are one value and `{{.after.a}}` reaches the
  CDC envelope. A value that is exactly one token keeps the token's type; mixed
  text writes each token with `stringify`.
- **An object or array** is a JSON document. Only strings that start with
  `source.` or hold `{{` are read; every other value stays as configured.

## Why the document rule is narrower than the expression rule

Reading every nested string as an expression breaks literals two ways, both
measured by a mutation of the test: `"007"` becomes the number 7 (the expression
reader tries `ParseFloat` first), and `"Paris (France)"` becomes `nil` — it
parses as a call to a function named `Paris`, and `CallFunction` answers `nil`
for any name it does not know. A JSON document already says which values are
numbers, so its text stays text unless it asks to be read. A function call
inside a document is written as a token: `"{{lower(source.name)}}"`.

## The copy is load-bearing

Before 2026-09-28 a non-string value was returned as configured, so `SetData`
put the node's own map into the message. A later node writing
`column.after.QueryParams.page` wrote into that map — the node's config — and
every following message through the first node carried the previous message's
value; two messages in flight wrote one map. `evaluateDocument` builds a new
map/slice per evaluation even when nothing in it is a reference. Any future
fast path that "returns the literal when there is nothing to resolve" puts this
back.

## Security notes

- `{{env.X}}` resolves to nothing here, as in every template, and a value read
  from the message is never scanned again (single forward pass).
- The `env()` and `secret()` *functions* used to read the server's process
  environment from any expression (`env('HERMOD_JWT_SECRET')` through the Test
  button; `{{env('X')}}` slipped past the dotted-only `env.` guard). Fixed: both
  now read the secret manager, prefix only — see
  [secrets_env_prefix](secrets_env_prefix.md).

## The editor

`ui/src/components/workflow/Transformation/fieldMappings/`:
`ColumnFieldsEditor` shows the rows (`SetFieldEditor`) or the JSON, one at a
time, for both `set` and `advanced`. A row's value is typed Expression or JSON
(`toJsonColumnValue` keeps non-JSON text as `{"value": <text>}` so a mis-click
loses nothing). `JsonObjectInput` must be `autosize` for `minRows` to do
anything — that was the two-line "Fields (JSON)" box.

Tests: `pkg/comm/transformer/advanced/set_object_value_test.go` (engine and
unprepared preview paths, literals, aliasing, env), `setFieldsObjectValue`,
`setFieldsConfigViews`, `jsonObjectInput` (vitest),
`ui/__tests__/set_fields_json_value_e2e.spec.ts` (fails against a backend
without the fix).

Related: [column_map_is_identity_and_order](column_map_is_identity_and_order.md),
[condition_value_shapes](condition_value_shapes.md) (shares `fieldToken`),
[after_image_writes](after_image_writes.md).
