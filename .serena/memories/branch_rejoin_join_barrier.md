# Branches that meet again: the join barrier

A node with several in-edges fires once every edge into it has *resolved*:
delivered by `resolveEdge` or pruned by `pruneBranch`
(`internal/engine/registry/traversal/traversal.go`). A branching node
(condition, switch, router) resolves the edges it did not take by pruning them.

## The loss (fixed)

`pruneBranch` treated "the last edge to resolve was a prune" as "the node was
reached only by pruned branches" and pruned it, even when a taken branch had
already delivered a message into its slot. The message was never processed.

    src -> if -true--> mark -> out
           +--false----------+

With the false edge listed before the true one, a false message reaches `out`
first (1 of 2), then the true branch's prune arrives (2 of 2) and prunes `out`.
Edge order is `wf.Edges` order (`adj` is built from it), so one workflow lost
every message on its straight branch and none on the other. The runner then saw
a message that resolved no sink: logged "Messages delivered nowhere", parked it
in the DLQ, or left it unacknowledged.

Fix: when the prune wins the barrier and `CurrentMessages[idx] != nil`, run the
node. `resolveEdge` stores the message before it counts the edge, so a delivery
that happened is visible to the prune that completes the count.

## Why nothing caught it

- **The simulation walks the graph differently.** `simulation.forward` counts
  every edge, taken or not, and `visit` runs a node that has a waiting message.
  The preview showed both branches delivering while the engine dropped one.
- **`Fired` is not delivery.** `TestWorkflowTraversal_ConditionalJoinReached`
  asserted `Fired[J] != 0`, and a pruned node is marked fired too. Assert the
  node *ran* (count `RunWorkflowNode` calls) or what reached `Routed`.
- **The test mock over-released.** `mockRegistry.RunWorkflowNode` returned the
  input without `Retain()`. The real registry retains it (`registry_routing.go`,
  after the executor runs), so every node released one reference too many and
  the message went back to the pool while still in use. The failures landed in
  *other* tests: the fan-out test failed 12/30 runs, and passed 50/50 alone.
  When a pooled-message test flakes only in a package run, pair it with each
  other test to find the one corrupting the pool.

## Tests

- `TestWorkflowTraversal_BranchesThatMeetAgainDeliverTheMessage` — both
  branches x both edge orders; only "false, direct edge first" failed.
- `TestConditionBranchesThatMeetAgainDeliverEveryMessage`
  (`internal/engine/registry`) — `StartWorkflow` with the editor's condition
  shape and a sink fixture; without the fix seq 1..3 never arrive.

## Still open (same function, not fixed, found by reading — no test yet)

A taken edge whose node emitted *no* messages (a filter that dropped it) is
neither delivered nor pruned in `handleResults` — the `for _, msg := range msgs`
loop runs zero times — so a join downstream of it never completes. A node that
failed returns before resolving any edge, with the same effect. A condition
alone cannot trigger it (one branch, one message); a multicast whose branches
meet again after a filter should. Reproduce it before fixing it.

See also [what a test run reports](simulation_path_contract.md).
