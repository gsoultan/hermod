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
TLS/x509/certificate errors (TLS mismatch, cert host name). A connection the server
closed without a reply (EOF/reset) is explained by `conn.TLS`: TLS off points at a
TLS-only port (`tls-port` with `port 0`, managed Redis) and suggests turning Use TLS on;
TLS on (toggle or rediss://) points at a plain server. Both add a Docker published port
with a stopped container / proxy with no backend, and the Redis log. Before 2026-11-19
every EOF got the TLS-setup list, which read as "you chose TLS" with the toggle off.
Other errors pass through unchanged.

`rediskv.NewClient` adds a `greeting` hook (`greeting.go`). Redis in protected-mode or at
maxclients writes `-DENIED ...` / `-ERR max number of clients` on accept and hangs up;
go-redis v9 `initConn` reads it as HELLO's reply, treats any Redis error there as "no
HELLO", and the next command fails with EOF / EPIPE, so the user saw "EOF". The hook wraps
each dialled conn, keeps an error line that is the conn's first bytes (not
`ERR unknown ...`, an old Redis's answer to HELLO), and swaps a later hang-up error for
`*RefusedError{Reply, Err}`; a successful command clears it. The wrapper forwards
`SyscallConn` so go-redis's pool health check keeps working. Both sinks wrap
Ping and write errors with it.

The message has newlines; the sink and source wizards render the Test Connection
alert with `white-space: pre-line`. A redis:// URL's password is kept out of every
error (a `*url.Error` quotes the raw URL), because errors reach the workflow status
and the "Workflow Error" alert (see connector_errors_quote_their_url.md).
