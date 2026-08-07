# Security Guidelines (Draft)

This document outlines the current and planned security posture for the Hermod UI and backend integration. It will evolve as we transition from token-in-storage to secure cookie-based auth.

## Authentication

- Current (temporary): Bearer token stored via a small storage abstraction (`ui/src/auth/storage.ts`). The UI reads the token to attach `Authorization: Bearer <token>` to requests.
- Target: Server-set HttpOnly session cookie.
  - Cookie attributes: `HttpOnly`, `Secure` (in production), `SameSite=Lax` (or `Strict` if UX allows), `Path=/`.
  - Session rotation on login and sensitive actions.
  - Short TTL with sliding expiration where appropriate.

## CSRF Protection (when using cookies)

- Prefer the **Double Submit Cookie** or **SameSite + CSRF token** strategy:
  - Backend issues a CSRF token (e.g., via header or non-HttpOnly cookie) and validates it against a request header `X-CSRF-Token`.
  - Enforce token validation on state-changing endpoints (POST/PUT/PATCH/DELETE).
  - For APIs used by third-party origins, consider an **Origin/Referer** check as an additional layer.

## Content Security Policy (CSP)

- Enforce a restrictive CSP to reduce XSS risk:
  - `default-src 'self'`
  - `script-src 'self'` (avoid inline scripts; if absolutely necessary, use nonces)
  - `style-src 'self' 'unsafe-inline'` (transition away from inline styles where possible)
  - `img-src 'self' data:`
  - `connect-src 'self'` (extend to API domains as needed)
  - `frame-ancestors 'none'`
- Avoid `dangerouslySetInnerHTML` in the UI. If rendering untrusted HTML is ever necessary, sanitize with a well-reviewed library on the server side.

## Secrets & PII

- Do not log secrets/PII in either UI or backend.
- Use parameterized queries for all SQL.
- Ensure least-privilege credentials for all services.

## Next Steps

1. Switch UI auth to cookie-based sessions (HttpOnly) and remove direct token handling from client code.
2. Implement CSRF token issuance and validation on the backend; update UI to pass token via `X-CSRF-Token`.
3. Add CSP headers in backend responses (environment-specific), document any allowed third-party origins.
4. Document local/CI runbook for security verification.

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
