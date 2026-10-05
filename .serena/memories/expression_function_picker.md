# The function picker, and where a function call is really evaluated

`ui/src/lib/expressionFunctions.json` is the one list of expression functions
the editor has: the picker (`shared/FunctionPicker` + `FunctionList`), the
function library beside Set Fields / Formulas, and the help modal all read it.
Before 2026-10-05 there were three hand-written lists — 13 functions in the
library, 27 in the help, 33 in the engine — and Set Fields, which evaluates the
same values as Formulas, offered none.

`pkg/infra/evaluator/function_catalog_test.go` holds the file to the engine:

- the names must be exactly the `case` strings of `CallFunction`'s switch, read
  from `evaluator.go` with `go/ast` so the list is not restated a fourth time
  (`env` is the one deliberate omission, in `unlistedFunctions`);
- each `example`, evaluated against the file's `source`, must answer `result`,
  bare and as a `{{ }}` token. That is the value the picker shows after `→`.

So adding a function is: the `case`, a `testdata/functions/*.json` fixture
([expression_function_parity](expression_function_parity.md)), and a catalog
entry — the test fails until the third is done.

## Where a call is evaluated (audit, 2026-10-05)

| Node / field | Engine reader | A call works? | Picker |
| :--- | :--- | :--- | :--- |
| `set`, `advanced` — a row's value | `EvaluateAdvancedExpression` | yes: bare, or `{{fn()}}` inside text | yes |
| condition / filter / switch / validate — Field | `EvaluateField` | yes, bare | yes (`field` mode) |
| same — Value | `resolveConditionValue` → `fieldToken` | yes, as a token only; bare is text | yes (`token` mode) |
| `mapping`, `data_conversion`, `aggregate`, `fuzzy_lookup`, `term_extraction` — field | `EvaluateField` | yes, **but see below** | no |
| `rate_limit` keyField, `aggregate` groupBy | `EvaluateField` | yes (read only) | no |
| `api_lookup` URL / headers / body, `db_lookup` templates, panmail `name` | `ResolveTemplateMsg` | yes, as a token | no — not verified end to end |
| `mask`, `char_map`, `encrypt`, `scd`, `dq_scorer`, `join`, `foreach`, `deduplicate`, `log`, `stateful` | `GetMsgValByPath` | **no — a path only** | no |

**The trap in the middle row.** `mapping`, `data_conversion`, `fuzzy_lookup`,
`term_extraction` and `aggregate` evaluate `lower(source.name)` and then, when
no target field is set, write the result to a field *named after the
expression* (`targetField = field`, `field + "_fuzzy"`, `field + "_terms"`,
`field + "_" + aggType`). `SetData("lower(source.name)", v)` splits on the dot.
Their inputs say "Field or expression", so the claim is true and the default is
a trap. Offering the picker there needs that default fixed first (require a
target field when the field is a call), which is why it is not wired.

`MaskConfig` said "Field or expression" with `lower(source.email)` as its
placeholder; `mask.go` reads a path. Fixed in the same change: the text, not
the engine.

## What a non-function turns into

Pinned by `TestAValueThatCallsNoFunction`, mirrored by `notFunctions` in
`ui/src/lib/functionCatalog.ts`, which is what puts the message under a row:

- an unknown name — `nwo()`, and also `Paris (France)` — evaluates to **null**;
- a dotted name — `time.now()` — is not a call at all (a name is
  `[A-Za-z0-9_]+`) and is written out **as that text**;
- neither is an error anywhere. The node is green.

`notFunctions` restates `ParseAndEvaluate`'s shape (which text is a call),
not its evaluation. It runs the same cases as the Go test; keep them in step.

## Editor details that cost time

- **Escape.** Mantine's modal listens on `window` in the capture phase and
  closes on any Escape whose target lacks `data-mantine-stop-propagation`. A
  popover over the node's settings closed both. `FunctionList`'s `inPopover`
  sets the attribute on its input and buttons. Only the browser spec saw it:
  the drawer was still "visible" for its exit transition, and the symptom was a
  Live Preview that stopped updating. `TemplateField`'s older "Insert variable"
  popover has the same fault and is not fixed.
- **The caret of an input never focused** is the start in jsdom and the end in
  a browser. `TemplateField` tracks whether the input has been focused and
  treats an untouched one as "caret at the end".
- **Insertion rules** are `applyFunction` in `ui/src/lib/expressionInsert.ts`:
  a selection becomes the first argument; a caret at either end of a finished
  expression wraps all of it; otherwise the call goes in at the caret with its
  first placeholder selected, so "Insert variable" replaces it. Inside an open
  `{{` a variable is written `source.x`, never a nested token.
- The catalog is a lazy 12 kB chunk: `TemplateField` and `FunctionPicker`
  import only types from `functionCatalog`, and the logic they need lives in
  `expressionInsert`, which does not import the JSON.

Related: [set_node_values](set_node_values.md),
[condition_value_shapes](condition_value_shapes.md),
[expression_function_parity](expression_function_parity.md).
