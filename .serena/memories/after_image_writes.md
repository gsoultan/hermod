# A write to `after.<column>` lands in the row

A CDC message's data map *is* its after-image: `ToMap`/`MarshalJSON` emit it as
`after`, and every reader resolves `after.x` to the column `x`
(`evaluator.resolveEnclosing`, `GetMsgValByPath`'s `after.` fallback). Writes
now follow the same rule: `DefaultMessage.SetData` passes the key through
`afterImageKeyLocked` (`pkg/comm/message/message.go`), which maps `after.x` to `x`.

## The rule

- **CDC message, no real `after` column:** `after.x` (any case, `$.` stripped
  first) is written as `x`, in the row.
- **A row with a real column named `after`:** the write goes inside that column,
  the same way the read side lets a real column win.
- **No operation:** not a CDC event, no after-image, so `after` is an ordinary
  nested object and the write nests.
- `before.x` is untouched; nothing writes it today.

## Why

Walked as a dotted path, the write created a literal `after` key holding one
field. `afterImageLocked` and `ensurePayloadLocked` serialise a literal `after`
key *alone*, so that one-field map became the row. Every later `{{.after.x}}`
read it, and every sink received it. The editor offers each sampled field as
`after.<column>`. So a `data_conversion` on `after.scheduled_at` ahead of an
`api_lookup` sent the operator's session API `{"created_by_id": ""}`, which
osiris decodes as `uuid.UUID`, and it answered `invalid request body`.

Test API Call passed throughout. With no run to read from, `useNodeContext`
hands a node the nearest payload up the graph (the source sample), skipping the
node that broke the row. Refresh and Run Simulation run every node.

## What came with it

- **A refused `api_lookup` names its empty tokens.**
  `evaluator.EmptyTokens(msg, templates...)` re-resolves the url, query params,
  headers, body and credential, but only after a non-2xx (`sync.OnceValue`, once
  across retries). The error ends with
  `these tokens had no value and were sent empty: {{.after.user_id}}`. Tokens
  are named, never values.
- **`useNodeContext` returns `inputSkipped`,** the number of nodes the input's
  walk passed. `TransformationForm` shows `UpstreamNotRunNotice` with *Run it on
  the sample* (= the refresh). When a Test disagrees with a run, look there
  first.

## Tests

`TestSetDataAfterPrefix*` (message), `TestRefreshSendsTheRowPastANodeThatWritesAnAfterField`
(HTTP: refresh + Test API Call parity against a uuid-strict endpoint),
`TestRunSimulationKeepsTheRowPastANodeThatWritesAnAfterField` (registry, the
strict path), `TestAPILookupRefusal*`. Each fails without its change.

Related: [[api_lookup_json_body]], [[template_sample_shape_differs_by_path]],
[[editor_sample_capture_path]].
