# Secrets: one path, and the env manager reads only its prefix

Every secret a workflow can name resolves through the configured
`secrets.Manager` (`pkg/security/secrets`):

- a connector config's `secret:KEY` / `{{secret:KEY}}` →
  `registry.resolveSecrets` → `ResolveSecret` (once, when the connector is built);
- an expression's `secret('KEY')` / `env('KEY')`, in any transformation,
  condition value or template → `evaluator.readSecret` → the manager the
  registry published with `evaluator.SetSecretSource`, wrapped in
  `secrets.CachedManager` (60 s TTL, 256 keys, 2 s timeout, singleflight) —
  expressions run once per message, and against Vault/AWS every read is a
  network call.

`EnvManager.Get` reads `Prefix+key` (or `key` when it already carries the
prefix); a blank prefix means `DefaultEnvPrefix` = `HERMOD_SECRET_`.

## Why (fixed 2026-09-28)

`env()` was `os.Getenv` and `secret()` plus `EnvManager` fell back to the bare
name, so anyone with editor rights read any process variable: live, a set value
`env('HOME')` returned `/Users/…` through `POST /api/transformations/test`, and
`env('HERMOD_JWT_SECRET')` would return the JWT signing key (editor → admin).
`{{env.X}}` was the only spelling refused — the guard matched the dotted form
while the function form sailed through `messageTokens`' `fn(...)` branch. A
condition value could test a variable for equality the same way. The Security
tab's "only env vars starting with this prefix will be searched" was false.

## The escape hatch

`HERMOD_SECRETS_ALLOW_UNPREFIXED=true` (strconv.ParseBool) in the *process*
environment restores the bare fallback so operators can rename
`PANMAIL_API_KEY` → `HERMOD_SECRET_PANMAIL_API_KEY` without an outage. It is
meant to be removed in a later release. Both the allowed read and the refused
one log once per process, naming the key, never the value (`sync.Once`, not a
per-key map: the keys are editor-supplied).

## Traps

- `evaluator.SetSecretSource` is process-wide (every transformer, template and
  condition makes its own `NewEvaluator()`); a test that sets it must restore
  `nil` in `t.Cleanup`. `NewRegistry` deliberately does not publish — the
  evaluator's default is the same strict `EnvManager`.
- A cached answer includes a *missing* secret (so a typo does not hit Vault per
  message); a *failed* lookup is not cached.

Tests: `pkg/security/secrets/{secrets,cache}_test.go`,
`TestSecretFunctions*` (evaluator), `TestExpressionSecrets*`
(registry, through `TestTransformationPipeline`).

Related: [set_node_values](set_node_values.md) (where the leak was found),
[connector_errors_quote_their_url](connector_errors_quote_their_url.md).
