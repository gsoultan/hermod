# A config list reaches the engine in two shapes

`updateNodeConfig` (useWorkflowStore.ts:284) merges its argument straight into
`node.data` — it does not serialise. So whatever an editor hands it is the shape
the engine sees, and the editors disagree:

- **JSON string**: `RouterEditor.tsx:49` (`rules`), `FilterDataConfig.tsx:61`
  (`conditions`), `MappingEditor.tsx` (`mapping`), `BatchSQLSourceConfig.tsx`
  (`queries`), the sink `column_mappings` — all `JSON.stringify(next)`.
- **Raw array**: `SwitchConfig.tsx` (`cases`), `ConditionConfig.tsx`
  (`conditions`, straight from `FilterEditor`'s `Condition[]`).

A raw array round-trips through JSONB and arrives as `[]any` of
`map[string]any`. `node.Config["cases"].(string)` then fails, and the `, _`
swallows it.

## Why it stayed invisible

An empty list is not an error to anything downstream — it is a *decision*:

- `switch` with no cases → `"default"`
- `EvaluateConditions(msg, nil)` → **`true`** (evaluator.go, `len == 0` guard)

So `switch` sent every message to `default` and `condition` sent every message
down `true`, with a populated config visible on screen and nothing logged. The
UI's own node preview (`MiscNodes.tsx:86`) and `transformationUtils.ts:478`
already read either shape, so the canvas drew the branches the engine was not
taking.

Every Go test hand-built `cases`/`rules` as a JSON string
(`switch_test.go`, `branch_preview_test.go:32`), so the suite was green against
a shape the UI never produces. Same trap as the sink form's
`configComponents[type] || 'database'` fallthrough
([sink_form_fallthrough_and_panmail.md](sink_form_fallthrough_and_panmail.md)):
a default that is indistinguishable from a correct answer.

## The fix

`evaluator.ParseObjectList(any)` accepts a JSON string, `[]any` or
`[]map[string]any`, and keeps the string path's parse cache and
`cloneConditions` copy. `ParseConditions`, `switch.go` (`cases` +
per-case `conditions`), and `router.go` (`rules` + per-rule `conditions`) all go
through it. `SwitchConfig.tsx` parses the string form on read, so an
API-created or bundle-restored workflow no longer renders zero cases and save
that emptiness back.

## What to check next time

Grepping for `Config["x"].(string)` finds the class. When adding a list-shaped
node config, test it in **both** shapes — a green test that only builds the
string form proves nothing about what the editor saves.
