# Expression functions: one evaluator, one fixture directory

The expression language -- `set`/`advanced` columns, and `{{ func(...) }}` tokens
in templates -- is evaluated by Go's `Evaluator.CallFunction`
(`pkg/infra/evaluator/evaluator.go`), and only there. Every editor preview is
answered by the server: a Formulas field through `/api/transformations/test`, a
condition or switch through `PreviewBranch`, a workflow Test through
`SimulateWorkflow`.

`ui/src/utils/transformationUtils.ts` still holds an older TypeScript evaluator
-- `callFunction`, `parseAndEvaluate`, `matchesCondition`,
`simulateTransformation` -- and its comments say the Test button runs it. Nothing
in the UI imports any of them, and a production build was byte-for-byte the same
with them changed (measured 2026-09-27), so the bundler drops them. Do not keep
it in lockstep with Go: that advice in
[condition_value_shapes](condition_value_shapes.md) predates this, and following
it cost real parity work -- and very nearly a new npm dependency -- before a
build showed the code was dead. Grep for callers before trusting a comment that
names them.

Every file in `pkg/infra/evaluator/testdata/functions/` is read by
`function_fixture_test.go`. A file is `{source, cases}`; a case is a whole
expression against `source`, so the parser runs too. The reader globs the
directory, so another function family is one new file and no reader change --
which is also why PRs adding different families never touch the same file.

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
