# Secrets per vhost — design

Date: 2026-10-02 · Status: awaiting review · Builds on: #217 (merged)

## Problem

A workflow can read a secret (`secret("NAME")`, `secret:NAME`), but Hermod has
nowhere to save one. Values live outside it: in `HERMOD_SECRET_*` environment
variables, or in Vault, OpenBao, AWS or Azure, configured once for the whole
process under Settings → Security. An operator who needs a new API key for one
workflow has to change the server's environment and restart it, and every
vhost sees the same secrets.

## Goal

Each vhost has its own store of secrets, saved from the UI, encrypted at rest,
and readable only by workflows of that vhost.

## Decisions (agreed 2026-10-02)

| Question | Decision |
| :--- | :--- |
| Shape | A built-in encrypted store inside Hermod, one namespace per vhost |
| Lookup order | The vhost's store first, then the global secret manager |
| Who manages | Administrators, and Editors whose vhost list includes the vhost |
| Reading a value back | Never. The API returns names, not values |
| Editor support | A picker of the vhost's secret names where a secret can be inserted |

## Out of scope

- Revealing a stored value.
- Masking secret values in Test / Live Preview output.
- A separate external manager (Vault, AWS…) per vhost.
- Secret history, versions or expiry.
- Secrets in workflow export/import bundles.

**Known limit, stated on purpose:** write-only protects the stored value from
the API and the UI. Anyone who can author a workflow that uses a secret can
still see it in a Test preview, because the preview shows what the expression
produced. Letting Editors manage secrets is consistent with that.

## Design

### 1. Storage

A new record, `VHostSecret`, in every storage backend (`internal/storage/sql`,
`mongodb`, `pebble`):

| Field | Notes |
| :--- | :--- |
| `vhost` | The vhost's name, as sources, sinks and workflows already reference it |
| `name` | `[A-Za-z_][A-Za-z0-9_]*`, at most 128 characters |
| `value` | Encrypted with the crypto master key (`pkg/security/crypto`), at most 64 KB of plaintext |
| `created_at`, `updated_at` | Stamped by storage |
| `updated_by` | Username of the last writer |

- `(vhost, name)` is unique. A write to an existing name replaces the value.
- Deleting a vhost deletes its secrets in the same operation.
- SQL: one migration, with up and down, embedded as `.sql` like the others.
- Storage interface additions: list names for a vhost, get one value, put,
  delete, delete all for a vhost.
- Re-encryption on a master-key change must include this table (the existing
  re-encrypt path covers connector configs today).

### 2. API

| Route | Does | Returns |
| :--- | :--- | :--- |
| `GET /api/vhosts/{vhost}/secrets` | Lists the vhost's secrets | `name`, `updated_at`, `updated_by` — never `value` |
| `PUT /api/vhosts/{vhost}/secrets/{name}` | Creates or rotates; body `{"value": "..."}` | 204 |
| `DELETE /api/vhosts/{vhost}/secrets/{name}` | Removes | 204 |

- Authorisation: Administrator, or Editor with the vhost in their list
  (`GetRoleAndVHosts` + `HasVHostAccess`). Viewers are refused, including the
  list. An unknown vhost is 404.
- Validation: name pattern and length, value non-empty and within the limit.
- Every write and delete is audit-logged with the vhost and the name. A value
  is never logged, echoed in an error, or returned.
- The routes go into `routes.golden`.

### 3. Resolution

One new manager wraps the global one:

```
secret NAME, asked for by vhost V
  1. V's store holds NAME      -> its decrypted value
  2. otherwise                 -> the global secrets.Manager (env prefix, Vault, …)
```

- **Connector configs** (`secret:NAME`): `registry.resolveSecrets` already has
  the source or sink, and so its vhost.
- **Expressions** (`secret("NAME")`, `env("NAME")`): the engine marks every
  message entering a workflow with that workflow's vhost, in an internal field
  that is neither data nor metadata, so a payload cannot set it. Clones carry
  it; the message pool clears it. The evaluator reads it through an optional
  interface and passes it to the secret source. A message with no mark resolves
  against the global manager only.
- **Test / preview**: the request names the vhost being edited; the handler
  checks it against the user's vhost list before marking the preview message.
- **Cache**: the expression-side cache from #217 is keyed by vhost and name,
  so one vhost is never answered with another's value. A put or delete drops
  that entry at once; the TTL stays as the bound for global lookups.

### 4. UI

- A **Secrets** page in the navigation for Administrators and Editors, scoped
  by the existing vhost selector.
- A table: name, last updated, updated by. Actions per row: **Rotate**,
  **Delete** (with confirmation naming the secret).
- **Add secret**: name and a masked value field. The value field is never
  pre-filled, on add or on rotate.
- States: loading, empty (explains `secret("NAME")` and the fallback to global
  secrets), error, and no access.
- **Editor picker**: where the editor already offers "Insert variable", a
  second list offers the current vhost's secret names and inserts
  `secret("NAME")` (or `{{secret("NAME")}}` in template fields). Names only.

### 5. Documentation

`SECURITY.md` (the store, who can manage it, the preview limit), `CHANGELOG.md`,
the Security settings tab's description of the lookup order, and a Serena
memory.

## Testing

- **Storage**: put/list/get/delete, uniqueness, vhost cascade, and that the
  stored bytes are not the plaintext — for each backend. Migration up and down.
- **API abuse cases**: Editor of vhost B against vhost A (list, put, delete);
  Viewer; bad names; oversize value; and that no response body or log line
  contains a value.
- **Reachability**: a workflow started through the registry in vhost A reads
  its own secret, cannot read vhost B's secret of the same name, and falls back
  to the global manager for a name the vhost does not hold. A rotate is seen by
  the running workflow without a restart.
- **Spoofing**: a message whose data or metadata claims another vhost still
  resolves against its workflow's vhost.
- **UI**: Vitest for the page and the picker; one Playwright spec that adds a
  secret, uses it in a Set Fields value, and sees it resolve in Live Preview.

## Open points for the implementation plan

1. Where exactly the engine can mark messages: it has no vhost today, so the
   registry must hand it one when a workflow starts.
2. Whether the current master-key re-encryption walks all tables or a fixed
   list.
3. How a vhost rename is handled today for sources, sinks and workflows; the
   secrets must follow the same rule.
4. Pebble and MongoDB have no migrations; confirm how new collections are
   introduced there.
