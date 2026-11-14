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
more than one statement in one entry.

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

Oracle is not supported yet.

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

## Limits

- One database per sink. To keep tables in two databases consistent, use a
  [transactional sink group](transactional-sink-groups.md).
- A batch holds its locks until it commits. Keep the statement list short and the
  `statement_timeout` modest.
- Sequences and identity columns advance even when a batch rolls back, so ids can
  have gaps.
- There is no form in the editor yet; create the sink through the API.
