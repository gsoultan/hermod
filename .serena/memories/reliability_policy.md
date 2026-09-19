# The workflow Reliability Policy

Four settings in the editor's Settings tab (`SidebarDrawer.tsx`, "Reliability
Policy"): **Dead Letter Sink**, **DLQ Alert Threshold**, **Prioritize DLQ on
startup**, **Dry-Run Mode**. They are persisted as columns on `workflows` and
read by `registry_workflow.go` when it builds an engine. Three of the four did
not work; the audit and the fixes are below.

## Dry-run means: read normally, write nowhere, acknowledge nothing

The rule is one sentence and it is load-bearing. `writeToSink` /
`writeBatchToSink` return the `errDryRun` sentinel (`pkg/engine/writer.go`)
*before* every other branch — above the safe-mode divert and above the
failed-validation divert, both of which write to the dead-letter sink. It used
to sit below them, so a "dry" run performed real writes against a real
destination.

The sentinel is not a failure and not a delivery:

- `processMessage` returns on it without logging a sink error, which is what
  skips `source.Ack` — the point of the whole change. Acknowledging is what
  advances a replication slot or a polling watermark, so the old behaviour
  (return `nil`, message acked) consumed the source and destroyed exactly the
  data the setting exists to protect.
- the sink writer's flush excludes it from `recordFailure`/`recordSuccess`, so a
  preview cannot trip a circuit breaker on a sink it never called.
- `IncProcessed` still runs. The message did go through the pipeline, and a
  frozen counter would have the stall watchdog read a working dry run as a wedge.

Three consequences that are not obvious:

1. **The stall watchdog stands down for the duration** (`pkg/engine/stall.go`).
   Unacked work that never completes *is* dry-run working; left in, the watchdog
   declares a stall the moment the stream goes quiet and the supervisor restarts
   the engine on a loop.
2. **A resumed message is the one exception.** `DeadLetterOrphanedMessage` writes
   even in dry-run, because a resumed message has no source row left holding it —
   the suspended row is deleted as soon as the resume returns — so declining the
   write destroys it rather than preserving it. Everywhere else, declining
   preserves. `replayLost` in `registry_workflow.go` is the only caller.
3. **A non-CDC source re-reads.** Nothing is acked, so a polling source sees the
   same rows each cycle and a queue source stalls on its prefetch limit. That is
   a loop you switch off, not data loss, and it was the accepted trade for never
   moving the cursor.

The traversal's node-failure log has a dry-run arm too: saying "the message is
lost" would be the opposite of what happened.

## The DLQ threshold is edge-triggered from the engine

Dead-lettering changes no status, and the alert lived inside the registry's
`OnStatusChange` callback — so nothing ever evaluated it. A pipeline parking
every message it received reported "running" and stayed silent.

`recordDeadLetter` (`writer.go`) is now the single counting path — every park
goes through it — and it calls `notifyStatusChange()` on the message that
crosses `Config.DLQThreshold`, exactly once per engine run. Once, deliberately:
the count only climbs, so firing per message past the line would have the
registry write workflow, source and sink status rows to storage for each one.
`notifyOnStatusChange` latches it again with an `atomic.Bool` so later
transitions (a sink flapping) do not re-send.

The count is the engine's own and resets on restart — `NewEngine` builds a fresh
`StatusTracker` and there is no Reset — so the alert says "dead-lettered N since
it started", never "N messages in the DLQ".

## A setting only reaches a running engine if hasConfigChanged says so

`internal/engine/worker/sync.go` is the *only* live-reconfiguration path;
`PUT /api/workflows/{id}` persists and returns without restarting anything. The
comparison used to be Name, VHost, DeadLetterSinkID and the graph, so every other
engine setting was inert on a running workflow — ticking Dry-Run Mode showed the
badge and changed nothing until an unrelated restart.

`workflowRuntimeConfigDiffers` now owns that comparison and **must stay in step
with the overrides `registry_workflow.go` applies**. Anything listed there and
missing here is a setting that silently does not take effect. Same bug class as
[reachability_tests](reachability_tests.md).

## Prioritize DLQ, and which sinks can be drained

`PrioritizeDLQ` wraps the source at start-up (`runner.go`) in a
`source.PrioritySource`: a 50 ms bounded read from the DLQ first, tagged
`_hermod_source=recovery`, then a blocking read from the primary; `Ack` routes
back to whichever produced the message. `POST /workflows/{id}/drain` does the
same on demand via `Engine.DrainDLQ`.

`DrainDLQ` swaps `e.source` **while the read loop is using it**. That was a real
data race (`engine.go` write vs `runner.go` `Ack` read). The field is now read
through `currentSource()` behind `sourceMu` — its own mutex, not `e.mu`, because
the read loop touches the source per message and sharing the engine-wide lock
would serialise the pipeline behind it. Never read `e.source` directly.

Both features need the dead-letter sink to be a type that is *also* a source.
The editor answered that from a hand-written list of 25 sink types and it had
drifted both ways — four types advertised that are not sources (workflow refused
to start), nine that work omitted (checkbox greyed out, feature unreachable).
`internal/factory/dlq_recovery.go` owns the list, `GET
/api/sinks/capabilities/dlq-recovery` serves it, and the editor asks. The drift
test reads the case labels out of both factory switches with `go/ast` rather than
keeping a third copy — which is how `ftp` and `s3` were found missing and
`scylladb` (a source that is not a sink) found wrongly present.

## Notification tests must call notifyOnStatusChange

`TestAlertingOnStatusChange` and `TestDLQThresholdAlerting` used to define a
closure in the test body that re-implemented the registry's logic — one even said
so: "Simulation function (replicates Registry's SetOnStatusChange logic)" — and
asserted on itself. They passed while the real callback never evaluated the
threshold. The decision body is extracted as `Registry.notifyOnStatusChange` so a
test has to come through the same code the engine calls.
