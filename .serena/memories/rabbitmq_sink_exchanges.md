# RabbitMQ sink: queue or exchange

The `rabbitmq_queue` sink (UI label "RabbitMQ Queue / Exchange") publishes either
straight to a queue (default exchange, routing key = queue name — the only mode before
exchanges, and what every older saved sink still does) or to an exchange.

- Settings: `pkg/comm/sink/rabbitmq/queue_settings.go` `ParseQueueSinkSettings`. Keys:
  `exchange`, `exchange_type` (direct|topic|fanout|headers, default direct),
  `exchange_declare` ("false" = passive check only; `amq.*` is never declared),
  `routing_key` (template, direct/topic), `binding_key`, `headers` / `binding_headers`
  (key=value per line or comma), `headers_match` (all|any), `queue_name` (optional with
  an exchange: declared durable and bound). Fields for another type are ignored, not
  refused. A templated routing key or header with a bound queue needs an explicit
  binding key/headers. Topic routing keys may not contain `*`/`#`.
- The factory parses settings at build time, so Test Connection / start reports them.
- Every publish is mandatory + publisher-confirmed under one mutex; a basic.return
  (arrives before the ack) makes Write fail "no queue received the message". Do not
  drop `mandatory`: an exchange with nothing bound silently discards otherwise.
- `ensureConnected` checks `channel.IsClosed()` too: a broker channel error (e.g.
  exchange deleted) closes only the channel.
- UI: `ui/src/components/workflow/Sink/RabbitMQRouteFields.tsx`; the form writes
  `exchange_type` as soon as "An exchange" is picked, and the gate in
  `lib/connectorRequirements.ts` (`rabbitmqSinkRequirements`) keys off it.
  `sinkConfigCoverage.test.tsx` now also scrapes sibling modules a form imports.
- Lineage: sink emits `exchange:` asset (KindExchange) plus the bound `queue:`.
- Tests: `queue_settings_test.go`, `rabbitmq_exchange_integration_test.go`
  (`-tags=integration`, HERMOD_INTEGRATION=1, RABBITMQ_URL), `rabbitmqExchangeSink.test.tsx`.
