# Security Guidelines

The security posture of the Hermod UI and backend, as implemented. Each claim
below names the code that backs it, so a reader can check rather than trust. Where
something is still open it says so, with the specific reason it is still open.

This document was previously marked "(Draft)" and described cookie auth and CSP as
future work. Both had since shipped. A security document that understates what
exists is not cautious — it costs the reader the ability to tell which of its
statements are current.

## Authentication

Session auth is a JWT (HS256, algorithm pinned against confusion attacks —
`internal/api/handlers/common.go:parseSessionClaims`). The server accepts it from
three places, in this order (`internal/api/handlers/common.go:~490`):

1. `Authorization: Bearer <token>`
2. `?token=<token>` query parameter — for WebSocket and SSE connections
3. `hermod_session` cookie

On login the server sets `hermod_session` (`internal/auth/transport/http/auth.go:176`):

| Attribute | Value |
| :--- | :--- |
| `HttpOnly` | always |
| `Secure` | when the request is HTTPS, detected via `r.TLS` or `X-Forwarded-Proto` |
| `SameSite` | from `SameSiteFromEnv()`; forced `Secure` when `None` |
| `Path` | `/` |
| `MaxAge` | 24h |

### Streams authenticate with the cookie; no credential goes in a URL

The UI's WebSocket and SSE endpoints used to accept the session JWT as a
`?token=` query parameter. That is the reason a copy of the token had to live in
`localStorage`: a browser cannot set headers on `new WebSocket(url)`, so for the
UI to put a credential in the URL it had to be able to read one from JavaScript.

That requirement was false. A browser sends cookies on a same-origin WebSocket
handshake exactly as it does on any other request (RFC 6455 §4.1) — and three of
the UI's own sockets (sinks, sources, layout) had always relied on that and
worked. The other five carried a token for no reason.

So as of this change:

- The UI sends **no credential in any stream URL**. All of its WebSockets
  authenticate with the `HttpOnly` session cookie.
- The UI stream endpoints — `/api/ws/live`, `/api/ws/status`, `/api/ws/dashboard`,
  `/api/ws/logs`, `/api/ws/debugger`, `/api/notifications/sse` — **reject** a token
  presented in the query string (`internal/api/handlers/common.go`,
  `uiStreamPaths`). Enforced by `stream_auth_test.go`, which also pins that the
  cookie path still works, since refusing the query parameter is only correct
  while the cookie does.

This keeps session tokens out of access logs, proxy logs and browser history.

**Residual, deliberate:** `/api/ws/in/*` and `/api/ws/out/*` still accept a query
token. They are integration endpoints with no auth of their own, so a non-browser
producer that cannot set headers has nothing else to use. Tightening them would
silently break external integrations. If you operate one, prefer the
`Authorization: Bearer` header — any non-browser client can set it — and treat a
token in the URL as logged.

### The session token is not reachable from JavaScript

`/api/login` no longer returns the JWT in its body, and the UI stores nothing:
`ui/src/auth/storage.ts` is gone. The session exists only as the `HttpOnly`
cookie, so an XSS cannot read it out of storage or a response.

The identity the UI needs is not the credential. Role and username come from
`GET /api/me` into an in-memory store (`ui/src/auth/session.ts`), hydrated once
in the router's `beforeLoad`. Those are not secrets; the token is.

Verified end to end in a browser by `ui/__tests__/no_token_in_storage_e2e.spec.ts`:
after login, nothing shaped like a JWT is in `localStorage` or `sessionStorage`,
`/api/login`'s body carries no token, `document.cookie` cannot see
`hermod_session`, and the cookie is flagged `HttpOnly`.

### Logout ends the session

There was no logout endpoint. The UI's logout button deleted its localStorage
copy and navigated away, which looked like a logout only because the route guard
then found no token — the cookie was untouched and stayed valid for its full 24
hours.

`POST /api/logout` now expires the cookie, and the UI calls it. Confirmed by the
same spec: `/api/me` returns 401 immediately afterwards.

**Residual:** the session is a stateless JWT, so expiring the cookie ends it for
that browser but does not revoke the token. A copy captured beforehand stays
valid until it expires. Real revocation needs server-side session state — a
store of issued or revoked IDs checked per request — which is not implemented.

### Remaining hardening, in priority order

1. **Session revocation** and rotation on privilege change; shorten the 24h
   window with a sliding expiry. The session is a stateless JWT, so this needs
   server-side session state.
2. **Document the CI security runbook** so these checks run per release rather
   than from memory.

## CSRF

State-changing requests authenticated **by cookie** must carry a double-submit
token: the server issues `hermod_csrf` as a readable cookie alongside the
session, and the client echoes it in `X-CSRF-Token`. The two must match, compared
in constant time.

It works because an attacker on another origin can make the browser *send* the
cookie but cannot read it to populate the header — and cannot set custom headers
cross-origin at all. The token is not a credential on its own, which is why it is
deliberately **not** `HttpOnly`; making it so would leave the client unable to
read it and disable the protection entirely.

Scope, and the reasoning behind it:

| Request | Enforced? | Why |
| :--- | :--- | :--- |
| Cookie-authenticated `POST`/`PUT`/`PATCH`/`DELETE` | **Yes** | This is the vector: the browser attaches the cookie to a cross-site request automatically. |
| `Authorization: Bearer` or `X-Worker-Token` | No | Not forgeable cross-origin — an attacker cannot set those headers. Enforcing would break every CLI, worker and integration for no gain. |
| `GET`, `HEAD`, `OPTIONS` | No | Read paths. A token buys nothing and would break navigation. |
| `/api/login`, 2FA, webhooks, form posts | No | Public by design, or the request that establishes the session in the first place. |

The token is issued wherever a session begins (login and all three 2FA paths) and
cleared on logout, so it cannot outlive the session it belonged to.

**This was the stated prerequisite for `SameSite=None`.** A deployment that needs
cross-origin embedding can now set it without losing cross-site protection
entirely — though `Lax` or `Strict` remains preferable where the UI is
same-origin.

## Content Security Policy

CSP is set on API responses (`internal/api/handlers/common.go:417`). Verify the
active policy against your deployment rather than assuming the values here, and
keep `frame-ancestors 'none'` unless the UI is deliberately embedded.

Avoid `dangerouslySetInnerHTML` in the UI. Given the localStorage gap above, XSS is
currently a session-compromise vector, not just a defacement one.

## Secrets & PII

- Do not log secrets or PII in the UI or the backend.
- Use parameterized queries for all SQL. Identifiers, which cannot be bound as
  parameters, go through `sqlutil.QuoteIdent` / `ValidateIdent` — never string
  interpolation.
- Use least-privilege credentials for every connected system.

## Dependency Vulnerability Scanning

Run before every release:

```bash
govulncheck ./...
```

### Current status (2026-08-07)

`govulncheck` reports **3 vulnerabilities, all in `github.com/hamba/avro/v2` v2.31.0**
(`GO-2026-5046`, `GO-2026-5047`, `GO-2026-5048` — CVE-2026-46385).

**These are accepted risk, not oversight**, and the reason is specific: *Hermod never decodes
Avro.*

The flaw is in the library's array and map **decoders**. A payload can declare a block of up to
`math.MaxInt64` elements and then truncate; the decoder loops over that count without
re-checking the reader's error state, pinning a CPU core until the process is killed. It is
reachable only by decoding attacker-influenced Avro binary data.

Hermod's entire use of the library is two calls in `pkg/infra/schema/validators.go`:

| Call | Direction | Input |
| :--- | :--- | :--- |
| `avro.Parse` | schema parsing | operator-supplied schema text |
| `avro.Marshal` | **encode** | Hermod's own `map[string]any` |

There is no `Unmarshal`, no decoder and no reader anywhere in the codebase, so the vulnerable
path is not reachable. `govulncheck` still flags it because the advisory marks *all symbols* in
the module as affected, which is a module-level judgement rather than a call-graph one.

There is no fixed upstream release: the advisories state `Fixed in: N/A` and v2.31.0 is the
latest published version. (A third-party fork, `github.com/iskorotkov/avro/v2` v2.33.0, carries
the fix. Adopting it is a supply-chain decision for the CTO, not a drop-in upgrade.)

**This assessment depends on a property of our code, so it is enforced by one.**
`TestNoUntrustedAvroDecoding` (`pkg/infra/schema/avro_exposure_test.go`) fails the build if any
Avro decode entry point is introduced. Whoever adds one has to bound the input and impose a
decode deadline, or move to a patched fork — rather than inherit an assessment that silently
stopped being true.

Independently of the above:

- Do not accept Avro schema definitions from untrusted or unauthenticated sources.
- Prefer JSON Schema or Protobuf for data contracts where the schema origin is not fully trusted.

Ten other vulnerabilities affecting this codebase were resolved on 2026-08-05 by upgrading
`grpc`, `golang.org/x/text`, the AWS SDK (`eventstream`, `kinesis`, `s3`), `mongo-driver`,
`pgx/v5`, `go-jose/v4`, `paho.mqtt.golang`, `ch-go`/`clickhouse-go`, `azidentity`, and
`pulsar-client-go` (which transitively removed the vulnerable `golang-jwt/jwt` v3).
