### Environment promotion: what is checked when, and the image's config volume

**Promotion (internal/environment).** Staging previews against production
(`prepareWith` → `checkAgainst`), production checks again when it receives a
submission (`admit` → `hasConnections`, soak, vhost), and checks a third time
when an admin approves (`deploy` → `hasConnections` against the sending peer,
`sender()`). The third check exists because a promotion can wait hours for
approval: before it, a sink deleted on production after the release was sent
was deployed as a dangling `ref_id`. The promotion read "deployed" on both
sides while the worker failed every 10s with "sink node … refers to missing
sink". Now the promotion is `failed` with the missing ID in `reason`, and in a
release the members deployed before it are undone by `deployGroup`.

A peer revoked between send and approve is treated as limited to the
promotion's own vhost (never wider than the link was).

`admin_except_sender` compares usernames across instances
(`strings.EqualFold(p.Sender, username)`): an `admin` on staging cannot approve
as `admin` on production. Intended — it is the only cross-instance identity.

On the sender, `Promotion.FromEnv` holds the *destination's* name (see the
comment in promote.go); on the receiver it is the source's. The UI relies on it.

Verified end to end on two live instances (sqlite, `--mode=standalone`): pair,
match, release, approve, v2 onto a running workflow, Undo, unmatched-blocked,
connection-deleted-before-approve.

**One connection per server (connection.go).** A vhost added over an existing
connection gets its own Peer and token on the receiver, in the same `Group`
(the first peer's ID; `groupOf` falls back to the ID for older peers), so
scoping stays one token per vhost. `Peer.AddVHosts` (set from the pairing
code) decides whether it is issued at once or held as a `VHostRequest` for an
admin. On the sender the Link is `Pending` with no token and `Via` = the link
whose token asks again; `TestLink` → `askAgain`. A peer with `VHost == ""`
(every vhost, legacy) cannot add vhosts. The UI groups links by normalized URL
(`groupLinksByServer`) and peers by Group (`groupPeers`).

**Deciding from the sender (decide.go).** `Peer.Decide` (from the pairing
code's `AllowDeciding()` or `PATCH /peers/{id}`) lets the sender relay
approve/reject/rollback: `DecideSent` → `Remote.Decide` →
`POST /peer/decisions` → `PeerDecide`, which checks `p.PeerID == peer.ID`,
applies the rule to the relayed username (`approve(ctx, id, username, by, secretValues)`)
and records `name (via Instance)`. The sender learns it from the `decide`
feature in PeerInfo (`Link.Decide`, refreshed by TestLink; DecideSent
re-tests once when it is false). Remote 400/403/404/409 map back to the
same error kinds so the UI sees a 403/409, not a 502. A vhost added to a connection copies the
group peer's `Decide` (issueVHostPeer).

**Promote dialog (ui ReleaseDialog.tsx).** One screen; the fixes come from
structured preview fields `unmatched` (MatchRow without candidates: the
preview is Editor-visible, production's connections stay behind the
admin-only matching route, which the dialog calls for admins) and
`missing_envs` (`HERMOD_ENV_` + name, pkg/security/secrets/expand.go).
`problems()` skips the "Connections matched" check when `unmatched` covers
it. The flow page shows `PromoteStrip`/`usePromoteTarget` (link for the
vhost, else the legacy every-vhost link; ready lane for how far behind).

**Secret values UI (ui SecretValues.tsx).** One component for both sides
(`mode: 'send' | 'approve'`): up to `INLINE_SECRETS` (5) are inputs in place,
more open a window with search, an Empty filter fixed at open (rows do not
vanish while typing), `parseEnvBlock` paste, Clear all. `neededSecrets`
lists each name once with the flows using it; `filledValues` drops blanks,
so a blank never reaches the server. The approve card asks for
`secrets` minus `secrets_filled` and posts `{secrets}` only when something
was typed (API from PR #361). ReleaseDialog uses it in `send` mode over
`neededSecrets(all previews)`, sends `secrets` only when one was typed, and
the footer says "N secret values left for the approver".

**Container image config volume.** `/var/lib/hermod` must exist in the image
owned by 65532 *before* `VOLUME`, or Docker creates new named/anonymous volumes
root-owned and first-run setup fails with
`open /var/lib/hermod/db_config.yaml: permission denied` (plain `docker run`
and deploy/compose). Kubernetes was unaffected: the chart's `fsGroup` chowns the
PVC. CI's image smoke test now uses a named volume and runs
`POST /api/config/setup`; it used to set `HERMOD_CONFIG_DIR=/tmp/hermod`, which
hid the bug.

**Empty secret at approval (missing_secret_start_test.go).** `deploy` keeps a
workflow stopped (`applyPromoted(..., stopped)`, Active=false even if it was
running) when `deploySecrets` leaves `stillMissing` and the approver did not
pass `ApproveOptions.StartAnyway`; the promotion records `KeptStopped`.
Automatic (`none` rule) deploys never start anyway. The flag rides
`POST /promotions/{id}/approve` (`start_anyway`), `POST /sent/{id}/approve`
and `Decision.StartAnyway` for relayed approvals. `copyOutcome` copies the
deploy outcome (status, secrets, kept_stopped) into the sender's sent log.
UI: `StartChoice` and `startLine` in SecretValues.tsx, used by ReleaseCard
and SentReleases' WaitingRow.

**Secrets of sources and sinks.** The secrets a promotion needs are the
workflow's own `{{secret.NAME}}` (`references`) plus those the sources and
sinks it uses read in their Config (`configSecrets`). The receiver computes
them: `PeerCheck` adds the secrets of `req.Sources`/`req.Sinks` (its own
connections, after matching) and answers `secret_uses` (name -> "sink
warehouse"); `secretsUsed(ctx, wf)` feeds `missingSecrets` and
`admitSentSecrets`. Preview carries `secret_uses`; `neededSecrets` turns it into
`via`, shown as "Read by sink warehouse".
