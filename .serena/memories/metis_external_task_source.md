# The metis external-task source: Hermod as a step of a BPMN process

`pkg/comm/source/metis/external_task.go`, factory type `metis_task`.

The other two metis connectors are one-way — the sink drives the engine, the
polling source watches it. This one *is* a step. A service task marked with a
topic is published by the engine as work rather than called out to; the source
locks it, the pipeline does the work, and `Ack` completes the task with
whatever the pipeline produced as the step's output variables. Because the
engine publishes rather than calls, a worker behind any firewall that can reach
the server can serve a step.

## It maps onto Hermod's contract rather than extending it

| External-task protocol | Hermod |
| --- | --- |
| fetch-and-lock | `Read` |
| complete, with output variables | `Ack` |
| let the lock lapse → redelivery | not acknowledging |

Hermod has **no Nack**: a failed pipeline simply returns without acknowledging.
That is exactly the lease model, so nothing here is persisted and the source is
deliberately **not `hermod.Stateful`** — what a restart resumes from is the
engine's own locks.

## The three decisions, each pinned by a mutation-verified test

- **A parked message fails the task instead of completing it.** See
  [An acknowledgement is not always a success](ack_is_not_always_a_success.md) —
  this is the whole reason that memory exists. Read `_hermod_failed_at`, not
  `_hermod_dead_lettered`.
- **Acknowledging past the lock is refused, not attempted.** The server refuses
  it too, inside its transaction (`externalTaskService.Complete` checks both
  `WorkerID` and expiry), so the client guard exists to skip a doomed round trip
  and name the knob: raise `lock_duration`, lower `max_tasks`.
- **An unset `worker_id` is generated per source.** The engine authorises a
  completion by that id *alone*, so a shared constant would let two pipelines
  finish each other's tasks. Hostname + 8 random bytes; the hostname alone is
  not enough because replicas share it.

## Pacing matches the SDK's own worker

A fetch that found work is followed **immediately** by the next; only an empty
or failed fetch is paced by `poll_interval`. Pacing every fetch caps a topic at
`max_tasks` per interval however fast the pipeline runs. `sdk.Worker.Run` sleeps
only when `len(tasks) == 0`, and `Fail` matches its `task.Retries - 1`
(clamped at 0) rather than inventing a retry policy.

## Gotchas

- **A task may be run twice.** Completion that did not reach the engine means
  redelivery, so sinks downstream of this source want to be idempotent.
- **The lock is the budget for the whole pipeline**, and every task in a batch
  is locked when the batch is fetched — so the last task's lock is burning while
  the ones ahead of it are still in flight.
- **Sampling it in the UI locks real work**, which is why it is not in
  `NON_DESTRUCTIVE_TYPES`.
- `fetch-and-lock` takes **no project**: a topic is subscribed to across the
  organization. The wizard gate needed a separate requirement list for that, or
  it would demand a project the form has no field for.

## Reachability

`internal/factory/metis_external_task_reachability_test.go` builds the source
from a stored config map and drives a real completion through the factory —
verified to fail on a config-key drift. The five UI lists that must agree
(palette, form dropdown, wizard gate, config panel, factory) are pinned by
`ui/src/__tests__/metisExternalTaskSource.test.ts`.

Related: [The metis connectors](metis_connectors.md),
[Connector conformance suite](connector_conformance_suite.md).
