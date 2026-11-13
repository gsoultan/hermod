# Renaming a vhost

Everything a vhost holds is keyed by the vhost's **name**, never its `id`:
`workflows`, `sources`, `sinks`, `users.vhosts` (a JSON array), `dead_letters`,
`dashboard_history`, every `ml_*` table, `ai_budgets`, `ai_usage` and
`vhost_secrets`. Several of those also build their primary key from the name:
`vhost_secrets`, `ml_models`, `ml_scripts`, `ml_blobs`, `ml_schedules`
(`<vhost>/...`), `ai_usage` (`storage.AIUsageID`), and `ml_quotas` /
`ai_budgets` (the key *is* the name).

Before 2026-11, `UpdateVHost` changed only the `vhosts` row, so a rename left
every workflow, user assignment and secret under a name no vhost had.

## How a rename works now

- **SQL** (`internal/storage/sql/vhost_rename.go`): `UpdateVHost` reads the
  current row; if the name changes, one transaction checks the new name is
  free (`storage.ErrVHostNameTaken`), updates the vhost, runs one
  `UPDATE ... SET vhost` per table in `vhostNameTables`, rewrites the id of every
  row in `vhostKeyedTables` with the table's own id function, and rewrites
  `users.vhosts` with `storage.RenameInVHostList`. Missing vhost is
  `storage.ErrNotFound`. Tested on SQLite and (with `HERMOD_INTEGRATION=1
  POSTGRES_DSN=...`) PostgreSQL.
- **MongoDB** (`internal/storage/mongodb/vhost_rename.go`): no multi-document
  transaction outside a replica set, so every step is re-runnable and the
  `vhosts` document is renamed **last**. An `_id` cannot change in place: a
  document is upserted under its new `_id`, then the old one deleted.
- **API** (`AuthHandler.UpdateVHost`): 400 for an empty name or `all` (the
  list filter's "every vhost"), 404 missing, 409 taken.
- **Running workflows** need no handler code: the worker's sync compares
  `wf.VHost` (`workflowRuntimeConfigDiffers` in
  `internal/engine/worker/sync.go`) and restarts the engine under the new name.
  The restart builds connectors through the registry's source/sink cache
  (`GetSourceConfig`/`GetSinkConfig`), which a rename does not touch. Until
  2026-11-13 that cache kept the old vhost name, so every connector reading a
  vhost secret failed to resolve it and the whole vhost's workflows stayed
  stopped. Each sync now calls `Registry.ForgetChangedResources`
  (`internal/engine/registry/resource_cache.go`) with the sources and sinks it
  just listed, dropping cached copies whose vhost, type or config moved, before
  any workflow is compared or restarted. Guard test:
  `TestRenamingAVHostKeepsItsWorkflowsRunning` (worker package).
- **UI** (`VHostForm.tsx`): says what a rename moves, shows the server's error,
  and invalidates every query after a rename.

## Adding a table that stores a vhost name

Add it to `vhostNameTables` (plain column) or `vhostKeyedTables` (id built from
the name) in the SQL store and the matching list in the Mongo store, and to
`seedVHostContents` / `countVHostContents` in `vhost_rename_test.go`. Also add it
to `DeleteVHost` if it should go with the vhost.

## Not carried over

- Environment promotion: rules keyed by vhost name (`environment.Rules.ByVHost`)
  and peers that promote into a vhost by name on another instance.
- Data orphaned by a rename made before the fix. To recover it, rename the vhost
  back to its old name (everything now under the new name joins the orphans),
  then rename it again: both moves now carry everything.
