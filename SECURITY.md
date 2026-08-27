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

Expiring the cookie only ends the session in that browser, so logout also
*revokes* the token: a copy captured beforehand stops working too. Every session
now carries a `jti`, and `POST /api/logout` adds it to a revocation list the auth
middleware checks before it accepts a request — and before it renews one, so a
revoked session is not handed a fresh cookie on its way out.

Every administrative action that invalidates a token's claims revokes the
sessions holding them. The middleware builds the request's user from those
claims and never reads the database — that is what keeps an authenticated
request free of I/O — so until the token expires, the role, the vhosts and the
account's very existence are whatever they were at login:

| Action | Why the session cannot be left alive |
| --- | --- |
| Password change | A password is rotated routinely or because it was compromised; in the second case the sessions opened with the old one are precisely what has to stop working. |
| Role change | A demotion that leaves the old session holding administrator claims has not demoted anybody. |
| VHost change | The grant is carried in the claims and used for scoping. |
| Account deletion | The account is gone and the token still authenticates. |

Sessions created *after* the change survive, so a user can log straight back in.
Changes that alter nothing a request is permitted to do — a display name, an
email — deliberately do not revoke. Logging people out for routine edits is how
a security control ends up switched off.

**How it is stored, and what that costs.** The middleware derives the user from
the token's claims specifically so an authenticated request costs no I/O, and a
revocation lookup per request would undo that. So the list lives in memory and is
*replicated* through the configured state store rather than read from it:
`IsRevoked` is a map lookup under a read lock, and a background refresh carries
revocations between instances every 10 seconds.

The list is bounded in two ways. Entries are dropped once the token would have
expired anyway, and a hard cap of 100,000 sessions stops an authenticated user
from growing it without limit by looping login and logout — time bounds the list
only if the input rate is not attacker-controlled. Reaching the cap evicts the
entries closest to expiring, which surrenders the least revocation per byte
reclaimed, and increments `hermod_revocation_evictions_total`. Any non-zero
value there means revoked sessions became usable again and wants investigating.

**Operationally:** `hermod_revoked_sessions` and `hermod_revoked_users` should
rise and fall. A value that only rises means pruning has stopped, which is
otherwise silent until the process runs out of memory.

**Residual:** on a multi-instance deployment a revocation is immediate on the
instance that performed it and takes up to that refresh interval to reach the
others. Closing that window entirely means a store lookup on every authenticated
request. The trade is deliberate, which is why the interval is short rather than
absent. On a single instance — the default — there is no window. Entries are
dropped once the token would have expired anyway, which is what bounds the list.

### Request-level denial of service

The API server sets its own timeouts, because Go's `http.Server` has none by
default and this one had none set — only `Addr` and `Handler`. A client that
opens a connection and then dribbles its request headers a byte at a time was
held for as long as it liked; enough such connections exhaust the server
without ever completing a request. That is Slowloris, and the Dockerfile
`EXPOSE`s this port directly, so the default deployment has no reverse proxy
to absorb it.

| Setting | Value | Why |
| :--- | :--- | :--- |
| `ReadHeaderTimeout` | 20s | Bounds the part that must be fast. Long enough for a slow mobile client, short enough that a stalled one cannot hold a slot. |
| `IdleTimeout` | 120s | Reclaims idle keep-alive connections between requests. |
| `MaxHeaderBytes` | Go default (1MB) | Stated explicitly so it is a decision rather than an inheritance. |
| `MaxHeaderValueCount` | Go default (500) | New in Go 1.27. A client repeating one header thousands of times is a cheap way to make the server allocate. |

**`WriteTimeout` and `ReadTimeout` are deliberately not set.** Both apply to the
whole exchange, and this server also carries the UI's WebSockets
(`/api/ws/live` and the rest) and Server-Sent Events, which are long-lived by
design. Either would sever a working stream mid-flight on a timer. The two
settings above touch only the header phase and the idle phase, so no stream in
progress is affected.

Verified against a real socket rather than by reading the struct:
`TestAClientThatDribblesHeadersIsDisconnected` in `cmd/hermod` opens a
connection, sends a partial header set and never terminates it, and requires
the server to hang up. With the timeout removed the same test waits five
seconds and fails, which is what makes it evidence.

**Residual:** a request *body* is still unbounded in time, deliberately — large
imports and uploads may legitimately take a while. A deployment that does not
need slow uploads should put a proxy in front with a body timeout.

The gRPC server (port 50051, also `EXPOSE`d) was constructed the same way — 
`grpc.NewServer()` with no options — and gRPC's default `MaxConcurrentStreams`
is *unlimited*, so one client could open streams until the process ran out of
memory. Its only authentication is the per-path API key checked inside
`Publish`, which means everything before that check is reachable
unauthenticated.

| Setting | Value | Why |
| :--- | :--- | :--- |
| `MaxConcurrentStreams` | 1000 | A ceiling no real producer meets. gRPC's default is unlimited. |
| `MaxRecvMsgSize` | 4MB | gRPC's default, stated rather than inherited. |
| `MaxConnectionIdle` | 15m | Reclaims connections doing nothing; streams in progress are untouched. |
| `KeepaliveEnforcementPolicy.MinTime` | 30s | Without one, a client may ping as fast as it likes — work for the server, nothing for the client. |

**`MaxConnectionAge` is deliberately not set.** This is a data ingestion
endpoint; forcing periodic reconnects would disrupt exactly the long-lived
producer streams it exists to serve. `PermitWithoutStream` stays true for the
same reason — a producer holding a connection open between batches is normal
here.

### Outbound requests, and why they are not all treated alike

`pkg/infra/httpclient` provides a client with timeouts and an SSRF guard that
refuses connections to private and loopback ranges. One file used it. Twelve or
more reached for `http.Get` or `http.DefaultClient` instead, both of which have
**no timeout at all** — `http.DefaultClient.Timeout` is zero — and no address
check.

The WASM transformer's module download now uses the guarded client. It is the
sharpest case in the codebase: the URL comes from configuration, and what
arrives is not data but a WebAssembly module that is compiled and executed. A
stalled server held the call forever, taking the worker preparing the
transformer with it, and a URL pointing at `169.254.169.254` or an internal
address made the server fetch on the caller's behalf.

**The remaining call sites got timeouts, not the SSRF guard**, because
converting them blindly would have been a bug. Hermod is self-hosted, and an
HTTP *data source* pointed at `http://internal-api.local` is an ordinary thing
to configure, not an attack — refusing private addresses there would break
exactly the deployments this software exists for.

So there are two clients, and the difference between them is the whole point:

| Client | Bounds | Refuses private addresses | For |
| :--- | :--- | :--- | :--- |
| `DefaultClient` | whole request, 10s | **yes** | code, and anything from a source the operator does not control |
| `DataClient` | dial, TLS handshake, and the wait for response headers | **no** | operator-configured sources and destinations |

`DataClient` deliberately sets no overall `Timeout`. A pipeline legitimately
downloads large files and uploads large batches, and an overall cap would abort
those on a clock rather than on a fault. It bounds the phases that must be fast
and leaves the transfer alone — the same distinction the API server makes
between `ReadHeaderTimeout` and `ReadTimeout`.

26 call sites moved onto it: the file and Excel sources, and the social and
messaging sinks (Slack, Discord, Telegram, Twitter/X, Facebook, Instagram,
TikTok, LinkedIn). `pkg/comm/transformer/lookup/api_lookup.go` was already
bounded by an explicit `context.WithTimeout` around each attempt and is left
alone.

A test pins the distinction rather than only the timeouts: `DataClient` reaches
a loopback address and `DefaultClient` refuses one. If those ever agree, one of
them has lost its purpose.

### Remaining hardening, in priority order

1. **Cross-instance revocation timing.** A revocation is immediate on the
   instance that performed it and reaches the others within the refresh interval.
   Closing that window entirely means a store lookup on every authenticated
   request, which is the cost the design exists to avoid — so this is a known
   trade rather than a defect, and it is listed here because the interval is not
   exercised by CI against a real multi-instance deployment.
2. **The `/api/ws/in` and `/api/ws/out` integration endpoints** have no
   authentication of their own and accept a token in the query string, because an
   external non-browser client that cannot set headers has nothing else. They are
   deliberately excluded from the rule that keeps credentials out of URLs;
   tightening them would silently break someone's running integration.

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

### A table name can come from a message

When a SQL sink is not pinned to a table it takes one from the message, and a
message's table originates upstream — on the wire, for a webhook or a generic
source. That identifier cannot be bound as a parameter, so it is interpolated
into `CREATE TABLE`, `INSERT` and `DELETE`.

The PostgreSQL sink has validated it for some time. The ClickHouse sink did not,
and the consequence was not theoretical: a message whose table was
`pwned (id String) ENGINE = Memory --` produced one entirely legal `CREATE
TABLE` and left `pwned` in the destination with the schema and engine the
message asked for. A semicolon is rejected by ClickHouse — "Multi-statements are
not allowed" — so the server stops the crude version of this and none of the
interesting ones; do not mistake that for a defence.

Both sinks now run the name through `sqlident.Validate` and refuse rather than
build a statement around it.
`TestAnUnsafeTableNameFromAMessageIsRefused` holds the line.

**Cassandra** had the same shape and is fixed: it took its table from
`msg.Table()` when unpinned and interpolated it into CQL unchecked. Cassandra's
parser rejects the clumsy payloads — the injected text still reached the
statement, arriving verbatim in `CREATE TABLE hermod_it.pwned (id[,]...` — so
that was the server refusing one shape, not the name being kept out. It now
validates the table and quotes mapped columns.

**Oracle is fixed and now verified against a real server.** A
`gvenzl/oracle-free:23-slim` container on the workstation runs the sink's
integration suite, including the identifier guard: a table name arriving on a
message is refused before a statement is built, and Oracle never grows a table
of the injected shape. That test is not wired into CI — Oracle Free wants ~2GB
of RAM and a slow first boot, and the integration job already runs at the edge
of a 7GB runner — so it stays a local gate until a larger runner or a hosted
instance exists, at which point setting `ORACLE_DSN` promotes it with no test
change.

Standing that server up immediately paid for itself. It found that the sink
could not write to a conventionally-named Oracle table **at all**: identifiers
were quoted exactly as PostgreSQL quotes them, but Oracle folds unquoted names
to UPPER case where PostgreSQL folds to lower, so a mapping to `id` produced
`"id"` — a lower-case column ordinary DDL never creates — and every statement
failed with `ORA-00904`. Snowflake folds the same way and had the same latent
defect. Both now fold before quoting. See
`TestQuotingFollowsTheDialectsOwnCaseFolding`.

**Snowflake remains the one fix not watched failing against a real server.** It
is cloud-only, so there is no warehouse to point it at from a workstation or
from CI. Its casing fix above shares a mechanism with Oracle's, which *was*
verified — but the Snowflake half of it is inference from documented folding
behaviour, not observation. Read it knowing that.

Oracle had both halves of the pattern at once: the unpinned table came from
`msg.Table()` and went into MERGE, DELETE and CREATE TABLE unexamined, and every
`QuoteIdent` error on a mapped column was discarded, so a rejected name became
an empty identifier — the MSSQL failure mode. Its mapped upsert also indexed
`cols[0]` unguarded, so a message whose every mapped field was identity-skipped
panicked the worker instead of failing the write.

What made both testable at all is where the check sits: the table and the mapped
column names are validated *before* anything connects, so a sink pointed at an
address that does not exist still refuses a bad identifier rather than failing
to dial. That is deliberate — a rejected identifier is the sink's own decision
and should not depend on whether the server is reachable — and it means each
guard has real tests that run anywhere, including a check that removing it makes
them fail with a dial timeout instead of a refusal.

What those tests cannot cover is whether the quoted SQL the server finally
receives is accepted by it. That still needs an account, or a server.

MSSQL and Cassandra were both on this list as "unreachable from an arm64
workstation". Both were wrong. **Azure SQL Edge** is arm64-native and speaks the
same T-SQL; the official Cassandra image publishes arm64 and merely wants
memory and patience. MSSQL also turned out to quote its identifiers already but
discard `QuoteIdent`'s error, so a rejected name became an empty identifier and
the write failed as `Incorrect syntax near ')'`, naming neither the column nor
the reason.

The lesson is worth keeping: *untestable* described the image reached for, not
the connector. Assume a connector is testable until an actual attempt says
otherwise.

### Structured payloads are encoded, never printed

The rule above is about SQL, but it generalises: **a value that came from a
message is data, and data goes into a serialiser rather than a format string.**
The document id, the table, the index — none of them belong to the connector.
They are whatever the pipeline put there: a CDC primary key, a Kafka message
key, a field lifted out of a webhook body.

The Elasticsearch sink built its bulk action lines with `fmt.Fprintf` and `%s`.
Bulk is NDJSON — one action object per line — so a document id containing a
quote and a newline closed the action object and wrote further action lines of
its own. Against an `index` action that is mostly inert, because Elasticsearch
reads the following line as the document source; against a `delete`, which has
no source line, the next line is parsed as another action. A document id could
therefore delete documents from an index the sink was never pointed at.

It is fixed, and `TestADocumentIDCannotInjectBulkActions` holds the line by
seeding a second index and asserting the injected delete does not reach it.
Document bodies are compacted for the same reason: a newline inside a
pretty-printed payload would otherwise end the line and turn the remainder into
actions.

## Verifying this document

Every claim above that can be checked has a check, and they run together:

```bash
./scripts/security-check.sh          # everything that needs no server
./scripts/security-check.sh --e2e    # plus the browser checks
```

It runs in CI on every build, so a claim that quietly stops being true fails
there rather than during an incident. The script prints the claim before each
check, so a reader can see what is being asserted and not just that something
passed.

| Claim | Held by |
| :--- | :--- |
| No reachable known vulnerability | `scripts/govulncheck.sh` (with reviewed exemptions) |
| Streams authenticate by cookie, refuse URL tokens | `internal/api/handlers/stream_auth_test.go` |
| Session window is an hour, capped at a day | `internal/api/handlers/session_lifetime_test.go` |
| CSRF on cookie state changes, header auth exempt | `internal/api/handlers/csrf_test.go` |
| Every mutating route is guarded, or listed | `internal/api/handlers/route_guard_test.go` |
| A restrictive CSP is set, and permits WebSockets | `internal/api/handlers/security_headers_test.go` |
| SQL identifiers are quoted, never interpolated | `pkg/infra/sqlutil` |
| Hermod never decodes untrusted Avro | `TestNoUntrustedAvroDecoding` |
| No credential reaches web storage | `ui/__tests__/no_token_in_storage_e2e.spec.ts` — CI's e2e job on every push, or `--e2e` locally |
| Logout revokes the token, not just the cookie | `TestRevokedCookieIsRejectedByTheMiddleware` |
| A demotion, vhost change or deletion ends that user's sessions | `internal/auth/transport/http/revocation_on_admin_action_test.go` |
| …while a cosmetic edit does not | `TestACosmeticEditDoesNotEndSessions` |
| The list is bounded against login/logout churn | `TestTheListIsBounded` |
| An idle refresh costs one store read, not one per entry | `TestRefreshDoesNotRereadWhatItAlreadyHolds` |
| A password change ends every session that user holds | `TestRevokeUserRejectsEveryCookieForThatUser` |
| …and does not lock the user out of their own account | `TestPasswordChangeDoesNotLockTheUserOut` |
| Revocation is checked before a session is renewed | `TestRevocationIsCheckedBeforeRenewal` |

**What has no check, stated so the table is not mistaken for the whole posture:**

- **Cross-instance revocation timing** — the tests cover replication through the
  store, but the refresh interval on a real multi-instance deployment is not
  exercised by CI.
- **"Do not log secrets or PII"** — a review discipline. A grep would be theatre,
  not a check.
- **The two-phase-commit operational hazard** — covered by the integration tests
  in `pkg/comm/sink/txgroup`, which need a real PostgreSQL and so run separately.

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
