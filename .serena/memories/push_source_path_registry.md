# Push sources hold a path: sourcebuf.PathRegistry

gRPC, webhook, GraphQL and form sources connect to nothing. Each registers a
path in an in-process registry, and the transport (`Publish`, the webhook
handler, …) calls the package's `Dispatch(path, msg)`. All four use
`sourcebuf.PathRegistry` (`pkg/comm/source/pathregistry.go`); each package
keeps its own `Register` / `Unregister` / `Dispatch` wrappers and its own error
text (`no webhook registered for path: …`), which callers and docs read.

## Who holds a path

- A source built while the path is free holds it **at once**, with no Read.
  This is load-bearing: a webhook that wakes a parked workflow is re-dispatched
  as soon as `WakeUpWorkflow` returns, before the new source has read anything.
- A source built while another holds the path **waits**. It takes over on its
  first `Read` (`TakeOver`), or when the holder closes — the newest waiting
  source is promoted.
- A replaced holder's channel is dropped, not closed, and its `Unregister` is a
  no-op. That is what keeps an outgoing engine's teardown from removing its
  successor.

## Why it is not "newest registration wins"

It was. `factory.CreateSource` is called by more than the engine: Test
Connection and sampling (`internal/discovery/service`, `openSource`) and the
worker's health check (`internal/engine/worker/health.go`) build a source, ping
it and close it. Built, the probe owned the path; closed, it deleted it. The
running workflow stayed `running` and every request was refused until it was
restarted. A probe is never read, so under the current rule it takes nothing.

Do not move registration into `Read` to fix a similar problem: the wake-up
retry above needs the path to exist as soon as the source is built.

## Details that bite

- `Dispatch` holds the read lock across the non-blocking send. Releasing it
  first lets `Unregister` close the channel in between, and the send panics.
- The registry is per process. The transport and the engine that runs the
  workflow must be in the same one.
- The form source also polls its submissions table every 5s, so a form
  submission that is not dispatched is still picked up, later.
- The reachability test is
  `internal/engine/registry/push_source_probe_test.go`: `Registry.CreateSource`
  for the running source, `Registry.TestSource` for the probe, then `Dispatch`.
