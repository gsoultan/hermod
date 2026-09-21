Two different nodes answer to "foreach", and the traversal could only carry one
message per node. Both facts are load-bearing.

## The traversal carries one message per node

`internal/engine/registry/traversal/traversal.go` holds `CurrentMessages[idx]`
— **one slot per node id** — and fires each node once (`Fired[idx]` CAS). A node
executor may return `[]hermod.Message`, and before this was fixed `handleResults`
looped over them into `resolveEdge`, which stores only into an empty slot. The
first message walked the graph; the rest were dropped with no error.

`ForeachNode` (`nodes/control/foreach.go`) is the only executor that returns more
than one, so:

- `source -> foreach -> sink` on a 3-line order wrote **1** row, green.
- `source -> foreach -> collect -> sink` wrote **0**: `CollectNode` emits only
  once it has `_fanout_total` items, and one arrived.

`handleResults` now keeps `msgs[0]` and hands `msgs[1:]` to `forkFanout`, which
walks each in a child traversal (`walkFrom`) over the same graph and folds
`Routed`, `InlineDelivered`, `InlineFailed` and `DeadLettered` back into the
parent — acknowledgement is decided on the parent, so an item delivered or
dead-lettered in a child has to be visible there.

Two constraints are deliberate and easy to undo by accident:

- The fork runs on its **own goroutine**. `handleResults` is called from inside
  `processNode`, which holds the node's concurrency semaphore; walking downstream
  synchronously there deadlocks any graph that loops back.
- The extras are walked **one at a time**. An array field is upstream-controlled;
  a goroutine plus a pooled traversal per element makes a 100k-row array a memory
  incident. `msgs[0]` is already walking in parallel with them.

## A fan-out used to cost O(N²)

`Message.Clone` deep-copies every data value (`deepCopyValue`, added because two
branches shared a nested map and delivered each other's data). Cloning once per
item therefore copied the whole N-element array onto every one of the N clones.
Measured, one message:

| items | allocated | time |
| --- | --- | --- |
| 100 | 3.64 MB | 1.7 ms |
| 1,000 | 353 MB | 102 ms |
| 4,000 | 5.64 GB | 3.6 s |

`ForeachNode` now clones **one** base, deletes the array from it
(`deleteByPath`, which walks to the parent — deleting the first segment of
`order.lines` would take `order.id` too), takes the items out of that base's
private copy so no two outputs alias an item, and clones the base per item.
4,000 items: 8.41 MB, 4.5 ms. The linearity gate is
`TestForeach_FanoutCostGrowsLinearlyWithTheArray` — it compares TotalAlloc for
500 vs 1,000 items and fails above 3x, because quadratic shows up as 4x.

Consequences to keep in mind:

- A fanned-out message **no longer carries the source array**. `keepSourceArray`
  opts back in, and back into the old cost. `useNodeContext` filters the
  consumed path out of Available Fields to match.
- `maxItems` caps the width, default `defaultMaxFanoutItems` = 10000. Over it is
  an **error**, not a truncation: a truncated fan-out is a partial write to every
  sink with nothing to tell it from a short array. `configuredMaxItems` reads
  `<= 0` as "use the default", never as "emit nothing".

## Two foreaches

| Palette | Node | Code | Behaviour |
| --- | --- | --- | --- |
| Logic & Flow → "Foreach (Fan-out)" | `type: foreach` | `internal/engine/registry/nodes/control/foreach.go` | splits into N messages, each with `_item`, `_index` and `_fanout_*` metadata |
| Common Transformations → "Expand List (Foreach)" | `type: transformation`, `transType: foreach\|fanout` | `pkg/comm/transformer/logic/foreach.go` | one message out, expanded array under `resultField` (default `_fanout`) |

`transType` cannot tell them apart: `TransformationForm` computes it as
`data.transType || node.type`, and both come out `"foreach"`. The discriminator
is the node's own `type`, now passed to config components as the `nodeType` prop.

`fanout` is **only ever a transformer alias** — `transformer.Register("fanout", …)`.
There is no `RegisterNodeExecutor("fanout")`, so a `fanout` key always means
"expand onto the same record", never the split. (`workflow_validation.go`'s
`case "foreach", "fanout"` is defensive, not evidence of a second node type.)

### The copy told users the opposite

The `nodeType` discriminator reached `ForeachConfig` but nothing else, so the
two stayed indistinguishable everywhere a user looks *before* opening the
editor body:

- `guideFor(transType)` took no node type, so the badge and sentence over both
  editors read "For each item / …works through it item by item." There is now a
  `NODE_TYPE_GUIDES` map checked first, mirroring `resolveConfigComponent`'s
  node-type-wins ordering; the node reads "Fan out", the transformation "Expand
  list". Omitting `nodeType` resolves to the transformation — the reading that
  does not promise a fan-out.
- The `fanout` guide entry said "emits one record per item". The transformer it
  keys to does not emit anything extra. It was the node's behaviour written
  under the transformation's key.
- The palette labelled the transformation "Foreach / Fanout" and described it as
  "Iterate array items and fan out". `paletteSearch.normalise` strips hyphens,
  so typing "fanout" returned both entries under near-identical names, and the
  one that did *not* fan out was the one that said it did.

Guards: `ui/src/__tests__/foreachPaletteCopy.test.ts` (only the entry that fans
out may say "fan-out"; "fanout" search must not reach the transformation) and
the `the two foreaches` block in `transformationGuide.test.ts`. The node label
"Foreach (Fan-out)" is pinned by `ui/__tests__/foreach_node_config_e2e.spec.ts`
— rename the transformation, not the node.

## The editor traps

- **`WorkflowNodeSettingsModal` used a literal node-type list**, and it had
  drifted from `NODE_TYPE_CONFIGS`: `foreach`, `collect`, `wait`, `join`,
  `circuit_breaker`, `approval`, `log`, `deduplicate` and `multicast` opened a
  panel with a title, a Remove button and no editor. Foreach could not be given
  its required `arrayPath` at all. It now asks
  `components/nodeEditorSurfaces.ts:rendersNodeEditor`, which reads the registry.
  Anything else that gates on node type should do the same.
- **A node's config is flat on `node.data`, not under `node.data.config`.**
  `useWorkflowInitialization` hydrates a saved node as
  `data: { ...node.config, ref_id }`, which is why `TransformationForm` passes
  `config: selectedNode.data`. Schema propagation read `node.data.config` and so
  read `{}` for every saved workflow — the pre-existing `targetField` inference
  had never fired outside a freshly-dragged node. Read the flat shape, with the
  nested one as a fallback.
- **Schema propagation** in `useNodeContext` knew only `targetField` and pipeline
  steps, so Available Fields past a foreach showed the source's columns. It now
  adds `_item`/`_index` (plus the element's own fields, read out of the upstream
  sample at `arrayPath`), collect's batch field and `_count`, and the
  transformation's `resultField`.
- Those paths are **not** `after.`-prefixed, unlike a transformation's
  `targetField`. The engine writes them with `SetData`, and
  `evaluator.GetMsgValByPath` reads the data map before any CDC envelope, so
  `_item` is what resolves at run time on a CDC message too.

Related: [[reachability_tests]] — every part here had passing unit tests and the
assembly had none. [[sink_form_fallthrough_and_panmail]] is the same
hand-maintained-map-drifts shape. [[message_payload_decoding]] for why `SetData`
lands in the after-image.
