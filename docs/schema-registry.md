# Schema registry and data governance

Schema validation and Confluent Schema Registry interop.

## Data Governance and Schema Validation

Hermod allows you to enforce data quality by validating incoming messages against a schema before they are processed or written to sinks.

Supported formats:
- **JSON Schema**: Standard JSON schema validation.
- **Avro**: Binary-friendly JSON-based schema.
- **Protobuf**: Enforce structure using `.proto` definitions.

Configuration:
1.  Open the **Workflow Panel** (right sidebar) in the Editor.
2.  Go to the **Settings** tab.
3.  Scroll to **Data Governance**.
4.  Select a **Schema Type** and provide the **Schema Definition**.

Messages that fail validation are:
- Logged as errors in the live workflow logs.
- Kept on the **Dead Letters** page when the workflow keeps failed messages there.
- Dropped from the pipeline to prevent downstream corruption.

### Confluent Schema Registry interop

The schema registry above is Hermod's own: it validates a message against a
schema you paste into the workflow. That is a different thing from interoperating
with the **Confluent Schema Registry** a Kafka estate already runs, and until now
Hermod could not do the second one at all.

A Confluent-framed record is five bytes of header — a zero magic byte, then the
schema's registry id as a big-endian `uint32` — followed by the encoded body.
A consumer that does not find those bytes reads the record as corrupt, which is
why a topic Hermod produced could not be consumed by ksqlDB, Kafka Connect or
any Confluent client, and why Hermod could not be pointed at an existing Avro
topic.

`pkg/comm/formatter/schemaregistry` is a `hermod.Formatter`, so it plugs into the
seam the Kafka sink already exposes:

```go
import (
    srformat "github.com/gsoultan/hermod/pkg/comm/formatter/schemaregistry"
    "github.com/gsoultan/hermod/pkg/comm/sink/kafka"
    "github.com/gsoultan/hermod/pkg/infra/schemaregistry"
)

client, err := schemaregistry.NewClient(schemaregistry.Config{
    BaseURL:  "https://psrc-xxxxx.eu-west-1.aws.confluent.cloud",
    Username: "SR_API_KEY",     // resolvable with the secret: prefix
    Password: "SR_API_SECRET",
})

f, err := srformat.New(srformat.Config{
    Client:  client,
    Subject: "users-value",     // Confluent's TopicNameStrategy is "<topic>-value"
    Schema:  userSchema,
    Type:    srformat.Avro,     // or srformat.JSONSchema
})

sink := kafka.NewKafkaSink(brokers, "users", user, pass, f)
```

The schema is registered once, on the first message, and its id is cached —
registration is idempotent at the registry, so a restart costs one round trip
rather than a duplicate subject version.

**From a stored workflow**, set `format: schema_registry` on the sink. Every key
is validated when the workflow is built, not on the first message, so a typo in
the subject is a refusal naming the field rather than a destination that starts
and then fails every record:

| Sink config key | Required | Meaning |
| :--- | :--- | :--- |
| `format` | ✅ | `schema_registry` |
| `schema_registry_url` | ✅ | Registry root |
| `schema_registry_subject` | ✅ | Subject to register under; Confluent's default naming is `<topic>-value` |
| `schema_registry_schema` | ✅ | The writer schema, as registry-ready text |
| `schema_registry_type` | — | `AVRO` (default) or `JSON` |
| `schema_registry_username` | — | API key; resolvable with the `secret:` prefix |
| `schema_registry_password` | — | API secret; resolvable with the `secret:` prefix |

This is wired for **sinks**, which is the direction that encodes. There is no
workflow-editor form for these keys yet — set them through the API or a
workflow bundle.

### Reading a Confluent topic

Consuming is wired too. Set `format: schema_registry` and `schema_registry_url`
on a **Kafka source**: records are unframed, the schema id is resolved against
the registry and cached, and the Avro body is decoded into the message.

Once a decoder is configured, an unframed record is an **error rather than a
fallback**. A topic that is not what the operator configured should say so; the
previous behaviour — try JSON, otherwise keep the raw bytes — would deliver
undecoded Avro to a sink looking like a legitimate payload.

| Source config key | Required | Meaning |
| :--- | :--- | :--- |
| `format` | ✅ | `schema_registry` |
| `schema_registry_url` | ✅ | Registry root |
| `schema_registry_username` / `_password` | — | API key and secret; `secret:` prefix resolves |
| `schema_registry_max_collection` | — | Cap on elements in one decoded array or map |
| `schema_registry_max_bytes` | — | Cap on one decoded string, bytes or fixed value |

There is no editor form for the source keys yet — API and bundle only.

### The decoder is Hermod's own, and why

Decoding Avro means an Avro decoder, and `github.com/hamba/avro/v2` — the
library Hermod uses to parse schemas and to encode — carries three unfixed
denial-of-service advisories in exactly that code path
(GO-2026-5046/5047/5048, CVE-2026-46385). Its array and map decoders loop over
an attacker-controlled block count without re-checking the reader's error state,
so a record declaring up to `math.MaxInt64` elements followed by a truncated
body pins a CPU core until the process is killed. **The module is archived
upstream**: every published version through v2.31.0 is affected and no fix is
coming.

Hermod decodes with `pkg/infra/avrodecode` instead. Only the decoder is
reimplemented — hamba still parses schemas and still encodes, neither of which
is the affected path.

It is not vulnerable to the same bug structurally rather than by patch. It
reads from a byte slice that is already fully in memory — a Kafka record, not a
stream — so there is no deferred reader error state a loop body can fail to
consult. Running out of bytes is an immediate hard error at the read. On top of
that:

- **No allocation is ever sized from a declared count.** A collection claiming a
  billion elements gets a small capacity and grows only as elements decode.
- **Collection sizes are bounded cumulatively**, across blocks rather than per
  block, so splitting a huge collection into many small ones evades nothing.
- **Nesting is depth-limited**, so recursion ends on a bound rather than on the
  goroutine stack.
- Value size, and total values per record, are bounded too.

The zero-width case is worth naming because it defeats the obvious defence: a
count cannot exceed the bytes remaining *unless the item type is `null`*, which
encodes to nothing. An array of `null` is arithmetically consistent with an
empty body at any count, and only the absolute bound stops it.

Verification is in `pkg/infra/avrodecode/decode_abuse_test.go` — including the
advisory's own `math.MaxInt64` block-count case under a wall-clock budget,
truncation at every byte offset of a valid record, out-of-range union and enum
indices, and a 200,000-level nesting bomb. Correctness is established
differentially against hamba's *encoder*, which is not implicated. The decoder
is fuzzed; the run standing behind this change was 11 million executions with no
panic and no hang.

Three tests fail the build if a call into hamba's decoder is ever introduced:
two in `pkg/infra/schema` covering that package, and `TestNoPackageDecodesAvro`
at the repo root, which walks the whole module. The root one exists because the
other two glob their own directory and so did not cover a second importer —
measured, not assumed: with a live `avro.Unmarshal` planted in the new formatter
package, both older guards passed.

### Logical types

The ten Avro logical types decode to their Go meaning rather than the primitive
underneath. Without this a `timestamp-micros` arrives as an `int64` of
microseconds, lands in a bigint column, and nobody notices until someone reads
a date.

| Logical type | Underlying | Decodes to |
| :--- | :--- | :--- |
| `date` | `int` | `time.Time`, midnight UTC |
| `time-millis` / `time-micros` | `int` / `long` | `time.Duration` since midnight |
| `timestamp-millis` / `timestamp-micros` | `long` | `time.Time` in UTC |
| `local-timestamp-millis` / `local-timestamp-micros` | `long` | `time.Time`, same wall clock, unshifted |
| `uuid` | `string` | `string`, unchanged |
| `decimal` | `bytes` / `fixed` | exact decimal `string` |
| `duration` | `fixed(12)` | `avrodecode.Duration{Months, Days, Milliseconds}` |

Three of those choices are deliberate and worth stating:

**`decimal` is a string, not a float or a `big.Rat`.** The scale is part of the
value — `1.10` and `1.1` are different to a `NUMERIC` column — and only the
string keeps it. It is also what SQL drivers accept for `NUMERIC` without a
lossy conversion.

**`time-millis` is a `Duration`, not a `time.Time`.** It is a time of day, not
an instant; giving it a date would invent information the record does not have.

**`duration` is not a `time.Duration`.** A month is not a fixed length of time,
so the three components stay separate rather than being collapsed against a
calendar the record does not carry.

A logical type Hermod does not recognise falls through to the underlying
primitive rather than failing — the annotation is advisory, and refusing a
record over one would break a topic that is otherwise readable. A *recognised*
logical type on the wrong primitive also falls through, because reinterpreting
a string as an epoch turns valid bytes into a garbage date.

`decimal` carries one bound the others do not need. `big.Int.String` is
superlinear in digit count, so an unscaled value limited only by `MaxBytes`
(16 MiB) is CPU exhaustion made entirely of valid bytes. The schema's own
`precision` is the bound: it is operator-supplied and states how large the
value can be.

### Protobuf is refused

| | Supported |
| :--- | :--- |
| Encode Avro / JSON Schema, framed | ✅ |
| Decode Avro, framed | ✅ |
| Protobuf, either direction | ❌ refused at construction — its framing needs a message-index array after the schema id that Hermod does not write, and an Avro-shaped frame would produce bytes no Protobuf consumer can read |
