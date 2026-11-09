# Secrets a promotion needs: names travel, values are typed for production

`internal/environment/secrets_sync.go`. Decided by Gembit (2026-11-03):
staging's own secret values never leave it. Production's values are typed by
a person -- the sender in the promote dialog (dev side, the redesign's main
path) or the approver -- never copied from staging.

- **Preview** (`Preview.MissingSecrets`) lists the `{{secret.NAME}}` references
  production's vhost lacks (from `PeerCheck`). Warning, not blocking.
- **Arrival** (`admit`): production computes `Promotion.Secrets` itself =
  `references(wf)` minus `secrets.HasSecret(vhost)`. Nothing from the sender is
  trusted for this, so an old staging works too.
- **Approve** (`ApproveWith(ctx, id, user, values)`; HTTP body
  `{"secrets": {"NAME": "value"}}`): blanks dropped; every name must be in some
  release member's `Secrets` (else `ErrInvalid`, naming the name, never the
  value); values pass `storage.ValidateVHostSecret`. Checked before the release
  is claimed, so a refusal leaves it pending.
- **Deploy** (`fillSecrets`, before the workflow is applied so it starts with
  them): create only where the vhost still lacks the name (never overwrite;
  "copy only missing" was Gembit's choice). Records `SecretsCopied`,
  `SecretsKept`, `SecretsMissing`. A deploy failing afterwards keeps them: they
  are production's own typed values.
- **Writer**: `SecretWriter` is optional on the SecretChecker;
  `transport/http.secretChecker.CreateSecret` does get-then-put on
  `VHostSecretStore` and calls `Registry.InvalidateVHostSecret`. Approve
  handler audits the created names. Not atomic against a concurrent Secrets
  page save (get/put race); acceptable, the window is one request.
- **Sender-typed values** (`PromoteOptions.SecretValues`, JSON `secrets`, on
  both promote and release): `checkSecretNames` refuses a name no preview lists
  as missing; `valuesFor` refuses a value for an instance without
  `FeatureSecrets` (listed in `PeerInfo` when its checker is a `SecretWriter`)
  instead of dropping it silently. Carried in `Submission.Secrets`; production
  (`admitSentSecrets`) refuses names the workflow does not use, drops values
  for names it already has, records `Promotion.SecretsFilled`, and keeps the
  values encrypted under `keyPromotionSecrets+id` (configsecrets, `_secret`
  suffix; refuses to store if encryption fails). Deploy merges sender values
  with the approver's on top (`valuesToCreate`). Dropped on deploy, fail,
  reject, "not deployed" release members and history trimming.
- Auto-deploy (approval `none`) uses only the sender's values; the rest lands
  in `SecretsMissing`.

- **Which names count** (2026-11-09): `references()` and `configSecrets()` use
  `secrets.References` (`pkg/security/secrets/references.go`), which reads every
  spelling the engine resolves: whole tokens (`{{secret.X}}`, `{{secret("X")}}`,
  `{{secret:X}}`), a secret inside a function in a token
  (`{{concat("Bearer ", secret.X)}}`, what the value builder writes), a bare
  expression (`secret("X")`, `concat(secret.X, "y")` -- dotted only when the bare
  text holds a call, so prose like "secret.txt" is not one), and bare legacy
  `secret:X`. Message fields (`{{.secret.x}}`, `source.secret.x`) and deeper paths
  (`secret.A.b`) are not. Before this only exact `{{secret.NAME}}` counted, so a
  step's secret written by the value builder was never asked for.
- **Where each is used** (`secret_places.go`): `SecretPlace{kind step|source|sink,
  name, field, node_id, value}`. `Preview.SecretPlaces` = sender's `stepPlaces(out)`
  + receiver's `CheckResult.SecretPlaces` (connections, field only, never values;
  older receiver: `placesFromUses(secret_uses)`) + `carriedPlaces`, kept to the
  missing names. `Promotion.SecretPlaces` is computed by production in `admit`
  (`Service.secretPlaces`). `secret_uses` is unchanged for older callers. Field
  labels: `fieldLabel` (list entries named by their key/name/header, trailing
  `value` dropped, config keys humanized with acronyms: "Headers › Authorization",
  "API key"). Step `value` is clipped to 300 bytes.
- UI (`SecretValues.tsx`): every row shows tags (Step blue, Source green, Sink
  orange), two places then "and N more"; a step name opens a Popover with the
  value (secret highlighted) and "Open step" (`/workflows/$id/edit`, send side
  only, via `workflow_id`); summary "3 are used by steps and 1 by a sink."; the
  many-secrets window has an Anywhere/Sources/Steps/Sinks chip filter.
  Tests: `pkg/security/secrets/references_test.go`,
  `internal/environment/step_secrets_test.go`, `ui/src/__tests__/secretValues.test.tsx`.

UI is being folded into the promote redesign (separate thread): approval card
inputs per `Promotion.Secrets`, "Approve and go live" off until filled.

Tests: `internal/environment/secrets_sync_test.go`,
`internal/environment/transport/http/secrets_sync_test.go`.
Related: [vhost_secrets](vhost_secrets.md), [environment_promotion_deploy](environment_promotion_deploy.md).
