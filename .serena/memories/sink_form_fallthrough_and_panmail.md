# The sink form fall-through, and the panmail sink

## A default that hid twelve broken forms

`SinkWizard.tsx` resolved a type's connection form as

```ts
const SelectedConfig = configComponents[sink.type] || configComponents['database'];
```

A type missing from the map did not fail — it rendered the **database** form.
Twelve did: `http`, `websocket`, `mqtt`, `file`, `stdout`, `eventstore`,
`mongodb`, and the five social sinks.

For `http` ("API / Webhook" in the picker) and `websocket` this was not cosmetic.
`DatabaseSinkConfig` never calls `updateConfig('url', …)`, both types require
`url` in `connectorRequirements.ts`, and `SinkWizard` disables **Next and Save**
while `missingConnectionFields` is non-empty. The sink could not be created or
edited from any of the four entry points — AddSinkPage, EditSinkPage,
WorkflowNodeSettingsModal, NodeConfigDrawer all route through `SinkForm`. Only
the REST API could make one. No error was ever shown.

`MiscSinkConfig.tsx` held the correct `http` form the whole time and had been
imported by nothing since `ce5d533` ("Remove legacy UI components…"). Deleting a
component's last import does not break a build when the consumer has an
`|| fallback`.

**The shape to watch for:** a lookup with a plausible-looking default, over a
list that is maintained somewhere else. `SINK_TYPES` and `configComponents` sit
in the same file and still drifted. `sinkConfigCoverage.test.tsx` now fails if
any offered type relies on the fallback, and asserts the exact set served by the
database form so `mongodb`/`cassandra` are a decision, not a leftover.

Three types also gained requirement gates because their factory case *returns an
error* rather than degrading — `mqtt` (broker_url, topic), `file` (filename),
`eventstore` (driver, dsn). Without them you could save a sink that failed only
once it ran.

Two keys the factory had always read — `compression` and `timeout` on the http
sink — had no input anywhere in the UI. Worth checking the factory case, not the
old form, when writing a connector form.

## The panmail sink and the retry it must not allow

`pkg/comm/sink/panmail` sends each message as one email through a panmail
gateway, via `github.com/gsoultan/panmail-sdk` (stdlib-only Go client).

The design problem is retries. Sending mail is not idempotent, the gateway has
no de-duplication key, and `RetrySink` (`pkg/comm/sink/decorators.go:134`)
retries **every** error identically — Hermod has no permanent-error concept. So
a transport error returned from a sink is a second email to a real person.

The SDK draws the line for us: it never repeats a send whose outcome it does not
know, and it classifies stated refusals into four types. The sink uses the
idempotency claim as the lever:

- **stated refusal** (`*RateLimitedError`, `*BacklogFullError`, `*AuthError`,
  `*APIError`) — definitively not accepted, so **release** the claim; a retry is
  free to take it.
- **unknown outcome** (transport error) — **keep** the claim, so the retry finds
  the key taken and does nothing. The message may go unsent; that is the cheaper
  failure.

With idempotency off there is nothing to hold the claim, and the error says so
rather than looking routine. `stated()` is the whole discriminator, and
`TestWrite_UnknownOutcome*` fail when it is inverted — verified by mutation, not
assumed.

Two SDK facts that shape the sink: both capacity refusals are
`resource_exhausted`/429 and are told apart **only** by the presence of
`Retry-After`; and "accepted" is not "will be delivered" — a quarantined message
returns byte-identical bytes to an accepted one, so held/expired are only
knowable from webhooks.

The SMTP sink's idempotency-store wiring moved to
`internal/factory/idempotency.go` and is shared. The table prefix is the sink
name: two sinks over one database must not suppress each other's sends.

Related: [connector_conformance_suite](connector_conformance_suite.md),
[reachability_tests](reachability_tests.md) — a feature configured through the
UI needs one test that starts from the UI.
