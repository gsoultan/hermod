# Initial load

Carrying the rows already in a table across before streaming starts.

## Which sources have it

Initial load is available for the **PostgreSQL**, **MySQL**, **MongoDB** and **Oracle
(LogMiner)** sources, and is off by default. Set `initial_load: "true"` on one of those
CDC sources and the rows already in the watched tables are carried across before
streaming begins. The rows arrive as `snapshot` messages, ahead of the first change.

The remaining CDC sources have no initial load. For those, a backfill and a stream have
to be sequenced by hand, position first, and the duplicates that ordering produces are
what sink-side idempotency is for; see [Delivery guarantee](reliability.md#delivery-guarantee).

## Where the backfill ends and the stream begins

**PostgreSQL is consistent rather than approximate.** The replication slot is created
over the replication protocol so that it exports a snapshot, the backfill reads at
exactly that snapshot, and streaming starts from the slot's consistent point — so
nothing committed before the boundary is missed and nothing is read twice at it.

**Oracle is consistent too.** The LogMiner source reads each table with a flashback
query as of the SCN it starts from, and mines changes from the next SCN on. See
[Oracle CDC](oracle-cdc.md).

**MySQL and MongoDB are gapless but not snapshot-isolated.** Neither offers Postgres's
exported-snapshot handshake, so the boundary is pinned by position instead: the binlog
coordinates (MySQL) or the cluster time (MongoDB) are taken *before* the tables are
read, and streaming resumes from there. Nothing committed during the backfill is lost,
but a row changed while it runs arrives twice — once as it was, once as it became.
That is the ordinary at-least-once bargain and sink-side idempotency collapses it; see
[Delivery guarantee](reliability.md#delivery-guarantee).

## It runs once

Each source keeps its own record of having run, so the backfill is once-only and
turning the flag on for a workflow that is already streaming does nothing.

- **PostgreSQL**: the record is the replication slot itself. If it exists, changes have
  already streamed from it and the rows are downstream, so enabling the flag takes
  effect only once the slot is dropped.
- **MySQL and MongoDB** leave nothing server-side that could serve the same purpose, and
  their stream positions cannot stand in either: a backfill moves no binlog position and
  produces no resume token, so a table carried across and then never written to would
  be indistinguishable from one that had never run. They record completion in the source
  state the engine persists on every ack (`initial_load: done`), alongside the position.
- **Oracle**: the load runs only when the source has no saved position. Once it has one,
  a restart resumes mining from it and does not read the tables again.

## Why it is off by default

Enabling it for every existing workflow would re-read every source table the first time
a position was reset, which is the opposite of what an upgrade should do.
