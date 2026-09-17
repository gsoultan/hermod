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

## The key itself was the worse bug

`%T` fixed a collision. What the key *omitted* was total collapse: it was built
from the **unresolved** `queryTemplate` and `whereClause` text. In query mode the
per-message input lives entirely inside the `{{ }}` token, and such a node has no
`keyField`, so `keyVal` is `nil` as well — every message in the workflow produced
a byte-identical key, and with no TTL the first row was served for the life of
the engine. Reported as "every RabbitMQ message gets the same `db_lookup`
result", and visible directly in `message_trace_steps`: the enriched block was
identical across messages while the input field differed.

Keys now append a digest of the values the template actually binds, via
`sqlutil.TemplateArgs`, which is the same walk that builds the statement — so the
key cannot describe a different query from the one that runs.

**The tell that it is a cache bug and not a query bug:** the SQL query builder and
the node preview disagree on the same variable. `DiscoveryService.ExecuteSQL` is
singleflight-only and never cached, so the builder is always right while the
pipeline is always stale. See
[`editor_sample_capture_path.md`](editor_sample_capture_path.md).

## The same class in two more places

- **`api_lookup`** keyed on the resolved URL and body — but headers, the auth
  credential and `responsePath` were applied *after* the key was built. A
  per-message `{{.userToken}}` returned whatever the first token had fetched.
  Its cache key is also its singleflight key, so a concurrent pair shared one
  HTTP request and the disclosure did not need a warm cache. Credentials go into
  the digest only, never into the key in the clear — the cache is an in-memory
  map whose keys are walked during eviction.
- **`getOrCreateBatcher`** is the same bug in closure form: built once per node
  id and returned forever, freezing the first message's `data` *and* the source.
  A templated `whereClause` filtered every later batch by message one's values,
  and repointing a node at another database kept querying the old one, defeating
  `invalidateLookupCacheForSource`. A templated `whereClause` is no longer
  batched at all — a per-message WHERE cannot be coalesced into one query — and
  batchers are keyed by a fingerprint of everything the closure captures. Drop
  the superseded batcher rather than `Close()` it: `Close` makes a concurrent
  `Execute` return `context.Canceled`, turning a reconfiguration into failed
  messages.

**When auditing any cache key here, list every input applied *below* the line
where the key is built.** That ordering is the whole bug, three times over.

## TTL

Both lookups now go through `resolveLookupTTL` (`pkg/comm/transformer/lookup/ttl.go`):

- a value with no unit is an error naming the field, not a discarded parse error
  leaving `0` behind — `5` and `300` are what people type into a box labelled
  Cache TTL, and both used to mean *forever*;
- an explicit `0` disables the cache, which was previously inexpressible;
- unset differs on purpose: 5m for `api_lookup`, 1h for `db_lookup`. A remote
  HTTP response is volatile and the call is somebody else's cost; a lookup table
  is slow-moving reference data queried against the operator's own database.
  Neither is "forever" any more — `db_lookup` was, and once the key was fixed
  that meant serving the *right* row indefinitely after the table had changed.
  The load is bounded from both sides by `MaxLookupCacheSize` (10000): more
  distinct keys than that and eviction is already re-querying; fewer and the
  ceiling is 10000 re-queries an hour. `87600h` restores the old behaviour.

Tests: `db_lookup_cache_key_test.go`, `db_lookup_batching_test.go`,
`api_lookup_cache_key_test.go`, `api_lookup_behavior_test.go`, and
`db_lookup_query_mode_integration_test.go` (build tag `integration`), which
reproduces the reported config against real PostgreSQL.

Related: [`message_payload_decoding.md`](message_payload_decoding.md) — the other
place where two representations of the same message disagreed.
[`sqlutil_owns_dialect_differences.md`](sqlutil_owns_dialect_differences.md) —
where `TemplateArgs` lives and why.
