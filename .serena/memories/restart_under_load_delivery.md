# Restarting a workflow under load: what must hold

`TestBranchingWorkflowUnderConcurrentLoad`
(`internal/engine/registry/branching_load_integration_test.go`, integration tag)
drives a multi-source workflow (Postgres CDC + RabbitMQ -> db_lookup -> parallel
api_lookup / db_lookup -> merge db_lookup -> audit sink + switch -> RabbitMQ sink
/ condition rejoin -> Postgres sink) at 300 producers and checks every record:
in audit once, in exactly one of review/enriched, every value as predicted, no
WARN/ERROR log. A trigger on the sink tables (`lt_writes`) counts writes, because
the upsert hides repeats from a plain count. `LOAD_RESTART_AT=n` stops and
restarts the workflow mid-run; repeats are then counted (at-least-once), loss and
wrong values still fail. Env: HERMOD_INTEGRATION, FLOW_SOURCE_DSN/FLOW_SINK_DSN or
POSTGRES_DSN, RABBITMQ_URL, LOAD_USERS, LOAD_PER_USER, LOAD_DRAIN_TIMEOUT.

Without a restart it was clean from the start. A restart found four defects:

1. **Source closed before the drain** (`pkg/engine/runner.go`). Drain-time acks
   then failed ("rabbitmq channel not connected") and the broker redelivered.
   `closeSourceOnShutdown` now runs after the sink writers finish. Nothing in the
   drain waits on the source's Read. Test: `TestGracefulStopAcknowledgesInFlight...`.
2. **No final standby status on stop** (`postgres.go confirmFinalPosition`). The
   position reached the server every `standbyMessageTimeout` (10s) only. Test:
   `TestCloseConfirmsTheAcknowledgedPosition` (integration).
3. **Highest ack, not lowest** (`ack_window.go`). Out-of-order acks confirmed
   in-flight changes; a restart lost ~30/3000. `ackWindow` keeps only outstanding
   LSNs (counts + min-heap, rebuilt before stale entries outnumber live ones), so
   one never-acked change holds the slot without holding memory for the stream.
   An ack may arrive before `noteEmitted` (channel send first): kept in `early`.
   Not `pkg/infra/ackwatermark`: it re-indexes per ack and keeps everything behind
   a stuck entry. Consequence: an unacked message (dead letters off) now pins the
   slot until redelivered, which is what docs/reliability.md always said.
4. **Stop mid-split acked the message** (`traversal.Interrupted`,
   `pkgengine.ErrWalkInterrupted`). A node that could not acquire its semaphore,
   or failed with the context done, left the walk partly routed; the engine wrote
   the finished branches and acked. The router now refuses the whole walk; the
   runner logs it at INFO and does not ack. Test:
   `TestStopMidWalkLeavesASplitMessageUnacknowledged`.

Stop-time noise moved off ERROR: `Node X failed` when ctx is done (traversal
handleResults) and `Failed to write message to buffer` when ctx is done.

Still open: `ackwatermark.Tracker` past `maxPending` stops recording, and once
its queue drains a later recorded item can move the mark past a dropped one.
The traversal's "keeps no dead letters, so the message is lost" line is wrong
for sources that redeliver unacked messages.
