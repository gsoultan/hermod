# execute_sql: returned rows are opt-in

`execute_sql` (`pkg/comm/transformer/advanced/execute_sql.go`) has two paths.

- **No `resultField`** — `ExecContext`, as it always was. Rows a `RETURNING` /
  `OUTPUT` clause hands back are dropped. This is the default on purpose: a node
  whose statement already had a `RETURNING` clause must not start adding a field
  to messages a sink is mapping.
- **`resultField` set** — `QueryContext`, rows written under that field.
  `resultRows` is `first` (default: one object, or null when nothing came back)
  or `all` (a list, empty when nothing came back). The shape follows the setting
  and never the row count — the opposite of `db_lookup`, which returns an object
  for one row and a list for two.

Things that are not obvious from the code:

- **The field is always written**, including null / `[]`. Leaving it alone would
  hand downstream whatever an earlier node or the source row had under that name.
- **The result is read to its end** (`sqlutil.ScanRowsCounted`), keeping at most
  `DefaultMaxRows`. `ScanRows` stops at the cap, which is right for a SELECT; for
  a write it closes a cursor the statement is still feeding and loses the count.
  pgx also reports a refused write (duplicate key) only when the result is read.
- **`affectedRowsField` on the query path is the number of rows returned.**
  `database/sql` gives a row count from `Exec` or rows from `Query`, never both.
  A statement with no result set (zero columns) has no count there, so the field
  is omitted rather than written as 0.
- SQLite reports `RowsAffected() == 0` from `Exec` on an `INSERT ... RETURNING`,
  so on the default path the count for such a statement is wrong on SQLite.

The editor (`SQLConfig.tsx`) offers both settings and shows a hint when a
statement returns rows and no field is named — the report that led here was
"Run Preview shows no data" for exactly that. `statementReturnsRows` only drives
the hint; it never changes what runs.

**Preview writes for real.** `/api/transformations/test` runs the transformer
itself, and Live Preview re-runs 400 ms after every config change, so editing an
`execute_sql` node executes its statement against the database each time.

Tests: `execute_sql_returning_test.go` (SQLite),
`internal/engine/registry/execute_sql_integration_test.go` (stored source →
registry → real PostgreSQL), `ui/src/__tests__/executeSqlReturningRows.test.tsx`.
