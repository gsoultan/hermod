# A `column.<path>` key is the row's identity *and* its order

`set` and `advanced` nodes store field mappings as flat keys on the node config
(`column.user.id: "source.id"`), parsed by `pkg/comm/transformer/advanced/advanced.go`.
One string is doing three jobs: it names the target path, it identifies the row,
and — because it lives in an object — it carries the list order. Every bug below
is that overload, and anything else built on a `prefix.<user-text>` map will have
the same three.

## 1. Order: a rename re-appends, so typing reorders the list

`SetFieldEditor` rebuilt the config as
`{ ...base, ...otherColumns, ['column.' + newPath]: value }`. That drops the
edited key and re-appends it last. The Target Path input fires per keystroke, so
each character sent its row to the bottom — and rows are keyed by position, so
the caret was left in whichever row moved up into it. Typing `_id` into the first
of three rows produced `alpha_`, `betai`, `gammad`.

Fix: `renameColumnField` (`ui/src/components/workflow/Transformation/columnFields.ts`)
rebuilds the whole object, substituting the key **in place**. Rows must stay keyed
by index, not by `fullKey` — the key changes every keystroke and React would
unmount the input being typed into.

## 2. Identity: a rename onto a taken path silently ate a row

An object holds a key once, so writing a path another row owns discarded that row
and its value with no error. `renameColumnField` returns `null` for a collision;
the editor holds the typed text in `draftPaths`, shows the clash, and commits when
it is unique again.

The draft cannot be flushed on blur alone: **blur fires before the click that
caused it**, so deleting the conflicting row runs the blur handler while the
conflict still exists. `commit()` applies any newly-possible held rename as part
of the same config update instead.

## 3. Order again: JSON has none, so the backend sorts

`Prepare` collected columns by ranging a Go map — randomised per range — and a
`set` node applies them one at a time. Two columns touching the same path, or one
reading what another just wrote, resolved differently message to message inside a
single run. Nothing is wrong in the config and nothing logs.

`parseColumns` now sorts by path, and the unprepared-config fallback (the preview
endpoint) shares it, so a node cannot resolve one way in the editor and another in
the engine. **The editor's row order cannot be used**: the config is stored as
JSON, which has no key order, so it is gone before the engine sees it. A parent
path sorts before the child that writes into it.

Sorting the evaluation was not enough. The `advanced` branch evaluates into a
`results` map and then ranged *that* map to write — a second random order.
`SetData` nests a dotted path, so overlapping paths gave different messages from
one node and one input: measured over 300 runs of `column.a` + `column.a.b`,
266 gave `{"a":{"b":"child"}}` and 34 gave `{"a":"parent"}`. It now writes by
iterating `columns`.

**Two traps when testing this.** `fmt`'s `%v` prints a map with its keys sorted,
so comparing `fmt.Sprintf("%v", msg.Data())` hides write-order differences —
compare marshalled JSON. And a *composite* config value is handed to `SetData` by
reference and mutated in place, so a config reused across iterations converges
after the first run and the nondeterminism disappears; use scalar expressions.
That aliasing is worth a second look on its own: a node's configured object
literal is reachable from the message and can be mutated by later writes.

## Also

`addField` generated `new_field_<count>`. Delete a middle row and the count names
a key that is still taken, so the button merged into that row: nothing appeared
and its value was replaced. `nextColumnFieldName` picks the first free index.

`'column.'` with an empty path is a real, intentional key — the "flatten" template
in `TransformationForm` writes `{'column.': '.'}`. Do not treat empty as invalid.
