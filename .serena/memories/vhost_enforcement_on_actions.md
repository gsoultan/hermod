# A vhost is enforced on what acts on a workflow, not only on reading it

A user's `VHosts` used to be checked where a workflow, source or sink was
*read*. Toggle, drain, rebuild, rollback, update, delete, health, report,
versions, export, the log list and the WebSocket streams did not check it, so
an Editor of one vhost could act on any other vhost's workflow by id.

## The pieces (internal/api/handlers)

- `MayUseVHost(r, vhost)` — no user (the server itself), an Administrator, or
  `HasVHostAccess`. Everyone may use `""`, `all` and `default`.
- `VHostScope` / `ScopeOf(r)` — answers `Workflow(id)`, `Source(id)`,
  `Sink(id)`, `Log(l)`, `LogFilter(f)`. `nil` means "everything" (Administrator
  or server) and every method is nil-safe. An object that does not exist is
  denied. Lookups are cached for the life of the scope, so build one per
  request or per stream, not per event.
- `AgentOnly(next)` — a worker principal (`user.ID == "worker:<id>"`) or an
  Administrator. For routes only the engine calls: `PATCH
  /api/workflows/{id}/status`, `/stats`, `POST /api/logs`, `/api/logs/batch`.
  A worker's heartbeat is accepted only from that worker.

## Where it is applied

- `internal/workflow/transport/http`: `inVHost(next)` wraps every route with
  `{id}` or `{export_id}`. A workflow that does not exist passes through, so
  the handler still answers 404. Create and update check the vhost in the body;
  the batch routes call `refuseForeignWorkflows`.
- `internal/logs/transport/http`: `ListLogs` refuses a foreign filter and
  filters rows. `total` is still the unfiltered count.
- `internal/ws/transport/http`: `watching(next)` refuses a per-workflow stream
  before the upgrade; global streams are filtered per event.

## The test that keeps it true

`route_guard_test.go` → `TestEveryRouteNamingAWorkflowIsHeldToItsVHost`. A new
route with `{id}` under `/api/workflows` fails it until it is wrapped. Do not
add an allowlist entry to get past it.

## Still open

Dashboard statistics and `/api/ws/dashboard` are instance-wide. A workflow may
reference a source or sink of another vhost. `GET /api/sources` and
`/api/sinks` return credentials to any reader of the vhost.

## Webhook history

`/api/webhooks/` is public for delivery, but `GET /api/webhooks/requests` and
`POST /api/webhooks/requests/{id}/replay` are not: `isWebhookHistoryPath` in
`AuthMiddleware` takes them out of the public rule, and the handlers scope them
to the vhosts of the webhook's source.
