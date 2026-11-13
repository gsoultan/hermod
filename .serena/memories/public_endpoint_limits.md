# Public endpoint limits (2026-11-13)

`/api/webhooks/`, `/api/graphql/`, `/api/forms/` and a few others are exempt
from `AuthMiddleware` (`internal/api/handlers/common.go`, the public prefix
list). Anything they do happens for an unauthenticated caller, so each needs a
bound of its own.

## Webhooks (`internal/webhooks/transport/http/webhooks.go`)

- **Body cap, before and after decompression.** `readRequestBody` wraps the
  body in `http.MaxBytesReader` and then inflates with
  `compression.DecompressLimit` at the same limit (default 10 MiB,
  `HERMOD_WEBHOOK_MAX_BODY_BYTES`, or `WebhookHandler.MaxBodyBytes` in tests).
  A cap on the raw body alone is not enough: 8 MiB of zeros is ~300 bytes of
  zstd. Past the limit the answer is 413 (`errBodyTooLarge`).
- `compression.DecompressLimit` reads through `io.LimitReader(limit+1)` for lz4
  and zstd (zstd also with `WithDecoderMaxMemory` and a closed, single-goroutine
  decoder) and trusts snappy's `DecodedLen` header, because snappy's block
  decode allocates exactly that length up front. Use it for any body that
  arrives from outside; `Compressor.Decompress` has no bound.
- **History is written after authentication, and only for a real source.**
  `CreateWebhookRequest` used to run first thing, so any caller could write 50
  rows (`webhook_requests` keeps the last 50 per path) for every path they
  invented. It now runs after `authenticateWebhook` and the mimir checks, and
  only when `sourceConfig` found a source. Mimir deliveries answered
  `ignored`/`duplicate` are not saved.
- **Credential headers are redacted** in `collectHeaders` (`isCredentialHeader`:
  Authorization, Proxy-Authorization, Cookie, X-Api-Key, and any name containing
  token/secret/password/signature/api-key/apikey/auth). Replay never used the
  headers, only the body and path, so nothing depends on the values.

## Every other request body (`internal/api/handlers/body_limit.go`)

`BodyLimitMiddleware` (in the chain in `internal/api/server.go`, before the
store guard and auth) wraps every body in a 16 MiB `MaxBytesReader`
(`HERMOD_MAX_REQUEST_BODY_BYTES`). Nested `MaxBytesReader`s: the tighter wins,
so handlers' own smaller limits still apply. The default must stay above every
handler's own limit (largest: uploads and webhooks 10 MiB, predict 8 MiB),
which `TestDefaultMaxRequestBodyIsAboveEveryHandlerOwnLimit` holds. The one
larger route, `PUT /api/vhosts/{vhost}/ml/datasets/{name}/file` (200 MiB), is
exempt in `bodyLimitExempt`; a new big-upload route must be added there.

## FTPS (`pkg/comm/sink/ftp/ftp.go`)

`tlsConfig()` verifies the server certificate against the configured host
(TLS 1.2+). `insecure_skip_verify: "true"` (form checkbox, shown when TLS is
on) is the opt-out, wired in `internal/factory/factory.go` like SMTP's.
