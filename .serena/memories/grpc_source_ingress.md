# The gRPC source: one fixed service, keys read per publish

A gRPC source is push-only. Hermod serves one fixed service,
`hermod.source.grpc.v1.SourceService/Publish`
(`pkg/comm/source/grpc/proto/source.proto`); there is no user `.proto`.
`GenericProtoSource` (`generic.go`) can decode a caller's own protobuf message
from a `.proto` on disk, but nothing in `internal/factory` builds it — only the
conformance suite does. A record travels as JSON in `payload`.

## How a publish is routed and checked

- `NewGrpcSource(path)` registers an in-process channel under `path` at
  construction; `Publish` dispatches to it. The path is a label, not a URL, and
  exists only while the workflow's engine holds the source. The registry is
  per process, so the ingress and the engine must be the same process.
- The API key is not on the registered source. `Publish` lists the stored
  sources on every call and takes the key of the gRPC source whose `path`
  matches (`""` is compared as `/grpc/default`). **A path the store holds no
  source for is refused**, not treated as unkeyed. Only a nil store skips the
  check.
- The store is asked for per call (`Server.StorageFunc`, wired to the handler's
  current storage in `internal/api/server.go`). A copy taken when the listener
  started was nil on a first run and stale after a database switch.
- The message has one body: `SetAfter` is `SetPayload`. The row is `payload`,
  or `after` when `payload` is empty. With `operation` set the workflow reads
  it as `after.<field>`; without, as `<field>`.
- `dispatched` means queued in the source's buffer. Nothing reports the
  workflow's outcome back: `GrpcSource.Ack` is a no-op and `hermod.Source` has
  no failure callback.
- Every failure is a plain Go error, so the client sees gRPC code `Unknown` and
  has to read the message.

## Open: a probe takes the path from a running workflow

Observed live 2026-10-04. **Test Connection** on a gRPC source whose workflow is
running (`POST /api/sources/test`) builds a second `GrpcSource` through the
factory. Its constructor supersedes the running registration, and its `Close`
— it is now the owner — deletes the path. The workflow stays `running`, reads a
channel nothing dispatches to, and every publish fails with `no gRPC source
registered for path` until the workflow is restarted. `webhook`, `graphql` and
`form` sources register the same way. Anything that builds a source through the
factory without running it has this effect, so the fix belongs in the source
(take the path on the first `Read`), not in each caller.

## Testing it live

`buf curl --protocol grpc --http2-prior-knowledge --schema source.proto -d …
http://host:50051/hermod.source.grpc.v1.SourceService/Publish`. The server has
no reflection. `bytes` fields are base64 in JSON. The reachability test is
`internal/api/grpc_key_after_setup_test.go`: a server built with nil storage,
the real setup handler, the real gRPC server on an in-process listener.
