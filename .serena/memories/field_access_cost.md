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

## The write side

`SetValByPath` was the same shape and worse — marshal, `sjson.SetBytes`,
unmarshal, clear the map, refill it: 44us / 33 KB / 694 allocs for one field of
a 128-column row, now **731ns / 32 B / 2 allocs**.

Its fast path is **narrower** than the read side's, and the reason is the thing
to remember: the round trip also JSON-normalises every *untouched* value in the
map. A targeted write cannot reproduce that, so it is taken only when the map is
already all-JSON-native — exactly when that side effect would have been a no-op.
A message hydrated from a payload qualifies; one built with `SetData(k, anInt)`
does not. `TestSetValByPathStillNormalisesNonNativeRows` guards the fallback.

**sjson and `json.Marshal` do not agree on `[]byte`**: sjson writes the literal
string, `json.Marshal` writes base64. Found by the parity matrix, not by
reading. So the write fast path uses an allowlist of value types it has been
proved equivalent for rather than mirroring sjson's type switch.

Allocations are flat in row width; wall time is still O(row), because checking
that the map is native is itself a scan.

**`SetValByPath` has no production caller.** Its only caller is a test-only
wrapper (`setValByPath`/`getValByPath` in
`internal/engine/registry/registry_routing.go`), used solely by
`registry_test.go`. It is exported from `pkg/`, so it was made fast and kept
exactly equivalent — but nothing in a running pipeline pays either cost, and
the dead wrapper is worth a decision. Check reachability *before* pricing a
function: the 44us looked like a hot-path risk and was not one.

**`BenchmarkSetValByPath` was self-warming** and averaged two code paths: its
first iteration took the round trip, which normalises the row, handing every
later iteration to the fast path. If a benchmark's subject mutates its own
input into a cheaper shape, iteration 1 and iteration N measure different
things.
