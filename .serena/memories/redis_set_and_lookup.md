# Redis Lookup and Redis Set

Redis Lookup (#330, transformation) reads a key. Redis Set (#336) writes one, as a
sink (`pkg/comm/sink/redisset`, type `redis_set`) and as a mid-flow transformation
(`pkg/comm/transformer/advanced/redis_set.go`). Both writers go through
`pkg/infra/rediskv`, so they parse one config and write the same bytes.

- `Command` is `set` or `hset`. HSET writes one `Field`, or with no field every
  top-level field of the value, which must then be a JSON object.
- Key, field and value are templates over the record. An empty value writes the
  whole record (the sink through its formatter, else JSON).
- Optional TTL. `OnlyIfAbsent` (SET NX) applies to `set` only; with `hset` it is
  refused at config time (HSETNX is one-field and indistinguishable from a
  refresh once an expiry is involved).
- **A key/field/value whose token has no value on the record is refused**, so
  records missing the field never all overwrite one shared key.
- A batch is built in full before anything is sent, then written in one MULTI
  (`ApplyAll` -> `TxPipelined`), so a hash is never left without its expiry;
  it returns every write that failed.
- The transformation keeps one client per connection, resolves the connection
  from secrets and env only, fails the record on a write error unless
  `onError` is `passthrough`, and passes the record on unchanged.

Wired into the factory, lineage (`internal/lineage`), the builder catalogue, the
connector reference in `docs/connectors.md` (`redis_set` is sink-only there), the
sink form (`RedisSetSinkConfig.tsx`), the transformation forms and the palette.
Both Redis steps use the wide middle column in the transformation editor so a
`customer:{{.after.customer_id}}` key shows in full.
