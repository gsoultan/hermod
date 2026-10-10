# Structure, parsing and reference-data nodes

Eight nodes reshape a record, parse text inside it, enrich it from a file or do
geometry on it. Six are under **Structure & Parsing** in the editor's palette;
**Reference Lookup** and **Geo** are under **Advanced Transformations**.

Explode is a node of its own (`type: "explode"`), because it turns one record
into several. The others are transformations (`type: "transformation"`,
`config.transType` as below) and accept the usual **On Error** (`fail`,
`continue`, `drop`) and **Status Field** settings.

| Node | `transType` / type | Does |
|---|---|---|
| Flatten | `flatten` | `{"a":{"b":1}}` → `{"a_b":1}` |
| Unflatten | `unflatten` | `{"a_b":1}` → `{"a":{"b":1}}` |
| Explode | node type `explode` | one record per array element |
| Parse Field | `parse_field` | JSON, CSV, XML or key=value text → structure |
| Template | `template_render` | Go text/template over the record → one field |
| Field Diff (CDC) | `field_diff` | only the columns a change event changed |
| Reference Lookup | `reference_lookup` | columns from a CSV/TSV/Excel file, by key |
| Geo | `geo` | haversine distance, or point in a GeoJSON polygon |

## Flatten and Unflatten

| Setting | Default | Meaning |
|---|---|---|
| `separator` | `_` | Joins (or splits) the keys |
| `maxDepth` | 64 (also the ceiling) | Flatten: how many levels to join; deeper objects stay whole. Unflatten: how many times a key is split |
| `arrays` | `index` | `index`: `tags_0`, `tags_1` (and rebuilt as arrays by Unflatten); `keep`: arrays stay arrays (and `0`, `1` keys stay an object) |
| `field` | whole record | Only this object field; the result goes to `targetField`, default the field itself |

Two paths that flatten to one key (`a_b` beside `a.b`), or a key that is both a
value and an object when unflattening (`a` and `a_b`), fail the record rather
than one silently overwriting the other. `_` also splits snake_case names:
unflatten with the separator the record was flattened with, or pick one such as
`__` or `.` for both.

## Explode

| Setting | Default | Meaning |
|---|---|---|
| `arrayPath` | required | The array, e.g. `lines` or `order.items` |
| `mode` | `field` | `field`: the element goes to `targetField`; `merge`: an object element's fields are written onto the record, winning over fields of the same name |
| `targetField` | the array's path | Where the element goes in `field` mode |
| `indexField` | none | Receives the element's position, from 0 |
| `maxItems` | 10000 | More elements fail the record instead of emitting part of it |
| `keepEmpty` | off | An empty array passes the record on; off, it emits nothing |

Every output keeps the record's other fields and loses the array, and carries
the `_fanout_group`, `_fanout_index` and `_fanout_total` metadata a **Collect**
node regroups by. Compared with **Foreach (Fan-out)**: foreach adds `_item` and
`_index` beside the array; explode puts the element where the record's shape
wants it. In merge mode every element must be an object, checked before
anything is emitted.

## Parse Field

| Setting | Default | Meaning |
|---|---|---|
| `field` | required | Text (a string or bytes) to parse |
| `format` | `json` | `json`, `csv`, `xml` or `kv` |
| `targetField` | the field | Where the result goes |
| `maxBytes` | 1 MiB, at most 16 MiB | Longer text fails the record |

- **csv**: `delimiter` (`,`; `tab` for a tab), `headers` (a list or
  comma-separated names) or `hasHeader` (the first line names the columns).
  The result is always an array — of objects with headers, of lists of values
  without — so one line gives a one-element array. Follow with Explode to get a
  record per row.
- **xml**: elements become objects keyed by name, attributes `@name`, text
  beside child elements `#text`, repeated elements arrays; the document element
  is the one top-level key. A document type declaration (`<!DOCTYPE …>`) is
  refused, and with it every entity definition, so no entity expansion
  (internal or external) can happen; nesting is limited to 64 levels.
- **kv**: `pairDelimiter` (empty means any run of spaces) and `kvSeparator`
  (`=`). Double-quoted values may contain the delimiter. Values stay text.

## Template

`template` is Go [text/template](https://pkg.go.dev/text/template) over the
record's fields: `{{.name}}`, `{{.customer.email}}`,
`{{range .lines}}{{.sku}} {{end}}`, `{{printf "%.2f" .total}}`. The result goes
to `targetField` (default `rendered`).

- **strict**: a field the record does not have fails the record. Off, it
  renders as empty text.
- **maxBytes**: the longest output, default 64 KiB, at most 1 MiB.
- The template sees the record's data and nothing else: only text/template's
  builtins (`printf`, `len`, `index`, `eq`…), no environment, no secrets, no
  files, and `call` is disabled.

## Field Diff (CDC)

Compares a change event's before- and after-image and writes the columns that
differ to `targetField` (default `changes`):

```json
{"changes": {"status": {"old": "open", "new": "closed"}}}
```

The images are read the way Hermod carries a CDC event: the before-image from
the message envelope, the after-image from its data. A record holding both as
`before` and `after` fields (a Debezium-style body posted to a webhook) is read
from those. An insert has no before-image, so every column is new; a delete has
no after-image, so every column goes to null. Values are compared as JSON, so
`2` and `2.0`, or one object in another key order, are not changes.

| Setting | Meaning |
|---|---|
| `ignoreColumns` | Never reported, e.g. `updated_at, version` |
| `onlyChanges` | The record's data becomes the changes alone; operation, table and before-image are kept |
| `dropUnchanged` | Drop a record with no changes, e.g. an update that touched only ignored columns |

## Reference Lookup

Adds columns from a reference file to each record, matched on a key.

| Setting | Default | Meaning |
|---|---|---|
| `filePath` | required | The file on the worker |
| `format` | from the extension | `csv`, `tsv` or `xlsx` |
| `sheet` | first sheet | For a workbook |
| `delimiter` | `,` (tab for `.tsv`) | For CSV |
| `keyColumn` | required | The file's column to match, as its first row names it |
| `keyField` | required | The record's field holding the key |
| `columns` | every column but the key | Which columns to copy |
| `targetField` | onto the record | Put the columns under this field as one object |
| `onMiss` / `defaultValue` | `passthrough` | As for DB Lookup: `passthrough`, `fail`, or `default` (writes `defaultValue` to `targetField`) |
| `maxBytes` | 32 MiB, at most 256 MiB | Larger files are refused |
| `maxRows` | 200 000, at most 2 000 000 | Longer files are refused |

**Upload** in the editor sends the file to `POST /api/files/upload` (Editor or
Admin; `.csv`, `.tsv` and `.xlsx` are among the accepted types, 10 MiB) and
fills in the path the server stored it at. With `file_storage.type: local` that
is a path on the server, which the worker reads when they share a disk; a file
in S3 storage (an `s3://` path) is not read — give a path the worker can open.

The file is read once and held in memory, indexed by the key column, shared by
every node that reads it with the same settings. It is checked for changes at
most once a second and read again when its modification time or size changes.
Values are copied as text. A key on several rows matches the first. A workbook
whose contents would inflate past ten times `maxBytes` is refused before it is
opened.

## Geo

**Distance** (`operation: "distance"`, the default): `lat1Field`, `lon1Field`,
`lat2Field`, `lon2Field` name decimal-degree fields; `unit` is `km` (default),
`mi` or `m`; the great-circle (haversine, mean Earth radius 6371.0088 km)
distance goes to `targetField` (default `distance`). Expect up to about 0.5%
against an ellipsoidal distance.

**Inside polygon** (`operation: "within"`): `latField`, `lonField`, and
`polygon`, a GeoJSON `Polygon` or `MultiPolygon` or a `Feature` holding one,
with coordinates as `[lon, lat]`. Holes are honoured. `true` or `false` goes to
`targetField` (default `inside`). A point exactly on an edge may fall either
way, and edges crossing the antimeridian are not handled. Polygons are limited
to 100 000 vertices.

A missing, non-numeric or out-of-range coordinate fails the record; it is not
read as 0, which is a real place. No geocoding: addresses are not turned into
coordinates.
