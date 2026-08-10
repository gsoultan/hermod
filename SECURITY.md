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

### Open: the token is also in localStorage, which defeats the cookie

**This is the top outstanding security item.** The login response returns the JWT in
its JSON body as well as in the cookie. The UI stores that copy in `localStorage`
(`ui/src/auth/storage.ts`) and attaches it as a Bearer header
(`ui/src/api.tsx:30`), while also sending the cookie via `credentials: 'include'`
(`ui/src/api.tsx:36`).

Both mechanisms are therefore live at once, and the weaker one sets the ceiling:
any XSS can read `localStorage` and exfiltrate a 24-hour session token. Making the
cookie `HttpOnly` buys nothing while a second copy sits in reachable storage.

It has not simply been deleted because **seven UI call sites need the raw token to
open a WebSocket or SSE stream** — `WorkflowDebugger.tsx:33`,
`useWorkflowWebSockets.ts:77,111`, `WorkflowDetailPage.tsx:181,219`,
`LiveStreamInspector.tsx:38`, `DashboardPage.tsx:153`. The browser WebSocket API
cannot set request headers, which is why the `?token=` path exists at all. Removing
localStorage without replacing that path breaks live streaming across the app.

Putting a 24-hour credential in a URL is itself a weakness: query strings land in
server access logs, proxy logs, and browser history.

**The fix is a short-lived stream ticket**, not a bigger cookie:

1. Add an authenticated `POST /api/v1/stream-ticket` returning a single-use,
   narrowly-scoped token with a TTL in the tens of seconds.
2. Have WebSocket/SSE callers fetch a ticket and pass *that* in the query string.
3. Accept tickets only on stream endpoints; drop `?token=` for session JWTs.
4. Stop returning the JWT in the login body and delete `ui/src/auth/storage.ts`.

Until step 4 lands, treat the session token as XSS-exfiltratable and keep the CSP
below strict.

## CSRF

The main API relies on `SameSite` for cross-origin protection. There is **no
double-submit CSRF token on the general API surface** — the only CSRF token
handling lives in the public forms endpoints
(`internal/forms/transport/http/forms.go`).

`SameSite=Lax` blocks cross-site POST/PUT/PATCH/DELETE, so state-changing calls are
covered in practice. Two caveats worth knowing:

- Deployments that set `SameSite=None` (needed for cross-origin embedding) lose that
  protection entirely and **must** add a CSRF token before doing so.
- Because the API also accepts `Authorization: Bearer`, a request carrying an
  explicit header is not a classic CSRF vector — but it does mean SameSite is the
  only control for cookie-authenticated requests.

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

## Open items, in priority order

1. **Stream tickets, then drop the localStorage token** (see above). Everything else
   here is lower impact.
2. **CSRF token on the general API**, required before any deployment sets
   `SameSite=None`.
3. **Session rotation** on login and on privilege change; sliding expiry to shorten
   the 24h window.
4. **Document the CI security runbook** so these checks run per release rather than
   on memory.

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
