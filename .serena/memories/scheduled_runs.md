# Scheduled runs: one at a time, and the last successful run

`pkg/comm/source/cron` (`runs.go`):

- `fire(now)` is one tick. While a run is in progress (`running` = its message
  id) the tick is skipped and counted; the next run carries `skipped_ticks`.
  A run older than `maxRun` (`max_run_duration`, default 1h) is given up on as
  failed, and a late report from it is ignored.
- Every run carries metadata `scheduled_at` and `last_success_at` (RFC 3339
  UTC, empty before the first), read in templates as `{{meta.last_success_at}}`.
  It moves only on success and is the source's state (`GetState`/`SetState`,
  key `last_success_at`), so it survives restarts.

The engine says how a run ended through `hermod.RunFinisher.RunFinished(msg,
ok)`, deferred in `processMessage` (`pkg/engine/run_finished.go`). ok is true
for Delivered, Completed and Filtered. It is not `Ack`, which is skipped for
an unparked failure and sent for a parked one (see
[ack_is_not_always_a_success](ack_is_not_always_a_success.md)).

Every wrapper between the engine and the source must pass it on, or the
schedule fires once per `max_run_duration` at best: `source.MetricsSource`
(factory), `registry.statefulSource` (which also saves state right after),
`registry.multiSource` (routes by `_source_node_id`), `source.PrioritySource`
(routes by `_hermod_source`). Each has a test.

`RunFinished` returns whether the source acted on it, and wrappers return
what the inner source said. A wrapper is a `RunFinisher` whatever it wraps, so
the type assertion alone means nothing: `statefulSource` saves state only on a
true answer. Without that it saved every polling source's cursor after each
message, ahead of acknowledgements in flight.
