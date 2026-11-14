# Connector credentials by role

`internal/api/handlers/viewer_credentials.go`: `Handler.ConfigForCaller(r, config)`.

Storage decrypts a source's or sink's config on read, so `GET /api/sources`, `/api/sources/{id}`,
`/api/sinks` and `/api/sinks/{id}` used to answer with passwords, API keys and tokens in the clear
to every signed-in user with access to the vhost, Viewers included.

`ConfigForCaller` is called on the way out of those four handlers:

- role `Viewer`: every key `configsecrets.IsSensitive` names is replaced with `[REDACTED]`, in a
  copy. Empty values and `{{ }}` secret references are kept (same rule as the audit log,
  `audit_redact.go`).
- any other role, and server-made requests with no user: the config as stored. The edit form saves
  back what it loaded, so redacting for an Editor would overwrite the credential with the marker.

A new endpoint that returns a connector's config must call it too. Workflow node configs
(`WorkflowNode.Config`) are not covered by this.

Tests: `viewer_credentials_test.go` in `internal/api/handlers`, `internal/source/transport/http`
and `internal/sink/transport/http`.
