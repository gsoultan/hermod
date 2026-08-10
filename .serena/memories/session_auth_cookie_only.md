### Session auth is cookie-only — the token is not reachable from JavaScript

**Decision:** the `hermod_session` HttpOnly cookie is the entire session. Nothing
returns the JWT to the browser and nothing stores it.

**What it replaced.** `/api/login` returned the JWT in its body, the UI kept it in
`localStorage`, and any XSS could lift a 24-hour credential straight out of
storage — which made the HttpOnly cookie decorative. It was kept there for two
reasons and both turned out to be unnecessary:

1. **WebSocket URLs.** A browser cannot set headers on `new WebSocket(url)`, so
   the token went in the query string. But **a browser sends same-origin cookies
   on the WebSocket handshake** (RFC 6455 §4.1) — and three of the UI's own
   sockets (sinks, sources, layout) had always relied on that and worked. The
   other five carried a token for no reason.
2. **Role decoding.** `getRoleFromToken()` unpacked the claims. The role is not a
   secret and the server already exposes `GET /api/me`.

**How it works now.** Identity comes from `GET /api/me` into an in-memory store
(`ui/src/auth/session.ts`), hydrated **once** in the router's `beforeLoad` — which
is what lets `getSessionRole()` stay synchronous at its ~21 call sites.
`ui/src/auth/storage.ts` is deleted.

**Enforced, not merely intended.** The UI stream endpoints (`/api/ws/live`,
`status`, `dashboard`, `logs`, `debugger`, `/api/notifications/sse`) **reject** a
token in the query string — see `uiStreamPaths` in
`internal/api/handlers/common.go`. Without that the old pattern returns in one
line and nobody notices until an audit.

**Deliberate carve-out:** `/api/ws/in/*` and `/api/ws/out/*` still accept a query
token. They are integration endpoints with no auth of their own, so an external
non-browser client that cannot set headers has nothing else. Tightening them
would silently break someone's running integration.

**Logout was cosmetic and is now real.** There was no logout endpoint at all — the
button deleted the localStorage copy and navigated away, while the cookie stayed
valid for its full 24 hours. `POST /api/logout` expires it. All four
session-cookie constructions were consolidated into one builder, because a cookie
is only replaced when name, path and domain match: a logout spelling any of them
differently would silently fail to end the session.

**Residual, by design:** the session is a stateless JWT. Expiring the cookie ends
it for that browser but does **not** revoke the token — a copy captured
beforehand stays valid until it expires. Real revocation needs server-side
session state. Recorded in SECURITY.md.

**Verified in a real browser** (`ui/__tests__/no_token_in_storage_e2e.spec.ts`,
`stream_auth_e2e.spec.ts`), because a server-side test cannot check what the
browser sends and the failure mode is silent — the socket closes, no frames
arrive, nothing throws.
