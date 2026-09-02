# Changelog

Notable changes to Hermod, newest first. Dates are ISO-8601.

This file starts at 1.0.0. Hermod carried tags up to `v1.7.3` before that, and
those releases were withdrawn rather than renumbered — see [Version numbering
restarts here](#version-numbering-restarts-here).

## [1.0.0-rc.1] — 2026-09-01

A release candidate, not a final release. Everything below is tested and the
gates are green, but most of it landed within the last few weeks and none of it
has yet run in a production deployment. `1.0.0` follows once it has.

### Version numbering restarts here

Every release before this one has been deleted: 52 GitHub releases, all 55 tags,
and both GHCR packages (`hermod` and `charts/hermod`). Nothing published under a
`1.x` number before 2026-09-01 is available any more, and none of it should be
treated as a supported upgrade path to this release.

Two consequences worth knowing about:

- **Container images and charts are gone.** `ghcr.io/gsoultan/hermod:1.7.3`,
  `:latest`, and the matching `charts/hermod` versions no longer resolve. If you
  are running one, keep your local copy until you have moved to `1.0.0-rc.1`;
  it cannot be pulled again.
- **The Go module proxy keeps its own copy.** `proxy.golang.org` is immutable by
  design, so the old tags still answer there and always will. This costs nothing
  in practice, because the module never declared a path anyone could import —
  see the module path change below — but it does mean `v1.7.3` will keep
  resolving on the proxy while resolving nowhere else.

### Breaking

- **The module path is now `github.com/gsoultan/Hermod`.** It was
  `github.com/user/hermod`, a placeholder that matched no repository, so
  `go get`, `go install` and importing Hermod as a library all failed with a
  path mismatch regardless of which version you asked for. Fixing it is why this
  release also renumbers: correcting a module path is breaking, and doing it at
  a version reset costs nothing, whereas doing it in a live `1.x` line would
  break importers for no benefit they asked for.
- **Go 1.27 is required to build.** `go.mod` declares `go 1.27.0`.
- **`hermod.TwoPhaseCommit.Prepare` takes a transaction ID:**
  `Prepare(ctx context.Context, txID string) (string, error)`. The coordinator
  now names a transaction and records the name *before* a participant is asked
  to hold it, so a crash between those two steps leaves a name that recovery can
  look for. Previously the participant chose the name and returned it, and a
  crash in that window left a prepared transaction pinned in the database with
  nothing on record pointing at it. In-tree this affects only the PostgreSQL
  sink; any out-of-tree implementation needs the new parameter.
- **Pebble is refused as a metadata store.** `--db-type=pebble` now exits with an
  explanation instead of starting. It never satisfied the metadata store's
  requirements, and previously reported itself as configured while failing to be
  one.

### Changed behaviour you will notice in production

These are corrections, but each one changes something an operator can see. None
of them need action; all of them are worth reading before you deploy.

- **A failed dead-letter park no longer counts as a delivery.** When a message
  could not be delivered *and* could not be written to the DLQ, the engine
  acknowledged it anyway and the record was gone. It is now retained. On a
  deployment with a misconfigured or unreachable DLQ this appears as replication
  slot or queue growth that was not there before — that growth is the data the
  previous behaviour was discarding.
- **Resume cursors advance on acknowledgement, not on read**, across thirteen
  database and OData sources (including SQLite, Oracle, MongoDB, Dynamics 365 and
  SAP). A crash between reading a batch and the sinks writing it no longer skips
  those rows on restart. The trade is at-least-once behaviour where the previous
  code was accidentally at-most-once: a restart may now redeliver a batch that
  was already written. Sinks with upsert semantics absorb this; append-only sinks
  may see duplicates.
- **Outbound HTTP requests time out.** Requests that previously used a client
  with no timeout are now bounded, and WebAssembly modules are fetched through
  the client that carries the SSRF guard. A remote host that accepts a connection
  and never answers now fails the request instead of holding a worker forever.
- **Per-message delivery logging moved from `Info` to `Debug`.** Steady-state log
  volume drops sharply. Raise the level if you were counting those lines.

### Hardening

- The HTTP API server has read-header, idle and header-size limits; it had none,
  and was answerable to a slowloris client.
- The gRPC server has a concurrent-stream ceiling, a receive-size limit, an idle
  timeout and a keepalive enforcement policy; it had none.
- Oracle and Snowflake identifiers are quoted the way those dialects fold case
  (upper), rather than the way PostgreSQL does (lower).
- `scripts/security-check.sh` fails when a `-run` pattern matches no tests, so a
  renamed test can no longer turn a security claim into a green tick that checks
  nothing.
- CI runs the browser security specs, the race detector within the runner's
  memory budget, and `govulncheck` in a job with the swap it needs.

### Added

- MQTT source, tested against a real broker and promoted to GA.
- Live-server integration tests for the Oracle sink and source, and the MSSQL
  sink against Azure SQL Edge.
- The UI moved to Tailwind v4; a pasted URL can configure a database connector,
  and transformation nodes explain themselves in the form.

### Known gaps

Stated here rather than discovered later. All three are also in `README.md` or
`SECURITY.md`.

- **Snowflake is the one identifier fix never watched failing against a real
  server.** No warehouse is reachable from CI, so the Snowflake half of the
  case-folding fix is inference from documented behaviour rather than
  observation. See `SECURITY.md`.
- **The MSSQL source lacks coverage, not capability.** It reads `CHANGETABLE`
  and emits updates and deletes, but no live SQL Server runs in CI.
- **Five social connectors still advance their cursor on read** — Twitter/X,
  LinkedIn, Facebook, Instagram and TikTok. Each drives a vendor pagination token
  whose semantics differ per API, and no test here can exercise them, so they
  were left alone rather than changed mechanically. Treat a restart as
  potentially lossy for these. They are Experimental in `README.md`.

[1.0.0-rc.1]: https://github.com/gsoultan/Hermod/releases/tag/v1.0.0-rc.1
