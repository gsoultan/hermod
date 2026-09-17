# List variables in SQL templates

**Where it lives.** `pkg/infra/sqlutil/template.go`. Everything that turns a
`{{ }}` template into a bound statement goes through `ParameterizeTemplateEx`:
the `db_lookup` node in query mode, the `execute_sql` node, the `batch_sql`
source, and the editor's query preview (`internal/discovery/service`). It sits in
`sqlutil` rather than `pkg/comm/transformer/core` so sources and sinks can use it
without importing a transformer package — no source or sink imports a transformer
package, and this change would have been the first. `core.ParameterizeTemplate`
is a thin alias kept for existing call sites.

**The rule that is easy to get wrong.** One token normally becomes one
placeholder and one argument. A *slice* bound that way is not a list — it is an
encoding error on every driver:

- `database/sql`: `unsupported type []interface {}, a slice of interface`
- pgx: `unable to encode []string{...} into binary format for uuid (OID 2950):
  cannot find encode plan`

So `id IN ({{.ids}})` had no working form at all, identically for uuid, integer
and text columns. A token sitting **directly in an `IN (...)` list** now expands
to one placeholder per element.

**The expansion is confined to `IN` on purpose.** Three cases must keep binding
the slice whole, and there are tests pinning each:

- `= ANY({{.ids}})` — the native PostgreSQL array form. It already worked;
  expanding it produces `= ANY($1, $2)`, a syntax error.
- `> ALL({{.ns}})` — same reason.
- `IN (SELECT ... WHERE k = {{.k}})` — a subquery, not a value list.

Detection is a one-pass state machine: a parenthesis records the identifier that
opened it (`prevWord`), and `sawWord` marks the first *letter* written inside it,
which is what separates a value list from a subquery or expression. Digits do not
set `sawWord`, so `IN (1, {{.rest}})` still expands. String literals are tracked
so a stray `'in ('` cannot open a list.

**Empty list binds a single NULL.** `IN ()` is a syntax error in every dialect;
`IN (NULL)` is valid and matches nothing, which is what an empty set means.

**`MaxListExpansion = 65535`** bounds one token. The element count comes from
message data, which nothing upstream bounds, and each element costs both
statement text and a bind slot. PostgreSQL's wire protocol stops at 65535 and SQL
Server at 2100, so a longer list never reaches a server that would take it — but
without the cap it is built in the worker's memory first. Over the cap,
`TemplateBinding.Err` is set and the three execution paths return it. Anything
reading `TemplateBinding` must check `Err`: `db_lookup` reported an oversized
list as `empty queryTemplate after processing` until it did.

**`AsSlice` decides what a list is**, by reflect kind, so `[]int32` and any other
slice type work. `[]byte` and `json.RawMessage` are excluded — they are bytea /
blob and JSON column values, not lists of bytes. `db_lookup`'s own `asSlice`
delegates here; it used to be a hand-written type switch over five types and
drifted.

**Feeding it.** Nothing in Hermod could *build* a list before this: the
`data_conversion` node rejected every type but int/float/bool/string/date, and
the evaluator has no `split`, no `join` and no array constructor. The node now
takes `array` (with `separator` and per-element `elementType`) and `uuid`, and
converting a list to `string` joins on the separator instead of rendering Go's
`%v` form (`[a b c]`).

**The handoff between nodes is type-preserving, but reads are not.**
`msg.SetData` keeps the Go type; `evaluator.EvaluateField` round-trips through
JSON, so a number read back is `float64`, a `[]byte` is base64 text and a typed
slice is `[]any`. `data_conversion` reads through the evaluator and writes with
`SetData`, and the SQL template path reads `msg.Data()` — so a list written by
one node arrives at the next intact. `elementType` exists because of the read
side: splitting text yields strings, and a string does not match an integer or
uuid column.

**`batch_sql` is the odd one.** It has no inbound message, so its variables come
from a `parameters` JSON object on the source config. Two deliberate loud
failures: an undefined token fails the query naming the token (binding NULL would
turn a typo into an empty result set that reads as an empty table), and a
parameters blob that will not decode is an error rather than an empty map (which
would silently drop every configured filter). `{{.last_value}}` keeps its
*textual* substitution — operators write it inside their own quoting,
`id > '{{.last_value}}'`, and binding it would break every such query. The
scheduled run and the editor's sample preview share `prepareQuery` so they cannot
drift.
