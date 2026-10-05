# Webhook and GraphQL endpoint credentials

A webhook source's endpoint (`/api/webhooks/<path>`, `HandleWebhook`) checks
what its source's stored config asks for, in `authenticateWebhook`:

- `api_key` — the request must carry it as `X-API-Key` (constant-time compare).
- `secret` — the request must carry `X-Hub-Signature-256: sha256=<hex>` or
  `X-Webhook-Signature`, the HMAC-SHA256 of the body.

A source may have either, both or neither, and every one it has must hold.
The GraphQL endpoint checks `api_key` only.

## What this replaced

Until 2026-10-05 the webhook endpoint read `secret` and nothing else. The
source form offered "API Key (Optional)" and saved `api_key`, which was never
checked; no form field wrote `secret`. The form's one credential did nothing
and the endpoint's one credential could only be set through the API. The same
shape as the wizard-gate drift: a form and a handler each naming config keys,
with nothing holding them together.

`ui/src/__tests__/webhookCredentials.test.tsx` pins the form to the keys the
handler reads; `webhook_auth_test.go` pins the handler.

## Fail closed

`sourceConfig` returns an error when the store cannot be read, and both
endpoints answer 503. They used to treat that as "no credentials configured"
and accept the request — the same fail-open the gRPC ingress had.

A path with no source at all still passes the credential check, and is then
refused by the dispatch (404): nothing holds the path.

## Not covered

- The form source's endpoint checks no key; its form no longer offers one.
- `/api/ws/in/<path>` feeds a webhook source but is authenticated by a Hermod
  session, not by that source's `api_key` or `secret`.
