**Fixed on branch `fix/cdc-typed-column-decode`.** Kept because the shape of the
bug explains the shape of the fix, and because the constraint at the bottom --
what REPLICA IDENTITY buys you -- is permanent.

A `jsonb` column used to have two shapes, depending on how the row entered the
pipeline.

**Snapshot / polling / `Sample` — a nested object.** These go through pgx's
`rows.Values()`, which dispatches to the registered codec
(`pgx/rows.go:308-313`). `JSONBCodec.DecodeValue` (`pgtype/jsonb.go:100-129`)
`json.Unmarshal`s into `any`, so the value arrives as `map[string]any` and the
`[]byte -> string(b)` branch at `postgres.go:1406`, `:2608`, `:2781`, `:2858` is
never reached for jsonb. `SanitizeValue` passes maps through untouched.

**Live CDC — was a string holding JSON.** `START_REPLICATION` asks for
`proto_version '1'` with no `binary` option, so pgoutput sends every column as
text, and four hand-written tuple decoders did `case 't': string(col.Data)`.
All four now go through `decodeTuple` (`pkg/comm/source/postgres/tuple_decode.go`),
which keys on the column OID that `RelationMessage` was already carrying.

**Only json/jsonb is decoded past text, deliberately.** Handing every column to
pgx's type map would turn `id` from the string "7" into the number 7 in every
message of every existing workflow. The narrow change fixes the column that was
actively wrong and leaves the rest alone.

**Why it bites.** The workflow editor builds its "available fields" list from the
source's stored `Sample` (`useNodeContext.ts:56-64`) and
`getAllFieldsWithTypes` (`transformationUtils.ts:18-33`) recurses into objects
only. So the editor offers `meta.addr.city` while the running CDC pipeline has
`meta` as one opaque string and that path resolves to nil. Same failure shape as
[[lookup_cache_fast_path]]: two paths, one tested, silently disagreeing.

**The second, worse bug on the same lines.** `'u'` — unchanged TOASTed value,
`pglogrepl/message.go:362` — was not a case in any of those switches, which
covered only `'n'`, `'t'`, `'b'`. An UPDATE that did not touch a TOASTed jsonb
column dropped it from the after-image entirely: measured live, the after-image
came back `[email id name]` with a 24 KB TOAST relation behind `meta`.

Probed with a raw pglogrepl stream: on that UPDATE the **new** tuple carries
`meta:'u'(len 0)` but the **old** tuple carries `meta:'t'(len 16012)` under
REPLICA IDENTITY FULL. So the after-image is repairable from the before-image,
and `decodeTuple` takes the old tuple as a fallback. Under REPLICA IDENTITY
DEFAULT no before-image is sent at all, nothing holds the bytes, and the column
stays out of the image with its name in the `unchanged_toast_columns` metadata
key. **Tell operators to set REPLICA IDENTITY FULL on tables with large jsonb or
text columns** — that is what makes updates complete.

**The other sources.** MySQL had the same shape bug on both its binlog and its
query paths and is fixed the same way, keyed on `schema.TYPE_JSON` and on
`DatabaseTypeName()`. **MariaDB cannot be fixed at this layer**: its `JSON` is an
alias for `LONGTEXT` and the driver reports it as `TEXT`, byte-identical to a real
long text column — verified against MariaDB 11.4. Deciding from content instead
would reshape every LONGTEXT that happens to parse.
`pkg/comm/source/mariadb/json_column_integration_test.go` pins that so nobody
adds a content sniffer. Yugabyte reads through pgx and was never affected.
SQLite and SQL Server (before 2025) have no JSON type either.

The shared rule is `pkg/infra/sqlutil/jsoncolumn.go`: `IsJSONColumnType` for the
type-name half, `DecodeJSONColumn`/`DecodeValue` for the bytes half. `DecodeValue`
accepts **both `[]byte` and `string`** — go-mysql has already converted a
MYSQL_TYPE_JSON value to a string by the time OnRow sees it, which every
hand-written `[]byte` unit test missed and only the live server caught.

**Escape hatch for anyone still holding a JSON string.** gjson's `@fromstr`
modifier works through `GetValByPath` (`pkg/infra/evaluator/evaluator.go:403-419`):
`source.meta.@fromstr.addr.city` resolves on a string *and* on an object, so it
is safe either side of this change. Nothing rescues the TOAST case — those bytes
never arrive.

## Testing it

`pkg/comm/source/postgres/tuple_decode_test.go` runs in the **default** suite --
synthetic tuples, no server -- and is where a regression should fail first.
`jsonb_shape_integration_test.go` (tag `integration`) proves the same three
things against a real replication stream. It needs `wal_level=logical`, and the
dev database on 5432 is usually busy, so stand up a second one rather than
fighting for the port:

```bash
HERMOD_DEV_PG_CONTAINER=hermod-jsonb-pg HERMOD_DEV_PG_PORT=5443 ./scripts/create-postgres.sh
HERMOD_INTEGRATION=1 \
POSTGRES_DSN='postgres://postgres:postgres@localhost:5443/hermod_test_source?sslmode=disable' \
  go test -tags=integration -run TestJSONB ./pkg/comm/source/postgres/
```

`create-postgres.sh` already takes `HERMOD_DEV_PG_CONTAINER` and
`HERMOD_DEV_PG_PORT`; nothing needed changing to get an isolated instance.

The TOAST test asserts `pg_relation_size(reltoastrelid) > 0` before concluding
anything from the column's absence. Without that it passes for the wrong reason
as soon as the document becomes compressible enough to stay inline.

For MySQL, `docker.io/arm64v8/mysql:8` with
`--binlog-format=ROW --binlog-row-image=FULL --server-id=1 --log-bin=mysql-bin`.
Use the **container IP**, not the forwarded host port: Apple's `container` drops
the `-p` forward once the server starts writing, and the failure surfaces as
`invalid connection` from a port that still accepts TCP.
