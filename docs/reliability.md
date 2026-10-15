# Reliability and delivery guarantees

What Hermod does to avoid losing data, the delivery guarantee it makes, and sink-side idempotency.

## Reliability and Data Loss Prevention

Hermod is designed to minimize data loss during operation and shutdown:

- **Graceful Draining**: During shutdown, Hermod drains its internal buffer to ensure all messages already read from the source reach the sink.
- **At-Least-Once Delivery**: The engine acknowledges messages to the source only after they have been successfully written to the sink. This ensures that if the process crashes, the source can re-deliver unacknowledged messages upon restart (depending on source implementation).
- **Retries with Backoff**: Failed writes to the sink are automatically retried with configurable exponential backoff.
- **Circuit Breaker Pattern**: Prevents cascading failures when downstream systems are unhealthy. Automatically opens after N consecutive failures and probes recovery in a half-open state.
- **Adaptive Batching**: Dynamically groups messages to optimize sink throughput and reduce network roundtrips.
- **Memory Safety**: Uses a bounded `RingBuffer` to prevent out-of-memory issues under high pressure.

**Important Note**: Since the default `RingBuffer` is in-memory, sudden process termination (e.g., `SIGKILL` or power failure) can result in the loss of messages currently held in the buffer. For use cases requiring absolute durability, consider implementing a persistent `Producer`/`Consumer` (buffer) interface (e.g., using a file-backed queue or a dedicated message broker).

### Payloads that are not JSON objects

A source is free to deliver something other than a JSON object — a RabbitMQ queue carrying plain text, a Kafka topic of CSV lines, a file read in `raw` mode, a bare number. Only a JSON *object* has field names to merge into the message root, so anything else is preserved under a single **`payload`** field rather than dropped:

| Source body | Resulting message fields |
| --- | --- |
| `{"id":7,"name":"ada"}` | `id` and `name` at the root, as before |
| `hello world` | `payload` = `"hello world"` |
| `id,name,qty` | `payload` = `"id,name,qty"` |
| `42` | `payload` = `42`, still a number |
| `["a","b"]` | `payload` = `["a","b"]`, still an array |

Valid JSON keeps its decoded type; bytes that are not JSON at all are kept as text. The field is addressable wherever a mapped field is — JSONPath in transformations, `{{.payload}}` in sink templates — and the workflow editor lists it among the transformation panel's available fields and in the live preview.

This matters for sinks that serialise the whole message, meaning any sink configured with `format: json` or `format: cdc`. A sink with no `format` set publishes the raw payload bytes directly and is unaffected either way.

### Dead letters

Some messages fail even after every retry: a sink keeps refusing them, a node cannot process them, or they fail validation. What happens next is the workflow's **Dead letters** switch (`dead_letters`), under Reliability Policy in the workflow's settings.

- **On** (the default for a workflow created in the editor): Hermod keeps the message in its own store and acknowledges it to the source, so the source moves on. The message is listed on the **Dead Letters** page with what failed it, where and why, and can be replayed, edited and replayed, or discarded there. If Hermod cannot store it, it is not acknowledged and stays on the source.
- **Off**: the message is not acknowledged. It stays on the source and is delivered again, which holds the source's position (a replication slot, a queue) until the cause goes away.

A waiting dead letter is the only copy of its message, so retention (`HERMOD_DEAD_LETTER_RETENTION`, 30 days by default) removes only dead letters already replayed or discarded.

**Draining.** The workflow's **Drain DLQ** action replays every dead letter it has waiting, in the background, each from the node or sink that failed it, and marks each one that goes through as replayed. With **Replay dead letters on startup** (`prioritize_dlq`) it does the same each time the workflow starts, for example after a destination outage. A drain stops after five replays in a row fail, since the cause is most likely still there.

Workflows saved by a release that named a dead-letter sink (`dead_letter_sink_id`) read as having dead letters on. Messages already in that sink stay there; Hermod no longer writes to or reads from it.

A sample template is available at `examples/templates/reliability_recovery_dlq.json`.

## Delivery guarantee

Delivery is **at-least-once**: a message is acknowledged to its source only after every sink write
succeeds, so a crash or an abrupt stop replays whatever was not acknowledged. Duplicates are
therefore possible and expected.

That guarantee has a second half which is easy to miss, because it lives in the source rather
than in the engine: **a source must not record its position until the messages at that position
have been acknowledged.** A polling connector that fetches a page of a hundred items and writes
down the newest one's cursor immediately has already said all hundred were consumed — so a crash
after the first item loses the other ninety-nine, whatever the engine does about acknowledgement.
Nothing fetches that window again.

Eleven polling connectors did exactly that until 1.8.0. All eleven now advance their stored cursor
only behind acknowledged messages (`pkg/infra/ackwatermark`), and a contract test fails the build if
a source gains a stored cursor without an `Ack` that acts on it.

The `file` source is the one that could not take the same shape, because a file is not an item. It
yields many rows, and a streaming reader cannot know a row is the last until it asks for one more —
so there is no row to hang the file's watermark on. The unit acknowledged there is the **file**: it
counts as consumed once it has been read to the end *and* every row it produced has been
acknowledged. Files enter the watermark in modification-time order, so a later file finishing first
cannot move the mark past an earlier one still in flight.

Its watermark is a `(modification time, name)` pair rather than a timestamp, because files dropped
as a batch share a second — and on a filesystem with one-second mtime granularity they share it
exactly, so "strictly newer than the last one acknowledged" would skip every sibling. State written by an
earlier build is still read: a bare Unix second orders before everything in that second, so it
redelivers rather than skips.

What makes that **exactly-once as observed at the destination** is the sink's upsert. Every SQL sink
writes with `ON CONFLICT` / `ON DUPLICATE KEY` / `MERGE`, keyed on the message id, so a redelivered
message overwrites its own row rather than adding one.

That holds on one condition: **the source must supply a stable identity.** The key is taken from,
in order:

1. `metadata["idempotency_key"]` — set this when the source knows its own natural key (a CDC row's
   primary key, an order number);
2. the message id;
3. a generated UUID, if neither is present.

A message that reaches case 3 gets a *different* key on every delivery, so its duplicates cannot be
collapsed — two deliveries of an identity-less message are genuinely indistinguishable. If a source
of yours produces messages without an id, set `idempotency_key` in a transformation before the sink.

Sinks that are not upserts (webhook, SMTP, queues) deduplicate only where they say so; SMTP computes
its own key from the message and recipient.

**S3 keeps every delivery by default.** Its object key carries a timestamp, so a redelivery lands
beside the delivery before it rather than on top of it. That is what an archive wants — successive
CDC updates to one row share a message id, and keying on the id alone would keep only the newest —
but it does mean a retry leaves a duplicate object, and retries are the mechanism at-least-once
works by. Set `idempotent_key: "true"` on the sink to key on the message id instead, which gives
the upsert behaviour the guarantee above describes, at the cost of keeping only the latest version
of each record.

## Idempotency and Exactly-Once Effects (Sink-Side)

Hermod processes messages with at-least-once delivery. To avoid duplicates at sinks, idempotency is implemented end-to-end:

- Engine ensures each message carries a stable idempotency key (defaults to message ID). Metrics are emitted for present/missing keys.
- SQL sinks (Postgres/MySQL/MariaDB) perform UPSERT semantics on the `id` primary key:
  - Postgres/Yugabyte: `INSERT ... ON CONFLICT (id) DO UPDATE SET data = EXCLUDED.data`
  - MySQL/MariaDB: `INSERT ... ON DUPLICATE KEY UPDATE data = VALUES(data)`
- Elasticsearch sink performs UPSERT by using the message `id` as the document `_id`.
- SQLite sink uses `INSERT OR REPLACE` into a table with `id TEXT PRIMARY KEY`.
- Redis sink deduplicates with `SETNX` using a configurable TTL and namespace; duplicates are skipped.

Environment variables:

- HERMOD_IDEMPOTENCY_REQUIRED=true — log warnings when idempotency keys are missing.
- HERMOD_IDEMPOTENCY_TTL=24h — TTL for Redis dedupe keys.
- HERMOD_IDEMPOTENCY_NAMESPACE=hermod:idemp — prefix for Redis keys.
