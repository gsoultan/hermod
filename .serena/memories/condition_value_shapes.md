# What a condition actually compares

`EvaluateConditions` (pkg/infra/evaluator/evaluator.go) renders both sides to
text before it knows which operator it is applying, so the *text* is the
contract. Two things decide it.

## 1. Every field is normalised through JSON first

`GetValByPath` reproduces what a JSON round trip produces — deliberately, and
`lookupByWalk` bails to a real `json.Marshal` for anything it cannot reproduce
exactly. So by the time a condition sees a field:

- every integer kind (`int8`…`uint64`, `json.Number`) is a `float64`
- `[]byte` is a **base64 string** — `[]byte("active")` is `"YWN0aXZl"`
- `time.Time` is RFC3339
- `int64` beyond 2^53 has already lost precision

The base64 is not a bug to fix: the API hands the browser the same JSON, so the
sample panel shows base64 too, and the editor's preview compares base64. Pinned
by `TestByteSliceFieldComparesAsBase64`.

## 2. `stringify` renders a number the way JSON does, not the way `%v` does

This was the bug. `%v` is `%g`, which switches to an exponent above 1e6, so
`1704207845` compared as `"1.704207845e+09"` while the wire and the browser
both said `1704207845`. Every id, timestamp and amount of 7+ digits silently
failed `=`, `contains` and `regex`; nothing under a million drifted, so test
data looked fine, and `>`/`<` were unaffected because they go numeric — a
switch could order rows correctly and never match one.

`formatJSONFloat` mirrors `encoding/json` (and therefore ECMAScript): fixed
notation between 1e-6 and 1e21, an exponent outside, `e-9` not `e-09`. NaN and
the infinities have no JSON form and keep their `%v` spelling.

Composites render as JSON with sorted keys (`{"a":1,"b":2}`), not `map[a:1 b:2]`.

## `=` is numeric when the field is

Exact text equality is checked first, then `numericallyEqual` — only when the
*field* is numeric. This can only add a match between two spellings of one
number (`100` = `"100.00"`), never remove one, and a string field keeps string
equality so `"007"` ≠ `"7"`.

## Tokens in the value read like the field

A value holding `{{ }}` is resolved by `resolveConditionValue`, which reads
each token with `EvaluateField` — the reader the field uses — and renders it
with `stringify`. It used `ResolveTemplate`, which walks the bare data map; a
CDC message's data map *is* its after-image, so `{{.after.x}}` (what the value's
own picker inserts), `{{.before.x}}`, `{{.operation}}`, `{{.table}}` and
`{{.meta.x}}` all rendered `""`, and `=` was false for every row.

Do not "fix" it with `ResolveTemplateMsg`: that keeps Go types for SQL binding,
so a `[]byte` renders `[97 98 99]` and a `time.Time` in Go's layout while the
field reads base64 and RFC 3339. `TestConditionValueTokenRendersEveryTypeLikeTheField`
fails on exactly that swap.

## Traps that still read as "always false"

None of these is an error; each compares text that can never match:

- a quoted literal, `'active'` — the Set node quotes literals, a condition
  compares the quotes too;
- a bare name inside a function, `lower(status)` — it is the literal `status`;
  write `lower(source.status)` (the placeholder says so);
- an operator the switch does not know (`==`, `equals`, missing) — `match`
  stays false, and the editor *displays* `=` for a missing one
  (`cond.operator || '='`);
- a row with a blank Field — it compares `""`;
- a leading or trailing space in the Field.

And the opposite: **no conditions at all is `true`** (`len == 0` guard), so an
empty If node sends everything down TRUE with nothing logged. Filter relies on
that; switch and router never hand the evaluator an empty list.

## There is no TypeScript twin to keep in step

`matchesCondition` in `ui/src/utils/transformationUtils.ts` and
`simulateTransformation`, its only caller, have no importers outside tests
(checked 2026-09-28). Every preview — Test, Run Simulation, the filter and
switch previews — is answered by the server, so `EvaluateConditions` is the only
evaluator that matters. An older version of this memory said to change both in
lockstep; that cost a session of parity work on dead code.

See also [a config list reaches the engine in two shapes](node_config_list_shape_drift.md).
