# Reading one field used to cost a whole row

`evaluator.GetValByPath` — the function under every transformation, router
condition, sink mapping and `{{.template}}` token — marshalled the **entire**
data map to JSON and parsed it back with gjson to extract one field.

So a field read was O(row), and a sink mapping with N placeholders was
O(N x row). Measured on Apple M5 Pro, `pkg/infra/evaluator/path_bench_test.go`:

| operation | 8-col row | 32-col | 128-col |
|---|---|---|---|
| one field read, before | 1.0us / 24 allocs | 3.9us / 78 | **17.9us / 294** |
| one field read, after | 29ns / 1 | 33ns / 1 | **32ns / 1** |
| 6-placeholder template, before | 6.7us / 143 | 25.0us / 467 | **106us / 1763** |
| 6-placeholder template, after | 490ns / 9 | 577ns / 9 | **576ns / 9** |
| 2-condition router filter (32 col) | 8.2us / 158 | | **250ns / 5** after |

558x on the widest read, and flat in row width where it used to be linear.

## Why it was not simply deletable

The round trip is also what **normalises types**: `int` -> `float64`,
`[]byte` -> base64 string, and the same recursively inside nested containers.
Every transformation, condition and mapping downstream is written against that
shape, so a naive `row[path]` fast path silently changes types.
See [`message_payload_decoding.md`](message_payload_decoding.md) for the
read-normalises / write-preserves split this belongs to.

The fix therefore walks the map and then normalises **only the leaf** —
a type switch for scalars, `json.Marshal` of just that subtree for containers.
`TestGetValByPathMatchesJSONRoundTrip` keeps a verbatim copy of the old
implementation as an oracle and diffs the two over a matrix of rows x paths.

## Where it bails to gjson (deliberately)

- the path contains any of `*?#|@\[]()!<>=~` — gjson metacharacters
- a segment lands on a typed container (`map[string]string`, `[]string`, a
  struct) that gjson can descend into through its marshalled form but a map
  walk cannot
- the leaf is NaN or +/-Inf: `json.Marshal` of the row fails, so *every* path in
  that row resolves to nil, and the fast path must reproduce that rather than
  quietly start working

## Still outstanding

`SetValByPath` (same file) is the write-side twin and was **not** changed:
44us / 694 allocs for one write into a 128-column row. It marshals the map,
`sjson.SetBytes`, unmarshals, deletes every key and copies back — so it also
re-normalises every *untouched* field in the row as a side effect. That side
effect may be load-bearing somewhere, which is why it needs its own parity
oracle before being touched.
