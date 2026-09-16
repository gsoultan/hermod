# Changelog

Notable changes to Hermod, newest first. Dates are ISO-8601.

This file starts at 1.0.0. Everything published before it was withdrawn — see
[The releases before this one are gone](#the-releases-before-this-one-are-gone).

## [Unreleased]

### Fixed — queries that borrow a source's database could still land on a CDC one

Two node types run SQL against a source they merely name rather than stream
from: `db_lookup`, once per message, and the `batch_sql` source, which holds no
connection of its own and runs whole queries on a cron against the source in its
`source_id`. Neither belongs on a database already paying for logical
replication — and where a `batch_sql` delegate is also a CDC source node, the
same rows arrive twice, once streamed and once batched.

`db_lookup` had carried that rule since it was written, with two holes. It only
fired when the source had an explicit `use_cdc` key, but the factory that builds
a source reads the flag as opt-out — `useCDC := cfg.Config["use_cdc"] !=
"false"` — so a source with no key runs as a CDC source and passed the check
anyway. And it sat inside one arm of the batching branch, so a node with Batch
Lookups switched on skipped it outright, cached the row it was not allowed to
fetch, and served every later message from that cache. `batch_sql` had no rule
at all.

There is now one definition — `hermod.SourceAllowsDirectQueries` — read by the
factory, the lookup transformer and the registry, so the three cannot drift.
`db_lookup` checks once, ahead of both branches. The registry refuses to build a
`batch_sql` source on a CDC delegate, on both of its constructors. SQL Server
stays the documented exception: its CDC is read back through ordinary queries
against change tables.

Two smaller decisions fell out of it. The lookup's refusal deliberately does not
go through `onMiss` — a misconfigured source is not a lookup that found no row,
and a passthrough policy must not turn it into silence. And a `batch_sql`
delegate that cannot be resolved now fails the source naming that delegate,
rather than being waved through: treating a failed lookup as "no objection"
would let a transient storage error during a restart build the very source the
check exists to refuse.

Both editors apply the same rule from one place (`ui/src/lib/sourceCdc.ts`). A
CDC source is listed but not selectable, labelled with the reason rather than
filtered out, so an absent entry never reads as a missing source. A node or
source already pointing at one keeps the value it was configured with and says
why it will not run.

**Behaviour change:** a query target with no `use_cdc` key is now refused. That
is the reading the rest of Hermod already uses, so such a source was being built
as a CDC source regardless — set `use_cdc` to `false` on it, which is what a
lookup or batch target is meant to be.

Validation knows the rule too. `GET /api/workflows/{id}/validate` and every
save now report a `db_lookup` or `batch_sql` node aimed at a CDC source, as a
warning — an error would turn saving the fix into a 400. That is the only
notice a workflow created through the API or restored from a bundle ever gets,
since it never passes through the editor's pickers.

And switching CDC *on* for a source a running workflow queries is now refused.
`checkActiveWorkflows` guarded only sources held in a source node's `ref_id`,
which is one of the ways a workflow names one: a `db_lookup` holds its source in
the node config, and a `batch_sql` source holds its database in `source_id`. A
source reached only those ways could be edited out from under a running
workflow, and after this release that edit breaks it on the next message.
`storage.WorkflowQueriesSource` answers the reference question in one place.

`execute_sql` is deliberately not blocked, because it is not the same hazard.
It writes — `ExecContext`, with nothing to hand back but a row count — so the
risk is not query load on a replicating database but a **feedback loop**: a
write into a published table produces a change event that comes back round the
pipeline. That is scoped to the table while `use_cdc` is scoped to the source,
so refusing the source would break the ordinary case of writing an audit or
status row nobody streams. Its editor names the real risk and leaves the choice.

### Fixed — a sink set to "Sequential Execution" never acknowledged anything it delivered

A sink node with Sequential Execution switched on writes the message itself, so
it deliberately hands the engine no routing targets. The engine read that empty
list as "this workflow has sinks and resolved none of them" — its data-loss
case — and took the branch that refuses to acknowledge, on every message, of a
workflow that was delivering all of them correctly.

Measured on a 202-row PostgreSQL CDC run: every row reached the destination,
all 202 were counted in `hermod_engine_messages_dropped_no_target_total` (a
metric documented as "any non-zero value is an incident"), an ERROR said the
data had gone nowhere, and the replication slot stopped advancing — 108 KB of
WAL retained and never released, growing for the life of the workflow, with the
whole backlog replayed on the next start. With the flag off, the same run
retained nothing.

A sink that writes inline now says so, and the engine acknowledges it. A sink
whose inline write *failed* still does not, so it is redelivered rather than
lost — and with several inline sinks, one failing keeps the message
unacknowledged even if another succeeded.


### Fixed — an enriched CDC message could reach the sink without its enrichment

What a message serialised to depended on whether anything had read it before the
first write. `SetData` hydrated a lazily-decoded payload differently from
`Data()`/`DataRef()`: on a CDC message it buried the row under a nested `after`
key and left the newly written field at the root, where `Payload()` — which
serialises only `data["after"]` — no longer included it. The same message then
came out four different ways, with `MarshalJSON` nesting it twice as
`after.after`.

`db_lookup` is how this reached a destination. In query mode there is no key
field, so nothing reads the message before the result is written, and the Cache
TTL box is empty by default — where `ttl <= 0` means never expire. The first
message took the query path, which happens to read; every message after it was
served from cache and silently lost its looked-up value. Measured live: 1 of 5
rows carried the enrichment. It is rate-dependent, which is what made it hard to
see — a burst is processed concurrently, races past the cold cache, and looks
fine.

Both paths now hydrate through the same helper, so the shape no longer depends
on access order, and `ToMap`/`MarshalJSON` share one after-image. The same
change stops `SetData` destroying a payload that is not a JSON object: the
unmarshal failed, nothing was stored, and the payload bytes were cleared at the
end of the call, so the first write threw away a plain-text body.


### Fixed — Data Conversion reported success when the field did not exist

A field name that resolved to nothing returned the message unchanged with no
error: a green node, untouched data, and nothing anywhere to say the conversion
had not run. A misspelling is the usual way to get there, and the editor offers
field names from the source's stored Sample, which is known to drift from the
names a live CDC stream carries.

An unresolvable field is now a conversion failure and follows the node's Error
Behaviour like any other — which the editor already defaults to "fail", so the
setting an operator is looking at is now the one that applies. Pipelines where
the field is genuinely optional set "keep" to pass the message through
untouched, or "null" to write an explicit null.


### Fixed — a `jsonb` column arrived as a string on the CDC path, and vanished when it was TOASTed

A PostgreSQL `jsonb` column had two different shapes depending on how the row
reached the pipeline, and the workflow editor showed you the one that does not
run in production.

The snapshot, polling and `Sample` paths read rows through pgx, whose registered
codec unmarshals `jsonb` into a map — so those messages carried a real nested
object. The live CDC path decodes pgoutput tuples by hand, and pgoutput sends
every column as text, so the same column arrived as a string that happened to
contain JSON. The editor builds its *available fields* list from the source's
stored sample, which comes from the first path. It therefore offered
`meta.addr.city`, and on a running CDC pipeline that path resolved to nothing.
Both paths now produce the object.

The second half is worse and is the reason to read this entry. PostgreSQL stores
a `jsonb` value larger than about 2 KB out of line, and an `UPDATE` that does not
touch such a column does not send it — it marks it *unchanged* instead. That
marker was not a case in the tuple decoder at all, so the column was dropped from
the after-image and a sink writing that image lost the document. Every update to
a row with a document of any size, silently. Where the table is `REPLICA IDENTITY
FULL` the before-image does carry the value and the after-image is now completed
from it. Where it is not — `REPLICA IDENTITY DEFAULT` sends no before-image at
all — nothing in the WAL record holds those bytes, so the column stays out of the
row image rather than being invented, and the message now carries
`unchanged_toast_columns` naming it. **If you stream a table with large `jsonb`
or `text` columns, `ALTER TABLE ... REPLICA IDENTITY FULL` is what makes updates
complete.**

MySQL had the same shape problem on its own `JSON` type, on both the binlog and
the query paths, and is fixed the same way.

Only columns the database itself types as JSON are decoded, never columns that
merely contain it: a `VARCHAR` holding `{"a":1}` is still a string. MariaDB is
therefore unchanged — its `JSON` is an alias for `LONGTEXT` and the driver
reports it as `TEXT`, indistinguishable from any other long text column, so
there is nothing to key on and guessing from content would reshape far more than
it fixed.

**This changes the shape of messages from `json`/`jsonb`/`JSON` columns on the
CDC path.** A transformation or sink template that treated such a column as a
string — parsing it itself, or passing it through as text — now receives an
object. Templates that reached into it with gjson's `@fromstr` modifier keep
working, since that modifier is a no-op on a value that is already an object.


## [1.4.0] — 2026-09-14

The FCM sink is the headline. It could address a device, a topic or a condition
and set a title and body, all of them only from message metadata; everything
else Firebase offers had no representation at all. It is now a full client, with
every field a Go template over the message. Two connectors arrive beside it —
the `panmail` sink, and `metis` as both source and sink for a BPMN workflow
engine — and twelve sink types stop rendering the database form instead of their
own fields, a wizard fallthrough rather than a missing-field bug.

**If you run an `encrypt` or `decrypt` node, read the first entry before
deploying.** A field list that matches nothing on the message now fails that
message instead of forwarding it untouched. That is the intent — a decrypt node
that silently passes ciphertext to a sink is the failure the node exists to
prevent — but a stream where some messages legitimately carry none of the named
fields needs `onMissingField: "skip"` to keep working. A partial match is
unaffected: one field present and another absent is an optional column, not a
misconfiguration.

### Changed — `encrypt` and `decrypt` report a field list that matches nothing

A security node whose configured fields are all absent from the message used to
run, touch nothing, and report success: encrypt forwarded plaintext, decrypt
forwarded ciphertext, and nothing anywhere said so. Both now fail the message
instead, naming the fields asked for and the fields the message actually
carries.

This is the misconfiguration operators hit when a source starts delivering a
body that is not a JSON object — the field list still names the old column while
the body now arrives under `payload`. The node looked healthy and the sink
quietly received ciphertext.

It sits one level above the existing `onError` and `onPlaintext` policies, which
are value-level: both need the value in hand, and neither can see a field that
was never reached.

A **partial** miss is deliberately unaffected. One field present and another
absent is an optional column, not a misconfiguration, and failing it would break
every heterogeneous stream.

**If a stream legitimately carries messages with none of the named fields**, set
`onMissingField` to `skip` to restore the previous behaviour. The editor exposes
it as *When no field matches*, next to the other failure policies.

### Added — the FCM sink is a real Firebase client

The Firebase Cloud Messaging sink could address a device, a topic or a
condition and set a notification title and body, all of them only from message
metadata. Everything else FCM offers — Android channels and collapse keys,
APNs badges and background pushes, Web Push links, analytics labels, dry runs,
topic subscription — had no representation at all, and the editor's form
offered four fields: the credentials and three destination defaults.

It is now a full client. Every destination and notification field is a Go
template over the message, so a device token or a deep link comes from a column
rather than needing a transformer to copy it into metadata first. A token field
that renders a comma-separated list fans out as a multicast. Android, APNs and
Web Push each have their own block, because "high priority" and "expire this
after ten minutes" mean different things on each. `subscribe` and `unsubscribe`
actions turn a device-registration table into an FCM audience, so a topic is
addressable without the pipeline holding a token list of its own.

The data payload gained a choice: the envelope it has always sent (the
formatted row under `payload`), the row's own columns as top-level keys, or
nothing at all for a notification-only push.

### Fixed — six ways the FCM sink misbehaved

- **Two defaults produced a message FCM refuses.** Configuring both a default
  device token and a default topic set `Token`, `Topic` *and* `Condition` on
  every message. FCM accepts exactly one, so every send failed with "exactly
  one of token, topic or condition must be specified". The sink now refuses
  that configuration when it is saved.
- **A blank `fcm_token` was treated as a destination.** The check was for the
  key's presence, not its value, so a transformer copying a nullable column
  produced a message addressed to the empty string instead of falling back to
  the configured default.
- **`Ping` sent a real message.** The connection test called `Send` with a
  made-up token, which cost send quota and, with a token that happened to be
  live, would have notified a real device. It now uses `SendDryRun`, and it
  distinguishes a refusal about the message — which proves the round trip
  worked — from one about the credentials, which is the failure it exists to
  find.
- **An empty credentials field silently used the machine's Google
  credentials.** On any host with `gcloud` logged in, that meant pushing to
  whatever project that account defaulted to. Using ambient credentials is now
  an explicit opt-in that has to name its project.
- **A payload over FCM's 4096-byte limit burned the whole retry budget.** FCM
  refuses an oversized message and it will not be smaller next time. The sink
  now checks before sending and reports it as permanent, with `truncate` and
  `drop` available for workflows that would rather deliver something.
- **The column a push was addressed by travelled inside the push.** A
  registration token is a capability — whoever holds it can push to that
  device. Under the new `fields` data mode every column became a data key, so a
  message addressed by `{{.device_token}}` carried that token back to the
  device it was addressed to, and a multicast, whose field holds *every*
  recipient's token, handed each device the whole list. The columns the
  destination templates read are now withheld from the payload; naming one
  under `data_json` puts it back for anyone who wants it.

Refusals FCM calls permanent — a dead registration token, the wrong project,
an invalid argument — are now wrapped in `ErrPermanent`, and a dead token
arrives as an `UnregisteredTokenError` naming the token so the registration can
be pruned. Batching is available through `NewBatching` but off by default: FCM
has no idempotency key, so retrying a partly-delivered batch notifies the
devices that already received it a second time.

The editor's form covers every key, and a Go test reads the form and fails if
it writes a key the sink does not read, or if the sink reads one no field
writes.

### Fixed — twelve sink types were rendering the database form

Picking **API / Webhook** in the sink wizard showed host, port, database and
table fields. It was not a missing-field bug: `SinkWizard` resolved a type's
form as `configComponents[type] || configComponents['database']`, and twelve
types had no entry in that map, so they silently fell through to the database
form.

For `http` and `websocket` that made the sink unreachable rather than awkward.
The database form never writes a `url`, both types require one, and the wizard
disables Next *and* Save while a requirement is unmet — so there was no way to
create or edit one from any of the four entry points (Add Sink, Edit Sink, the
editor's node modal, the node drawer). Only the REST API could.

All twelve now have a form matched to the keys the factory actually reads:

- **API / Webhook** (`http`) — URL and headers, plus **compression** and
  **timeout**, which `createSinkBase` has always read and which had no input
  anywhere in the UI.
- **WebSocket** — URL, headers, subprotocols, the three timeouts, acknowledgement
  and the four TLS keys `buildWSTLSConfig` reads.
- **MQTT** — broker URL, topic, client id, credentials, QoS, retain, keepalive,
  clean session.
- **File**, **Stdout**, **Event Store**, and the five social sinks
  (Twitter/X, Facebook, Instagram, LinkedIn, TikTok).
- **MongoDB** and **Cassandra** keep the database form, now listed explicitly so
  it is a decision rather than a fall-through.

`MiscSinkConfig.tsx` held the correct `http` form all along but had been
imported by nothing since `ce5d533`; it is deleted. MQTT, File and Event Store
gained requirement gates, because their factory cases return an error rather
than degrading — saving one without them produced a sink that failed only when
it ran.

A test now fails if any type offered in the picker relies on that fall-through.

### Added — panmail sink

A new `panmail` sink sends each message as an email through a
[panmail](https://github.com/gsoultan/panmail) gateway's API, using
`github.com/gsoultan/panmail-sdk`. It sits beside the SMTP sink, which can reach
the same gateway through its SMTP door; what the API buys is the message id every
send returns — the handle delivery events and webhooks are keyed by — and
refusals that say which refusal they are.

Recipients, subject and both bodies are Go templates over the message, as in the
SMTP sink. A stored gateway template can be used instead.

**On retries and duplicate mail.** Sending is not idempotent and the gateway has
no de-duplication key, so a retry after a timeout may deliver a second copy. The
SDK refuses to make that call for you and never repeats a send whose outcome it
does not know; Hermod's `RetrySink` has no such discrimination and retries every
error alike. The sink resolves this with the idempotency claim:

- a refusal the gateway **stated** (rate limit, full backlog, bad key, bad
  argument) means the message was definitively not accepted, so the claim is
  released and a retry is free to take it;
- an **unknown** outcome keeps the claim, so the retry that follows finds the key
  taken and does nothing instead of mailing the recipient again.

With idempotency off there is nothing to hold the claim; the error says that,
rather than looking like any other failure. Turning it on is worth more for this
sink than for most.

The SMTP sink's idempotency-store wiring moved into
`internal/factory/idempotency.go` and is shared, with the sink name as the table
prefix so two sinks over one database cannot suppress each other's sends.

### Added — metis source and sink, for a BPMN workflow engine

Two new connectors reach a [Metis](https://github.com/gsoultan/metis) BPMN 2.0
workflow engine through `github.com/gsoultan/metis-sdk`, a client that depends on
nothing outside the standard library.

The **`metis` sink** turns each message into one act on the engine, chosen by its
`action` setting:

- `start_process` — start an instance of a deployed definition, so a committed
  database transaction is what begins the business process that answers it;
- `send_message` — correlate a message into whichever instance is already waiting
  on it, selected by a correlation key;
- `broadcast_signal` — reach every instance in the project listening for it.

The message's data map becomes the process variables, with the envelope (`id`,
`operation`, `table`, `schema`) written underneath it so a CDC row's own column
named `table` still wins. `variable_fields` narrows that to a named subset. The
definition key, message name, signal name and correlation key are Go templates
over the message.

**On retries and duplicate process instances.** Starting a process is not
idempotent and the engine has no de-duplication key, so this uses the same
idempotency claim as the panmail sink — but draws the line in a different place,
because the engine's failures are not the gateway's:

- a **stated** refusal (400, 401, 403, 404) means the request was rejected before
  anything was written, so the claim is released and a corrected retry may take
  it;
- an **unknown** outcome keeps the claim. That covers a transport failure *and a
  5xx*: a 500 is an answer that says the engine broke, not that it broke before
  committing the instance. Treating it as a refusal is what would start somebody's
  order-fulfilment process twice.

`TestWrite_ServerErrorKeepsTheClaim` fails when that classification is inverted,
so the distinction is verified rather than asserted.

The **`metis` source** polls a project and emits one message per row of a chosen
stream — `instances`, `tasks` or `incidents` — which is how process history
reaches a warehouse. Its listings are newest-first with no "since" filter, so the
source keeps the watermark itself: the `created_at` of the last row the pipeline
**acknowledged**, plus the ids of any rows sharing that exact instant, so a tie
is neither re-delivered nor dropped.

Only `Ack` moves it. Reading moves a second, in-process position that stops a
running poll re-reading what it just handed out, and that one is deliberately not
persisted — advancing the *persisted* cursor on read is the defect this
repository has fixed in ten other polling sources, and
`TestAck_AdvancesTheCursorAndReadDoesNot` fails when it is reintroduced.

The incidents stream carries a limitation the engine's API imposes: incidents are
listed per instance, not per project, so the source finds failed instances first
and asks each one. That is a request per failed instance per poll, and an
instance that fails after ageing out of `scan_pages` of the instance listing is
never asked.

Both connectors refuse plaintext `http` to a non-loopback host, because the
bearer token travels in a header; both take either a token or a username and
password, and prefer the password for a long-running pipeline, since only that
can log in again when the token expires. `Ping` lists projects rather than
writing, so a health check never starts somebody's process. Both are registered
in the `pkg/comm/conformance` contract suite.

`connectorRequirements` gained an optional `when` predicate on a required field,
because the metis sink's required name field follows its action: demanding a
definition key, a message name *and* a signal name at once would disable Next for
every configuration that is actually valid.

## [1.3.0] — 2026-09-11

The trace tables are the headline: listing traces was a sequential scan and
`message_trace_steps` stored every payload twice. Both are fixed, and
`currentSchemaVersion` moves to 2 as a result — **read the upgrade note below
before deploying**, because an older binary is refused rather than left failing
every trace write. The dashboard gains persisted history and panels for latency,
error rate, backpressure and circuit breakers. `db_lookup` gets a correctness
fix that made "Flatten Result" appear to do nothing, and the transformation
preview stops hiding what it did — including a CDC row that could lose its own
`table` or `id` column.

### Changed — message traces: half the disk, and a list that stops scanning the table

Two separate problems, one table. `message_trace_steps` is the largest thing
Hermod writes — a row per node per message — and it was both storing more than
it needed and being read in the worst possible way.

**Listing traces was a sequential scan.** The list query was
`SELECT DISTINCT message_id, MIN(timestamp) ... GROUP BY message_id`, which no
index can satisfy: every step a workflow had ever produced had to be aggregated
before the first 25 rows could come back. Measured on PostgreSQL 17 at 250k
steps — a Seq Scan, ~390 MB of buffer I/O and 58.5 ms to return 25 rows, growing
linearly with the table. A new `message_traces` table holds one row per traced
message, written alongside the steps, so the same page is an index scan:
**0.071 ms, and flat as the table grows.**

**Half the table was storing the payload twice.** `before_data` held what
entered a node, which is by definition the `after_data` of the node before it;
it is reconstructed on read instead. The `id` was a UUID written on every step
and selected by nothing — the same write-only column `dashboard_history` had.
Measured with realistic incompressible payloads, 250k rows: **262 MB → 123 MB.**

Also in this change:

- **Paging is by cursor.** `GET /api/workflows/{id}/traces` accepts `before`
  (RFC3339), so the next page is an index seek rather than a scan-and-discard.
  `limit`/`offset`/`page` still work; the trace viewer now uses the cursor.
- **One step cannot write an unbounded row.** Payloads over
  `HERMOD_TRACE_MAX_PAYLOAD_BYTES` (default 32 KiB) are replaced by a marker
  that is itself valid JSON, so the viewer still renders it.
- **Retention on PostgreSQL drops partitions instead of deleting rows.** New
  PostgreSQL tables are `PARTITION BY RANGE (timestamp)` with a day per
  partition, a DEFAULT partition so a lagging maintenance run can never fail an
  insert, and a week of lookahead so attaching one never waits on a DEFAULT
  scan. Expired days go by `DROP TABLE`: a catalogue change and an unlink, with
  no row-level WAL and the space returned immediately. Opt out with
  `HERMOD_TRACE_PARTITIONING=off`. Existing tables are left alone — a table
  cannot be altered into a partitioned one, and rebuilding one is an operator's
  decision to make in a window, not something to do unattended at start-up.
- **Tracing is off in `DefaultConfig`.** It defaulted to `TraceSampleRate: 1.0`,
  so any engine that did not apply the per-workflow rate traced every message.
  The registry does apply it, which meant the default only governed the paths
  that forgot — the ones nobody is watching. Off is fixable by configuration; a
  full disk is not.

**`currentSchemaVersion` is now 2.** This is the rollback the version exists to
block: the previous release inserts `id` and `before_data` by name and selects
`before_data`, and both columns are gone after this migration. An older binary
would fail every trace write and every trace read, so it is refused instead.

**Upgrading.** The columns are dropped in place where the engine allows it
(PostgreSQL, MySQL). SQLite cannot drop a primary key, so the `id` survives
there and the insert keeps supplying one — no saving on those databases, but no
breakage either. Two things worth doing deliberately:

- If the table is already huge, truncate or copy-and-swap **before** deploying.
  Nothing here bulk-deletes, but the first retention sweep after the parse fix
  above will.
- Traces recorded before this upgrade have no parent row and so will not appear
  in the list until backfilled. This is not run automatically because on a large
  table it is a long aggregate that would block start-up:

  ```sql
  INSERT INTO message_traces (workflow_id, message_id, started_at, last_step_at, duration_ms, step_count, error_count)
  SELECT workflow_id, message_id, MIN(timestamp), MAX(timestamp), COALESCE(SUM(duration_ms),0), COUNT(*),
         COUNT(*) FILTER (WHERE error IS NOT NULL AND error <> '')
  FROM message_trace_steps GROUP BY workflow_id, message_id
  ON CONFLICT DO NOTHING;
  ```

### Fixed — trace retention never ran, and `message_trace_steps` grew without bound

`purgeRetention` parsed each workflow's `trace_retention` and `audit_retention`
with `time.ParseDuration`, which has no `d` unit. The workflow editor defaults
the field to `7d`. So the parse failed **on the default value**, the call site
only acted when the error was nil, and the sweep was skipped — silently, for
every workflow, forever.

Nothing else bounds that table. `message_trace_steps` stores `before_data` and
`after_data`: the entire message payload, twice, per node, per message. The bug
was found on a deployment whose PostgreSQL grew **50 GB in a couple of hours**.

A day-aware `parseDuration` already existed in the same file, 1,400 lines below
the call site. Both call sites now use it, so `7d`, `30d` and `365d` work as the
field has always been documented. An unparseable value is now **logged as an
error** naming the workflow and the value, instead of being swallowed: there is
no safe fallback — defaulting to a short window would delete traces nobody asked
to lose, and defaulting to none restores exactly this bug — so the sweep keeps
the data and says why.

`message_trace_steps` also gained `idx_trace_ts` on `timestamp`. The purge
filters on that column alone and the only existing index was
`(workflow_id, message_id)`, so a sweep that now actually runs would otherwise
sequentially scan the largest table Hermod owns, hourly, once per workflow.
`audit_logs` has had `idx_audit_ts` for this reason all along.

**Upgrading with a table that is already huge:** truncate or copy-and-swap
*before* deploying this. The first successful sweep issues a single
`DELETE ... WHERE timestamp < ?` against everything past the window, which on a
50 GB table means tens of GB of WAL and a table still holding its dead tuples
until `VACUUM FULL`. `CREATE INDEX` on that table will also block startup until
it completes. Both are instant against an empty table.

**Still open — per-workflow trace retention is not per-workflow.**
`PurgeMessageTraces` executes `DELETE FROM message_trace_steps WHERE timestamp
< ?` with no `workflow_id` predicate, but the caller loops over workflows and
computes the cutoff from each one's own setting. A workflow with `7d` therefore
deletes the traces of a workflow configured for `365d`, and the sweep repeats
the same global delete once per workflow every hour. Fixing it needs a decision
about traces belonging to deleted workflows, so it is reported rather than
quietly changed.

### Fixed — the dashboard stopped updating when nothing was happening

The only thing that ever pushed dashboard statistics was `BroadcastStatus`, and
that is wired to an engine's status-change callback. With no workflow running
there is no engine, so nothing fired: the WebSocket delivered one snapshot when
the page loaded and then went silent. Measured against a running server, that
was one message in fifteen seconds — uptime, worker count and health frozen at
whatever they happened to be when the page opened.

This is the worst possible failure for a monitoring screen, because a dashboard
that has stopped updating looks exactly like a system with nothing wrong. The
registry now samples on a five-second tick regardless of engine activity, which
is the floor rather than the only source: `BroadcastStatus` still pushes on
engine activity so a busy pipeline stays responsive.

Two related gaps closed with it. The socket was opened once with no `onclose`
or `onerror`, so a dropped connection was never re-established and never
surfaced; it now reconnects with jittered backoff and the header says plainly
whether what you are reading is **Live** or **Reconnecting**. And every fetch
ended in `.catch(console.error)`, so an API returning 500 rendered as a tidy
dashboard full of zeros — which reads as "healthy and idle". Failures are now
shown.

### Removed — the dashboard's invented trend badge

The throughput card rendered a green "+5%" whenever throughput was above zero.
It was a literal `5` in the source, with no previous value behind it and no
period it referred to. A fabricated number on the one screen whose entire job
is to be believed costs more than the decoration was worth.

### Added — latency, error rate, backpressure and circuit breakers on the dashboard

The engines already computed all of this per workflow and it had nowhere to go:
`telemetry.StatusUpdate` carried average latency, sink buffer fill and per-sink
circuit-breaker state, and the dashboard read throughput and lag from it and
dropped the rest. It is now aggregated across running engines, each with the
combining rule the quantity actually calls for — throughput sums, latency
averages over engines that report one, and backpressure takes the *worst* sink
rather than the mean, because one jammed sink among nine idle ones is a stalled
pipeline and averaging it to 10% is the reading least likely to get anyone to
look.

Error rate is derived from the persisted counters as the dead-lettered share of
everything attempted. There is deliberately no separate dead-letter count:
`total_errors` already is that number, and showing one value twice under two
names is how a dashboard loses the reader's trust.

The page also now shows the six figures the API had been returning all along
and the UI discarded — lag, failed workflows, uptime, and the running-against-
configured counts for sources and sinks.

### Added — persisted dashboard history

The throughput chart lived entirely in React state, so every reload threw the
trend away and restarted from a flat line, making a page refresh
indistinguishable from an outage. Samples are now written to a new
`dashboard_history` table on the same five-second tick and the chart is seeded
from `GET /api/dashboard/history` on load.

The global series is always kept, because history exists to answer questions
asked after the fact and a series that only accrues while someone has the page
open is missing for exactly the outage nobody was watching. Per-tenant series
accrue while that tenant's dashboard is open. The table is swept on the
existing hourly retention pass with a seven-day window, and both the window and
the row limit on the endpoint are clamped, since both come off the query string.

`currentSchemaVersion` is deliberately unchanged. The previous release never
reads or writes `dashboard_history`, nothing existing changed shape, and no
foreign key points at it, so a rollback leaves the table unpopulated rather
than misread — and bumping the version would have refused start-up during
exactly the rollback it was meant to make safe.

**What it costs, and how to spend less.** This is the only append-only table in
the metadata database — a row every five seconds per watched vhost, whether or
not anyone is looking — so its footprint is a feature of the product, not an
implementation detail. Measured in SQLite over one week of one series it is
**14.08 MB**, down from 21 MB as first written:

- the surrogate UUID primary key is gone. Rows here are never addressed
  individually — every read is a range scan over `(vhost, timestamp)` and every
  delete a range sweep — so the id was written on every tick and selected by
  nothing. It was 9.6 MB of a 21 MB table, 46% of the disk, and on MongoDB a
  36-byte random `_id` has been replaced by the driver's 12-byte ascending
  ObjectId;
- timestamps are truncated to the second. A five-second sample never had
  microsecond precision, and the SQLite driver stores a `time.Time` as text, so
  those digits were bytes in the row and again in the covering index.

Two new environment variables make the rest adjustable, because the right
answer depends on the box: `HERMOD_DASHBOARD_SAMPLE_INTERVAL` (default `5s`)
and `HERMOD_DASHBOARD_HISTORY_RETENTION` (default `168h`). They multiply —
`30s` at `24h` is roughly 1/42 of the default footprint. Setting retention to
`0` turns history off entirely and sweeps what is already there: the dashboard
stays live, the chart just shows what the open page has collected. A malformed
value falls back to the default rather than to zero, so a typo cannot silently
delete the series.

Backends that cannot store history — Pebble, and any worker node, which reaches
the control plane over HTTP and owns no database — now say so with
`hermod.ErrNotSupported` instead of a bare error or a silent `nil`. The sampler
latches that and stops asking. Previously Pebble logged a failure every five
seconds forever, which made the backend that stores no history the one that
wrote the most to disk.
### Fixed — `db_lookup` stopped flattening after the first message with a given key

`flattenInto` ("Flatten Result") was applied only on the path that actually
queried the database. Every lookup is cached, and with no Cache TTL configured
it is cached forever, so the second message carrying a given key took the
cache-hit path — which wrote the target field and returned, skipping flattening
entirely. One configuration therefore produced two different message shapes:
message one had the columns as fields, every message after it did not.

The editor made this look like the feature simply did nothing. The live preview
re-runs on a debounce, so by the time an operator turned Flatten Result on and
pressed **Run Preview**, the lookup was already cached and the flattened fields
never appeared.

Both paths now go through one function, so what is written into the message no
longer depends on whether the row came from the database or the cache.

The cache key also interpolated the key value with `%v` alone, so the string
`"1"` and the number `1` hashed to the same entry and two lookups keyed on the
same id in different types served each other's rows. The key now includes the
type.

### Added — the preview panel states what happened to the target field

A lookup that matches no row passes the message through unchanged and without an
error (`onMiss` defaults to passthrough), so a miss and a working lookup rendered
as identical JSON. For a CDC sample the enriched field also lands nested inside
`after`, while every field picker in the editor shows the hoisted root path — so
an operator looking for `user_details` saw `after`.

The panel now reports the node's target field above the JSON: its value when the
field was produced, and **Not produced** with the reason when it was not. The
path is resolved the same way the rest of the editor resolves it, so the panel
and the field pickers agree.

### Added — `db_lookup` lets you choose what a miss does

`onMiss` has been in the transformer since miss policies were introduced, and
its whole purpose is that a lookup finding no row should be an explicit,
auditable choice rather than a silent passthrough. The editor never offered the
choice, so every workflow ran on whichever policy was inferred from whether
Default Value happened to be filled in.

The Advanced tab now has **When no row matches**: pass the message through, write
the default value, or fail the message. It mirrors the backend's inference, so
an unset policy displays the one actually in force rather than a blank field,
and it warns when "write the default value" is selected with no default value —
a combination that writes nothing and behaves exactly like passthrough.

### Added — `db_lookup` names the paths its output will have

Value Column(s), Target Field and Flatten Result combine into four different
output shapes, and nothing said which one the current settings produce. The
Output Mapping tab now spells out the paths the next node can address — a single
value, an object at the target field, every column of the row, or the flattened
per-column paths — instead of a general tip about objects.
### Fixed — a previewed CDC row lost its own `table`, `id` or `operation` column

`populateMessageFromMap` copied the message envelope's system fields into the
data map "for convenience in transformations". `ToMap` serialises a CDC message
by marshalling that same data map as the after-image, so two things followed.

Every system field appeared twice in a previewed message — once at the root,
once inside `after` — which is most of why the preview panel was hard to read.

Worse, the copy raced the after-image. Both write the same key and Go randomises
map iteration order, so a row with a column called `table`, `id`, `operation`,
`op` or `schema` kept the envelope's value instead of its own in roughly three
previews out of four. The operator then mapped downstream nodes against a value
the row does not have. Measured before the fix: 15 of 20 runs lost the column;
after, 20 of 20 keep it.

The copy was never needed. `evaluator.GetMsgValByPath` already exposes
`operation`, `op`, `table`, `schema` and `id` as virtual fields resolved from the
message itself, and deliberately lets a real data column of the same name
outrank them — leaving these out of the data map is what gives that rule
something to resolve against.

Only the preview is affected: a live CDC source sets the after-image as the
payload (`SetAfter`), so `ToMap` never falls back to the data map for it.
Non-CDC samples are untouched, because there is no after-image to duplicate into
and dropping the copy would change the previewed type of a field like `id`.

### Added — detect decryption settings from a sample value

The decrypt node's settings interact: the key format decides the key bytes, the
encoding decides the payload bytes, the nonce length decides where the
ciphertext starts, the tag position decides which end the tag is on, and the AAD
decides whether authentication can succeed at all. One wrong setting is
indistinguishable from all of them wrong, so matching an external system meant
searching that space by hand with nothing to search by. 1.2.0 made every
combination expressible; it did not make the right one findable.

**Detect settings** in the decrypt editor takes one encrypted value and the key
and reports the configurations that actually read it, each with a truncated
preview and an Apply button. `POST /api/transformations/detect-decryption` is
the same thing for scripting.

Confidence is load-bearing rather than decorative. `certain` means an
authenticated algorithm verified its tag, so the configuration is not a guess —
nothing else could have produced that value. `likely` means an unauthenticated
mode produced plausible-looking text, which on a short value can be coincidence,
and the UI says so at the point of applying it. Ranking puts proven candidates
first, and candidates differing only between the base64 and base64url alphabets
are collapsed, since offering a choice that is not a choice is noise.

Two things are deliberately not searched, and the failure reason says so instead
of leaving them to be discovered. PBKDF2 and scrypt take a salt and cost
parameters that are inputs rather than properties of the ciphertext — guessing a
salt is a dictionary attack, not a search. A fixed IV has nothing in the payload
to recover it from.

The endpoint is editor-only and grants no capability its caller lacked: it needs
the key, so anyone able to call it could already decrypt. It never echoes the key
back, sends `Cache-Control: no-store`, and truncates the preview so it cannot be
used to drain a column.

### Fixed — a source payload that was not a JSON object was silently dropped

Sources are free to deliver a bare string, a number, or bytes that are not JSON
at all: a RabbitMQ queue carrying plain text, a Kafka topic of CSV lines, a file
read in `raw` mode. Only JSON *objects* survived. Anything else reached the sink
as `{"id":"…","metadata":{…}}` — the body gone, no error logged, nothing in the
trace. `MarshalJSON` unmarshalled the payload into the output map and discarded
the error, so a payload with no fields to merge simply left nothing behind.

A payload that is not a JSON object is now preserved under a `payload` field,
holding the decoded JSON value where there is one (`42` stays a number, `[1,2]`
stays an array) and the literal text otherwise. It is addressable by
transformations and by sink templates as `{{.payload}}`.

Three consequences of the same root cause are fixed together:

- **Bodies reaching the sink.** Any sink configured with `format: json` or
  `format: cdc` now emits the body. Sinks with no `format` set were never
  affected — they publish `Payload()` bytes directly and always passed strings
  through untouched.
- **The workflow editor and message traces.** `ToMap` carried the same ignored
  error, so a string payload rendered as an empty body in the test/preview panel
  and in traces. It now agrees with `MarshalJSON`, and a test pins them together.
- **CDC messages whose payload was not JSON.** These failed to marshal at all
  (`invalid character 'h' looking for beginning of value`), failing the sink
  write rather than losing the body quietly. The `before`/`after` envelope now
  wraps such bytes instead of rejecting them.

The S3 Parquet sink needed a matching change. It refused a record it could not
build a row from by testing `Data()` for emptiness, which is exactly the
invariant that moved: a non-object payload now decodes to one synthetic field,
so the check passed the record through to the writer and the batch failed in
`WriteStop` with `interface conversion: interface {} is nil, not string` --
naming neither the record nor the reason. The guard now measures a record
against the schema's own columns, which is what it always meant.

The fix is in the message layer, so every source benefits without connector
changes. JSON object payloads serialise exactly as before. Array payloads were
already exposed under `payload`, which is why that name was widened to cover
strings and scalars rather than a new one introduced: existing `{{.payload}}`
templates keep resolving. Arrays also gain determinism — whether an array
survived used to depend on whether a transformation had read the message first.

## [1.2.0] — 2026-09-10

The `encrypt` and `decrypt` transformations added in 1.1.0 gain an algorithm
picker, and `decrypt` stops silently doing nothing on data Hermod did not write.
`enc:v1:` values written by 1.1.0 keep decrypting unchanged under the default
configuration; a regression test pins that literal wire format.

Nothing in the public Go API changed and no dependency moved, so this carries no
security fix of its own — but the bug it fixes is a security-relevant one: a
decrypt node pointed at data another system encrypted reported success while
forwarding ciphertext to the sink. If you run one, check that it is actually
decrypting rather than passing values through.

### Fixed — `decrypt` silently did nothing on data Hermod had not encrypted

In 1.1.0 the node only touched values carrying its own `enc:v1:` prefix and
passed everything else through. That is right midway through a rollout, when a
column holds a mix of sealed and plain values — and it meant that pointing the
node at a column encrypted by *another* application matched nothing, changed
nothing, and reported success, with no error and nothing in the logs. The
pipeline looked healthy while forwarding ciphertext to the destination, which is
the same class of failure as an unknown transformation type resolving to a no-op.

Setting `format` to `raw` now drops the envelope: every listed field is
decrypted, and a value that will not decrypt is an error rather than a
pass-through. Together with `ivPlacement`, `encoding` and the key formats below,
that is enough to describe what an external system actually wrote — verified in
both directions against `openssl enc -aes-256-cbc`. For operators staying on the
envelope format, the new `onPlaintext` policy (`passthrough`, the default,
`fail`, or `null`) turns the silence into a stopped pipeline once a rollout is
complete.

### Added — an algorithm, key-derivation and encoding picker

- **Fourteen algorithms.** AES-128/192/256 in GCM, CBC, CTR and CFB, plus
  ChaCha20-Poly1305 and XChaCha20-Poly1305. AES-256-GCM stays the default. GCM
  and Poly1305 are authenticated and detect a modified ciphertext; CBC, CTR and
  CFB cannot, and are offered so that data another system already wrote in them
  can be read at all. The editor says which is which at the point of choosing,
  and a Go test fails the build if the two lists ever drift apart.
- **Key derivation is selectable.** `passphrase` (SHA-256 of the text — 1.1.0's
  behaviour, and still the default), `raw`, `hex` and `base64` for key material
  of exactly the algorithm's length, and `pbkdf2` or `scrypt` for a human-chosen
  password. A raw key of the wrong length is an error rather than padded or
  truncated: padding leaves the remaining bytes known to an attacker, and
  truncating means two keys sharing a prefix encrypt identically, so an operator
  rotating between them would see success and get no rotation. PBKDF2 and scrypt
  require a salt and have no default for it.
- **Payload encoding is selectable** — `base64`, `base64url` or `hex` — and
  decoding accepts either base64 alphabet with or without padding, because
  external systems disagree about both and the difference otherwise looks
  exactly like a wrong key.
- **`enc:v2:` names its algorithm.** A value can then be read without the node
  being told how it was written, and a node explicitly configured for a
  different algorithm reports the conflict instead of failing with a generic
  authentication error. A node configured the way 1.1.0 behaved still emits
  `enc:v1:`, so a mixed-version fleet keeps working during a rollout.
- **Optional `aad`** binds additional authenticated data to the ciphertext for
  the GCM and Poly1305 modes. It is rejected for the unauthenticated modes
  rather than accepted and dropped, which would suggest a binding that does not
  exist.

Three limits are worth knowing before leaving the defaults. In `raw` format
nothing marks a value as encrypted, so encrypting into it is not idempotent —
running the same workflow twice encrypts the column twice. The optional fixed IV
exists only to match external systems that use one: it makes identical inputs
produce identical ciphertext, and under GCM or CTR reusing an IV with one key
exposes the XOR of the two plaintexts and, for GCM, the authentication key. And
CBC, CTR and CFB cannot detect tampering at all — a wrong key yields plausible
garbage rather than an error, except where CBC's padding check happens to catch
it.

### Added — decrypted JSON can become an object

A column often holds one JSON document rather than a scalar, and decrypting it
returned a *string* that happened to contain JSON. That is not the same as an
object: no downstream node could address into it with a dotted path, and the live
preview rendered it as a single escaped line instead of a tree.

`decrypt` now takes **`parseJson`** — `off` (the default), `objects`, or
`strict`. `objects` parses a value that starts with `{` or `[` and leaves
everything else as text; `strict` treats the whole value as a JSON document,
scalars included, and routes a parse failure through the existing `onError`
policy. Parsing is opt-in because it changes a field's type, and doing that
silently would reshape every message flowing through an existing node.

The split between the two modes is deliberate. A decrypted `"12345"` is valid
JSON, so a single "parse if you can" mode would quietly turn an account number
into a float — a schema change downstream that nobody asked for. `objects` never
does that; `strict` is how an operator asks for it, and is also what complains
when a column declared to be JSON is not.

`encrypt` gained the inverse, **`serializeJson`**. It previously refused a field
holding an object, because rendering a map with `%v` produces Go syntax that
decrypt would hand back as a literal string. With the opt-in the subtree is
marshalled and sealed as one document, so an object survives a full round trip
and a later node can mask or map `payload.contact.email` directly. Without it the
refusal stands, and the error now names the option that lifts it.


### Added — an explicit AAD mode, and a diagnosis for authentication failures

An authenticated algorithm reports a wrong key, a wrong AAD and a tampered
ciphertext identically. That is correct — GCM cannot distinguish them — and it
means one opaque error covers every setting on the node. Configuring a decrypt
node against a system you do not control turns into guesswork, and the guess
people reach for first is "the key must be wrong", which it usually is not.

`diagnose` on the decrypt node decrypts once **without** checking the tag and
reports which half is wrong: either the key and framing are right and the AAD is
the difference, or the trial produced nothing sensible and the AAD is not worth
looking at. The trial result is never returned or logged — only the
classification — because handing back unauthenticated plaintext is exactly what
the tag exists to prevent. It is off by default, both for that reason and
because reporting whether forged input decrypts to something plausible is a
small oracle to expose on a hot path. The check reads "plausible" as well-formed
text, so a binary plaintext reads as "key wrong" even when the key is right; the
message says so rather than overstating what it knows.

Alongside it, **`aadMode`** makes the AAD an explicit choice — `none` (the
default), `value`, or `key` — instead of inferring it from whether a text box is
empty. An empty box could equally mean "no AAD" or "not filled in yet", and the
two produce ciphertext that cannot be told apart until it fails to open;
selecting `none` now also genuinely drops a value left behind in the field
rather than leaving it quietly in effect. Nodes that set only `aad`, from before
the mode existed, keep working unchanged.

`aadMode: "key"` covers systems that pass the encryption key itself as the AAD.
It buys nothing — the key is already bound to the ciphertext by construction —
but it is what their data requires, and without the preset there is nothing to
discover it from, because the failure is identical to a wrong key.


### Added — tag placement and nonce length for AES-GCM

The last two ways an external AES-GCM value can be framed differently from Go's.
`gcm.Seal` appends the authentication tag, so a Go-written value is
nonce||ciphertext||tag with a 12-byte nonce. Node's crypto, Java's Cipher and
.NET's AesGcm all return the tag *separately*, which leaves whoever wrote the
storage code to choose where it goes — and putting it in front of the ciphertext
is a common choice. 16-byte nonces appear for the same reason: nothing stopped
them.

Neither is recoverable from the bytes. Both layouts are the same length and both
fail authentication in the same way, so `tagPlacement` (`suffix`, the default,
or `prefix`) and `nonceSize` have to be told rather than inferred. Both work for
writing as well as reading, so a pipeline can produce data a partner system
consumes instead of only consuming theirs.

`nonceSize` is GCM-only: the Poly1305 constructions fix their nonce as part of
the construction and the block modes take a full 16-byte IV, so setting it there
is rejected rather than silently ignored. It is also read by presence rather
than by value — an explicit `nonceSize: 0` is a configuration error, and a
sentinel of 0 would have quietly accepted it as "unset". The nonce length is
part of the derived-cipher cache key, because it changes the constructed AEAD
and two nodes sharing a passphrase must not share an entry across it.

Fixtures for both come from Node's crypto module rather than from this package,
so they test interoperability instead of self-consistency.


## [1.1.0] — 2026-09-09

One new capability and a set of connector-wizard fixes. Nothing in the public Go
API changed, and no dependency moved, so this carries no security fix of its own.

### Added — field-level encryption and decryption

Two transformations, `encrypt` and `decrypt`, seal and unseal named fields with
AES-256-GCM. The configured key is hashed to 256 bits, so any length works, and
encrypt and decrypt nodes must be given the same one.

Three decisions are worth stating, because each one closes a failure that the
obvious implementation leaves open:

- **Ciphertext is tagged `enc:v1:`.** Without a marker the two transformations
  cannot tell ciphertext from plaintext. Re-running a workflow would encrypt an
  already-encrypted column a second time, and decrypt could not distinguish
  "never encrypted" from "corrupt". With it, encrypt skips sealed values and
  decrypt passes untagged ones through — which is also what a column holds
  midway through a rollout. The version segment leaves room for a second scheme
  that does not strand data written under this one.
- **Every value gets a fresh random nonce.** Identical plaintexts therefore
  encrypt differently, which is the safe default and the reason an encrypted
  field cannot be used as a join or lookup key downstream.
- **Both fail closed.** A missing key or an empty field list is an error rather
  than a silent pass-through: a step asked to encrypt that quietly forwards
  plaintext is the whole failure. Decryption failures — a wrong key or an
  altered ciphertext, which GCM reports identically — fail the message by
  default; `onError` can relax that to `skip` or `null`.

There is deliberately no `*` wildcard. Mask has one, but masking every field
degrades a message where encrypting every field destroys it, keys and routing
columns included. Only scalar values can be encrypted: rendering a map with `%v`
gives Go syntax, and decrypt would hand that literal string back in place of the
object, so a field naming an object is refused rather than silently mangled.

Two operational limits worth knowing. The nonce is 96 random bits, and NIST
SP 800-38D caps a key used that way at 2^32 encryptions — reachable on a busy
pipeline, and rotation is what resets it. Values are read through the same JSON
path every other node uses, so a `[]byte` field is already its base64 form by
the time it is encrypted, and round-trips as base64.

The key is set on the node, so it is stored with the workflow definition and is
readable by anyone who can read or export that workflow. Rotating it does not
re-encrypt data already written under the old key.

### Fixed — connector wizards could not be completed

The connection step's **Next** button read config keys that nothing produced.
For a `rabbitmq_queue` source the gate required `url` and `queue`, while the
form writes `host`, `port`, `username`, `password`, `dbname` and `queue_name`
and hides the URL input once a host is set. Neither key was reachable, so the
button was dead for every RabbitMQ queue source and sink.

Test Connection succeeded at the same moment, which is what made it baffling:
`BuildConnectionString` assembles the AMQP URL from the host fields and the
factory reads `queue_name` separately, so the step genuinely worked while the
gate that guarded it asked for fields the user could not fill.

Two connectors had drifted the same way. `mqtt` required `broker` where the form
writes `broker_url`, giving the same dead button; it now also requires a topic,
because the source refuses to start without one. The `snowflake` sink required
`dsn` where both the form and the factory use `connection_string`, so its
"Required:" message named a field the form does not show.

Requirements gained an alias list, so "a server" is one requirement satisfied by
a host *or* a whole connection string, mirroring `BuildConnectionString`'s own
precedence instead of keeping a second copy of it that can drift.

Two further gaps in the same area closed with it:

- **A pasted connection string satisfied every requirement, not just the ones it
  replaces.** Any `uri` or `connection_string` cleared the whole step, so a
  MongoDB source with a URI but no database or collection advanced and failed
  later — at a screen that no longer showed the fields, which is the failure the
  gate exists to prevent. A pasted string now satisfies the host-shaped fields
  only; the factory reads database and collection from their own keys and cannot
  derive either.
- **A connection URL could override the host fields while invisible.**
  `BuildConnectionString` prefers `url` over host and port, but the RabbitMQ
  forms rendered that input only while the host was empty. A URL entered first
  kept winning from behind a field that had disappeared, so editing the host
  changed nothing and Test Connection reported on whichever server the hidden
  URL named. It now stays on screen whenever it holds a value, and its label
  says that it overrides.

### Fixed — palette categories could render under the wrong heading

Category titles are unique only within a group: "Databases", "Messaging &
Streams" and "Social Media" each name both a source and a sink group. The
palette's combined tab keyed one list by title, so three pairs collided and
React warned twelve times per render of the workflow panel. Duplicate keys let
React reuse or drop the wrong child, meaning a category's contents could appear
under another category's heading. Keys now pair the group with the title.

## [1.0.0] — 2026-09-07

The first generally available release. It is `1.0.0-rc.2` plus one concurrency
fix; no dependency changed, so it carries no security fix of its own and
`1.0.0-rc.1`'s advisory remains the current one.

### Fixed — a health pass could clear a stall it raced

`checkHealth` publishes `"running"` whenever the source is up and every sink
answers `Ping`, and a wedged sink does answer `Ping`: it accepts the connection
and never completes a write. So the stall watchdog and the health pass both
write the engine status, and the watchdog has to win.

An earlier fix guarded the write with `engStatus != "stalled"`, which closed the
case of a health tick arriving *after* a stall. It could not close the case
inside a single tick: `engStatus` came from an earlier `GetStatus`, and the
write was a separate lock acquisition, so a watchdog setting `"stalled"` between
the two was overwritten by a guard that had already decided the pipeline was
fine. A supervisor was told the workflow had stalled while the status the UI
reads said it was healthy.

The exclusion moved into the write. `StatusTracker.SetEngineStatusUnless`
decides and publishes under one lock and reports whether it wrote.

The window was only as wide as the gap between the two calls, so it never
reproduced on a developer machine and surfaced instead as an intermittent CI
failure. Both halves of the fix now have a test that fails without it: the
tracker races 2000 pairs of writers, and the engine-level test races the
watchdog against `checkHealth` 300 times.

### The version number, and what it does not buy you

`1.0.0` is the release you can run: the container image, the Helm chart, the
binaries and the git tag were all free at this number.

It is **not** installable with `go get`, and no future release can make it so.
`proxy.golang.org` is immutable and permanently maps `v1.0.0` to the February
commit that carried that tag, under `module github.com/user/hermod` — a path
matching no repository. That is unchanged from
[`go get` does not work at this version, by choice](#go-get-does-not-work-at-this-version-by-choice),
and the `retract` block in `go.mod` deliberately still covers `v1.0.0`:
narrowing it would un-retract the February commit without making this one
reachable, and would break the plain `go get` that currently resolves cleanly to
the newest candidate.

Consume this release as an image, a chart or a binary.

### Known gaps

Everything listed under Known gaps in `1.0.0-rc.1` still applies; none of it was
addressed here. The five social connectors that advance their cursor on read —
Twitter/X, LinkedIn, Facebook, Instagram and TikTok — remain the most
significant: treat a restart as potentially lossy for those.

## [1.0.0-rc.2] — 2026-09-07

The second release candidate. Almost all of it is the editor UI: a measured pass
over rendering, navigation and forms, plus the first-run defect that made a
fresh install unable to start a workflow without a restart. No dependency
changed, so nothing here carries a security fix; `1.0.0-rc.1`'s advisory
remains the current one.

### Fixed — a fresh install could not run anything until you restarted it

A first run has no database, so `shouldStartWorker`
(`cmd/hermod/worker_util.go`) was false at process start and `main` built no
worker. Setup then opened the database the admin chose but left the registry
holding `nil`, so every toggle failed with `registry storage is not
initialized`, with nothing on screen to say a restart was needed. Every
`dev.sh --reset` stack and CI's E2E job ran in that state.

Setup now announces the database it opened and `main` answers by starting a
worker — last, after every setup step has succeeded, and behind a `sync.Once`
so a second call cannot put two workers on the same workflows.

Two data races surfaced alongside it, both on state that is now swapped while
the process runs. `Registry.SetStorage` took `r.mu`, but 57 of the 70 reads of
`r.storage`/`r.logStorage` took nothing, on request paths and on the stats and
retention tickers. `r.mu` could not be the fix — `StartWorkflow` holds it and
calls `ValidateWorkflow`, which reads storage, and `sync.RWMutex` is not
reentrant — so the two fields moved to their own `storeMu` behind `store()` and
`logStore()`. `Handler.Worker` gained the same treatment.

### Fixed — the editor did work on every keystroke and every render

- **Preview ran once a second while idle.** `usePreviewTransformation` spread a
  mutation result into a fresh object each render, so the effect debouncing it
  re-armed its timer every render and the 1s debounce behaved as a 1s poll.
  Measured at 4 requests in 5.6s with no user input.
- **Column discovery queried the sink's own database on every keystroke of a
  table name.** The `mappings.length === 0` guard only closes once a discovery
  succeeds, and a partial table name is not a table, so typing `orders` issued
  six live queries.
- **Raw-JSON panes discarded what you were typing.** They were controlled off
  the node config, so half-typed JSON failed to parse, committed nothing, said
  nothing, and the next unrelated re-render replaced the text with the last
  serialised config. A keystroke that did parse came back reformatted, moving
  the caret to the end.
- **Lists blanked between keystrokes.** No query set `placeholderData` and only
  `WorkflowDetailPage` debounced, so each character minted a query key, data
  dropped to `undefined`, and the table emptied and refilled. Logs did this
  while polling every 5s. Now debounced at 300ms with the previous result held,
  across Workflows, Sources, Sinks, Users, Logs and Audit Logs.
- **`FlowCanvas` merged edges with `JSON.parse(JSON.stringify(...))`.** It read
  as a deep-equality guard and did the opposite: a new object per edge per
  change, so React Flow's memoisation never held and every edge re-rendered
  whenever any one did — several times a second under live telemetry. It also
  silently mangled anything JSON cannot carry.
- **`SinkForm` fetched workers with a bare `useEffect`** — no cache, no dedupe,
  no abort, a request per mount, and a `setState` after unmount if the form
  closed mid-flight. `SourceForm` already read the same list through React
  Query, so one resource had two mechanisms.
- **Seven transformation types rendered their configuration twice.** Config
  moved to a registry but the inline blocks it replaced were never deleted, so
  set, advanced, pipeline, lua, wasm, foreach and aggregate showed their
  settings under both Configuration and Advanced, with stale labels the second
  time. 288 inline lines removed.
- **Forms hydrated over your edits.** `useSourceForm` re-parsed `initialData`
  on every keystroke, restoring a sample `updateConfig` had just cleared;
  `UserForm` and `VHostForm` re-hydrated on every `initialData` identity, so a
  refetch after their own save overwrote in-progress edits.
- Routing nodes previewed the `{branch, result}` envelope instead of the
  message, and `WorkflowsPage` crashed on a non-array `/api/workspaces`
  response.

### Changed — navigation, first paint and layout

- **Cold loads no longer flash white.** The app renders dark by default but the
  theme was applied only after hydration, and `index.html` set no background.
  It now inlines Mantine's own `ColorSchemeScript` algorithm, a `color-scheme`
  meta and matching `theme-color`, and `#root` holds a shell placeholder. First
  paint measured at `rgb(26,27,30)`.
- **Navigation stopped paying a fixed cost.** Router `defaultPreload: 'intent'`
  warms a route's lazy chunk on hover instead of fetching chunk then data in
  series; `defaultPendingMinMs` drops 500 → 0 and `defaultPendingMs` rises
  0 → 300, so a warm route that paints in 20ms no longer costs a pinned 500ms
  full-viewport spinner.
- **The manual chunk buckets are gone**, measured rather than assumed. The
  `reactflow` rule matched a package renamed to `@xyflow/react` long ago, so it
  only ever caught dagre and d3 — and fixing the name made it worse, promoting
  the graph library and drag-and-drop kit onto the login screen's critical path
  at 1.45MB. Rolldown's own splitting tracks the eager/lazy boundary properly.
- **One width grammar for every form.** A measured audit found five different
  grammars across six forms. `<Group grow>` was the broken one — a flex row
  that never wraps, squeezing host/port/user/password to ~80px on a narrow
  viewport instead of stacking. 72 of these became `FormRow`, a `SimpleGrid`
  that does collapse and bottom-aligns, replacing a global `min-height: 1.2em`
  that only ever reserved one line.
- The editor toolbar at 390px was a non-wrapping flex row; it wraps now.
- Two theme blocks were silently dead: Mantine `styles` become inline styles,
  so nested selectors in them were not CSS rules — React rejected them and
  logged an error every render. Active nav links rendered at weight 400 instead
  of 600 and table headers had no background in either scheme.

### Added

- **Search in the workflow palette.** It lists well over a hundred sources,
  sinks and transformations across three tabs and a dozen categories, and the
  only way to find one was to scroll. Labels, sub-types and descriptions are
  all searched — so "drop records" finds Filter — ignoring spaces, underscores
  and slashes, because the catalogue is not consistent about them. A tabbed
  palette hides matches by design, so an empty tab reports where they are, and
  says so rather than offering a link when the target tab is locked.
- **The PWA is finished**: an explicit manifest `id` (it was derived from
  `start_url`, so changing that later would have orphaned every install),
  192/512 PNG icons, a maskable icon inside the inner 80% safe zone, and an
  `apple-touch-icon`, which iOS needs because it ignores the manifest for Add
  to Home Screen.
- **Accessible names that describe the right action.** A bulk find-and-replace
  had given icon buttons the wrong ones: copy-to-clipboard announced "Confirm",
  generate-password announced "Refresh", clear-stream announced "Delete", and
  ten delete buttons in a table all announced "Delete" with nothing to say
  which row. 26 corrected.
- **A bundle budget in CI.** `check-bundle-budget.mjs` sums every script and
  stylesheet `index.html` references and fails past 781kB; measured 736kB. One
  eager import of a page-only library puts it back to the 1,092kB it was before
  the buckets came out, with nothing else in CI noticing.
- Editor measurement scripts (`measure-editor.mjs`, `profile-editor-cpu.mjs`,
  `visual-sweep.mjs`) used to settle the memory and worker-offload questions
  rather than argue them. The apparent +17MB peak on the new code was GC sample
  timing: 4.55 vs 4.35MB sampled over 30s, identical composition.

### Developer experience

- `scripts/dev.sh` chooses its ports rather than assuming they are free. It
  prefers 4005 (API), 50051 (gRPC) and 5175 (UI) and steps up when one is
  taken, so a second stack no longer fails with `address already in use`. The
  gRPC port was previously unmanaged, and a failed bind on it is fatal for the
  whole process.

### Known gaps

- Everything listed under Known gaps in `1.0.0-rc.1` still applies; none of it
  was addressed here.
- The 239kB stylesheet costs 36.8kB over the wire with FCP at 44ms on the
  production build. Splitting it was measured and judged not worth the work,
  and is recorded rather than done.

## [1.0.0-rc.1] — 2026-09-03

A release candidate, not a final release. Everything below is tested and the
gates are green, but most of it landed within the last few weeks and none of it
has yet run in a production deployment. `1.0.0` follows once it has.

### Security

- **`golang.org/x/crypto` 0.54.0 → 0.56.0**, for [GO-2026-6354] and
  [GO-2026-6355] — two denial-of-service defects in `golang.org/x/crypto/ssh`
  where a channel can deadlock on an established connection. Both are reachable
  from the SFTP file source, at `pkg/comm/source/file/generic.go:736`, where
  `sshDialContext` calls `ssh.NewClientConn`.

  The connection is outbound, so reaching it means Hermod has been configured to
  poll an SFTP server that is hostile or has been compromised — not an exposure
  anyone can reach unprompted. It is still reachable, which is why this is an
  upgrade rather than an exemption.

  The handshake was already bounded by a deadline, cleared immediately
  afterwards so transfers are not cut short. That is exactly the window these
  advisories describe, so the timeout does not mitigate them.

  `golang.org/x/text` (0.40.0 → 0.41.0) and three indirect `golang.org/x`
  modules moved with it.

[GO-2026-6354]: https://pkg.go.dev/vuln/GO-2026-6354
[GO-2026-6355]: https://pkg.go.dev/vuln/GO-2026-6355

### The releases before this one are gone

Every earlier release has been deleted: 52 GitHub releases, all 55 tags, and
both GHCR packages (`hermod` and `charts/hermod`). Nothing published under a
`1.x` number before 2026-09-03 is available any more, and none of it should be
treated as a supported upgrade path to this release.

**Container images and charts are gone.** `ghcr.io/gsoultan/hermod:1.7.3`,
`:latest`, and the matching `charts/hermod` versions no longer resolve. If you
are running one, keep your local copy until you have moved to `1.0.0-rc.1`; it
cannot be pulled again.

### `go get` does not work at this version, by choice

The version numbering restarts here, and that is possible everywhere except one
place: `proxy.golang.org` is immutable, and it still maps `v1.0.0` to the commit
that carried that tag in February, under the old `module github.com/user/hermod`
which matched no repository.

```
$ curl proxy.golang.org/github.com/gsoultan/hermod/@v/v1.0.0.info
{"Version":"v1.0.0","Time":"2026-02-09T07:38:40Z","Hash":"915f5346..."}
$ curl proxy.golang.org/github.com/gsoultan/hermod/@v/v1.0.0.mod
module github.com/user/hermod
```

Re-tagging does not change what the proxy serves. Every number from `v1.0.0` to
`v1.7.4` is spent for this module path, and `v1.0.0` has to stay retracted — if
it did not, `go get` would resolve to that February commit and fail on the
module path anyway.

The consequence, stated plainly: **Hermod is not currently consumable as a Go
module.** `go get github.com/gsoultan/hermod` and
`go install github.com/gsoultan/hermod/cmd/hermod@…` do not work at `1.0.0`, and
will not until the line passes `v1.7.4`. The container image, Helm chart,
GitHub release and packaged binaries are unaffected and are the supported ways
to run it. Building from a checkout also works.

This was a deliberate trade: version numbers that read correctly everywhere a
user actually installs Hermod, against a `go get` path that had never worked in
any published version anyway — the module path was a placeholder until
2026-09-02, so no release before this one could be imported either.

### Why the withdrawn versions are retracted

Two tombstones exist: `v1.7.4` and `v1.8.0`. A tombstone is a tag holding a
`retract` directive and nothing else — no code, no image, no chart. Both are
listed in `.github/tombstones`, which is how the release workflow knows to
publish no artifacts for them.

They exist because Go reads retractions from the go.mod of the **highest release
version**, which makes retracting anything require publishing something above
it. `v1.7.4` was cut to retract the withdrawn `1.x` line. `v1.8.0` was cut
because that turned out not to be enough:

- `v1.8.0-rc.1`, an intermediate candidate withdrawn for the `x/crypto` SSH
  defects, sorts *above* `v1.7.4`, so no directive in `v1.7.4` could reach it.
  `go get github.com/gsoultan/hermod` selected it and installed the vulnerable
  build without complaint.
- `v1.0.0-rc.1` sorts *below* `v1.0.0`, since a pre-release precedes its
  release, so the `[v1.0.0, v1.7.4]` range never covered it either. It needs a
  directive naming it, and that directive has to live in the highest release —
  not in `v1.0.0-rc.1` itself, which is a pre-release and therefore not where Go
  looks.

The retraction is now `[v1.0.0, v1.8.0]` plus `v1.0.0-rc.1` by name, carried by
the `v1.8.0` tombstone. Between them they cover every version the proxy can
serve for this module path.

`v1.0.0-rc.1` — this release — is itself retracted, which is unusual and
deliberate. The proxy holds that version string against an older commit from an
earlier attempt at this reset, and re-tagging cannot displace it, so a Go user
asking for it would receive code without the SSH denial-of-service fix above.
Failing loudly is the better outcome. The image, chart and binaries are freshly
built from this commit, carry no such history, and are published normally.

### Breaking

- **The module path is now `github.com/gsoultan/hermod`.** It was
  `github.com/user/hermod`, a placeholder that matched no repository, so
  `go get`, `go install` and importing Hermod as a library all failed with a
  path mismatch regardless of which version you asked for. Nothing could import
  Hermod at any version, so there was no importer for the change to break —
  which is why it happens here rather than being carried forward.
- **Go 1.27 is required to build.** `go.mod` declares `go 1.27.0`.
- **`hermod.TwoPhaseCommit.Prepare` takes a transaction ID:**
  `Prepare(ctx context.Context, txID string) (string, error)`. The coordinator
  now names a transaction and records the name *before* a participant is asked
  to hold it, so a crash between those two steps leaves a name that recovery can
  look for. Previously the participant chose the name and returned it, and a
  crash in that window left a prepared transaction pinned in the database with
  nothing on record pointing at it. In-tree this affects only the PostgreSQL
  sink; any out-of-tree implementation needs the new parameter.
- **Pebble is refused as a metadata store.** `--db-type=pebble` now exits with an
  explanation instead of starting. It never satisfied the metadata store's
  requirements, and previously reported itself as configured while failing to be
  one.

### Changed behaviour you will notice in production

These are corrections, but each one changes something an operator can see. None
of them need action; all of them are worth reading before you deploy.

- **A failed dead-letter park no longer counts as a delivery.** When a message
  could not be delivered *and* could not be written to the DLQ, the engine
  acknowledged it anyway and the record was gone. It is now retained. On a
  deployment with a misconfigured or unreachable DLQ this appears as replication
  slot or queue growth that was not there before — that growth is the data the
  previous behaviour was discarding.
- **Resume cursors advance on acknowledgement, not on read**, across thirteen
  database and OData sources (including SQLite, Oracle, MongoDB, Dynamics 365 and
  SAP). A crash between reading a batch and the sinks writing it no longer skips
  those rows on restart. The trade is at-least-once behaviour where the previous
  code was accidentally at-most-once: a restart may now redeliver a batch that
  was already written. Sinks with upsert semantics absorb this; append-only sinks
  may see duplicates.
- **Outbound HTTP requests time out.** Requests that previously used a client
  with no timeout are now bounded, and WebAssembly modules are fetched through
  the client that carries the SSRF guard. A remote host that accepts a connection
  and never answers now fails the request instead of holding a worker forever.
- **Per-message delivery logging moved from `Info` to `Debug`.** Steady-state log
  volume drops sharply. Raise the level if you were counting those lines.

### Hardening

- The HTTP API server has read-header, idle and header-size limits; it had none,
  and was answerable to a slowloris client.
- The gRPC server has a concurrent-stream ceiling, a receive-size limit, an idle
  timeout and a keepalive enforcement policy; it had none.
- Oracle and Snowflake identifiers are quoted the way those dialects fold case
  (upper), rather than the way PostgreSQL does (lower).
- `scripts/security-check.sh` fails when a `-run` pattern matches no tests, so a
  renamed test can no longer turn a security claim into a green tick that checks
  nothing.
- CI runs the browser security specs, the race detector within the runner's
  memory budget, and `govulncheck` in a job with the swap it needs.

### Added

- MQTT source, tested against a real broker and promoted to GA.
- Live-server integration tests for the Oracle sink and source, and the MSSQL
  sink against Azure SQL Edge.
- The UI moved to Tailwind v4; a pasted URL can configure a database connector,
  and transformation nodes explain themselves in the form.

### Known gaps

Stated here rather than discovered later. All three are also in `README.md` or
`SECURITY.md`.

- **Snowflake is the one identifier fix never watched failing against a real
  server.** No warehouse is reachable from CI, so the Snowflake half of the
  case-folding fix is inference from documented behaviour rather than
  observation. See `SECURITY.md`.
- **The MSSQL source lacks coverage, not capability.** It reads `CHANGETABLE`
  and emits updates and deletes, but no live SQL Server runs in CI.
- **Five social connectors still advance their cursor on read** — Twitter/X,
  LinkedIn, Facebook, Instagram and TikTok. Each drives a vendor pagination token
  whose semantics differ per API, and no test here can exercise them, so they
  were left alone rather than changed mechanically. Treat a restart as
  potentially lossy for these. They are Experimental in `README.md`.

[1.1.0]: https://github.com/gsoultan/hermod/releases/tag/v1.1.0
[1.0.0]: https://github.com/gsoultan/hermod/releases/tag/v1.0.0
[1.0.0-rc.2]: https://github.com/gsoultan/hermod/releases/tag/v1.0.0-rc.2
[1.0.0-rc.1]: https://github.com/gsoultan/hermod/releases/tag/v1.0.0-rc.1
