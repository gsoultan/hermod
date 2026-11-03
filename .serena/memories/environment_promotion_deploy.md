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

**Container image config volume.** `/var/lib/hermod` must exist in the image
owned by 65532 *before* `VOLUME`, or Docker creates new named/anonymous volumes
root-owned and first-run setup fails with
`open /var/lib/hermod/db_config.yaml: permission denied` (plain `docker run`
and deploy/compose). Kubernetes was unaffected: the chart's `fsGroup` chowns the
PVC. CI's image smoke test now uses a named volume and runs
`POST /api/config/setup`; it used to set `HERMOD_CONFIG_DIR=/tmp/hermod`, which
hid the bug.
