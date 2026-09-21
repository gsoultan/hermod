# An acknowledgement is not always a success

Hermod's `Source.Ack` means "this message is fully handled" — and the engine
calls it in two cases that look identical to a source and are not:

1. Every sink write succeeded. The message was delivered.
2. The message could **not** be delivered but **was** preserved: a node failed,
   a sink exhausted its retries, safe mode diverted it, or validation rejected
   it — so the engine parked it in the dead-letter sink and then acknowledged,
   because replaying something already kept pins a replication slot and grows
   the queue without bound (`pkg/engine/runner.go`, the no-target and validation
   branches).

For a queue or a replication slot, case 2 is exactly right. For any source whose
`Ack` has a side effect on an external system, it is a trap.

## Where it bit

The [metis external-task source](metis_external_task_source.md) completes a BPMN
task in `Ack`. Taken at face value, case 2 completes the task — and the process
advances to its next step (approve the payment, ship the order) on work that is
sitting in a dead-letter queue.

## The marker to read is `_hermod_failed_at`

Measured, not assumed — `pkg/engine/ack_sees_pipeline_output_test.go` prints
what each path sets:

| Park route | `_hermod_failed_at` | `_hermod_dead_lettered` | `_hermod_validation_failed` |
| --- | --- | --- | --- |
| a node failed (`deadLetterNodeFailure`) | ✅ | ✅ | — |
| no sink resolved / sink outage (`writeToDLQ`) | ✅ | — | — |
| validation rejected it (runner + `writer.go`) | — | — | ✅ |

`_hermod_failed_at` is set by `prepareDLQMessage`, which is on the common path of
every park. `_hermod_dead_lettered` is the obvious-looking one and is set **only**
for a node failure — a guard reading it alone misses the sink-outage case, which
is the common one. The first version of the metis guard did exactly that, passed
its own tests, and was caught by an engine-level test rather than a connector
one.

**A connector cannot verify this from inside its own package.** The fake in a
connector test sets whatever metadata the test author believes the engine sets.
`pkg/engine/ack_sees_pipeline_output_test.go` drives the real runner through both
park routes and fails if a park ever becomes illegible to a source.

## The sibling fact, same file

`Ack` receives the message **as the pipeline left it**, not as the source read
it: for a node-graph workflow the router runs the whole traversal and rewrites
the message in place. That is what lets the metis source complete a task with
the pipeline's output. `TestAckReceivesThePipelinesOutputNotTheSourcesInput`
pins it and fails if the engine ever acknowledges a pre-pipeline copy.

Related: [The metis connectors](metis_connectors.md),
[Watermark-on-read in the polling sources](watermark_on_read_api_sources.md).
