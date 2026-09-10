# Message payload decoding: what happens to a body that is not a JSON object

Sources may deliver a bare string, a scalar, an array, or bytes that are not
JSON at all — a RabbitMQ queue of plain text, a Kafka topic of CSV lines, a file
read in `raw` mode. Only JSON **objects** have field names to merge into the
message root; everything else is exposed under a single key.

**That key is `payload`** (`message.NonObjectPayloadKey`). The name was not
chosen freely: array payloads have always landed there, and the UI advertises
`{{.payload}}` as the template variable for notification sinks
(`ui/src/components/workflow/Sink/NotificationSinkConfig.tsx`). Widening the
existing key to cover strings and scalars keeps working templates working; a new
name would have broken them. The HTTP source and registry discovery independently
use `raw` for the same idea — that inconsistency is pre-existing and deliberate to
leave alone, since changing it would break their consumers.

## `SetAfter` is an alias for `SetPayload` — it is a trap

`SetAfter(b)` calls `SetPayload(b)`. There is no separate "after" buffer. The
idiom `SetAfter(raw) // Fallback for non-JSON`, copied into 24 source files, is
therefore a **no-op**: it re-sets the payload it was just given and clears the
data map. It reads like it rescues the body, which is why nobody looked past it
for years. If you are chasing a lost payload, this line is not doing anything.

## The real defect was an ignored error, in two places

`MarshalJSON` and `ToMap` each did `json.Unmarshal(m.payload, &res)` and
discarded the error, so a payload with no fields to merge left `res` untouched
and the body vanished — no error, no log. Three symptoms, one cause:

- Sinks with `format: json` or `format: cdc` published `{"id":…,"metadata":…}`.
  Sinks with **no** `format` were never affected: `internal/factory/factory.go`
  only builds a formatter for `payload`/`json`/`cdc`, and a nil formatter makes
  the sink publish `Payload()` bytes directly.
- `ToMap` feeds the workflow editor's test/preview panel and message traces, so
  string payloads also rendered as an empty body there.
- CDC messages whose payload was not JSON failed to marshal **outright**
  (`json.RawMessage` rejects non-JSON), failing the sink write.

Both paths now go through `decodePayloadFields`, and CDC envelope fields through
`jsonRawOrWrapped`. Keep them sharing one helper: when they drifted, an array
serialised differently depending on whether a transformation had called `Data()`
first, because only the lazy `Data()` path knew how to handle arrays.

## Testing it

`pkg/comm/message/nonobject_payload_test.go` pins the contract, including that
`MarshalJSON` and `ToMap` agree. `TestRabbitMQQueueSource_NonObjectPayload_ReachesSink`
drives `run() -> Read() -> Formatter` against a live broker
(`HERMOD_INTEGRATION=1` + `RABBITMQ_URL`); declare test queues **durable** to
match the source or the broker rejects the second declare with
`PRECONDITION_FAILED`.

## Changing this has blast radius into sinks

A schema'd sink cannot tell "undecodable" from "decoded to something my schema
has no column for", and at least one used `len(Data()) == 0` as the proxy. The
S3 Parquet sink refused a record it could not build a row from that way; once a
non-object payload decoded to one synthetic field, the check passed the record
through to the writer and `WriteStop` failed the whole batch with
`interface conversion: interface {} is nil, not string` — naming neither the
record nor the reason, and failing identically on every retry.

Its guard now counts how many of the schema's own columns a record can fill
(`writableFieldCount`). If you touch payload decoding again, grep for
`len(data) == 0` on the sink side before assuming nothing depends on emptiness.

The failing test — `TestAnUndecodableMessageIsNotSilentlyDropped` — is behind
the `integration` build tag, so `go test ./...` never compiles it and only CI
caught this. The `schema_guard_test.go` beside it needs no S3 and runs in the
default suite; prefer that shape for guards worth protecting.
