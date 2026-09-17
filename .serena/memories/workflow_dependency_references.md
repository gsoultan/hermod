### A workflow names its dependencies in more than one place

**Rule.** Anything that answers "what does this workflow depend on?" must use
`collectWorkflowRefs` (`internal/workflow/transport/http/workflow.go`). Walking
nodes for `type == "source"` / `type == "sink"` and reading `RefID` is the
answer to a *different*, smaller question, and every caller that asked it that
way has been wrong.

**The four reference sites.**

| Where | Key | Used by |
| --- | --- | --- |
| Node, typed `source`/`sink` | `ref_id` | every pipeline |
| Node config, any node type | `sourceId` | `db_lookup` (`pkg/comm/transformer/lookup/db_lookup.go:65`) |
| Node config, any node type | `sourceId` or `sourceID` | enrichment SQL (`pkg/comm/transformer/advanced/execute_sql.go:35`, `SQLConfig.tsx` writes both spellings) |
| Source config, transitively | `source_id` | a `batch_sql` source delegates its connection to another source (`internal/engine/registry/registry.go:801`) |
| Workflow | `dead_letter_sink_id` | DLQ |

`collectWorkflowRefs` scans *any* node's config for the two `sourceId`
spellings rather than matching on node type. Node type is written three
different ways for the same transformation — palette entries use
`type: 'transformation'` with a `subType`, validation reads
`Config["transType"]`, `internal/ai/service.go:449` reads `Config["type"]` —
so keying on it means missing a shape.

**What it cost.** `ExportWorkflow` used the small answer, so a bundle for any
enriched pipeline omitted the lookup source. The import succeeded, the workflow
started, and every message failed with `failed to get source for lookup
(sourceId: ...)`. Covered by `TestExportBundlesSourcesReferencedByNodeConfig`
and `TestExportBundlesBatchSQLUnderlyingSource`, and end to end by
`ui/__tests__/workflow_export_import_e2e.spec.ts`.

**Still carrying the old answer — a known open defect.** The source delete
guard (`internal/source/transport/http/source.go:231`) matches
`node.Type == "source" && node.RefID == id`. A non-CDC source used *only* by a
`db_lookup` node is therefore unguarded: `DELETE /api/sources/{id}` returns 204
and leaves the workflow with a dangling reference. Verified live on 2026-09-15.
The same blind spot, in a handler that has not been fixed yet;
`collectWorkflowRefs` is the fix when someone takes it.

### An export bundle is a description, not a snapshot of a run

`Source.State` is the CDC cursor, and `UpdateSource` writes it wholesale, so the
import's upsert used to replace a live replication position with one from
another database — the watermark-class bug this codebase keeps meeting. Export
strips `Status`, `WorkerID`, `State`, `Sample` from sources and sinks and
`Status`/`WorkerID`/`OwnerID`/`LeaseUntil`/`Total*` from the workflow; import
ignores them if a hand-edited bundle carries them anyway, and preserves the
target's own on update. `Active` is configuration ("should this run"), not
runtime, and is deliberately kept.

**The import is not atomic.** There is no transaction primitive on
`storage.Storage`, so a bundle whose second source fails to save leaves the
first one written. The handler orders dependencies before the workflow so the
failure cannot produce a workflow that references nothing, and reports the
failing ID — but a partial import is still possible and is the known limit here.

### Copying a resource on import: the two constraints that bite

`ui/src/utils/importBundle.ts` holds the client-side twin of
`collectWorkflowRefs`, because the import wizard's "import as a separate copy"
hands out new ids and every reference has to follow. `NODE_CONFIG_SOURCE_KEYS`
there and `nodeConfigSourceKeys` in the Go handler must stay in step.

Two things the schema enforces that the wizard has to know about:

- **`sources.name` and `sinks.name` are `NOT NULL UNIQUE`**
  (`internal/storage/sql/queries.go`). A copy therefore cannot keep the
  original's name, and *any* bundle carrying a name held by a different id fails
  the write — whichever way the id conflict was resolved. `nameCollisions`
  blocks that before the request; `suggestFreeName` picks the replacement. Found
  by an end-to-end run, where it surfaced as
  `constraint failed: UNIQUE constraint failed: sources.name (2067)`.
- **`workflows.name` is not unique**, so the same rule must not be applied to
  workflows — two workflows may legitimately share a name.

A source's subtype is read three different ways across the codebase
(`config.transType` in validation, `config.type` in `internal/ai`, the node type
itself elsewhere). `nodeTransType` accepts all three rather than betting on one;
anything dispatching on subtype should do the same.
