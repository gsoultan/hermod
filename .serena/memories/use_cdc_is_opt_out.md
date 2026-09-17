# `use_cdc` is opt-out, and everything that queries a source must read it that way

`internal/factory/factory.go` decides what a source actually *is*. The flag is
**opt-out**, so a source with **no `use_cdc` key at all** is a CDC source:

```go
useCDC := hermod.SourceUsesCDC(cfg.Config)   // config["use_cdc"] != "false"
```

The UI agrees in three places (`SourceForm.tsx`, `DatabaseSourceConfig.tsx`,
`SourceConfigFields.tsx`: `config?.use_cdc !== 'false'`), and
`useSourceForm.ts` seeds new sources with `use_cdc: 'true'`. Any code that reads
the flag as `if v, ok := cfg["use_cdc"]; ok` — treating an absent key as "not
CDC" — silently disagrees with the factory that built the source.

## One definition, three readers

Since 2026-09-16 the rule lives in the root package (`stringmap.go`):

- `hermod.SourceUsesCDC(config)` — the opt-out reading.
- `hermod.SourceAllowsDirectQueries(type, config)` — may this source be the
  target of SQL that is not its change stream? SQL Server is the documented
  exception (its CDC is change tables read with ordinary queries).

UI mirror: `ui/src/lib/sourceCdc.ts`.

## Who must not query a CDC source

| Node | Reference | Guard |
| :--- | :--- | :--- |
| `db_lookup` | `sourceId` | `requireNonCDCSource`, `pkg/comm/transformer/lookup/db_lookup.go` |
| `batch_sql` source | `source_id` (delegate) | `Registry.requireNonCDCDelegate`, called from **both** `createSource` and `createSourceInternal` |

`batch_sql` holds no connection of its own — `GetOrOpenDB` resolves the delegate
(`registry.go:801`) — and runs whole queries on a cron. Beyond the load, a
delegate that is *also* a CDC source node delivers every row twice.

`execute_sql` (`pkg/comm/transformer/advanced/execute_sql.go`) deliberately has
no such guard: it `ExecContext`s writes, so aiming it at a CDC database is a
separate question.

## What the two fixes were

1. `db_lookup`'s check was `if v, ok := src.Config["use_cdc"]; ok`, so a source
   with no key sailed through while the engine ran it as a replication client.
2. It sat inside the `else` arm of the batching branch, so Batch Lookups skipped
   it entirely, cached the row, and served every later message from that cache.
   A check inside one arm of a fast-path/slow-path split is not a check — see
   [The lookup cache is a second write path](lookup_cache_fast_path.md).
3. `batch_sql` had no check at all.

The lookup refusal deliberately bypasses `applyMissPolicy`: a misconfigured
source is not a lookup that found no row, so `onMiss: passthrough` must not
swallow it. `requireNonCDCDelegate` fails **closed** on an unresolvable delegate
(golangci's `nilerr` flagged the first draft, which returned `nil`) — a
transient storage error during a restart must not build the source the check
exists to refuse.

Both pickers disable CDC entries rather than filtering them out; a vanished
entry reads as a missing source.

## Consequences for fixtures

A `storage.Source` literal with no `Config` is a CDC source. Any fixture using
one as a lookup or batch target needs
`Config: hermod.StringMap{"use_cdc": "false"}`. Updated: `db_lookup_test.go`,
`db_lookup_cache_test.go`, `internal/engine/registry/db_lookup_integration_test.go`.

Tests: `pkg/comm/transformer/lookup/db_lookup_cdc_guard_test.go`,
`internal/engine/registry/batch_sql_cdc_delegate_test.go`,
`ui/src/__tests__/dbLookupNonCdcSource.test.tsx`,
`ui/src/__tests__/batchSqlNonCdcSource.test.tsx`.

## Closed since

- **Validation** — `ValidateWorkflow` is ctx-aware and flags a `db_lookup` or
  `batch_sql` node aimed at a CDC source, as a *warning* (an error would make
  saving the fix a 400). Split into `nodeConfigIssues`/`edgeIssues`/
  `orphanIssues` because `gocognit` put it at 43.
- **Source save** — `UpdateSource` refuses to switch CDC *on* for a source an
  active workflow queries. `checkActiveWorkflows` only walks
  `node.Type == "source"`, so a lookup-only source had no guard at all; see
  [Workflow dependency references](workflow_dependency_references.md).
  `storage.WorkflowQueriesSource` + `storage.NodeConfigSourceKeys` are now the
  single answer to "what does this workflow name", used by both
  `collectWorkflowRefs` and the new guard.
- **Stale lookup cache** — `Registry.UpdateSource` drops the lookup rows cached
  from that source. `hermod.LookupCacheKeyPrefix` is the prefix both sides
  match on; the trailing `:` is load-bearing, or invalidating `cust` also drops
  `customers`. A failed write keeps the cache.

## execute_sql is deliberately exempt

It is **write-only** — `ExecContext`, nothing back but a row count — so its
hazard is not query load but a *feedback loop*: a write into a published table
produces a change event that re-enters the pipeline. That is scoped to the
table while `use_cdc` is scoped to the source, so refusing the source would
break the ordinary case of writing an audit row nobody streams. `SQLConfig.tsx`
warns and leaves it selectable; a test asserts it stays selectable.

Note the editor calls this node "SQL Enrichment" and says it will "enrich your
message by executing a query". It cannot — `ExecContext` returns no rows. That
copy is wrong and is not yet fixed.
