# PostgreSQL CDC

Replication slots and publications.

## PostgreSQL CDC: Replication Slots & Publications

When using a PostgreSQL source in CDC mode, Hermod relies on a logical **replication slot** and a **publication**. The Source form now helps you reuse existing objects instead of guessing names:

- **Discovery**: Opening the **Slot Name** or **Publication** dropdown queries the connected database (`pg_replication_slots`, `pg_publication`, `pg_publication_tables`) and lists what already exists. Publications that already cover your configured tables are highlighted.
- **Choose or Create**: Pick an existing slot/publication from the list, or type a new name — Hermod will automatically create it on startup if it does not exist.
- **Defaults**: If both fields are left blank, Hermod falls back to the safe defaults `hermod_slot` and `hermod_pub`.

This is backed by the API endpoint `POST /api/sources/discover/replication`, which accepts `{ "type": "postgres", "config": { ... } }` and returns the available `slots` and `publications`.

> **Requirement**: The database must have `wal_level = logical` and the connecting user must have replication privileges for slot creation to succeed.

## Many workflows on one source, and PgBouncer

Every workflow that uses the same PostgreSQL source shares one connection pool per Hermod
worker. It is capped at 50 connections unless the source form's **Max connections** field
(or `pool_max_conns` in a connection URI) says otherwise. Size it to what the database or
PgBouncer's `default_pool_size` can serve, not to the number of workflows: a workflow that
finds the pool busy waits for a connection.

- **Polling** reads a poll's rows in full and gives the connection back before handing
  them to the workflow, so a slow sink never holds a database connection. The first poll
  waits a random part of the poll interval, at most 10 seconds, so workflows started
  together do not all query the source at the same instant.
- **PgBouncer is found automatically.** When the pool opens, Hermod asks the server which
  port it listens on. A server on a different port from the one dialled means a proxy in
  between (PgBouncer, Pgpool-II or a forwarded port), and the pool then runs without
  server-side prepared statements, which transaction pooling breaks. The **PgBouncer**
  choice on the form defaults to **Detect automatically**; choosing **Used** or **Not
  used** (`pgbouncer=true` or `false`) turns detection off.
- **CDC through PgBouncer** works with PgBouncer 1.23 and later, which passes a
  replication connection to the server unpooled. An older PgBouncer refuses it, and
  Hermod says so: upgrade it, or give the CDC source a direct connection string.

### Diagnosing a source database

A PostgreSQL source's page has a **Diagnosis** tab (`POST /api/sources/{id}/diagnosis`, Editor
and up, audit `SOURCE_DIAGNOSIS`). It only reads, in a read-only transaction with a 5 second
statement timeout, and shows:

- how Hermod reaches the database: directly, or through a pooler, found by itself when the
  server's `port` differs from the port Hermod dials;
- Hermod's own queries on the database right now, by `application_name` (`hermod`,
  `hermod-sql-steps`), with what each waits on and for how long;
- checks for locks (who blocks whom), queries running over 5 minutes, sessions idle in a
  transaction over a minute, wait events, and how close the server, role and database are to
  their connection limits;
- PgBouncer's own pool figures (`SHOW POOLS`, `SHOW DATABASES`) when the source goes through
  PgBouncer and its user is in `stats_users` or `admin_users`. When PgBouncer refuses, the check
  says so with the SQLSTATE and nothing else depends on it.

Query text is never shown: only its first SQL keywords, because query text can hold personal
data. Without `pg_read_all_stats` (or `pg_monitor`) PostgreSQL hides other users' sessions; the
tab then says which grant would show them.

### Many CDC workflows on one slot

A replication slot admits one reader, PostgreSQL allows ten slots and ten walsenders by
default, and every slot decodes the whole WAL again. So CDC workflows on one database that
name the **same slot** (the default, `hermod_slot`, does) share one replication stream in
each Hermod worker:

- One replication connection reads the slot and hands each workflow the changes on its own
  tables. The publication covers every table any of those workflows follows; a workflow
  joining with new tables widens it.
- The slot is confirmed past a change only once every workflow it went to has acknowledged
  it.
- **A workflow that stops does not lose changes.** While it is stopped, the slot is held
  where it stopped. When it starts again the stream replays from there, and each workflow
  skips the transactions it already acknowledged, so only the returning workflow gets what
  it missed. The server keeps WAL while the slot is held, so the hold ends after 24 hours: a
  workflow stopped longer than that, or deleted, resumes at the current position and misses
  what happened meanwhile (the live log says so).
- After a **worker restart** every workflow resumes from the slot's confirmed position, the
  earliest any of them needed, so a workflow that was ahead may get some changes twice.
- Workflows on one slot must use the same publication. An initial load runs only for the
  workflow that creates the slot.

A workflow that needs a position of its own names its own slot; it then gets a replication
connection of its own, as before.
