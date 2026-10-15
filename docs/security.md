# Security

Authentication, two-factor sign-in, audit logging, security headers and CORS.

## Audit Logging

Hermod includes a robust audit logging system that tracks all critical administrative actions.

Tracked actions include:
- Workflow lifecycle (Create, Update, Delete, Start, Stop)
- Source and Sink management
- User authentication and role changes
- Draining a workflow's dead letters

Audit logs are stored in the primary database (SQL or MongoDB) and can be viewed by Administrators in the **Audit Logs** page in the dashboard.

## Authentication & Account Security

Hermod hardens the login flow to defend against brute-force attacks and information disclosure:

- **Login Lockout**: After **5 consecutive failed login attempts** for a given username/IP combination, that combination is temporarily locked out for **15 minutes**. Locked requests receive an HTTP `429 Too Many Requests` response with a `Retry-After` header, and the event is recorded in the audit log. A successful login immediately clears the failure counter, and stale counters reset automatically after 15 minutes of inactivity.
- **Sanitized Database Errors**: When Hermod cannot connect to its database, error messages returned to API/UI clients are sanitized to strip database host/IP addresses and ports (e.g. `dial tcp 10.0.0.5:5432: connection refused`). This prevents leaking internal infrastructure details during setup, connectivity tests, and login. Internal database errors during login are surfaced only as a generic `internal server error`.

These protections apply automatically and require no configuration. The relevant thresholds are defined as constants (`MaxLoginAttempts`, `LoginLockoutDuration`) in `internal/api/handlers/login_security.go`.

### Two-Factor Authentication (2FA)

Hermod supports TOTP-based Two-Factor Authentication (compatible with Google Authenticator, 1Password, Authy, etc.).

- Enable 2FA (user self-service):
  1. Start setup to get a temporary secret and provisioning URL (QR):
     - `POST /api/auth/2fa/setup` (authenticated)
     - Response: `{ "secret": "...", "url": "otpauth://totp/..." }`
  2. Scan the QR code (or enter the secret) in your authenticator app and generate a 6‑digit code.
  3. Confirm to enable 2FA permanently:
     - `POST /api/auth/2fa/verify` with body `{ "secret": "...", "code": "123456" }`
  4. On success, the server stores the secret and marks the account as 2FA‑enabled.

- Disable 2FA:
  - `POST /api/auth/2fa/disable` (authenticated)

- Login flow with 2FA enabled:
  1. Submit username and password:
     - `POST /api/login` → returns `{ "two_factor_required": true, "user_id": "...", "pending_token": "..." }` when 2FA is enabled.
  2. Complete login by submitting the 6‑digit TOTP code with the pending token:
     - `POST /api/auth/2fa/login` with `{ "user_id": "...", "pending_token": "...", "code": "123456" }`
     - On success, the API returns a session `token` and sets the `hermod_session` cookie.

- First-time enrollment during login (when 2FA is enabled but not registered yet):
  - If an administrator enabled 2FA for a user without completing registration, password login will respond with
    `{ "two_factor_enroll_required": true, "user_id": "...", "pending_token": "..." }`.
  - Use the pending token to enroll without a session:
    1. Start enrollment and get a secret + provisioning URL (QR):
       - `POST /api/auth/2fa/setup/pending` with `{ "user_id": "...", "pending_token": "..." }`
       - Response: `{ "secret": "...", "url": "otpauth://..." }`
       - The login page renders the `url` as a **scannable QR code** (generated locally in the browser) so it can be added by scanning with any authenticator app — Google Authenticator, Authy, Microsoft Authenticator, Apple/iOS Passwords, etc. The plaintext `secret` is also shown as a manual-entry fallback.
    2. Verify and complete enrollment (also completes login):
       - `POST /api/auth/2fa/verify/pending` with `{ "user_id": "...", "pending_token": "...", "secret": "...", "code": "123456" }`
       - On success, the server persists the secret, issues a session token, and sets the `hermod_session` cookie.

Notes:
- 2FA secrets are never returned after verification; responses and cookies omit the secret.
- Audit logs record 2FA enable/disable and successful 2FA logins.
- The `HERMOD_JWT_SECRET` must be set (or present in `~/.hermod/db_config.yaml`) for token issuance.
- The pre-auth endpoints `/api/auth/2fa/login`, `/api/auth/2fa/setup/pending`, and `/api/auth/2fa/verify/pending` do **not** require a session cookie: they are authenticated solely by the short-lived signed `pending_token` issued by `/api/login`. They are intentionally exempt from the session-auth middleware so the OTP-challenge and first-time enrollment steps can complete before a session exists. The UI also avoids redirecting to `/login` on a `401` from these endpoints (e.g. a wrong code), so users can retry without being bounced out.

## Security Headers and CORS

Production defaults are secure by default:

- CORS allowlist via `HERMOD_CORS_ALLOW_ORIGINS` (comma-separated). In production, no allowlist -> no CORS.
- Security headers: `Content-Security-Policy`, `X-Frame-Options=DENY`, `Referrer-Policy=no-referrer`, `X-Content-Type-Options=nosniff`.
- HSTS can be forced with `HERMOD_HSTS_ENABLE=true` or when `X-Forwarded-Proto: https` is detected.
- Worker registration requires `X-Worker-Registration-Token` when `HERMOD_ENV=production`. Provide the secret via `HERMOD_WORKER_REG_TOKEN`.
- **UI Build & Deployment**:
  - **Development**: If `HERMOD_ENV` is not `production`, Hermod automatically builds the UI on startup if the `ui/` source exists. This requires `bun`, `curl`, and `unzip`.
  - **Production**: Set `HERMOD_ENV=production`. Hermod will skip runtime builds and serve assets from the embedded filesystem. Build the UI during CI/CD using `go run ./cmd/hermod --build-ui` before the final `go build`.
