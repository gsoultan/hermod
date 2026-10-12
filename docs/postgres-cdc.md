# PostgreSQL CDC

Replication slots and publications.

## PostgreSQL CDC: Replication Slots & Publications

When using a PostgreSQL source in CDC mode, Hermod relies on a logical **replication slot** and a **publication**. The Source form now helps you reuse existing objects instead of guessing names:

- **Discovery**: Opening the **Slot Name** or **Publication** dropdown queries the connected database (`pg_replication_slots`, `pg_publication`, `pg_publication_tables`) and lists what already exists. Publications that already cover your configured tables are highlighted.
- **Choose or Create**: Pick an existing slot/publication from the list, or type a new name — Hermod will automatically create it on startup if it does not exist.
- **Defaults**: If both fields are left blank, Hermod falls back to the safe defaults `hermod_slot` and `hermod_pub`.

This is backed by the API endpoint `POST /api/sources/discover/replication`, which accepts `{ "type": "postgres", "config": { ... } }` and returns the available `slots` and `publications`.

> **Requirement**: The database must have `wal_level = logical` and the connecting user must have replication privileges for slot creation to succeed.
