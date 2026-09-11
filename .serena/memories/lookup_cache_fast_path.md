# The lookup cache is a second write path, and it drifted

`db_lookup` writes its result into the message from two places: after a real
query, and on a cache hit. They were written separately, so they drifted —
`flattenInto` was applied only on the query path.

Why that is worse than it sounds: `SetLookupCache` treats `ttl <= 0` as "no
expiry" (`internal/engine/registry/registry.go:1043`), and the Cache TTL field in
the editor is empty by default. So the *first* message with a given key is the
only one that ever takes the query path. Every message after it got the target
field and none of the flattened fields — one configuration, two message shapes,
no error anywhere.

The editor's live preview re-runs on a 1s debounce, which made this look like
"Flatten Result does nothing": by the time an operator toggled it and pressed Run
Preview, the lookup was already cached.

Fixed by routing both paths through `applyLookupResult`
(`pkg/comm/transformer/lookup/db_lookup.go`). Tests:
`pkg/comm/transformer/lookup/db_lookup_cache_test.go`.

**Generalise this.** Any cache-hit fast path in this repo is a second
implementation of whatever the slow path does after the fetch. Test it by
running the same operation twice against a registry whose cache actually
remembers — `fullFakeRegistry` in `db_lookup_test.go` returns `false` from
`GetLookupCache` and `SetLookupCache` does nothing, so it can never catch this
class of bug. `cachingFakeRegistry` exists for that.

The cache key had a second defect: it interpolated the key value with `%v`
alone, so `"1"` and `1` collided. It now includes `%T`.

Related: [`message_payload_decoding.md`](message_payload_decoding.md) — the other
place where two representations of the same message disagreed.
