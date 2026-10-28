# Environments: promoting workflows from staging to production

Run one Hermod per environment, a staging install and a production install, each with its own
database, workers and connections. Build and test a workflow on staging, then **promote** it.
Production receives the workflow with its own connections in place of staging's, and an
administrator there approves it before it goes live.

Each **vhost** is connected on its own: staging's `hr` vhost promotes into production's `hr`
vhost, its `marketing` vhost into production's `marketing`, and so on. Production creates a vhost
it does not have yet when its administrator allows the connection. Workflows that go out together
form a **release**, which production approves, undoes and rolls back as a whole.

A development install can promote straight to production in the same way; staging is optional.

Nothing is shared between the two installs except the link described below. Staging never
writes to production's database, and production never sees staging's credentials.

## Set it up

Everything is in the UI, under **Settings › Environments**. You need an administrator account on
both installs.

1. **On production**, pick **Production** as what this instance is, and give it a name if you want
   one other than "Production". Its badge is now in the header of every page.
2. **On staging**, pick **Staging**.
3. **On staging**, click **Connect an environment**. Enter production's address, the one you open
   it at in the browser, such as `https://hermod.example.com`; pick the vhost whose workflows go
   there under **Workflows from**; and under **Land in**, the vhost on production they land in.
   It defaults to the same name.
4. Approve the connection on production. Either:
   - click **Sign in to … and approve**. Production opens in a small window; sign in there if asked
     and click **Allow**, or **Create hr and allow** when production has no vhost of that name
     yet. The window closes and staging finishes the connection on its own; or
   - ask a production administrator to name the vhost under **Into vhost** and click **Create
     pairing code** under Settings › Environments there, and type the code in. A code works once,
     for 10 minutes, and its workflows land in the vhost it was made for, which production creates
     when the code is used if it does not have it.
5. **Match connections.** Staging lists each of the vhost's sources and sinks with the production
   connection that stands in for it. Hermod matches by name and type: `orders-db` matches
   `orders-db`, and `orders-db-stg` is suggested for `orders-db`, marked **Check this**. Confirm
   the suggestions, pick the rest, and follow **Create it in Production** for any that production
   does not have yet. You can come back to this at any time with **Match connections** on the link.

Repeat steps 3 to 5 for each vhost that should promote. Connecting a vhost that is already
connected replaces its connection. A vhost a production created this way starts empty: its
connections, secrets and variables are added there like in any other vhost, and **Create it in
Production** on the matching screen opens the right page.

Production needs to be reachable from staging over HTTP(S). Staging does not need to be reachable
from production.

A connection made before vhosts were connected one by one shows as **Every vhost (older link)** and
keeps working for every vhost that has no connection of its own. Connect a vhost to replace it.

## Promote

**Environments** in the side menu shows one lane per connected vhost, such as
`hr → Production · hr`, listing the workflows that changed since they last went through it:
**New** for one that never went, or `v4 → v5`. **Promote all ready** sends every lane at once;
**Promote** on a lane sends that lane, and **Promote** on a workflow sends just it, with a choice
of version.

Each workflow goes to the environment its vhost is connected to, and production checks them all
first. The dialog groups them by destination. One that cannot land shows why, and **Leave out**
takes it out of this release; the button stays off until everything left can land. It reads, for
example, **Promote 5 workflows to 2 environments**. A release carries at most 50 workflows.

For each workflow Hermod checks, against production:

| Check | Blocks the promotion |
| --- | --- |
| The workflow is valid | Yes |
| Production is reachable | Yes |
| Every connection the workflow uses is matched | Yes |
| Each matched connection still exists in production | Yes |
| The secrets it references exist in production | No, a warning |
| The environment variables it references exist in production | No, a warning |
| It has been on staging for the minimum time production asks for | Yes |
| It lands in the connection's vhost, and its ID is not a workflow in another vhost there | Yes |

It also shows what changes in production: nodes added, removed or changed, links between nodes,
and workflow settings. Add a note for the approver, choose whether the workflows start once
deployed and whether the release is undone on its own if one of them starts failing, and click
**Promote**. **Recently sent** on the Environments page shows where each release is: waiting for
approval, live, rejected or undone.

Against a production that predates releases, the workflows are sent one by one and each is
approved on its own there.

Secrets are checked against production's vhost secrets and `HERMOD_SECRET_*` environment
variables. A secret kept in an external secret manager cannot be seen from here, which is why a
missing secret is a warning and not a blocker.

## Approve it on production

Production lists each release under **Environments › Waiting for you**: where it came from and the
vhost it lands in, the sender, the note, and each workflow's changes. An administrator clicks
**Approve and go live** or **Reject**, once for the whole release.

A release goes live whole or not at all. If one workflow fails to deploy, the ones before it are
put back as they were, and the rest are marked failed with the reason.

What production keeps from its own copy when it deploys:

- the workflow's runtime state (status, counters, assigned worker),
- its vhost and workspace,
- whether it was running. A running workflow keeps running on the new version; a new or stopped
  one starts only if the sender asked for that.

Every deploy is a new version in the workflow's history on production, made by the approver, so
it can be rolled back from **History** like any edit.

## Rules

Production's rules are under Settings › Environments › **Receives from**:

- **Who approves**: an administrator of production other than the sender (the default), any
  administrator, or nobody, which deploys a release as soon as it arrives.
- **Roll back automatically**: within 10 minutes of a deploy, if any workflow in the release turns
  to an error, or logs at least 10 new errors making up more than 5% of what it processed, the
  whole release goes back to how it was. On by default; the sender can turn it off for one
  release.
- **Minimum time in the sending environment**: how long a version must have existed on staging
  before it can be promoted. 0, the default, means no minimum.
- **Rules for one vhost**: a vhost listed here uses its own approval and minimum time instead,
  for example `payroll` waiting a day on staging while the rest wait for none.

**Undo**, under **Recently live**, puts every workflow in a release back to the version before
it. It is offered only while the release is still the newest deploy of each of its workflows, so
an older release cannot undo a newer one. A workflow the release created is stopped.

## Security

- The link is a token production issues once, when the pairing code is redeemed. Production
  stores only its SHA-256 hash; staging stores it encrypted with the master key, and it is
  re-encrypted when the master key is rotated.
- Pairing codes are single-use, expire after 10 minutes and are stored hashed. Ten wrong codes in
  a row clear every outstanding code, and one address can try at most 30 times an hour.
- Each token belongs to one vhost on production. It only lets staging read that vhost's
  connection names and types, run the checks, and submit releases into that vhost that wait for
  approval. It cannot read connection settings or secrets, touch another vhost, change anything
  already in production, or approve its own releases. A workflow sent with the ID of a workflow
  in another vhost is refused.
- Production creates a vhost only when a pairing code an administrator issued for it is used,
  which is what **Allow** does.
- **Revoke** a linked instance on production, or **Unlink** it on staging, and the token stops
  working at once.
- The sign-in window sends the pairing code back only to the address it displays, and only after
  an administrator clicks Allow.
- Every link, pairing code issued, promotion, approval, rejection and rollback is in the audit log.
  Pairing codes themselves are not.

## Separate workers

Each install has its own workers, so staging load never reaches production. A promoted workflow
keeps the worker production assigned it, if any; staging's worker assignment is not carried over.

## API

All under `/api/environments`. Session routes need an administrator unless marked Editor.

| Route | Who | |
| --- | --- | --- |
| `GET`, `PUT /self` | any, administrator | What this instance is |
| `GET`, `PUT /rules` | any, administrator | Production's rules |
| `POST /pairing-codes` | administrator | Issue a code for a vhost (`{"label", "vhost"}`), creating it if missing |
| `GET /peers`, `DELETE /peers/{id}` | administrator | Instances linked to this one |
| `GET /promotions` | Editor | What arrived, in the vhosts you may see |
| `POST /promotions/{id}/approve`, `/reject`, `/rollback` | administrator | Decide |
| `GET /links` | Editor | Where this instance promotes to |
| `POST /links`, `DELETE /links/{id}` | administrator | Connect a vhost (`{"url", "code", "self_url", "vhost"}`) and unlink |
| `POST /links/{id}/test` | Editor | Check it answers |
| `GET /links/{id}/matching`, `PUT /links/{id}/mapping` | administrator | Connection matching |
| `POST /links/{id}/preview`, `POST /links/{id}/promotions` | Editor | Check and promote |
| `GET /sent` | Editor | What this instance sent |
| `GET /ready` | Editor | Per connected vhost, what changed since it was last promoted |
| `POST /releases/preview` | Editor | Check a release: `{"items": [{"workflow_id", "version"}]}` |
| `POST /releases` | Editor | Send it, with `note`, `start` and `auto_rollback`. 201 with each destination's release; 502 with `error` and `sent` when only part went |

Linked instances call `POST /pair` with a pairing code, and `/peer/*` with their token, including
`POST /peer/releases` to submit a release and `GET /peer/promotions` to read back what became of
the ones they sent. These are outside the session and checked by the handler.
