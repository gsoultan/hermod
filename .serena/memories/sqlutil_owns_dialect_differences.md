### sqlutil owns SQL dialect differences — do not hand-write them in a connector

**Decision:** row-limiting syntax, bind placeholders and identifier quoting live in
`pkg/infra/sqlutil`. A connector that needs any of them calls the helper.

**Why this exists.** Seven source connectors had each hand-written the same
"fetch the next row after a watermark" query, and they disagreed. Oracle's was:

```sql
SELECT * FROM t WHERE id > ? AND ROWNUM <= 1 ORDER BY id ASC
```

Oracle assigns `ROWNUM` as rows are produced by the `WHERE` clause, **before**
`ORDER BY` runs, so this returns an *arbitrary* qualifying row rather than the
next one. The poller then advanced its cursor to that row, permanently skipping
every row with a smaller id — silent, non-deterministic data loss that no test
would notice.

**The helpers.**

- `BuildIncrementalQuery(sourceType, table, idField)` — next row after a
  watermark, bound as parameter 1. Applies the row limit *after* the sort in
  every dialect: `LIMIT` (Postgres/MySQL/SQLite/ClickHouse/Yugabyte/Snowflake),
  `FETCH FIRST` (DB2), `TOP` (SQL Server), and an **ordered subquery** wrapped in
  `WHERE ROWNUM <= 1` (Oracle).
- `BuildFirstRowQuery(sourceType, table)` — any single row, no watermark, no
  ordering. Ordering is deliberately omitted: there is no cursor to advance, so
  a sort would be wasted work.
- Unknown dialects **error** rather than guessing. Emitting plausible SQL for the
  wrong engine is the failure this exists to prevent.

`TestBuildIncrementalQuery_RowLimitNeverPrecedesOrdering` pins the shape for
every dialect, so the Oracle mistake cannot return.

**Where the pattern does not apply.** Cassandra and ScyllaDB cannot use it: CQL
permits `ORDER BY` only on clustering columns within a restricted partition, so
`WHERE id > ? LIMIT 1 ALLOW FILTERING` returns an arbitrary row and the watermark
can skip. That is a limitation of the language, not a bug to fix in the query —
both sources carry the constraint in a comment, warn once at runtime, and are
listed Experimental in README.md.

**Also fixed by routing through the helper:** Oracle was hand-rolling `?` as its
bind placeholder while `sqlutil.Placeholder` — documented as the single source of
truth — returns native `:1`.
