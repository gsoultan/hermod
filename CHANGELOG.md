# Changelog

Notable changes to Hermod, newest first. Dates are ISO-8601.

This file starts at 1.8.0. Everything published before it was withdrawn — see
[The releases before this one are gone](#the-releases-before-this-one-are-gone)
for what that means if you are running one, and why the number is 1.8.0 rather
than the 1.0.0 the reset was aiming at.

## [1.8.0-rc.2] — 2026-09-03

### Security

- **`golang.org/x/crypto` 0.54.0 → 0.56.0**, for [GO-2026-6354] and
  [GO-2026-6355] — two denial-of-service defects in `golang.org/x/crypto/ssh`
  where a channel can deadlock on an established connection. Both are reachable
  from the SFTP file source, at `pkg/comm/source/file/generic.go:736`, where
  `sshDialContext` calls `ssh.NewClientConn`.

  The connection is outbound, so reaching it means Hermod has been configured to
  poll an SFTP server that is hostile or has been compromised — not an exposure
  anyone can reach unprompted. It is still reachable, which is why this is a
  release rather than an exemption. `1.8.0-rc.1` shipped with the vulnerable
  version: both advisories were published after it was cut, and the gate caught
  them on the next run.

  The handshake was already bounded by a deadline, cleared immediately
  afterwards so transfers are not cut short. That is exactly the window these
  advisories describe, so the timeout does not mitigate them.

  `golang.org/x/text` (0.40.0 → 0.41.0) and three indirect `golang.org/x`
  modules moved with it.

[GO-2026-6354]: https://pkg.go.dev/vuln/GO-2026-6354
[GO-2026-6355]: https://pkg.go.dev/vuln/GO-2026-6355

## [1.8.0-rc.1] — 2026-09-02

A release candidate, not a final release. Everything below is tested and the
gates are green, but most of it landed within the last few weeks and none of it
has yet run in a production deployment. `1.8.0` follows once it has.

### The releases before this one are gone

Every earlier release has been deleted: 52 GitHub releases, all 55 tags, and
both GHCR packages (`hermod` and `charts/hermod`). Nothing published under a
`1.x` number before 2026-09-02 is available any more, and none of it should be
treated as a supported upgrade path to this release.

**Container images and charts are gone.** `ghcr.io/gsoultan/hermod:1.7.3`,
`:latest`, and the matching `charts/hermod` versions no longer resolve. If you
are running one, keep your local copy until you have moved to `1.8.0-rc.1`; it
cannot be pulled again.

### Why 1.8.0 and not 1.0.0

The intent was to restart the numbering at 1.0.0. That is possible for
everything except the one place it cannot be undone.

`proxy.golang.org` is immutable by design. It still answers for all 55 withdrawn
versions and always will, and it maps `v1.0.0` to the commit that carried that
tag in February — under the old `module github.com/user/hermod`, which matched
no repository:

```
$ curl proxy.golang.org/github.com/gsoultan/hermod/@v/v1.0.0.info
{"Version":"v1.0.0","Time":"2026-02-09T07:38:40Z","Hash":"915f5346..."}
$ curl proxy.golang.org/github.com/gsoultan/hermod/@v/v1.0.0.mod
module github.com/user/hermod
```

Re-tagging `v1.0.0` on current code would not change what the proxy serves.
Every number from `v1.0.0` to `v1.7.4` is spent for this module path, so the
release line resumes above them. GitHub releases, container tags and chart
versions were never affected — those were deleted and are free — but a version
number that works in four places and not the fifth is worse than one that works
everywhere.

`v1.8.0-rc.1` was never published before, which is why it is available.

### Withdrawn versions are retracted

Because `v1.7.3` sorts above this release, an unversioned
`go get github.com/gsoultan/hermod` selected a withdrawn version and failed on
it:

```
go: github.com/gsoultan/hermod@upgrade (v1.7.3) requires ...: parsing go.mod:
    module declares its path as: github.com/user/hermod
            but was required as: github.com/gsoultan/hermod
```

`v1.7.4` fixes this. It is a tombstone carrying a single `retract` directive
covering `[v1.0.0, v1.7.4]` — Go reads retractions from the highest version, so
retracting `v1.7.3` requires publishing something above it — and it holds no
code, no image and no chart. `go get` now resolves to this release, and
`go list -m -versions` reports it as the only selectable one.

### Breaking

- **The module path is now `github.com/gsoultan/hermod`.** It was
  `github.com/user/hermod`, a placeholder that matched no repository, so
  `go get`, `go install` and importing Hermod as a library all failed with a
  path mismatch regardless of which version you asked for. Nothing could import
  Hermod at any version, so there was no importer for the change to break —
  which is why it happens here rather than being carried forward.
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

[1.8.0-rc.1]: https://github.com/gsoultan/hermod/releases/tag/v1.8.0-rc.1
