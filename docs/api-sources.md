# API sources and streams

GraphQL, webhook and gRPC sources, synchronous responses, and the real-time SSE stream.

## Real-time Data Streams (SSE)

Hermod includes a built-in **SSE (Server-Sent Events) Sink** that allows you to stream data directly to web applications or any SSE-compatible client. This is ideal for real-time dashboards, live notifications, or reactive data orchestration without the complexity of a full message broker.

### Security Features
- **Authentication**: Secure your streams with an Auth Token. Clients must provide the token via the `Authorization: Bearer <token>` header or the `token` query parameter.
- **Origin Verification**: Restrict access to specific web origins (CORS) to prevent unauthorized websites from subscribing to your data streams.
- **Interactive Integration Guide**: The Hermod UI provides a real-time, tailored JavaScript snippet for each SSE Sink, including security parameters, to simplify client-side integration.

### Consuming the Stream

Clients can subscribe to a specific stream using the following endpoint:
`GET /streams/sse?stream={stream_name}&token={optional_token}`

The endpoint is separated from the management API to ensure data isolation and high throughput.

### Sample Client

A complete HTML/JavaScript sample demonstrating how to consume an SSE stream is available in the `examples/sse-sink/` directory.

To use it:
1.  Configure an **SSE Sink** in your workflow with a custom stream name and optional security settings.
2.  Open `examples/sse-sink/index.html` in your browser.
3.  Enter the stream name and connect to see live data.

## Modern API Sources (GraphQL & gRPC)

Hermod supports modern API protocols as data sources, allowing you to push data directly into Hermod instead of relying solely on CDC or webhooks.

### GraphQL Source

Hermod exposes a GraphQL endpoint at `/api/graphql/{path}`. You can send standard GraphQL POST requests:

```json
{
  "query": "mutation { publish(table: \"orders\", payload: \"...\") }",
  "variables": {}
}
```

The entire request body is captured as the message payload, and if it's a valid GraphQL JSON, the `query` and `variables` are extracted into the message data.

### Webhook Source

A webhook source receives HTTP requests at `/api/webhooks/<path>`; the source's
path is the whole request path. It answers `202 {"status":"dispatched"}` once
the request is queued, or with the workflow's result when its **Response** is
**Synchronous** (see
[Synchronous responses](#synchronous-responses-webhook-grpc-and-websocket)).

A source can ask for either credential, both, or neither:

| Credential | Config key | The request must carry |
| :--- | :--- | :--- |
| API key | `api_key` | `X-API-Key: <key>` |
| Signing secret | `secret` | `X-Hub-Signature-256: sha256=<hex>` (or `X-Webhook-Signature`), the HMAC-SHA256 of the request body |

A request that fails either is answered 401. If the store holding the
credentials cannot be read, the request is answered 503 rather than let through.

```bash
BODY='{"order_id":7}'
SIG=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" | sed 's/^.* //')
curl -X POST http://localhost:4000/api/webhooks/orders \
  -H "X-API-Key: $KEY" -H "X-Hub-Signature-256: sha256=$SIG" -d "$BODY"
```

#### mimir-server events

A webhook source whose **Sender** is **mimir-server events** (`"preset":
"mimir"`) takes the events a [mimir-server](https://github.com/gsoultan/mimir-server)
sends to a webhook made on its console: `training.finished`,
`training.failed`, `training.queued`, `model.accuracy_dropped`,
`model.created`, `model.updated`, `model.deleted`, `user.created`,
`user.deleted` and `test`. Paste Hermod's webhook URL into the console and
the `whsec_…` secret it shows once into the source's **Webhook secret**
(`secret`). Then:

| | |
| :--- | :--- |
| Signature | Each delivery is checked against its `Mimir-Signature` header (`t=<unix>,v1=<hex HMAC-SHA256 of "<t>.<body>">`, within five minutes of now). The API key and `X-Hub-Signature-256` of a plain webhook do not apply. A source with the preset but no secret answers 503 to every delivery. |
| Repeats | mimir retries a delivery for about a day until it gets a 2xx, so the same event can arrive again. A repeat (same event `id` on the same path) is answered `200 {"status":"duplicate"}` and not dispatched again. Ids are remembered for 25 hours, per Hermod process. |
| Events | **Events** (`events`, comma-separated types) keeps only those; another type is answered `200 {"status":"ignored"}`. Empty takes every event. |
| The message | The body as sent: `{"id", "type", "time", "data"}`, with `data` shaped by the type (a training event carries `job`, `model.accuracy_dropped` carries `model`, `week` and `accuracy`). The type is also in the `mimir_event` metadata and the id in `mimir_event_id`, so a Switch node can route on `metadata.mimir_event`. |

A replayed request (**Webhook history → Replay**) is dispatched as before,
without the signature or repeat check.

### gRPC Source

A gRPC source receives records that your service pushes to Hermod. Hermod does
not call your service, and it does not load a `.proto` of yours: it serves one
fixed service, `hermod.source.grpc.v1.SourceService`, and your record travels
inside it as JSON.

**The contract** is [`pkg/comm/source/grpc/proto/source.proto`](../pkg/comm/source/grpc/proto/source.proto).
Generate your client from that file; the source form also shows it with a copy
button.

```proto
service SourceService {
  rpc Publish(PublishRequest) returns (PublishResponse);
}
```

**Setting it up**

1. Create a source of type **gRPC** and give it a path, for example
   `/grpc/orders`. The path is a label, not a URL: the client sends it in
   `PublishRequest.path`, exactly as written. An empty path in a request means
   `/grpc/default`.
2. Optionally set an API key. Clients then send it as gRPC metadata `x-api-key`.
3. Put the source in a workflow and start the workflow. The path exists only
   while the workflow runs; before that every call fails with
   `no gRPC source registered for path: /grpc/orders`.
4. Call `Publish` on the gRPC port: `50051`, or whatever `--grpc-port` /
   `HERMOD_GRPC_PORT` set. The port is plaintext and has no server reflection,
   so tools need the `.proto`.

```bash
buf curl --protocol grpc --http2-prior-knowledge \
  --schema pkg/comm/source/grpc/proto/source.proto \
  -H 'x-api-key: YOUR_API_KEY' \
  -d '{"path":"/grpc/orders","payload":"eyJvcmRlcl9pZCI6MX0="}' \
  http://localhost:50051/hermod.source.grpc.v1.SourceService/Publish
# {"id":"…","status":"dispatched"}
```

`payload` is a `bytes` field. A generated client passes the JSON text as it is
(`Payload: []byte(`{"order_id":1}`)` in Go); a JSON tool such as `buf curl` needs
it base64-encoded, which is what the value above is.

**What the workflow sees**

| You send | The record in the workflow |
| :--- | :--- |
| `payload` only | Its fields at the top level: `order_id` |
| `payload` and `operation` (`create`, `update`, `delete`, `snapshot`) | A change event: `after.order_id`, and `before.…` if you sent `before` |
| `after` instead of `payload` | The same as `payload`; `payload` wins when both are set |
| `payload` that is not a JSON object | One string field, `payload` |
| no `id` | A generated UUID, returned in the response |

**What the reply means.** By default `dispatched`: the record was queued for
the workflow, and `Publish` did not wait for transformations or sinks. Set the
source's **Response** to **Synchronous** and `Publish` waits for the workflow
instead — see [Synchronous responses](#synchronous-responses-webhook-grpc-and-websocket).
A call that fails before the record is queued returns an error whose message
says why:

| Message | Meaning |
| :--- | :--- |
| `invalid api key` | The source has an API key and `x-api-key` was missing or wrong |
| `no gRPC source is configured for path: …` | No gRPC source has that path; check it character for character |
| `no gRPC source registered for path: …` | The source exists but its workflow is not running |
| `gRPC source buffer full for path: …` | The workflow is behind; retry the call |

**Many records: `PublishStream`.** `PublishStream` is `Publish` over one
long-lived, bidirectional stream, with the same request and response messages.
Every record sent is answered with one response under that record's `id`:
`dispatched`, or for a synchronous source what the workflow did with it.

- Answers are sent as each record is answered, so for a synchronous source they
  can arrive in a different order from the requests. Match them by `id`.
- A record that cannot be queued — the workflow is not running, or its buffer is
  full — is answered `rejected` with the reason, and the stream stays open.
- The API key is sent once, as `x-api-key` metadata when the stream is opened.
  A record the key does not cover ends the stream with the error. A key that is
  changed or removed stops working on open streams within 30 seconds.
- When the producer closes its side, the stream ends once every record already
  sent has been answered.

```bash
printf '%s\n' \
  '{"path":"/grpc/orders","id":"a","payload":"eyJvcmRlcl9pZCI6MX0="}' \
  '{"path":"/grpc/orders","id":"b","payload":"eyJvcmRlcl9pZCI6MX0="}' |
buf curl --protocol grpc --http2-prior-knowledge \
  --schema pkg/comm/source/grpc/proto/source.proto -d @- \
  http://localhost:50051/hermod.source.grpc.v1.SourceService/PublishStream
```

### Synchronous responses (webhook, gRPC and WebSocket)

A webhook or gRPC source answers its caller as soon as the record is queued:
`202 {"status":"dispatched"}` from the webhook endpoint, `dispatched` from
`Publish`. The caller does not learn what the workflow did with it.

Set **Response** to **Synchronous** on the source (`response_mode: sync`) and the
caller is held until the workflow has finished with the record, then told what
happened:

| `status` | Meaning | Webhook HTTP status |
| :--- | :--- | :--- |
| `delivered` | Written to every sink the workflow routed it to | 200 |
| `completed` | The workflow ran and had nothing to write: it has no sink, or it is a dry run | 200 |
| `filtered` | The workflow dropped it on purpose: a filter, validator or deduplicate node let it go no further, or its outcome had no edge to follow | 200 |
| `dead_lettered` | It failed and was parked as a dead letter; `error` says why | 502 |
| `failed` | It failed and is not preserved; `error` says why | 502 |
| `pending` | The response timeout ran out first. The record is still being processed — do not send it again | 202 |

```json
{"id":"…","status":"delivered","record":{"after":{"order_id":7},"id":"…","metadata":{…},"operation":"create","table":"webhook"}}
{"id":"…","status":"failed","error":"sink write error: sink write failed after 3 retries: unexpected status code: 422","record":{…}}
```

- `record` is the record as the workflow left it, in the shape a JSON sink
  receives. When the workflow wrote to several sinks it is the one sent to the
  first of them.
- **Response timeout** (`response_timeout`, for example `10s` or `2m`) is how
  long a caller is held: 30s when empty or unreadable, 5m at most.
- `Publish` returns the same `status`, `error` and `record` fields, and the
  call itself succeeds for every status: what the workflow did is in the reply.
- A message the workflow could not deliver and did not drop on purpose — a
  sink node that could not be resolved, for one — is reported as `failed` or
  `dead_lettered` with `the workflow reached no sink for this message`. The
  engine does not acknowledge such a message. A message a filter dropped is
  `filtered`, and is acknowledged.
- At most 10,000 callers are held at once across the process; past that a
  synchronous request is refused (503 from the webhook endpoint) rather than
  queued without limit.

**WebSocket source.** The WebSocket source dials a server and reads frames from
it. Set to Synchronous, it writes one result frame back on the same connection
for each frame it read, once the workflow has finished with it:

```json
{"id":"req-1","status":"delivered","record":{…}}
```

`id` is the `id` of the frame being answered, empty if the frame carried none,
and the statuses are the ones above; `pending` is written when the response
timeout runs out. A result is written to the connection its frame arrived on:
if that connection drops first, the result is not sent on the next one.

**Inbound WebSocket.** A caller can send records over a WebSocket connected to
`/api/ws/in/<path>`. They go to the webhook source whose path is that URL —
`/api/ws/in/<path>` — which the editor's palette offers as *WebSocket (Server)*.
When that source is Synchronous, each frame is answered on the connection with
the same result frame, `id` being the frame's `id` (or a generated one, for a
frame that is the record itself), in place of the `{"ack":"…","ok":true}`
acknowledgement an asynchronous source sends. A frame that could not be queued
is answered `rejected`.
