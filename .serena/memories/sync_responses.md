# Synchronous responses: pkg/comm/reply

A webhook or gRPC source can hold its caller until the workflow has finished
with the record and answer with what happened. Source config:
`response_mode: sync` and `response_timeout` (a Go duration; 30s when missing or
unreadable, capped at 5m). Anything else answers `dispatched` at once.

## How the answer gets back

- The transport calls `reply.Expect(msg)` **before** it dispatches. That
  registers a waiter in a process-wide table under a fresh UUID and writes the
  id to the message as `_hermod_reply_id`. Registered after the dispatch, a fast
  workflow could finish first and answer nobody.
- `Runner.processMessage` calls `reply.Take(m)` first thing: it reads the id and
  **removes it from the message**, so neither a sink nor the returned record
  carries it. A deferred `replyState.resolve` answers the caller on every way
  out, including a panic (the outcome starts as `failed`).
- Nothing in between — source wrappers, buffers, the router — knows about it.
  That is the point of carrying the id on the message rather than adding a
  callback to `hermod.Source`.
- Both ends are in one process, as a push source's transport and engine
  already must be.

## What each exit of processMessage reports

| Exit | Status |
| :--- | :--- |
| every routed write returned nil, none parked | `delivered` |
| sink node wrote inline (`MetaDeliveredInline`) | `delivered` |
| workflow has no sink; or a dry run | `completed` |
| a write failed and was parked (`MetaDeadLettered` on the routed message) | `dead_lettered` |
| a node failed and parked it; validation failed and parked it | `dead_lettered` |
| a sink write returned an error; routing returned an error | `failed` |
| the workflow has sinks and routed to none | `failed`, or `dead_lettered` if parked |

The record is captured in `conclude`, not in the deferred `resolve`: by then
the routed messages have been released. It is the first routed message, as
`json.Marshal` renders it (the shape a JSON sink receives), or the source
message when nothing was routed.

## Things that bite

- **A filter that drops a message is not distinguishable from an unresolved
  sink.** The traversal reports neither; both arrive as "has sinks, no
  targets", which the engine does not acknowledge. A synchronous caller is told
  `failed` / `dead_lettered` with `the workflow reached no sink for this
  message`. Telling them apart needs a marker from the traversal.
- `writeToDLQ` now sets `MetaDeadLettered` on a message it parked, after the
  write, and a failed sink write records `_hermod_last_error` before it is
  parked. Before, "parked" and "delivered" both looked like a nil error.
- The waiter table is bounded (`defaultMaxPending`, 10,000). Past it `Expect`
  returns `ErrTooManyPending`; the webhook endpoint answers 503.
- A timeout is answered `pending` with 202 / an OK gRPC reply, never as an
  error: a caller that retries an error sends the record twice.
- `replyState` lives on processMessage's stack and its methods return at once
  for a message nobody awaits. Do not turn `conclude` into a closure: the
  captured outcome would move to the heap for every message.
  `allocation_budget_test.go` is the guard.
- gRPC `Publish` drops a client-supplied `_hermod_reply_id` metadata key.
- Read a message's id **before** dispatching it. After, it is the engine's and
  may be back in the pool; both transports used to read it afterwards.

## The WebSocket source

It is a client: Hermod dials a URL and reads frames. With `response_mode: sync`
(`Source.SetResponse`, set by the factory from `reply.ModeOf`) `emit` calls
`reply.Expect` on each message before handing it to the engine and starts one
goroutine (`answer`) that waits and writes a result frame —
`{"id","status","error","record"}`, `id` being the frame's envelope id — back on
the connection the frame arrived on. Writes are serialised by `writeMu`; a
connection takes one data writer at a time. A reconnect is a new conversation:
an answer for a frame from the old connection is dropped. The reachability test
is `internal/factory/websocket_sync_reachability_test.go`.

An inbound WebSocket source — one callers connect to — does not exist yet.
