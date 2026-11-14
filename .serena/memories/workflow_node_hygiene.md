### What a saved workflow node may hold

`internal/storage/nodeclean` is the one rule for the JSON in `workflows.nodes`
and `workflow_versions`. SQL and Mongo call `nodeclean.ForWrite` on create,
update and version write, `nodeclean.Nodes` on every read, and
`CleanStoredWorkflows` once in `Init` (versions first, only changed rows
written, best-effort with a log line). Pebble stores no workflows.

- **Source and sink nodes** keep `label` + `type` (plus anything the engine
  reads that is not a connection copy). The editor used to spread the whole
  source/sink record into `config` (`id, name, vhost, config, sample, state,
  status, …`), password included. `connectionCopyKeys` lists what is dropped;
  `name` becomes `label` when there is none.
- **Every node** loses `lastSample`, `testResult`, `ref_id` (`editorOnlyKeys`).
  `ref_id` lives on the node itself, not in config.
- **Typed step credentials** (`credentialFields`: api_lookup username /
  password / token / sensitive headers, redis_lookup password, `ai_*` /
  mimir_ask / panmail_providers apiKey, fallbackApiKey, storeApiKey) move to a
  vhost secret `WF_<8 hex of wf id>_<LABEL|NODEID>_<FIELD|HEADER>` and the
  field becomes `{{secret.NAME}}`. A duplicated label falls back to the node
  id. `{{…}}` and `[REDACTED]` are never moved. Same value already there →
  no write. The HTTP handler (`asSaved`) re-reads the row and invalidates the
  moved names in the registry's secret cache.
- **Encrypt `key` is not moved** — that step does not resolve templates, so a
  reference would break it. Adding a field to `credentialFields` is only safe
  where the step resolves it with `ResolveTemplateMsg`/`ResolveTemplateScoped`.
- **Export / promotion** (`stripWorkflowRuntime`) run `Nodes` and then
  `redact.StepCredentials` + `redact.AIKeys`. The bundle's own `sources` /
  `sinks` still carry connection configs for import — by design, not fixed.
- **Versions carry no run state** (`withoutRunState`); a rollback copies the
  current row's status, owner, lease and counters.
- **UI mirror:** `ui/src/pages/workflows/WorkflowEditor/store/savedNode.ts`
  (`savedNodeConfig`) applies the same drop in the save payload and in
  `persistedSnapshot`, so a saved canvas is not "dirty" against the server.
  Change both sides together.
