# A step that reads a missing secret

`internal/engine/registry/step_secrets.go` (`checkStepSecrets`), `pkg/security/secrets/references.go`
(`RequiredReferences`).

## Why

The expression evaluator (`pkg/infra/evaluator`, `secretFunction` / `readSecret`) has no way to
return an error, so `secret("NAME")` and `env("NAME")` for a name with no value evaluate to `""`.
Nothing is logged. A step then sends an empty credential for every message.

Other paths were already strict: a connector's config (`Registry.resolveSecrets`) and `{{ }}`
tokens in a node's settings (`expandNodeConfig`) fail with "unresolved reference(s)".

## What happens now

`Registry.ValidateWorkflow` step 9 calls `checkStepSecrets`. For every node that is not a source
or sink it walks every string in the settings, takes `secrets.RequiredReferences` (references read
at least once with no fallback) and looks each up for the workflow's vhost through
`r.vhostSecrets()`. An empty answer refuses the start, naming step, reference and vhost.

`ValidateWorkflow` also runs for a full simulation, so the editor's test run reports it.

Deliberately not refused:

- a reference with a fallback, `secret("NAME", "default")`;
- a lookup that returns an error (secret manager unreachable): not evidence the secret is missing,
  and refusing would stop every workflow from restarting during a Vault outage;
- a whole value that is only `secret.NAME` with no call and no `{{ }}`: `References` reads that as
  prose (it could be a topic or tag name), though a `set` column does evaluate it. Known gap.

Per-message evaluation is unchanged: a secret deleted while the workflow runs still evaluates to
`""` until the next start.

Tests: `missing_secret_test.go` (registry), `required_references_test.go` (secrets).
