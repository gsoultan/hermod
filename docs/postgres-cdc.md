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
  Hermod says so: upgrade it, or give the CDC source a direct connection string. Each CDC
  workflow still holds its own replication connection and slot.
