# Hermod operations runbook

For the person holding the pager. Every procedure here has been exercised
against a real system; where something is untested or unknown, it says so
rather than guessing.

The organising idea: Hermod's dangerous failures are quiet. A pipeline that
crashes gets noticed. A pipeline that acknowledges messages and delivers them
nowhere, or writes unmasked data, or reports "running" while nothing moves, does
not. Most of what follows is about those.

---

## Alerts, and what to do about them

Four metrics indicate data being lost or withheld while every status stays
green. They ship as a `PrometheusRule` with the Helm chart
(`metrics.prometheusRule.enabled=true`).

### `HermodMessageOverRelease` — page

A message was released after its reference count reached zero, so it returned to
the pool while still in use and another message overwrote it. The symptom is
payloads both duplicated *and* lost while the counters still balance.

This is a code defect, never capacity. Do not restart and hope.

1. Capture the workflow topology (`GET /api/workflows/{id}`) and the value of
   `hermod_message_over_releases_total`.
2. Stop the affected workflow. The corruption is per-message; other workflows on
   the same worker are unaffected.
3. Open a bug with the topology. `TestMain` guards in `internal/engine/registry`
   and `pkg/engine` fail the build on any over-release, so a reproduction in a
   test is the fastest route to a fix.

### `HermodMessagesDroppedNoTarget` — page

A workflow that has sinks resolved none of them. Messages were acknowledged to
the source and dropped, and they are **not** in a dead-letter queue.

Data already acknowledged cannot be replayed from the source. Act quickly.

1. Stop the workflow to stop the bleeding.
2. Check the workflow's edges: a sink node with no inbound edge, or an edge
   pointing at a node that no longer exists, produces exactly this.
3. Check sink reachability (`POST /api/sinks/test`).
4. For the window between the first alert and the stop, the data is gone from
   Hermod's side. Recover it from the source if the source retains history —
   Postgres CDC does if the replication slot still exists (see *WAL retention*).

### `HermodWorkerSheddingLoad` — investigate

The worker is above its admission threshold and refusing to start new workflows.
Affected workflows simply never start; nothing reports an error.

Thresholds default to 0.85 for both CPU and memory
(`internal/engine/worker/admission.go:27`). The reading is **host-wide**, so on a
shared machine this fires on load Hermod does not own.

```bash
HERMOD_ADMISSION_CPU_THRESHOLD=0.95   # or 1 to disable that dimension
HERMOD_ADMISSION_MEM_THRESHOLD=0.95
```

Prefer giving the worker a dedicated node over disabling the check.

### `HermodSubSourceBackingOff` — investigate

One source inside a multi-source workflow is failing and has been backed off.
Its siblings keep streaming, so the workflow still reports healthy while that
source delivers nothing.

Check that source's connectivity. The backoff caps at 5s between attempts, so
sustained growth means it is not recovering on its own.

---

## Crypto master key

### Rotating it

Use `PUT /api/config/crypto` (Admin only, minimum 16 characters). **Do not edit
`db_config.yaml` and restart.**

The endpoint re-encrypts every stored credential under the new key *before*
installing it, in one transaction, and installs the key only once that commits.
If anything cannot be re-encrypted the request fails and nothing is changed.

```bash
curl -X PUT https://hermod.example.com/api/config/crypto \
  -H 'Content-Type: application/json' \
  -H "Cookie: $SESSION" \
  -d '{"crypto_master_key":"<new key, 16+ chars>"}'
```

Expect `204`. Any other status means nothing was changed — read the body, it
names what failed.

### If you rotated by editing the file instead

Every source and sink credential is now encrypted under a key the process no
longer has. Symptom: authentication failures against your *own* databases, with
nothing pointing at the key change.

Put the old key back in `db_config.yaml`, restart, confirm connectors recover,
then rotate properly through the endpoint.

A value that cannot be decrypted reads back **empty**, never as raw ciphertext.
That is deliberate: handing ciphertext to a driver as a password is what made
this failure so hard to trace.

### If the master key is lost

Stored credentials are unrecoverable. There is no escrow.

1. Set a new key (any value — nothing decrypts under it either way).
2. Re-enter every source and sink credential by hand.

Which is the argument for keeping the key in a secret manager, and for the
backup below.

### Upgrades

Key derivation changed from truncate-and-zero-pad to SHA-256. `Decrypt` falls
back to the old derivation, so existing data still opens and moves forward as it
is rewritten. No action needed.

---

## Backup and restore

`GET /api/backup/export` and `POST /api/backup/import`, both Admin only. They
carry sources, sinks, workflows, vhosts, workspaces and notification settings.

### The export file is a secret

It contains **decrypted credentials in plaintext** — necessarily, since a backup
that cannot restore a credential is not a backup, and the target may have a
different master key. Treat the file as every credential in the deployment in
one document. Store it where you would store the master key.

### Taking one

```bash
curl -fsS -H "Cookie: $SESSION" \
  https://hermod.example.com/api/backup/export -o hermod-backup.json
```

Both endpoints fail loudly rather than quietly:

- An export that cannot read the database returns an error rather than
  downloading an empty file with a plausible name.
- It **refuses rather than truncating** if the deployment holds more than 1000
  objects of any one kind (`exportLimit`, `internal/infra/transport/http/infra.go:1337`).
  If you hit this, the export is not partially useful — it did not happen.

### Verifying one

A backup you have not restored is a hypothesis. At minimum:

```bash
jq '{sources: (.sources|length), sinks: (.sinks|length), workflows: (.workflows|length)}' hermod-backup.json
```

Compare against the live counts. Better: restore into a scratch instance.

### Scheduling one

Backups can be written on a timer. It is **off unless a destination is named** —
there is no default directory, so it cannot be switched on by accident, which for
this payload matters more than convenience.

The destination must not be readable beyond its owner. A backup is every
credential in the deployment in plaintext; a `0755` directory would make that
readable by every user on the host, so the schedule refuses to start rather than
writing one there:

```
Scheduled backups are configured but disabled: backup directory "/var/backups"
is mode 0755 and readable beyond its owner; ... chmod 700 it
```

Files are written `0600`, through a temporary file renamed into place, so a
reader never sees a half-written backup and a crash never leaves one. Retention
keeps the newest N and only ever deletes files it wrote itself — a destination
shared with anything else cannot lose that.

Every outcome is logged, successes included, which is how you confirm the
schedule is alive without going to look at the directory. A failure is logged
and retried on the next tick rather than stopping the service.

The same refusals as the manual export apply: a deployment over the object cap
gets no file at all rather than a truncated one.

### Restoring

```bash
curl -fsS -X POST -H "Cookie: $SESSION" -H 'Content-Type: application/json' \
  --data-binary @hermod-backup.json \
  https://hermod.example.com/api/backup/import
```

`204` means everything was written. Any 5xx names the objects that failed — the
restore still attempts every object rather than stopping at the first bad row,
because recovering most of a configuration beats recovering the prefix.

### What is *not* backed up

Users, sessions and audit logs are not in the export. Neither is the JWT secret
(it lives in `db_config.yaml`) — losing it logs everyone out but is otherwise
recoverable by generating a new one.

**There is no scheduled backup.** Nothing runs the export on a timer; it is a
manual call or your own cron. That is a real gap.

---

## Workers, leases and failover

A workflow runs on exactly one worker at a time, enforced by a lease. When a
worker stops heartbeating, another steals the lease after the TTL expires and
restarts the workflow.

Rendezvous hashing with load weighting decides who gets what, with hysteresis
favouring the incumbent so a workflow does not thrash between workers on small
load differences.

### A workflow is not running anywhere

1. Check whether any worker holds its lease.
2. Check for admission shedding (`HermodWorkerSheddingLoad` above) — a worker
   over its threshold refuses to start workflows silently.
3. On MongoDB storage before v1.7.3, no worker could ever acquire a lease: the
   lease queries filtered on the wrong document key and matched nothing.
   `AcquireWorkflowLease` returned "not acquired" with no error, which looks
   identical to another worker holding it. Upgrade.

### Rolling restarts

`terminationGracePeriodSeconds` must exceed `HERMOD_SHUTDOWN_TIMEOUT` (25s by
default). Hermod drains in-flight messages on SIGTERM; killed partway through,
everything taken from a source but not yet written is discarded. The Helm chart
refuses to render if the relationship is wrong.

---

## Postgres CDC

### WAL retention — the one that fills disks

A replication slot pins every WAL segment it has not consumed. A slot left
behind by a stopped consumer grows until the disk fills, and Postgres will not
reclaim it on its own.

```sql
SELECT slot_name, database, active,
       pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)) AS pinned
FROM pg_replication_slots
ORDER BY pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn) DESC;
```

An inactive slot with a large `pinned` value and no workflow using it is safe to
drop:

```sql
SELECT pg_drop_replication_slot('slot_name');
```

An **active** slot cannot be dropped, and one held by a live Hermod backend
should not be — stop the workflow first. This is not hypothetical: two slots
left by old test runs were found pinning 326 MB.

### A CDC workflow stopped delivering

- Confirm the slot exists and is `active`.
- Confirm the publication still lists the tables you expect. Adding a table to a
  running source requires the source to restart before the publication is
  reconciled; until then the added table silently delivers nothing.
- `wal_level` must be `logical`. Changing it requires a server restart.

### After a restart

Changes committed while the consumer was down are replayed from the slot's
`confirmed_flush_lsn`, so nothing is lost — provided the slot is **persistent**.
A temporary slot is dropped on disconnect and cannot resume by construction.

Redelivery after a restart is normal and expected: delivery is at-least-once,
and sinks upsert on the message identity.

---

## Delivery guarantees, stated plainly

At-least-once, with sink-side upsert making it exactly-once *at the destination*
— provided the source supplies a stable identity. A message with neither an id
nor an `idempotency_key` gets a generated UUID, so two deliveries of it are
genuinely indistinguishable and cannot be collapsed.

If a source produces messages without an id, set `idempotency_key` in a
transformation before the sink.

Sinks that are not upserts (webhook, SMTP, queues) deduplicate only where they
say so.

---

## Things this runbook does not cover

Stated so nobody assumes otherwise:

- **Restoring a single workflow** from a full backup. The import is all-or-nothing
  per object; there is no selective restore.
- **Multi-region failover.** Untested.
- **Sequential-sink behaviour under partial failure.** The flag resolution is
  tested; the execution difference is exercised only by an end-to-end browser
  spec that runs nightly and is not a gate.
