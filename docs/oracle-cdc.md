# Oracle CDC

The Oracle source captures changes in one of two ways, chosen by **Change capture method** on the
source form (config key `cdc_method`):

| Method | `cdc_method` | Sees | Needs |
| :--- | :--- | :--- | :--- |
| Incremental polling (default) | `incremental` or empty | inserts only | an increasing column (`id_field`) |
| LogMiner | `logminer` | inserts, updates and deletes, with before and after images | archive logging, supplemental logging, LogMiner grants |

Incremental polling reads each table for rows above the last `id_field` value seen. A row changed
after it was read is never re-emitted, and a deleted row is never retracted. Use LogMiner when
updates and deletes matter.

## LogMiner

### Database setup

Run once as a DBA. On a multitenant (CDB) database, the database-level statements run in `CDB$ROOT`.

```sql
-- Archive logging, so redo the reader has not reached yet is kept (the database must be MOUNTed).
SHUTDOWN IMMEDIATE
STARTUP MOUNT
ALTER DATABASE ARCHIVELOG;
ALTER DATABASE OPEN;

-- Minimal supplemental logging for the database.
ALTER DATABASE ADD SUPPLEMENTAL LOG DATA;

-- Full row images for every captured table.
ALTER TABLE APP.ORDERS ADD SUPPLEMENTAL LOG DATA (ALL) COLUMNS;
```

Without `(ALL) COLUMNS` an update's redo holds only the columns it changed, so the before and after
images would be incomplete. Test Connection checks both settings, and names the statement that fixes
whichever is missing.

### Grants

```sql
GRANT CREATE SESSION, LOGMINING TO hermod;
GRANT EXECUTE ON DBMS_LOGMNR TO hermod;
GRANT SELECT ANY TRANSACTION TO hermod;
GRANT SELECT ON V_$DATABASE TO hermod;
GRANT SELECT ON V_$LOGMNR_CONTENTS TO hermod;
GRANT SELECT ON V_$LOG TO hermod;
GRANT SELECT ON V_$LOGFILE TO hermod;
GRANT SELECT ON V_$ARCHIVED_LOG TO hermod;
-- and SELECT on each captured table, for its column list and the initial load
GRANT SELECT ON APP.ORDERS TO hermod;
```

### Where to connect

- **To the PDB itself** (Oracle 21c and later): leave **Pluggable database** empty. LogMiner finds
  the redo logs on its own.
- **To `CDB$ROOT`** (Oracle 19c multitenant): connect as a common user (`C##HERMOD`) with the grants
  above plus `SET CONTAINER`, and set **Pluggable database** (`pdb_name`) to the PDB to capture.
  Hermod registers the online and archived log files for each window itself.
- **To a non-CDB database**: leave **Pluggable database** empty. Hermod registers the log files
  itself.

### Settings

| Key | Meaning |
| :--- | :--- |
| `tables` | Comma-separated `OWNER.TABLE` names. A bare `TABLE` belongs to the connecting user. Names are folded to upper case, as Oracle folds unquoted names. |
| `pdb_name` | The PDB to capture when connected to `CDB$ROOT`. |
| `initial_load` | `true` emits each table's rows as of the start SCN (a flashback query) as `snapshot` messages before the first change. |
| `poll_interval` | How long to wait between reads of the redo when there is nothing new. Default `5s`. |

### What a message carries

`operation` is `create`, `update` or `delete`. `before` holds the row before an update or delete,
and `after` holds the row after an insert or update. The after-image of an update is the whole row:
the before-image with the changed columns replaced. `schema` is the owner and `table` is the table
name.

Numeric columns are JSON numbers. `DATE` and `TIMESTAMP` values are ISO 8601 text
(`2026-10-11T07:30:18`, with `.ffffff` for timestamps). Everything else is the text Oracle's
`DBMS_LOGMNR.MINE_VALUE` returns.

Metadata: `oracle_scn` (the change), `oracle_commit_scn` and `oracle_xid` (its transaction).

### Delivery and restarts

Changes are emitted when their transaction commits, in commit order. A rolled-back transaction emits
nothing, and a statement undone by `ROLLBACK TO SAVEPOINT` is dropped.

The saved position is the SCN of the oldest change still open when the last fully acknowledged
transaction committed, together with that transaction's commit SCN. A restart mines from there and
skips transactions already delivered, so a long transaction that was open when Hermod stopped is
still delivered whole. Delivery is at least once: a transaction that was partly acknowledged when
Hermod stopped is delivered again in full.

If the saved SCN is no longer in the online or archived logs (they were deleted before Hermod caught
up), the source stops with an error saying so, rather than skipping the gap. Reset the source's state
to start again from now, with an initial load if rows must not be missed.

### Limits

- **Large objects are not captured.** `CLOB`, `BLOB`, `NCLOB`, `LONG`, `XMLTYPE`, `JSON` and `BFILE`
  columns are left out of the row images, because `MINE_VALUE` cannot read them.
- **Column names** must be plain identifiers (letters, digits, `_`, `$`, `#`). A column with another
  name is left out.
- **DDL**: when a captured table's definition changes, Hermod reads its columns again after the
  mining window that contains the change. Changes in that same window are decoded with the old column
  list.
- **Throughput**: each column is read through `MINE_VALUE`. That is simple and exact, but slower than
  parsing the redo SQL. It suits tables changing at hundreds of rows a second, not tens of thousands.

## Testing

`pkg/comm/source/oracle/logminer_integration_test.go` and
`internal/factory/oracle_logminer_reachability_test.go` run against a real Oracle:

```bash
docker run -d --name oracle -p 1521:1521 -e ORACLE_PASSWORD=oracle \
  -e APP_USER=hermod -e APP_USER_PASSWORD=hermod gvenzl/oracle-free:23-slim-faststart
# then enable ARCHIVELOG and supplemental logging and grant the privileges above

HERMOD_INTEGRATION=1 \
ORACLE_DSN='oracle://hermod:hermod@127.0.0.1:1521/FREEPDB1' \
ORACLE_ROOT_DSN='oracle://system:oracle@127.0.0.1:1521/FREE' \
  go test -tags=integration -run LogMiner ./pkg/comm/source/oracle/
```

They are not run in CI: Oracle Free needs about 2 GB and a slow first boot.
