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

Each production is one card under **Promotes to**, however many vhosts are connected to it. To
promote another vhost into the same production, click **Add vhosts** on its card, tick the vhosts
and name where each lands. No new pairing code and no sign-in to production are needed: staging
asks over the connection it already has, and production issues a separate token for each vhost.
Typing the address of a production you are already connected to in **Connect a server** offers
**Add vhosts instead**.

When production authorized the connection with **It may add more on its own** (the default on
the authorize page), an added vhost is ready at once. With **Ask me each time**, it shows
**Waiting for approval** until a production administrator clicks **Allow** under **Receives from**;
staging asks again when you test the vhost. **Decline** tells staging once, and the vhost is
removed there.

Connecting a vhost that is already connected with a pairing code replaces its connection. A vhost a production created this way starts empty: its
connections, secrets and variables are added there like in any other vhost, and **Create it in
Production** on the matching screen opens the right page.

Production needs to be reachable from staging over HTTP(S). Staging does not need to be reachable
from production.

A connection made before vhosts were connected one by one shows as **Every vhost (older link)** and
keeps working for every vhost that has no connection of its own. Connect a vhost to replace it.

## Promote

**Releases** in the side menu shows one lane per connected vhost, such as
`hr → Production · hr`, listing the workflows that changed since they last went through it:
**New** for one that never went, or `v4 → v5`. Every one starts ticked; untick what should wait.
**Promote all ready** (or **Promote 2 of 3** once something is unticked) sends what is ticked in
every lane at once, and **Promote** on a lane sends that lane's.

On a flow's page, **Promote to Production** sits next to **Edit flow**, and a line under the header
says how far behind production is: **Up to date**, **not there yet**, or
`last sent v12 · 2 versions to promote`.

The promote dialog is one screen: who approves it, where it goes (`v14 → now runs v12`, or **new
there**), what changes, and the button. Each workflow goes to the environment its vhost is
connected to, and production checks them all first.

- **Ready**: "Ready to send. All 6 checks passed." with **Show checks**, and the button reads
  **Send for approval** (or **Send and go live** where production approves nothing).
- **Something to fix**: the dialog lists each thing, numbered, with the fix under it. A connection
  with no match in production is matched right there by an administrator, from production's
  connections of the same type, and saved for next time; an editor is told an administrator does
  it. A server variable production lacks says which `HERMOD_ENV_` variable to set there. The
  button stays off until everything left can land.
- **Several flows**: each is a row, with **Changes** to see what changes, or **Fix** and **Leave
  out** when it cannot land yet. **Add another flow** adds one of the flows ready to promote.
- **More options** holds the version, whether it starts after the deploy, and whether a failure
  undoes the release; the closed row says what is chosen.

After sending, the dialog says whether each release is live or waiting, with **Follow it on
Releases**. A release carries at most 50 workflows.

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

The secrets a workflow uses are the ones its nodes name with `{{secret.NAME}}` **and** the ones the
sources and sinks it uses read in their settings, such as a database password. A secret only a
connection reads is listed with that connection ("Read by sink warehouse").

### Sources and sinks production lacks

A source or sink production has no match for does not have to be made there by hand first. When
its credentials are secrets (`{{secret.NAME}}` or `{{env.NAME}}`), it travels with the release and
is created in production, with the same ID, when the release is approved. Next time anything uses
it, production already has it and reuses it. The dialog's **Connections** list shows each one:

- **Uses Production's …**: production has it already (matched by mapping, name, or the same ID).
- **Created in Production**: it comes with the release. Its other settings, such as a host or a
  database name, can be changed for production right there; its secrets resolve in production on
  their own. The approver's card says which connections approving creates.
- **Needs one step**: a credential is typed in plain text, so it can't travel. **Move … to a
  secret** stores that value as a secret here (named, say, `SLACK_ALERTS_TOKEN`), rewrites the
  setting to `{{secret.SLACK_ALERTS_TOKEN}}`, and checks again; the secret then appears with the
  ones production lacks so its value there can be typed.

A connection that exists in production in another vhost is refused. At most 50 connections travel
in one promotion. A production too old to create connections still needs them matched first.

Secrets are checked against production's vhost secrets and `HERMOD_SECRET_*` environment
variables. A secret kept in an external secret manager cannot be seen from here, which is why a
missing secret is a warning and not a blocker.

Staging's own secret values never go to production: staging's credentials stay on staging.
Production notes which of the secrets a workflow uses its vhost lacks. The dialog lists them, and
the sender can type **production's** value for each one there. The value is sent with the release,
kept encrypted on production until it is decided, and never stored on staging. Anything left blank
can be filled in by the approver (see below). Up to five are typed in the dialog itself; more open
one window with search, an Empty filter and a `.env` paste. The footer says how many values are
left for the approver.

Through the API: `"secrets": {"CRM_KEY": "..."}` in the body of a promotion or a release. A value
for a secret production does not lack is refused, and so is one sent to a production too old to
take values.

## Approve it on production

Production lists each release under **Approvals › Releases**, the second entry of the side menu,
whose red badge counts what waits for you; Home lists it under **Needs you** too, and **Releases**
shows it under **Waiting for you**. The card says where it came from and the vhost it lands in, the
sender, the note, and each workflow's changes. An administrator clicks **Approve and go live** or
**Reject**, once for the whole release. A vhost a connection asks to add waits on the same tab.

A release is not the same as a **held message** (the other tab of Approvals): that is one message an
Approval step of a running flow paused, which an editor lets continue. Approving it changes nothing
else.

When a release uses secrets its vhost has no value for, the card asks for them under **N secrets
have no value in hr yet**, leaving out the ones the sender already typed (named on the card).
More than five open one window with search, an **Empty** filter and **Paste a .env block**, which
fills the boxes from `NAME=value` lines and lists the names that are not needed. Leave one empty
when a secret manager serves it. Once live, **Recently live** shows which secrets were saved and
which still have no value.

A workflow sent to start that still has a secret without a value goes live **stopped**, so it does
not start and fail. While one is empty the card asks what to do: **Keep it stopped until NAME has a
value** (the default) or **Start it anyway** (when a secret manager serves it). A workflow already
running there is stopped too when its new version lacks a secret. Afterwards **Recently live** says
it was kept stopped; start it from the flow once the value is set. The same choice is on **Needs
your approval** when approving from the sending side. A release that goes live without approval
(the `none` rule) is kept stopped in the same way.

Approving checks once more that every connection a workflow uses still exists in production, in its vhost. A connection deleted or moved while the release waited fails the release with the connection's ID as the reason, instead of deploying a workflow that cannot start.

When a workflow uses secrets that production's vhost lacks, the release lists them by name, says
which ones the sender filled in, and the approver types the rest (or replaces one) before
approving. Approving creates them in that vhost's secrets,
as the Secrets page would, before the workflows start. A rejected or failed release drops the
values it carried. A secret that exists by then, for example
one added on the Secrets page while the release waited, is kept as it is and never replaced. A
value left blank stays missing, which is right for a secret served by a secret manager. The
promotion then lists which secrets were created, which were already there, and which are still
missing. The audit log names the secrets that were created, never their values.

Through the API: `POST /api/environments/promotions/{id}/approve` with
`{"secrets": {"CRM_KEY": "..."}, "start_anyway": true}`. A name the release does not need is
refused. `start_anyway` starts the workflows even with a secret still missing; the promotion
answers `kept_stopped: true` when it did not start for that reason.

A release goes live whole or not at all. If one workflow fails to deploy, the ones before it are
put back as they were, and the rest are marked failed with the reason.

### Or approve it from staging

Nobody has to sign in to production to approve. When production lets a connection decide (the
**Its administrators may approve, reject and undo what it sends, from there** box on the authorize
page, on by default, or the **Its admins approve from there** switch on a linked instance under
**Receives from**), staging's **Releases** page lists each waiting release under **Needs your
approval**. A staging administrator clicks **Approve** or **Reject** there, and **Undo** under
**Recently sent** on a live one.

Production still applies its own rules: under **another administrator than the sender**, the
person who sent a release cannot approve it from staging either. Production records the decision
as `name (via Staging)`, in the promotion and in its audit log, and staging's audit log records
it too. Production can switch it off for a connection at any time.

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
  working at once. **Cut off** on production revokes every vhost of a connection at once.
- Adding a vhost over a connection gives it a token of its own, scoped to that vhost; the token
  that asked keeps its own scope. A connection that sends into every vhost (an older link) cannot
  add vhosts, and a connection authorized with **Ask me each time** waits for an administrator.
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
| `POST /pairing-codes` | administrator | Issue a code for a vhost (`{"label", "vhost", "add_vhosts", "decide"}`), creating it if missing; `add_vhosts` lets the connection add vhosts without asking, `decide` lets the linked instance decide what it sends |
| `GET /peers`, `DELETE /peers/{id}` | administrator | Instances linked to this one; `?all=true` cuts off the whole connection |
| `PATCH /peers/{id}` | administrator | `{"decide": bool}`: let its administrators approve, reject and roll back what it sends, from there |
| `GET /vhost-requests`, `POST /vhost-requests/{id}/allow`, `DELETE /vhost-requests/{id}` | administrator | Vhosts a connection asked to add: list, allow, decline |
| `GET /promotions` | Editor | What arrived, in the vhosts you may see |
| `POST /promotions/{id}/approve`, `/reject`, `/rollback` | administrator | Decide |
| `GET /links` | Editor | Where this instance promotes to |
| `POST /links`, `DELETE /links/{id}` | administrator | Connect a vhost (`{"url", "code", "self_url", "vhost"}`) and unlink |
| `POST /links/{id}/vhosts` | administrator | Add vhosts to the connection `{id}` belongs to: `{"routes": [{"vhost", "target_vhost"}]}`; 201 with the new links, `pending` while production waits for an administrator |
| `POST /links/{id}/test` | Editor | Check it answers; a pending vhost asks production again |
| `GET /links/{id}/matching`, `PUT /links/{id}/mapping` | administrator | Connection matching |
| `POST /links/{id}/preview`, `POST /links/{id}/promotions` | Editor | Check and promote. A preview lists `unmatched` connections (this side's, never production's), `connections` (each with `status` uses, create or blocked) and `missing_envs`; a promotion takes `settings` (`{connection ID: {key: value}}`) for the connections it creates |
| `GET /sent` | Editor | What this instance sent |
| `POST /sent/{id}/approve`, `/reject`, `/rollback` | administrator | Decide it on the instance it went to, when that one allows it; `reject` and `rollback` take `{"reason"}`, `approve` takes `{"start_anyway"}`. A deploy that failed there answers 422 with `error` and `promotion` |
| `POST /connections/{kind}/{id}/secret` | Editor | Move a plain-text credential of source or sink `{id}` to a secret: `{"key", "name"}`; 204, 409 when the secret exists |
| `GET /ready` | Editor | Per connected vhost, what changed since it was last promoted |
| `POST /releases/preview` | Editor | Check a release: `{"items": [{"workflow_id", "version"}]}` |
| `POST /releases` | Editor | Send it, with `note`, `start` and `auto_rollback`. 201 with each destination's release; 502 with `error` and `sent` when only part went |

Linked instances call `POST /pair` with a pairing code, and `/peer/*` with their token, including
`POST /peer/vhosts` to add a vhost to the connection, `POST /peer/releases` to submit a release, `POST /peer/decisions` to approve, reject or roll back one it sent (`{"promotion_id", "action", "by", "reason"}`; refused unless the peer may decide) and `GET /peer/promotions` to read back what became of
the ones they sent. These are outside the session and checked by the handler.
