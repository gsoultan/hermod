# Transactional SQL sink

Writing one message to several tables of one database in a single transaction,
where a later statement uses what an earlier one returned.

## What it is for

An order and its lines, an invoice and its rows, an account and its audit entry:
the parent's generated key only exists once the parent is inserted, so the child
has to run after it and read that key back, and if the child fails the parent must
not be left behind.

The **Transactional SQL** sink (type `txsql`) runs an ordered list of statements —
`INSERT`, `UPDATE`, `DELETE`, `MERGE` or `SELECT` — inside one transaction. A
statement can be named, and the statements after it read its first returned row as
`{{name.column}}`. Either every statement commits or none does.

It differs from its neighbours like this:

| | Execute SQL step | Transactional SQL sink | Transactional group (2PC) |
| :--- | :--- | :--- | :--- |
| Databases | one per step | one | several |
| Pass a returned value on | yes (`resultField`) | yes (`{{name.column}}`) | no |
| Atomic | no: each statement commits on its own outside a preview | yes, per batch | yes, across members |

A Transactional SQL sink on PostgreSQL, YugabyteDB, MySQL or MariaDB can also be a
member of a transactional group, or a sink node that commits together with others;
see [Committing together with other databases](#committing-together-with-other-databases).

## Configuring it

Connection settings are the ones the chosen database uses everywhere else in
Hermod: `host`, `port`, `user`, `password`, `dbname`, or `connection_string`; a
file `path` for SQLite.

| Field | Meaning | Default |
| :--- | :--- | :--- |
| `driver` | `postgres`, `yugabyte`, `mysql`, `mariadb`, `mssql` or `sqlite`. | required |
| `statements` | JSON list of statements, run in order (below). At most 50. | required |
| `on_unresolved` | `fail` refuses a message whose `{{variable}}` resolved to nothing; `null` binds NULL instead, as Execute SQL does. | `fail` |
| `isolation` | `read_committed`, `repeatable_read` or `serializable`. | the database's default |
| `statement_timeout` | Upper bound on one statement, such as `30s`. | `30s` |

Each statement:

| Key | Meaning | Default |
| :--- | :--- | :--- |
| `name` | Lower-case letters, digits and `_`. Later statements read its first returned row as `{{name.column}}`. | `step1`, `step2`, ... |
| `sql` | One statement. `{{path}}` tokens are bound as parameters, never spliced into the text. | required |
| `forEach` | A message path to a list. The statement runs once per element. An empty list runs it zero times. At most 10,000 elements. | none |
| `as` | The name of the current element: `{{item}}` for a scalar, `{{item.field}}` for an object. | `item` |
| `expect` | `one`: exactly one row returned (or, for a statement that returns none, changed). `affected`: at least one. `any`: no check. A failed check rolls the message back. | `one` for a statement that returns rows, else `any` |
| `returnId` | MySQL only: bind the driver's last-insert id as `{{name.id}}` after an `INSERT`. | `true` |

A `{{token}}` resolves in this order: the current `forEach` element, then an earlier
statement's result, then the message (with the same paths every other template
accepts: `after.x`, `meta.k`, and so on). A statement name therefore hides a message
field of the same name.

The sink refuses, when it is saved and when its workflow starts: a duplicate name; a
reference to a later statement, to itself, or to a `forEach` statement (which has no
single result); schema changes (`CREATE`, `ALTER`, `DROP`, `TRUNCATE`, ...); and
more than one statement in one entry; and a statement that would end or escape the
sink's transaction (`COMMIT`, `ROLLBACK`, `BEGIN`, `SAVEPOINT`, a change to autocommit,
and on MySQL `CALL` or `LOCK`, which can commit on their own).

### Example: an order and its lines on PostgreSQL

```json
{
  "type": "txsql",
  "config": {
    "driver": "postgres",
    "host": "db", "port": "5432", "user": "app", "password": "{{secret.ORDERS_DB_PASSWORD}}", "dbname": "shop",
    "statements": "[{\"name\":\"order\",\"sql\":\"INSERT INTO orders (external_id, customer_id, total) VALUES ({{id}}, {{customer}}, {{total}}) ON CONFLICT (external_id) DO UPDATE SET total = EXCLUDED.total RETURNING id\"},{\"name\":\"items\",\"forEach\":\"items\",\"as\":\"item\",\"sql\":\"INSERT INTO order_items (order_id, sku, qty) VALUES ({{order.id}}, {{item.sku}}, {{item.qty}}) ON CONFLICT (order_id, sku) DO UPDATE SET qty = EXCLUDED.qty\"},{\"name\":\"audit\",\"sql\":\"UPDATE customers SET last_order_id = {{order.id}} WHERE id = {{customer}}\",\"expect\":\"affected\"}]"
  }
}
```

### Getting the new id back on each database

| Database | Write it as | The sink reads |
| :--- | :--- | :--- |
| PostgreSQL, YugabyteDB, SQLite 3.35+, MariaDB 10.5+ | `INSERT ... RETURNING id` | the returned row |
| SQL Server | `INSERT ... OUTPUT inserted.id VALUES (...)`, or `MERGE ... OUTPUT inserted.id;` | the returned row |
| MySQL | a plain `INSERT` | the last-insert id, as `{{name.id}}` |
| Oracle | `INSERT ... RETURNING id` | the columns, through OUT binds the sink adds |

### Oracle

Oracle's writes have no `RETURNING` result set: they hand columns back through OUT
bind variables named in an `INTO` clause. Write `RETURNING id` as on PostgreSQL and
the sink adds the `INTO` clause and the binds; writing your own `INTO` is refused.
What else is different:

- A statement that gives columns back must change exactly one row (`expect` stays
  `one`), because a scalar OUT bind holds one row. `RETURNING` lists plain or quoted
  column names only.
- Values come back as text. Oracle converts a `NUMBER` or `DATE` to text for the
  bind and back when a later statement binds it, under the session's NLS settings.
- Column names come back lower-cased: `{{order.id}}` reads `ID`.
- `MERGE` hands nothing back, so an upsert that later statements need the id of is a
  `MERGE` followed by `SELECT id FROM orders WHERE external_id = {{id}}`. Guided mode
  writes exactly that pair.
- Isolation is read committed (the default) or serializable. Oracle has no
  repeatable read or read uncommitted, and they are refused.
- `CALL`, `EXEC`, a PL/SQL block (`BEGIN`/`DECLARE`) and DDL are refused: each can
  commit on its own. A trailing `;` is dropped.
- The connection is `host`, `port` (1521), `user`, `password`, and the service name
  in `dbname`, or an `oracle://user:pass@host:1521/SERVICE` connection string.

## Guided mode: no SQL to write

The editor opens a new sink in **Guided** mode. Each step names a table, an action
and which field fills each column; Hermod writes the SQL for the sink's database.
Stored as `mode: "guided"` and a JSON `steps` list (a sink with `statements` and no
`mode` is SQL mode, as every sink saved before Guided mode existed is):

```json
{"driver": "postgres", "mode": "guided", "steps": "[{\"name\":\"order\",\"table\":\"orders\",\"action\":\"upsert\",\"match\":[\"external_id\"],\"columns\":[{\"column\":\"external_id\",\"value\":\"{{id}}\"},{\"column\":\"total\",\"value\":\"{{total}}\"}],\"returning\":[\"id\"]},{\"name\":\"items\",\"table\":\"order_items\",\"action\":\"upsert\",\"match\":[\"order_id\",\"sku\"],\"forEach\":\"items\",\"as\":\"item\",\"columns\":[{\"column\":\"order_id\",\"value\":\"{{order.id}}\"},{\"column\":\"sku\",\"value\":\"{{item.sku}}\"}]}]"}
```

| Step key | Meaning | Default |
| :--- | :--- | :--- |
| `table` | The table, or `schema.table`. | required |
| `action` | `upsert`, `insert`, `update` or `delete`. | `upsert` |
| `match` | The key columns an upsert, update or delete finds its row by. Each must also be under `columns`. | required except for `insert` |
| `columns` | `[{"column", "value"}]`. Each value is one `{{field}}`: a message field, `{{item.x}}`, or an earlier step's `{{name.column}}`. | required except for `delete` |
| `forEach`, `as` | As in SQL mode. | none, `item` |
| `returning` | The columns later steps may read. On MySQL, one column: the generated id. | none |

- **Upsert by default.** Each database's own form is used: `ON CONFLICT ... DO UPDATE`
  (PostgreSQL, YugabyteDB, SQLite), `ON DUPLICATE KEY UPDATE` with
  `LAST_INSERT_ID` (MySQL, MariaDB), `MERGE ... WITH (HOLDLOCK) ... OUTPUT`
  (SQL Server). `DO NOTHING` is never generated, so a redelivered parent still hands
  its id on.
- **Names cannot carry SQL.** A table or column name must be a plain identifier
  (letters, digits, `_`, `$`) and is quoted for the database; a value is always a
  bound parameter.
- An update or delete without a match column is refused, and an update must set at
  least one column besides the ones it matches on. An update must change a row.
- **Show SQL** displays what the steps compile to, and **Convert to SQL mode** turns
  them into SQL statements to edit by hand. Converting is one-way.

## Testing it before it runs

- **The Test button** on the form runs every statement once against the database
  with a sample message, inside a transaction that is always rolled back, and shows
  each statement's SQL, bound values, returned rows (the first 20) and changed rows.
  It is `POST /api/sinks/txsql/test` (Editors, for the sink's vhost). Lock waits are
  capped at 5 seconds, so a test against a busy table fails fast rather than queueing.
- **A workflow dry run** writes to no sink, but a dry run that reaches a
  Transactional SQL sink now runs its statements in the run's preview transaction and
  logs what they would do, or why the message would fail. The log line keeps the SQL,
  counts and errors; the bound values and returned rows are left out of it, because
  logs are stored. Nothing it does is committed, and a live workflow never runs it.
- In both, sequences and identity columns may still advance, so the next real ids
  can skip the ones a test used.

## How a batch runs

A batch is one transaction. The statements run in order for each message, and each
message sees only its own statements' results.

- **A data error** — a constraint violation, a bad value, a missing table, a failed
  `expect`, an unresolved variable — rolls the whole batch back and is reported as
  permanent. The engine then writes each message of the batch on its own, so the
  good ones commit and only the one that cannot be written goes to the dead-letter
  sink, with the statement's name in its error.
- **A transient error** — a deadlock, a serialization failure, a lock timeout, a
  dropped connection — rolls the batch back and the engine retries it on the sink's
  retry schedule.
- **A failed commit** leaves the outcome unknown and is retried like a transient
  error, which is one reason the statements should be idempotent.

## Make the statements idempotent

A message can be delivered twice: after a crash between the commit and the
acknowledgement, or after a commit whose outcome was lost. A plain `INSERT` then
writes a second order. Upsert on a natural key instead, so a redelivery finds the
row it already wrote and hands back the same id:

- PostgreSQL and SQLite:
  `INSERT ... ON CONFLICT (external_id) DO UPDATE SET ... RETURNING id`
- MySQL:
  `INSERT ... ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id), ...` —
  `LAST_INSERT_ID(id)` makes the update hand back the existing row's id.
- SQL Server:
  `MERGE t WITH (HOLDLOCK) USING (SELECT {{id}} AS external_id) AS s ON ... WHEN MATCHED THEN UPDATE ... WHEN NOT MATCHED THEN INSERT ... OUTPUT inserted.id;`

Do not use `ON CONFLICT DO NOTHING RETURNING id`: on a conflict it returns no row,
so the child would get no parent id. The default `expect` of `one` refuses that
message instead of writing children with a NULL parent.

## Committing together with other databases

On PostgreSQL, YugabyteDB, MySQL and MariaDB the sink implements two-phase commit,
so it can be a member of a [transactional sink group](transactional-sink-groups.md),
or a sink node that names a transaction it commits in together with other sink
nodes. A batch's statements then run inside the group's transaction, and nothing is
committed until every member has prepared.

- On PostgreSQL and YugabyteDB, prepare is `PREPARE TRANSACTION`. It needs
  `max_prepared_transactions` above 0 and a direct connection, not a transaction
  pooler; the group refuses to start otherwise.
- On MySQL and MariaDB the transaction is an XA transaction on one dedicated
  connection. The isolation level is set before `XA START`, which takes none. MySQL
  5.7.7+ and MariaDB 10.5.2+ are required, and the account needs `XA_RECOVER_ADMIN`
  on MySQL 8.0+.
- On SQL Server, SQLite and Oracle the sink cannot hold a transaction in doubt. A
  sink node on one of them that names a transaction is refused when the workflow is
  saved, naming the node and the databases that can; as a member of a Transaction
  Group node it is refused when the group starts.

Read the operational hazard notes in
[transactional-sink-groups.md](transactional-sink-groups.md) first: a prepared
transaction holds its locks until it is resolved.

## Limits

- One database per sink. To keep tables in two databases consistent, put the sinks
  in one transaction; see above.
- A batch holds its locks until it commits. Keep the statement list short and the
  `statement_timeout` modest.
- Sequences and identity columns advance even when a batch rolls back, so ids can
  have gaps.
