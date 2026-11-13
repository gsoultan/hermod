# Redis connections: parsing and "what to check"

Both Redis sinks (`redis` stream, `redis_set`) and the Redis Set transformation read
their connection through `rediskv.ParseConn` (`pkg/infra/rediskv/conn.go`): `addr`
is `host:port`, a bare host (port 6379 added), or a `redis://`/`rediss://` URL;
`username`, `password`, `db`, `tls` fields override the URL. An empty `addr` is a
config error. Before 2026-11-13 the stream sink passed it to go-redis, which dialled
`localhost:6379` and reported "connection refused" for a server nobody named. The
stream sink now builds from `redis.New(cfg, formatter)`; `NewRedisSink` delegates.

`rediskv.ExplainConnError(conn, err)` wraps a reach/login failure in `*ConnError`
(`Unwrap` keeps `errors.Is(err, syscall.ECONNREFUSED)`), whose text is
`cannot connect to Redis at <addr>: <cause>` then `What to check:` lines covering
both Hermod's settings and redis.conf: refused (running, `port`, `bind`,
protected-mode, firewall, Docker localhost), DNS, timeout, NOAUTH/WRONGPASS
(requirepass, ACL), DENIED (protected-mode), maxclients, NOPERM (ACL SETUSER),
EOF/reset/TLS (TLS mismatch). Other errors pass through unchanged. Both sinks wrap
Ping and write errors with it.

The message has newlines; the sink and source wizards render the Test Connection
alert with `white-space: pre-line`. A redis:// URL's password is kept out of every
error (a `*url.Error` quotes the raw URL), because errors reach the workflow status
and the "Workflow Error" alert (see connector_errors_quote_their_url.md).
