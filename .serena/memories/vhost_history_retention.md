### Per-vhost history retention

Three tables had no purge at all: `audit_logs`, `workflow_versions`, `approvals`. A vhost now
sets a limit for each. Unset (zero) keeps everything, so an upgrade deletes nothing.

**Where it lives**

- Policy: `storage.VHostRetention` (`audit_log_days`, `workflow_versions`, `approval_days`) in
  `internal/storage/vhost_retention.go`, stored in the settings KV under
  `vhost_retention:<vhost name>`. By name, not id, because `default` is a vhost with no row
  (`storage.RetentionVHost` maps `""` and `"all"` to `default`). A rename moves the key
  (`renameVHostContents` in both the SQL and MongoDB stores).
- API: `GET`/`PUT /api/vhosts/{vhost}/retention` in `internal/auth/transport/http/vhost_retention.go`.
  Read: Administrator, or Editor with the vhost. Save: Administrator only, because it decides
  what leaves the audit trail. No check that the vhost exists, as with vhost secrets.
- Sweep: `Registry.purgeVHostHistory` (`internal/engine/registry/vhost_retention.go`), called
  from the hourly `purgeRetention`. Audit rows are purged once per vhost; versions and approvals
  once per workflow, by the workflow's vhost. A policy that cannot be read keeps everything.
- Storage: optional interfaces `storage.VHostAuditPurger` (log store) and `storage.HistoryPurger`
  (main store), implemented by SQL and MongoDB; Pebble implements only the audit one (it stores
  no versions or approvals). The worker's API storage adapter implements neither, so workers skip.
- UI: `VHostRetentionForm` on the edit page of a vhost, and in a modal on the vhost list for
  `default`.

**How an audit row gets its vhost**

`audit_logs.vhost` is new. `Handler.auditVHost` (`internal/api/handlers/audit_vhost.go`) reads
it from the payload first (a saved or deleted entity says where it lives), then looks the
workflow, source or sink up. `vhost` entities carry their name as the entity id, except vhost
CRUD, which carries the id and the name in the payload. Users, logins and anything not found
get no vhost and are never purged by a vhost's limit.

`backfillAuditVHosts` runs on every SQL `Init` and gives older rows the current vhost of what
they are about. It can do nothing when the log store is a different database from the
configuration; those rows are kept.

**Things that bite**

- Pebble audit keys are `a:%020d` of `999999999999999999 - UnixNano`, which has been negative
  since 2001, so keys do not sort newest first. `PurgeAuditLogsForVHost` therefore reads the
  time from the value. The older `PurgeAuditLogs` and `PurgeLogs` range over the key and delete
  the wrong side of the cutoff; that is a separate bug, not fixed here.
- MongoDB inserts versions and approvals as structs without bson tags (`workflowid`), while
  filters elsewhere use `workflow_id`. The purges match both (`ofWorkflow`).
- Adding the nullable column changed the schema fingerprint in `schema_downgrade_test.go` but
  not `currentSchemaVersion`: the previous release names its columns and still reads and writes.
- A workflow's own `audit_retention` still sweeps the whole audit log at the longest window any
  workflow asks for. Both apply.
