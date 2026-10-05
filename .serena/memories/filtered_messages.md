# A filtered message is not an undeliverable one

The engine sees "the workflow has sinks and routed this message to none" for two
opposite things: a record a filter dropped on purpose, and a record no sink
could be resolved for. Since an outage acknowledged records in the second state
(see the comment in `Runner.processMessage`), the engine refuses to acknowledge
that shape: it parks the record in the dead-letter sink, or leaves it on the
source. Until 2026-10-05 that also happened to **every record a filter dropped**.

## How they are told apart

`WorkflowTraversal` carries two flags, merged back from fan-out forks:

- `Filtered` — a `transformation`, `validator` or `deduplicate` node emitted
  nothing without an error (`dropsOnPurpose`), or a node emitted a message and
  none of its outgoing edges was taken (a condition with no edge for its outcome).
- `Unaccounted` — a node failed; a sink node was reached that `SinkNodeToIndex`
  does not know; or any other node type emitted nothing.

The router (`setupWorkflowRouter`) stamps `MetaFiltered` only when nothing was
routed, `Filtered` is set and `Unaccounted`, `DeadLettered`, `InlineDelivered`
and `InlineFailed` are all clear. One unexplained walk outweighs every
deliberate drop. `processMessage` then acknowledges, parks nothing, and tells a
waiting caller `reply.Filtered`.

## Deliberately not covered

`approval` (pending), `wait` (suspended), `collect` (absorbed) and `foreach`
over an empty array also emit nothing. They are holding the message or have
nothing to do, not dropping it, and each needs its own answer about
acknowledgement. They set `Unaccounted`, so the engine treats them as before —
which today means parked in the dead-letter sink or left unacknowledged. That is
probably wrong for them too and has not been looked at.

## The markers are verdicts, not inputs

`MetaFiltered`, `MetaDeliveredInline` and `MetaDeadLettered` each make the
engine acknowledge a message it routed nowhere. A producer can set any metadata
on its record, so the router deletes all three from a message as it enters.
Before that, a record arriving with `_hermod_delivered_inline: true` was
acknowledged undelivered whenever its workflow routed it nowhere.

## Tests

- `traversal/filtered_marker_test.go` — each way a walk can end, and that an
  unresolved sink or a failed node is never "filtered".
- `registry/filtered_message_e2e_test.go` — a real workflow: the dropped record
  is acknowledged and not parked; a producer cannot declare a verdict.
- `pkg/engine/sync_reply_test.go` — the engine's side: marker → ack, no park,
  `filtered`.
