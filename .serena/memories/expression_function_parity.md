# Expression functions: two evaluators, one fixture

The expression language -- `set`/`advanced` columns, and `{{ func(...) }}` tokens
in templates -- is evaluated twice: by Go's `Evaluator.CallFunction`
(`pkg/infra/evaluator/evaluator.go`) and by the editor's TS twin `callFunction`
(`ui/src/utils/transformationUtils.ts`).

The TS twin is reached through `matchesCondition` → `resolveTemplateStr` →
`parseAndEvaluate`: a condition value holding `{{ ... }}` is resolved in the
browser for the Test button and the filter and switch previews. Formula previews
do **not** use it -- a Formulas node previews through `/api/transformations/test`,
which is the engine. `simulateTransformation` has no callers.

Every file in `pkg/infra/evaluator/testdata/functions/` is read by both
`function_fixture_test.go` and `ui/src/__tests__/functionParity.test.ts`, the
arrangement [condition_value_shapes](condition_value_shapes.md) describes for
`condition_cases.json`. A file is `{source, cases}`; a case is a whole expression
against `source`, so each side's argument parser runs too. Both readers glob the
directory, so another function family is one new file and no reader change --
which is also why two PRs adding different families never touch the same file.

## split

`split(value, sep)` is the list of parts; `split(value, sep, index)` is one part.
Text is cut by `evaluator.SplitText`, which Data Conversion's Array target calls
too, so the node and the function cannot cut the same text differently: parts
trimmed, an empty sep is a comma, blank text has no parts. A list passes through.
A number is rendered with `stringify` (the digits JSON shows), never `%v`. The
index must be a whole number; negative counts from the end; anything else, or out
of range, is null so `coalesce` can supply a default. Bounds are checked as
floats because `int(±Inf)` is implementation-defined in Go.

One deliberate difference from Data Conversion: JSON-array *text* is split as
written, not parsed. A jsonb list is reachable as `source.tags.0` on both CDC
paths already, so a string function has no need to guess.
