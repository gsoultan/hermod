# The audit trail does not keep credentials

`RecordAuditLog` (internal/api/handlers/common.go) writes the saved object to
two places: `logs.data` (the activity log, `GET /api/logs`) and
`audit_logs.payload` (`GET /api/audit-logs`). Callers pass whole sources, sinks
and workflows, so configuration goes with them. The owning tables encrypt
credentials (`configsecrets`); these two columns are plain text and are in
every backup.

`redactAuditPayload` (audit_redact.go) decodes the payload and replaces every
value under a credential's key with `[REDACTED]` before either row is written.

- The key rule is `configsecrets.IsSensitive`, applied after the key is read as
  snake_case, so `apiKey`, `clientSecret` and `X-Api-Key` are judged as
  `api_key`, `client_secret` and `x_api_key`. Add a new connector credential to
  `configsecrets`, not here.
- `auditSensitiveExact` adds header names that are never connector config keys:
  `authorization`, `proxy_authorization`, `cookie`, `set_cookie`, `apikey`.
- A value containing `{{` is a reference to a vhost secret and is kept.
- Numbers and booleans are kept, so `maxTokens` is not a casualty.
- Nothing to redact means the payload is stored byte for byte as before.

Known cost: `configsecrets` treats the exact keys `key`, `token` and `uri` as
credentials, so a node setting with one of those names reads `[REDACTED]` in
the trail even when it is not secret. The stored workflow is not touched.

Rows written before the change are not rewritten.

Pass objects to `RecordAuditLog`, never a pre-formatted sentence containing a
secret: text that is not JSON is stored as it came.

A *name* must not sit under a credential's key. The vhost secrets handler
audited `{"secret": "<NAME>"}` and lost the name to redaction; it now writes
`secret_name`. `TestVHostSecretAPINeverRepeatsAValue` holds that.
