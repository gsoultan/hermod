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
the envelope (`id`, `operation`, `table`, `schema`, `metadata`, and `meta` as an
alias) plus `after` (the data map) and `before` (the decoded before-image), with
the row's own fields copied over the top, so a column named `table` keeps its
own value.

`after`/`before` exist because the editor offers a CDC sample's columns as
`after.<col>` (`preparePayload` keeps the nesting and also hoists). Before they
were in scope every such field failed with `map has no entry for key "after"`
under `missingkey=error`. A **delete** has an empty data map — Postgres
`handleDelete` only calls `SetBefore` — so `rowOf`/`pickRow` use the
before-image as the row; `DataFields` uses the same row. `collectFields` treats
`.after.col`/`.before.col` as column `col`, so withholding still applies.

Test these with a real `message.DefaultMessage` built via `SetAfter`/`SetBefore`
(`cdc_test.go`), never the `mockMessage`: its `Before()` is nil and its data map
is whatever the test wrote, which is how every template test passed while real
updates and deletes failed.

Templates are compiled in `New`, so a broken one is refused at save and no
message pays to re-parse it. They use `missingkey=error`: Go's default renders
a missing field as the literal `<no value>`, and a registration token of
`<no value>` is a push sent nowhere. A genuinely optional field is reachable
with `{{index . "name"}}`.

`Ping` uses `SendDryRun`, not `Send`. The old one issued a real send with a
made-up token. `reachedFCM` is what separates a refusal about the *message*
(the round trip worked — a pass) from one about the *credentials* (the failure
Ping exists to find).

## The preview endpoint must build the message like a run

`internal/sink/transport/http/fcm_preview.go` builds its message with
`message.PopulateFromMap` (after setting defaults). It used `SetData` per key,
which turned a CDC sample's `after`/`before` into two data keys a run never
sends. Any new preview for a sink should use the same populator.

## The form (ui/src/components/workflow/Sink/Fcm*.tsx)

`FcmSinkConfig` → `FcmDataSection` (App data: mode cards labelled Selected
fields / All fields / Whole row as JSON for `none`/`fields`/`envelope`, column
chips that toggle `{"col":"{{.col}}"}` in `data_json`, KeyValueEditor, oversize)
→ `FcmMessagePreview` (auto-runs 350ms after the form settles, newest request
wins). Chip helpers are in `fcmData.ts`; the chips skip CDC envelope keys and
the destination columns (`destinationColumns` mirrors `destinationFields`).
The oversize error quotes the section name "App data" — keep the two in step.

## Config keys and the UI

`ConfigKeys()` is derived from `FromMap` — the parser records every key it
reads — rather than restated beside it. `TestUIFormMatchesConfigKeys` reads
every `ui/src/components/workflow/Sink/Fcm*.tsx` (the form is split: the
per-platform options live in `FcmPlatformOptions.tsx`), extracts the
`updateConfig('key'` calls and fails in both directions: a form field nothing
reads, or a sink capability no field can reach. `token` is an alias of
`device_token`; `batch` is read by the factory, not `FromMap`. A new form file
must match the glob, and an `updateConfig` call must keep its key a string
literal, or the gate cannot see it.

The form's three data choices are the three `data_mode` values; "Only the values
listed below" is `none` plus `data_json`, which is how a payload is narrowed
without a transformer. A new sink is written `data_mode: none` by `ui/src/lib/newSinkDefaults.ts`
(applied in `useSinkForm` only when not editing — on mount and when a type is
picked). The backend default stays `envelope`, because a saved sink with no
`data_mode` has always meant that. The destination choice is component state seeded from
the config — derived from the config it snapped back to Device token whenever
all three keys were empty, so a topic could never be entered.

## Preview

`PreviewMessage(cfg, msg)` builds the message through `build` — the code Write
sends with — and reports the data size before and after the oversize policy. It
uses `newUnconnected`, which is `New` without `resolveProject`, so it needs no
credentials and the form withholds `credentials_json` from the request. A row
the sink would refuse is `Preview.Refused`, not an error; the error is for a
config no row could build.

`POST /api/sinks/fcm/preview` goes through `factory.PreviewFCMMessage` so the
formatter matches the worker's: the sink form always sets `format: json`, which
changes the `payload` size. `format=schema_registry` is refused there, because
formatting through it contacts the registry (one request, measured) and a
preview must contact nothing.

The request's config is decoded as `hermod.StringMap`. The sink form's config
holds a boolean (`sequential`), and `map[string]string` refused every request
the real form made — the SMTP preview had the same defect and no test saw it,
because every test posted an all-string config. Only pressing the button in a
browser found it.

Not done: `ErrPermanent` is still consumed by nothing outside this package.
`RetrySink` (3 attempts, `factory.CreateSink`) and the engine's own loop
(`pkg/engine/writer.go`) both retry a permanent refusal, which is why the
reported error read "failed after 3 retries: fcm: permanent failure".

`FromMap` refuses every value it cannot parse rather than defaulting to zero.
A duration that silently means "off" is how the trace purge stopped running.
