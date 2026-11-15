# Dashboard and vhosts

`internal/api/handlers/dashboard_vhost.go`: `Handler.DashboardVHost(w, r) (vhost, ok)`.

`GET /api/dashboard/stats`, `GET /api/dashboard/history` and `GET /api/ws/dashboard` serve totals
for the vhost in the query; `""` and `all` mean every vhost (`storage.NormalizeVHost`). They used
to pass the query value straight to the registry, so any signed-in user could read another
tenant's totals, or everyone's.

All three now call `DashboardVHost` first (the stream before the websocket upgrade, so a refusal
is still an HTTP 403):

- Administrator, a user whose vhosts include `*`, or no user (server-made request): as asked.
- Anyone else naming a vhost: served if `HasVHostAccess` allows it (their vhosts, plus `default`,
  which that helper gives everyone); otherwise 403.
- Anyone else asking for all: one vhost -> that vhost; none -> `default`; several -> 403 "choose a
  virtual host".

Why several is refused: the storage call takes one vhost or none (`GetDashboardStats(ctx, vhost)`,
`GetDashboardHistory`, and the stream's subscription key). Summing per-vhost results would need
merge rules for averages, the history series and the broadcast keys. The UI defaults to
`all` (`ui/src/context/VHostContext.tsx`), so a non-administrator with several vhosts sees the
"choose a virtual host" error on Home until they pick one. Making "all" total their vhosts is the
follow-up.

Tests: `dashboard_vhost_test.go` in `internal/api/handlers` and
`internal/dashboard/transport/http`, `ws_dashboard_vhost_test.go` in `internal/ws/transport/http`.
