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

## The TypeScript twin must agree

`matchesCondition` in `ui/src/utils/transformationUtils.ts` is what the Test
button, the filter preview and the switch preview run. It had drifted:
`not_contains` unimplemented (flat `false`), no `eq`/`neq`/`gt`/`gte`/`lt`/`lte`
aliases, `Number()` turning null/`''`/`false`/`[]` into `0`, and RFC1123 dates
sorting by weekday.

`pkg/infra/evaluator/condition_number_shape_test.go` and
`ui/src/__tests__/matchesConditionParity.test.ts` are transcriptions of each
other. **Change one side and change both**, or the preview starts lying — which
is worse than no preview, because the user tunes against it.

See also [a config list reaches the engine in two shapes](node_config_list_shape_drift.md).
