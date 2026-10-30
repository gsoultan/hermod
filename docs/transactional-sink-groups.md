# Distributed transactions

Transactional sink groups: committing one batch to several sinks atomically.

## Distributed transactions (transactional sink groups)

A **transactional sink group** writes to several sinks inside one distributed
transaction: either every member applied the batch or none did, and that holds across
a crash. It is built on `pkg/engine/twopc` (the coordinator and its durable log) and
`pkg/comm/sink/txgroup` (the group, which presents to the engine as a single sink).

**Two sinks can participate: PostgreSQL and MySQL.** Each implements
`hermod.TwoPhaseCommit` with real prepared-transaction semantics — `PREPARE
TRANSACTION` on PostgreSQL (and YugabyteDB, which shares the sink), XA transactions on
MySQL (and MariaDB, which shares the sink) — so a group can keep any mix of PostgreSQL
and MySQL destinations consistent with each other. A group containing a sink that
cannot participate is **refused at construction**, not silently degraded.

### Setting one up in the UI

1. **Give Hermod a durable state store**, once: *Settings → Platform → Global State
   Store*. SQLite is enough for a single Hermod server; use Redis or Etcd when several
   workers share the work, so whichever runs the group can read its log. Out of the box
   none is configured, and every group is refused until one is.
2. **Create the member sinks** as ordinary PostgreSQL, YugabyteDB, MySQL or MariaDB
   sinks, and prepare their servers (see *Database setup* below and in the form).
3. **Add a sink of type *Transactional Group (2PC)*** — on the Sinks page, or from the
   workflow editor's palette — and pick at least two of those sinks under *Sinks to write
   to*. The form says whether the group is ready to run, and *Test Connection* asks each
   database whether it really accepts two-phase commit, without touching a running
   group's transaction log.
4. **Wire the group into a workflow** as its sink. Every batch that reaches it is written
   to all members in one transaction.

If a member refuses a batch, nothing is written anywhere, and the engine retries that
batch the way it retries any failed sink write.

### Declaring one

A group is a sink of type `txgroup` whose config names its members. It is a
single node in the DAG, which is what lets the engine drive it through one
writer — every sink otherwise gets its own writer goroutine with its own
batching loop, and independent batches cannot share a transaction boundary.

```json
{
  "type": "txgroup",
  "config": {
    "members": "orders-primary,orders-replica",
    "max_prepared_age": "15m"
  }
}
```

| Field | Meaning |
| :--- | :--- |
| `members` | Comma-separated sink IDs, at least two. Each must implement two-phase commit, or the group is refused at construction. |
| `max_prepared_age` | Optional (default 15m). How long a transaction may sit in doubt before the reaper rolls it back. |

**A durable state store is required.** The coordinator's log has to outlive the
process — a crash with an in-memory log strands prepared transactions with
nothing able to resolve them — so a group refuses to start unless a state store
that survives a restart is configured: SQLite for a single Hermod server, Redis or
Etcd when several workers share the work. The `memory` store is refused.

On start-up the group preflights every member, resolves anything the previous
run left in doubt, and starts its reaper. A member that cannot genuinely
participate stops the workflow starting rather than failing mid-batch.

### Different rows for different databases: sink nodes that commit together

A Transactional Group node sends the **same** rows to every member. When one event
has to put **different** rows in different databases — branch X builds the order row
for PostgreSQL while branch Y builds the invoice row for MySQL — give each database
its own sink node and have the nodes name the same transaction:

```json
{ "id": "orders-pg", "type": "sink", "ref_id": "sink-orders", "config": { "transaction": "order" } }
{ "id": "invoice-my", "type": "sink", "ref_id": "sink-invoices", "config": { "transaction": "order" } }
```

| Node config key | Meaning |
| :--- | :--- |
| `transaction` | The name of the transaction this node commits in. Every reachable sink node of the workflow with the same name is a member. |
| `max_prepared_age` | Optional, on any member (the first one found wins). As for a group. |

When the workflow starts, the registry builds the sink nodes that share a name into
**one** group (`internal/engine/registry/commit_together.go`), driven by one writer, so
the rows one event produced for the members leave the engine in one batch and reach
their databases in one two-phase transaction. The engine records on every routed row
which sink node it came through, and the group hands each row to **that member only**:
PostgreSQL gets the order row, MySQL the invoice row, and both commit or neither does.

What the rules are:

- **At least two connected members per transaction.** A single database already
  commits its own rows alone, so a transaction with one member is refused, by node, when
  the workflow is saved and again when it starts.
- **Members must be PostgreSQL, YugabyteDB, MySQL or MariaDB sinks.** Any other type is
  refused by node at save time.
- **A member that produced no row for an event is left out of that event's
  transaction**, and the event still commits. An event whose branches produced several
  rows for one member puts all of them in the same transaction.
- **A member's identity is its node ID**, which is what the transaction log records,
  so the same sink record can back two nodes of one workflow and a recovery after a
  crash still finds the right member.
- **The log is scoped by workflow and transaction name**, so two workflows can both call
  their transaction `order` without sharing a log.
- **Resumed and replayed messages** — an approval that was granted, a wait that
  elapsed, a replay from an event store — write to each member through its own sink
  and commit alone; they are one row for one node, and a side path must not recover
  the running workflow's transaction log.

Everything above about the durable state store, preflight, recovery, the reaper and the
operational hazard applies to these groups exactly as to a Transactional Group node.

### Why it is opt-in

The engine gives every sink its own writer goroutine with its own batching loop. That
independence is why it sustains ~100k msgs/s, and it is also why sinks cannot otherwise
agree on a transaction boundary. A group replaces that independence with a barrier: its
members commit in lockstep at the pace of the slowest, and a member that cannot prepare
aborts the batch for all of them. Use one where a partial write is genuinely worse than
a slower pipeline; leave everything else on the fast path.

### ⚠️ Operational hazard — read before enabling on PostgreSQL

`PREPARE TRANSACTION` leaves a transaction **in doubt**: it holds its locks, keeps its
snapshot, and **survives a server restart** until somebody resolves it. An unresolved
prepared transaction therefore **blocks VACUUM cluster-wide**, which ends in transaction
ID wraparound if it is left long enough. This is a database-availability problem, not an
untidy row.

Hermod defends against that in three places, and you should understand all three before
turning it on:

| Defence | What it does |
| :--- | :--- |
| **Preflight** | At start-up, refuses to run if `max_prepared_transactions = 0` (the PostgreSQL default, which makes `PREPARE` fail) or if the sink is behind a transaction pooler (where a prepared transaction cannot be resolved on the backend that created it). |
| **Recovery** | On every start, resolves transactions left in doubt by the previous run. **Presumed abort**: a transaction commits only if the coordinator's decision was durable before the crash. |
| **Reaper** | Rolls back anything prepared for longer than `MaxPreparedAge` (default 15 minutes). Run it continuously with `Sink.StartReaper(ctx, interval)` — `Recover` only covers restarts, and the case that hurts is a coordinator that prepares, dies, and never comes back. It only ever rolls back: committing on a timer would apply a batch nobody decided to commit. |

**Required PostgreSQL configuration.** `max_prepared_transactions` must be at least the
number of concurrent transactional groups. It defaults to `0` and **changing it requires
a server restart**:

```
max_prepared_transactions = 16   # postgresql.conf, then restart
```

**Do not put a pooler in front of a group member.** PgBouncer in transaction or statement
mode cannot guarantee that the commit lands on the backend that prepared it. Hermod's
preflight refuses this outright rather than degrading, because the degraded behaviour —
committing at prepare time — would leave the coordinator believing it could still roll
back.

**Checking for stranded transactions.** Prepared transactions are visible in
`pg_prepared_xacts`, and this is worth alerting on:

```sql
SELECT gid, prepared, owner, database
FROM pg_prepared_xacts
ORDER BY prepared;
```

Anything older than a few minutes that Hermod is not actively working on should be
resolved. If Hermod's own log was lost — the state store was wiped, say — it cannot
resolve them and you must do it by hand:

```sql
ROLLBACK PREPARED '<gid>';
```

Roll back rather than commit unless you have positively established the transaction
should have committed. Presumed abort is the safe direction for the same reason the
coordinator uses it.

### MySQL and MariaDB members (XA)

The MySQL sink — which MariaDB shares — takes part through XA transactions: `XA
START`, the writes, `XA END`, `XA PREPARE`, then `XA COMMIT` or `XA ROLLBACK`. The
same three defences apply. A prepared XA transaction holds its row locks and a
metadata lock on every table it touched (so an `ALTER` or `DROP` of that table waits
behind it) until it is resolved, and it survives a server restart.

**Server requirements, checked by the preflight.** A group refuses to start when a
MySQL member fails any of these:

| Requirement | Why |
| :--- | :--- |
| **MySQL 5.7.7+** or **MariaDB 10.5.2+** | Earlier servers roll a prepared XA transaction back when the client disconnects, so a crash after the vote would abort it behind the coordinator's back. |
| `innodb_support_xa = ON` (MySQL 5.7.7–5.7.9 only) | With it off, `XA PREPARE` is not crash-safe. From 5.7.10 it is always on, and 8.0 removed the variable. |
| The account can run `XA RECOVER` | On MySQL 8.0+ that needs the `XA_RECOVER_ADMIN` privilege. Without it an in-doubt transaction is invisible to the account that created it: `GRANT XA_RECOVER_ADMIN ON *.* TO 'hermod'@'%';` |

**Use MySQL 8.0.30 or later if binary logging is on** (it is by default from 8.0).
Earlier versions had known cases where a *server* crash during `XA PREPARE` or `XA
COMMIT` left the binary log and InnoDB disagreeing about the transaction, so a replica
could diverge from its source; 8.0.30 made XA crash-safe with the binary log. The
preflight does not refuse older 8.0 releases, because the hazard is a server crash
at a precise moment rather than a misconfiguration, but it is a reason to upgrade.

**What the sink does differently from PostgreSQL**, and why:

- **The ID is given at Begin.** `XA START` takes the transaction ID and it cannot be
  changed, so the coordinator hands each participant its ID before it opens the
  transaction (`hermod.NamedBegin`), and records it before `XA PREPARE` as for every
  member. The sink refuses to prepare under any other ID.
- **One dedicated connection per transaction.** Everything between `XA START` and
  `XA PREPARE` has to run on the same session, so the sink holds one connection
  for the round. After `XA PREPARE` it closes that connection rather than returning
  it to the pool: on MySQL before 8.0.27, with `xa_detach_on_prepare = OFF`, and on
  MariaDB, a prepared XA transaction stays attached to the session that prepared it,
  and no other connection can commit it until that session ends. Commit and rollback
  then run on a fresh connection, as recovery after a restart must.
- **Table DDL runs outside the transaction.** MySQL refuses a statement that commits
  implicitly — `CREATE TABLE`, `ALTER TABLE`, `TRUNCATE` — while an XA transaction
  is active, so when the sink has to create or alter its table it does that through
  the pool first. MySQL DDL is never transactional, so nothing is lost by it.
- **Hermod's XIDs are marked.** They use format ID `1213026116` ("HMOD") with an
  empty branch qualifier, so `XA RECOVER` output can be told apart from another
  application's on the same server.

**Checking for stranded transactions.** `XA RECOVER` is MySQL's counterpart of
`pg_prepared_xacts`, and worth alerting on:

```sql
XA RECOVER;   -- Hermod's rows have formatID = 1213026116
```

If Hermod's own log was lost, resolve them by hand, naming the format ID — without it
MySQL looks for a different XID and answers `XAER_NOTA`:

```sql
XA ROLLBACK '<data>','',1213026116;
```

As on PostgreSQL, roll back unless you have positively established the transaction
should have committed.

### Verified against a real server

The coordinator's unit tests use fakes, which prove the protocol is implemented
but say nothing about whether PostgreSQL behaves the way it assumes. The
integration tests in `pkg/comm/sink/txgroup` close that gap, asserting against
`pg_prepared_xacts` — the same view an operator would check — rather than
trusting a sink's account of itself:

```bash
HERMOD_INTEGRATION=1 POSTGRES_DSN='postgres://...' go test ./pkg/comm/sink/txgroup/
```

They found two defects that every fake-based test had passed over:

- `PreflightTwoPhaseCommit` read `SHOW max_prepared_transactions`, which returns
  **text**. Scanning it into an int failed on every call — and being a preflight,
  that would have blocked 2PC from ever starting.
- The coordinator never called `Begin`, so a real sink refused to prepare with
  "no active transaction". The fakes accepted `Prepare` unconditionally, so the
  missing phase was invisible.

Both are fixed, and both are now covered by tests that would catch a regression.

The MySQL sink's XA tests do the same against `XA RECOVER`: prepare then commit or
roll back from a fresh connection, a crash after the commit decision and a crash
before it (each recovered by a new coordinator over the same log), the sink creating
its table inside a round, and a server that keeps prepared transactions attached to
their session. A group spanning PostgreSQL and MySQL is tested from stored sink
configuration through the registry, in both failure directions:

```bash
HERMOD_INTEGRATION=1 MYSQL_DSN='user:pass@tcp(host:3306)/db' \
  go test -tags=integration -run XA ./pkg/comm/sink/mysql/
HERMOD_INTEGRATION=1 POSTGRES_DSN='postgres://...' MYSQL_DSN='user:pass@tcp(host:3306)/db' \
  go test -tags=integration -run Mixed ./internal/engine/registry/
```

### Residual risk, stated plainly

**This window is closed.** It used to exist between a participant's `Prepare`
returning and its identifier reaching the log: the participant named its own
transaction, so a crash in between left a prepared transaction the coordinator
could not name — and on PostgreSQL a prepared transaction holds its locks
cluster-wide until somebody finds it by hand in `pg_prepared_xacts`.

`hermod.TwoPhaseCommit.Prepare` now takes the transaction ID as an argument.
The coordinator generates it, makes it durable, and only then asks the
participant to prepare under it. The failure is inverted rather than narrowed:
a crash now leaves at worst a name recorded for a transaction that was never
prepared, and rolling back an identifier that does not exist is a no-op. An
orphan you can name is a cleanup; one you cannot is an outage.

`TestTheTransactionIsNamedBeforeItCanExist` holds the ordering by observing,
from inside a participant, what the store already contained at the moment it
was asked to prepare. Reverting the order makes it fail and prints the
identifier that would have been orphaned.

A participant may still report a different identifier than the one supplied —
the PostgreSQL sink does when it is behind a transaction pooler and degrades to
a local commit — and the coordinator records what it is told, because that is
what recovery must act on. The `pg_prepared_xacts` check remains as a backstop
for transactions this coordinator never created.

The window stays closed for MySQL, which names a transaction at `XA START`
rather than at prepare time. The coordinator chooses every participant's ID
before Begin and passes it to a participant that needs it then
(`hermod.NamedBegin`); it is still made durable before `XA PREPARE`. A
transaction that has been started but not prepared is not in doubt — MySQL rolls
it back when the session ends — so it needs no name in the log until it is about
to be prepared. The MySQL sink never substitutes an identifier: asked to prepare
under one it was not opened with, it refuses. `XA RECOVER` is the backstop there.
