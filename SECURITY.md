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
