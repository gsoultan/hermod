# A vhost's own secrets

A vhost holds secrets (`vhost_secrets`), saved from the Secrets page, encrypted
with the crypto master key, read only by that vhost's workflows. Lookup order:
the vhost's store, then the global `secrets.Manager`
([secrets_env_prefix](secrets_env_prefix.md)).

## How a running message knows its vhost

The engine and messages carried no vhost. `Engine.SetVHost` is set by the
registry when it builds a workflow's engine; the runner marks each message it
reads (`hermod.VHostScoped`, an internal field on `DefaultMessage` that is not
data, not metadata, not serialised, copied by `Clone`, cleared by `Reset`).
`registry.runWorkflowNode` copies the mark onto messages a node built from
scratch (`inheritVHost`). The evaluator reads it in `ParseAndEvaluate` — the
only place `secret()`/`env()` have the message; `CallFunction` alone has none
and resolves globally.

A template resolved with *no* message cannot be scoped. `panmail_providers`
resolved its gateway and key with `ResolveTemplate(x, nil)` on purpose, so row
data cannot choose them; it now uses `ResolveTemplateScoped(x, msg)`, which
still reads no row data but knows the vhost. Any other "resolve against nil"
call site has the same gap.

## The preview check is the one that matters

The API never returns a value (`VHostSecret.Value` is `json:"-"`), but a preview
shows what an expression produced. `mayPreviewVHost` in
`internal/workflow/transport/http/workflow.go` gates Test, Live Preview, Run
Simulation, Test-by-ID and node unit tests: without it an Editor of vhost B
names vhost A in the request and reads A's secret off the panel. Run Simulation
takes the vhost from the *request's* workflow, which is the caller's own claim.

Not a bug, and stated in SECURITY.md: someone who may author a workflow in a
vhost can see that vhost's secrets in a preview. Write-only protects the stored
value, not a value in use.

## Decisions and their reasons

- **`default` is not a row.** `HasVHostAccess` lets everyone into `""`, `all`
  and `default`, so the API authorises with that rule and does not require the
  vhost to exist; only `""`/`all` are refused. A "404 for an unknown vhost"
  would lock the default vhost out.
- **Row id is `vhost + "/" + name`.** A secret name cannot hold a slash, so the
  last one always separates them even if the vhost's name has one; and one TEXT
  key is what every SQL dialect here accepts.
- **A store error is not a fall-through** to the global manager: it may hold a
  different value under the same name.
- **Schema version not bumped.** Standalone table; a rollback loses the
  feature, it does not misread data (note in `schema_downgrade_test.go`).
- **Pebble has no vhosts**, so no vhost secrets; the API answers 501 there.
- **A remote worker** reads a value through
  `GET /api/worker/vhosts/{vhost}/secrets/{name}`, which answers a `worker:`
  principal only — not even an Administrator.

## Known gaps

- Test Connection / discovery on a source or sink builds
  `factory.SourceConfig`/`SinkConfig` with no `VHost`, so `secret:NAME` there
  reads the global manager only. Wiring it needs an access check in each of ~14
  handlers: without one, an Editor names another vhost, points the connector at
  their own server and receives that vhost's secret as the password.
- A vhost rename moves nothing (sources, sinks, workflows and secrets all key on
  the name); pre-existing.
- The MongoDB store's tests are integration-tagged and were not run locally.

Tests: `pkg/comm/message/vhost_scope_test.go`, `pkg/security/secrets/vhost_test.go`,
`TestSecretFunctionsAnswerForTheMessagesVHost` (evaluator),
`internal/storage/sql/vhost_secrets_test.go`,
`internal/engine/registry/vhost_secrets_test.go`,
`internal/auth/transport/http/vhost_secrets_test.go`,
`internal/workflow/transport/http/vhost_secret_preview_test.go`,
`ui/__tests__/vhost_secrets_e2e.spec.ts`.
