# The FCM sink

`pkg/comm/sink/fcm` is a full Firebase Cloud Messaging client. Before this it
was a ~170-line wrapper that could set a destination and a notification title
and body, and only from message metadata.

## The rules FCM itself imposes

- **Exactly one of token, topic and condition per message.** The SDK's
  `validateMessage` refuses anything else. The old sink's "fall back to the
  configured defaults" branch assigned all three at once, so configuring a
  default token *and* a default topic failed every send. `New` now refuses that
  configuration at save time.
- **The data map is capped at 4096 bytes**, keys included. An oversized message
  is refused and will not shrink on retry, so it is reported as permanent.
  `on_oversize` offers `truncate` (largest values first; the envelope scalars
  `id`/`operation`/`table`/`schema` are protected) and `drop`.
- **Data values are strings.** `stringify` renders numbers and times; composites
  become JSON, not Go's `map[k:v]`.
- **There is no idempotency key.** This is why the default sink deliberately
  does *not* implement `hermod.BatchSink`: `RetrySink.WriteBatch` retries the
  whole slice, so a batching sink turns one transient failure into a second
  notification for every device that already got the first. Without
  `WriteBatch` the engine retries one message at a time. `NewBatching` is the
  opt-in, surfaced as the `batch` config key.

## Error classification

`ErrPermanent` wraps the refusals another attempt cannot satisfy — `UNREGISTERED`,
`INVALID_ARGUMENT`, `SENDER_ID_MISMATCH`, `THIRD_PARTY_AUTH_ERROR` — so a
caller can dead-letter instead of spending the retry budget to reach the same
answer. A dead token arrives as `*UnregisteredTokenError` naming the token,
which is the only signal that says a device registration row should be deleted.

Use only the non-deprecated `messaging.Is*` predicates: `IsUnregistered`,
`IsInvalidArgument`, `IsSenderIDMismatch`, `IsThirdPartyAuthError`,
`IsUnavailable`, `IsQuotaExceeded`, `IsInternal`. `IsMismatchedCredential`,
`IsInvalidAPNSCredentials`, `IsRegistrationTokenNotRegistered` and
`IsTooManyTopics` are deprecated and staticcheck fails on them; the last always
returns false.

## Testing against a stand-in FCM

The SDK's send endpoint is injectable, which is what makes the wire format
assertable rather than guessed:

```go
firebase.NewApp(ctx, &firebase.Config{ProjectID: "p"},
    option.WithHTTPClient(srv.Client()), option.WithEndpoint(srv.URL))
```

`option.WithHTTPClient` must be used *instead of* `WithCredentialsJSON`, not
alongside it — a caller supplying a transport supplies its own auth — and the
project id has to be passed explicitly because there are no credentials to read
it from. `SendEach` issues one HTTP call per message to the same endpoint, so
multicast and batch are covered by the same server.

The instance-id endpoint used by `SubscribeToTopic`/`UnsubscribeFromTopic` is a
package constant and *cannot* be redirected. That is why the sink talks to a
`client` interface: topic management is covered by injecting a fake through
`setClientForTest`.

## The destination column does not travel in the payload

`destinationFields` reads the parsed token/topic/condition templates for the row
columns they reference, and `DataFields` mode withholds those from the data map.

A registration token is a capability: whoever holds it can push to that device.
Without this, a message addressed by `{{.device_token}}` carried that token back
to the device it addressed, and a multicast — whose field holds every
recipient's token — handed each device the whole list. Only a live run found it;
every unit test addressed messages from metadata or a literal, so no test row
had the destination as one of its own columns.

`tmpl.fields()` walks `text/template/parse` for `*parse.FieldNode` and for the
`{{index . "name"}}` form. Withholding is a default, not a prohibition:
`data_json` names the value back in.

`DataEnvelope` mode is not covered by this — the `payload` key is the formatted
row by definition, destination column included. Use `fields` mode or drop the
column with a transformer.

## Templating

Every destination and notification field is a Go template over `renderData` —
the envelope (`id`, `operation`, `table`, `schema`, `metadata`) with the row's
own fields copied over the top, so a column named `table` keeps its own value.

Templates are compiled in `New`, so a broken one is refused at save and no
message pays to re-parse it. They use `missingkey=error`: Go's default renders
a missing field as the literal `<no value>`, and a registration token of
`<no value>` is a push sent nowhere. A genuinely optional field is reachable
with `{{index . "name"}}`.

`Ping` uses `SendDryRun`, not `Send`. The old one issued a real send with a
made-up token. `reachedFCM` is what separates a refusal about the *message*
(the round trip worked — a pass) from one about the *credentials* (the failure
Ping exists to find).

## Config keys and the UI

`ConfigKeys()` is derived from `FromMap` — the parser records every key it
reads — rather than restated beside it. `TestUIFormMatchesConfigKeys` reads
`ui/src/components/workflow/Sink/FcmSinkConfig.tsx`, extracts the
`updateConfig('key'` calls and fails in both directions: a form field nothing
reads, or a sink capability no field can reach. `token` is an alias of
`device_token`; `batch` is read by the factory, not `FromMap`.

`FromMap` refuses every value it cannot parse rather than defaulting to zero.
A duration that silently means "off" is how the trace purge stopped running.
