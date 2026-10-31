# What a test run reports, and what the canvas draws from it

The editor's Test button (and a field refresh) posts the workflow to
`POST /api/workflows/test`; `Registry.SimulateWorkflow` walks it and answers with
one `registry.WorkflowStepResult` per node visit. The canvas draws the run from
that list alone (`ui/src/pages/workflows/WorkflowEditor/simulation/`), so the
list is a contract with two consumers: the field lists (`useNodeContext`, which
reads `payload`) and the path highlight.

## The fields the highlight needs

- `taken_edges` — the IDs of the edges a node's output was delivered along,
  recorded in `simulation.forward` at the moment it delivers. The UI never
  re-derives which edges were taken from `branch` + edge labels: that rule already
  exists twice in Go (see below) and a third copy in TypeScript would drift.
- `skipped` — no message reached the node. An unreached node was, and still is,
  reported `filtered: true`, which is also what a node that *dropped* the message
  reports; `skipped` is the only thing that tells them apart.

A node can be reported more than once: a failure is an `error` step followed by a
`filtered` one. The UI ranks a node's steps (error > emitted > skipped >
filtered) rather than taking the last.

A failed node sends nothing on, whatever its executor handed back. The live
traversal prunes every edge out of a failed node (`handleResults`); `simulation.run`
used to forward `msgs[0]` even with an error, so the preview showed the nodes
after a failure running on its output. Now the error step keeps that payload (for
the editor to show) and `run` returns nil, so what follows is `skipped`.
`TestSimulationSendsNothingPastANodeThatFailed`.

## Where branches meet

A node with several in-edges fires once, and what every delivering branch
brought is merged into the waiting message by one rule, `traversal.MergeData`
(deep by default, the node's `strategy` can say `overwrite`/`shallow` or
`if_missing`). `simulation.deliver` and the live `resolveEdge` both call it.
The traversal used to keep the first arrival and merge only into a `join`
(Stateful Join) node, so a line split in two and drawn back together ran the
meeting node on one half's data and dropped the other's silently while the
preview showed both. `TestWorkflowTraversal_BranchesThatBothDeliverMergeWhereTheyMeet`.

## What the editor reads from the run, and when it stops

`useNodeContext` reads a predecessor's step as a node's input ahead of every
stored sample — but only while the run still speaks for it. `staleSimulationNodes`
(`simulation/simulationPath.ts`, next to `simulationOutdated`) marks a node stale
when its `data` object or type differs from `testRunGraph`, when an edge into it
was added, removed or rewired, and everything downstream of either. A stale node's
step is passed over like an unrun node's, so the walk falls back to the nearest
output that still holds and `inputSkipped` counts the gap. Without `testRunGraph`
(tests that set `testResults` alone) nothing is stale. Before this, renaming a
`targetField` after a Test left the old name in the next node's Available Fields
and ran its Live Preview on the old payload. `useNodeContextStaleRun.test.tsx`.

## Why this existed as a bug

The highlight was deleted with the legacy `useStyledFlow` (`ce5d533`) and nothing
replaced it, while the toast kept saying "Active paths are highlighted". Nobody
noticed partly because Data Pulse is on by default and already draws every edge
as the same animated dash — a path has to override that styling to be visible.

## One routing rule, shared with the engine

Which edges a node's output takes is `traversal.TakesEdge(branch, label)`: a node
that names a branch sends it along the edges labelled for it and along unlabelled
ones; a node that names none sends it along every edge. The live traversal
(`handleResults`) and `simulation.forward` both call it.

They used to keep a copy each, and the simulation's honoured a branch only for
`condition` and `switch`. `router` names one too (a rule label or `"default"`),
so a simulated router sent the sample down every edge and the canvas drew all of
them (#181). A third copy still lives in `replayTargets`, the resume path, and
differs: with a forced branch it does not take unlabelled edges.

## Testing it

- Go: `TestSimulationReportsOnlyTheBranchAConditionTook`,
  `TestSimulationTakesOnlyTheBranchARoutingNodeChose` and friends in
  `internal/engine/registry/simulation_test.go`; the wire names are pinned through
  the handler in `TestSimulationEndpointReportsThePathTheMessageTook`.
- The HTTP transport tests did not link node executors or the `advanced`
  transformers, so there a condition passed everything through and every `set`
  node was a no-op; the blank imports now match `cmd/hermod`.
- UI: `simulationPathHighlight.test.tsx` renders the real `FlowCanvas` in jsdom.
  React Flow needs a ResizeObserver that reports `contentRect`, a
  `DOMMatrixReadOnly`, and non-zero `offsetWidth/Height` before it draws edges.
- E2E: `simulation_path_e2e.spec.ts`. Screenshot with `animations: 'disabled'` —
  a capture taken as the result lands shows the canvas before its 0.2s
  transitions, which reads exactly like the highlight not working.
