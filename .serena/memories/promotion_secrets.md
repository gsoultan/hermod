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

UI is being folded into the promote redesign (separate thread): approval card
inputs per `Promotion.Secrets`, "Approve and go live" off until filled.

Tests: `internal/environment/secrets_sync_test.go`,
`internal/environment/transport/http/secrets_sync_test.go`.
Related: [vhost_secrets](vhost_secrets.md), [environment_promotion_deploy](environment_promotion_deploy.md).
