# Changelog

Notable changes to Hermod, newest first. Dates are ISO-8601.

This file starts at 1.0.0. Everything published before it was withdrawn — see
[The releases before this one are gone](#the-releases-before-this-one-are-gone).

## [1.0.0] — 2026-09-07

The first generally available release. It is `1.0.0-rc.2` plus one concurrency
fix; no dependency changed, so it carries no security fix of its own and
`1.0.0-rc.1`'s advisory remains the current one.

### Fixed — a health pass could clear a stall it raced

`checkHealth` publishes `"running"` whenever the source is up and every sink
answers `Ping`, and a wedged sink does answer `Ping`: it accepts the connection
and never completes a write. So the stall watchdog and the health pass both
write the engine status, and the watchdog has to win.

An earlier fix guarded the write with `engStatus != "stalled"`, which closed the
case of a health tick arriving *after* a stall. It could not close the case
inside a single tick: `engStatus` came from an earlier `GetStatus`, and the
write was a separate lock acquisition, so a watchdog setting `"stalled"` between
the two was overwritten by a guard that had already decided the pipeline was
fine. A supervisor was told the workflow had stalled while the status the UI
reads said it was healthy.

The exclusion moved into the write. `StatusTracker.SetEngineStatusUnless`
decides and publishes under one lock and reports whether it wrote.

The window was only as wide as the gap between the two calls, so it never
reproduced on a developer machine and surfaced instead as an intermittent CI
failure. Both halves of the fix now have a test that fails without it: the
tracker races 2000 pairs of writers, and the engine-level test races the
watchdog against `checkHealth` 300 times.

### The version number, and what it does not buy you

`1.0.0` is the release you can run: the container image, the Helm chart, the
binaries and the git tag were all free at this number.

It is **not** installable with `go get`, and no future release can make it so.
`proxy.golang.org` is immutable and permanently maps `v1.0.0` to the February
commit that carried that tag, under `module github.com/user/hermod` — a path
matching no repository. That is unchanged from
[`go get` does not work at this version, by choice](#go-get-does-not-work-at-this-version-by-choice),
and the `retract` block in `go.mod` deliberately still covers `v1.0.0`:
narrowing it would un-retract the February commit without making this one
reachable, and would break the plain `go get` that currently resolves cleanly to
the newest candidate.

Consume this release as an image, a chart or a binary.

### Known gaps

Everything listed under Known gaps in `1.0.0-rc.1` still applies; none of it was
addressed here. The five social connectors that advance their cursor on read —
Twitter/X, LinkedIn, Facebook, Instagram and TikTok — remain the most
significant: treat a restart as potentially lossy for those.

## [1.0.0-rc.2] — 2026-09-07

The second release candidate. Almost all of it is the editor UI: a measured pass
over rendering, navigation and forms, plus the first-run defect that made a
fresh install unable to start a workflow without a restart. No dependency
changed, so nothing here carries a security fix; `1.0.0-rc.1`'s advisory
remains the current one.

### Fixed — a fresh install could not run anything until you restarted it

A first run has no database, so `shouldStartWorker`
(`cmd/hermod/worker_util.go`) was false at process start and `main` built no
worker. Setup then opened the database the admin chose but left the registry
holding `nil`, so every toggle failed with `registry storage is not
initialized`, with nothing on screen to say a restart was needed. Every
`dev.sh --reset` stack and CI's E2E job ran in that state.

Setup now announces the database it opened and `main` answers by starting a
worker — last, after every setup step has succeeded, and behind a `sync.Once`
so a second call cannot put two workers on the same workflows.

Two data races surfaced alongside it, both on state that is now swapped while
the process runs. `Registry.SetStorage` took `r.mu`, but 57 of the 70 reads of
`r.storage`/`r.logStorage` took nothing, on request paths and on the stats and
retention tickers. `r.mu` could not be the fix — `StartWorkflow` holds it and
calls `ValidateWorkflow`, which reads storage, and `sync.RWMutex` is not
reentrant — so the two fields moved to their own `storeMu` behind `store()` and
`logStore()`. `Handler.Worker` gained the same treatment.

### Fixed — the editor did work on every keystroke and every render

- **Preview ran once a second while idle.** `usePreviewTransformation` spread a
  mutation result into a fresh object each render, so the effect debouncing it
  re-armed its timer every render and the 1s debounce behaved as a 1s poll.
  Measured at 4 requests in 5.6s with no user input.
- **Column discovery queried the sink's own database on every keystroke of a
  table name.** The `mappings.length === 0` guard only closes once a discovery
  succeeds, and a partial table name is not a table, so typing `orders` issued
  six live queries.
- **Raw-JSON panes discarded what you were typing.** They were controlled off
  the node config, so half-typed JSON failed to parse, committed nothing, said
  nothing, and the next unrelated re-render replaced the text with the last
  serialised config. A keystroke that did parse came back reformatted, moving
  the caret to the end.
- **Lists blanked between keystrokes.** No query set `placeholderData` and only
  `WorkflowDetailPage` debounced, so each character minted a query key, data
  dropped to `undefined`, and the table emptied and refilled. Logs did this
  while polling every 5s. Now debounced at 300ms with the previous result held,
  across Workflows, Sources, Sinks, Users, Logs and Audit Logs.
- **`FlowCanvas` merged edges with `JSON.parse(JSON.stringify(...))`.** It read
  as a deep-equality guard and did the opposite: a new object per edge per
  change, so React Flow's memoisation never held and every edge re-rendered
  whenever any one did — several times a second under live telemetry. It also
  silently mangled anything JSON cannot carry.
- **`SinkForm` fetched workers with a bare `useEffect`** — no cache, no dedupe,
  no abort, a request per mount, and a `setState` after unmount if the form
  closed mid-flight. `SourceForm` already read the same list through React
  Query, so one resource had two mechanisms.
- **Seven transformation types rendered their configuration twice.** Config
  moved to a registry but the inline blocks it replaced were never deleted, so
  set, advanced, pipeline, lua, wasm, foreach and aggregate showed their
  settings under both Configuration and Advanced, with stale labels the second
  time. 288 inline lines removed.
- **Forms hydrated over your edits.** `useSourceForm` re-parsed `initialData`
  on every keystroke, restoring a sample `updateConfig` had just cleared;
  `UserForm` and `VHostForm` re-hydrated on every `initialData` identity, so a
  refetch after their own save overwrote in-progress edits.
- Routing nodes previewed the `{branch, result}` envelope instead of the
  message, and `WorkflowsPage` crashed on a non-array `/api/workspaces`
  response.

### Changed — navigation, first paint and layout

- **Cold loads no longer flash white.** The app renders dark by default but the
  theme was applied only after hydration, and `index.html` set no background.
  It now inlines Mantine's own `ColorSchemeScript` algorithm, a `color-scheme`
  meta and matching `theme-color`, and `#root` holds a shell placeholder. First
  paint measured at `rgb(26,27,30)`.
- **Navigation stopped paying a fixed cost.** Router `defaultPreload: 'intent'`
  warms a route's lazy chunk on hover instead of fetching chunk then data in
  series; `defaultPendingMinMs` drops 500 → 0 and `defaultPendingMs` rises
  0 → 300, so a warm route that paints in 20ms no longer costs a pinned 500ms
  full-viewport spinner.
- **The manual chunk buckets are gone**, measured rather than assumed. The
  `reactflow` rule matched a package renamed to `@xyflow/react` long ago, so it
  only ever caught dagre and d3 — and fixing the name made it worse, promoting
  the graph library and drag-and-drop kit onto the login screen's critical path
  at 1.45MB. Rolldown's own splitting tracks the eager/lazy boundary properly.
- **One width grammar for every form.** A measured audit found five different
  grammars across six forms. `<Group grow>` was the broken one — a flex row
  that never wraps, squeezing host/port/user/password to ~80px on a narrow
  viewport instead of stacking. 72 of these became `FormRow`, a `SimpleGrid`
  that does collapse and bottom-aligns, replacing a global `min-height: 1.2em`
  that only ever reserved one line.
- The editor toolbar at 390px was a non-wrapping flex row; it wraps now.
- Two theme blocks were silently dead: Mantine `styles` become inline styles,
  so nested selectors in them were not CSS rules — React rejected them and
  logged an error every render. Active nav links rendered at weight 400 instead
  of 600 and table headers had no background in either scheme.

### Added

- **Search in the workflow palette.** It lists well over a hundred sources,
  sinks and transformations across three tabs and a dozen categories, and the
  only way to find one was to scroll. Labels, sub-types and descriptions are
  all searched — so "drop records" finds Filter — ignoring spaces, underscores
  and slashes, because the catalogue is not consistent about them. A tabbed
  palette hides matches by design, so an empty tab reports where they are, and
  says so rather than offering a link when the target tab is locked.
- **The PWA is finished**: an explicit manifest `id` (it was derived from
  `start_url`, so changing that later would have orphaned every install),
  192/512 PNG icons, a maskable icon inside the inner 80% safe zone, and an
  `apple-touch-icon`, which iOS needs because it ignores the manifest for Add
  to Home Screen.
- **Accessible names that describe the right action.** A bulk find-and-replace
  had given icon buttons the wrong ones: copy-to-clipboard announced "Confirm",
  generate-password announced "Refresh", clear-stream announced "Delete", and
  ten delete buttons in a table all announced "Delete" with nothing to say
  which row. 26 corrected.
- **A bundle budget in CI.** `check-bundle-budget.mjs` sums every script and
  stylesheet `index.html` references and fails past 781kB; measured 736kB. One
  eager import of a page-only library puts it back to the 1,092kB it was before
  the buckets came out, with nothing else in CI noticing.
- Editor measurement scripts (`measure-editor.mjs`, `profile-editor-cpu.mjs`,
  `visual-sweep.mjs`) used to settle the memory and worker-offload questions
  rather than argue them. The apparent +17MB peak on the new code was GC sample
  timing: 4.55 vs 4.35MB sampled over 30s, identical composition.

### Developer experience

- `scripts/dev.sh` chooses its ports rather than assuming they are free. It
  prefers 4005 (API), 50051 (gRPC) and 5175 (UI) and steps up when one is
  taken, so a second stack no longer fails with `address already in use`. The
  gRPC port was previously unmanaged, and a failed bind on it is fatal for the
  whole process.

### Known gaps

- Everything listed under Known gaps in `1.0.0-rc.1` still applies; none of it
  was addressed here.
- The 239kB stylesheet costs 36.8kB over the wire with FCP at 44ms on the
  production build. Splitting it was measured and judged not worth the work,
  and is recorded rather than done.

## [1.0.0-rc.1] — 2026-09-03

A release candidate, not a final release. Everything below is tested and the
gates are green, but most of it landed within the last few weeks and none of it
has yet run in a production deployment. `1.0.0` follows once it has.

### Security

- **`golang.org/x/crypto` 0.54.0 → 0.56.0**, for [GO-2026-6354] and
  [GO-2026-6355] — two denial-of-service defects in `golang.org/x/crypto/ssh`
  where a channel can deadlock on an established connection. Both are reachable
  from the SFTP file source, at `pkg/comm/source/file/generic.go:736`, where
  `sshDialContext` calls `ssh.NewClientConn`.

  The connection is outbound, so reaching it means Hermod has been configured to
  poll an SFTP server that is hostile or has been compromised — not an exposure
  anyone can reach unprompted. It is still reachable, which is why this is an
  upgrade rather than an exemption.

  The handshake was already bounded by a deadline, cleared immediately
  afterwards so transfers are not cut short. That is exactly the window these
  advisories describe, so the timeout does not mitigate them.

  `golang.org/x/text` (0.40.0 → 0.41.0) and three indirect `golang.org/x`
  modules moved with it.

[GO-2026-6354]: https://pkg.go.dev/vuln/GO-2026-6354
[GO-2026-6355]: https://pkg.go.dev/vuln/GO-2026-6355

### The releases before this one are gone

Every earlier release has been deleted: 52 GitHub releases, all 55 tags, and
both GHCR packages (`hermod` and `charts/hermod`). Nothing published under a
`1.x` number before 2026-09-03 is available any more, and none of it should be
treated as a supported upgrade path to this release.

**Container images and charts are gone.** `ghcr.io/gsoultan/hermod:1.7.3`,
`:latest`, and the matching `charts/hermod` versions no longer resolve. If you
are running one, keep your local copy until you have moved to `1.0.0-rc.1`; it
cannot be pulled again.

### `go get` does not work at this version, by choice

The version numbering restarts here, and that is possible everywhere except one
place: `proxy.golang.org` is immutable, and it still maps `v1.0.0` to the commit
that carried that tag in February, under the old `module github.com/user/hermod`
which matched no repository.

```
$ curl proxy.golang.org/github.com/gsoultan/hermod/@v/v1.0.0.info
{"Version":"v1.0.0","Time":"2026-02-09T07:38:40Z","Hash":"915f5346..."}
$ curl proxy.golang.org/github.com/gsoultan/hermod/@v/v1.0.0.mod
module github.com/user/hermod
```

Re-tagging does not change what the proxy serves. Every number from `v1.0.0` to
`v1.7.4` is spent for this module path, and `v1.0.0` has to stay retracted — if
it did not, `go get` would resolve to that February commit and fail on the
module path anyway.

The consequence, stated plainly: **Hermod is not currently consumable as a Go
module.** `go get github.com/gsoultan/hermod` and
`go install github.com/gsoultan/hermod/cmd/hermod@…` do not work at `1.0.0`, and
will not until the line passes `v1.7.4`. The container image, Helm chart,
GitHub release and packaged binaries are unaffected and are the supported ways
to run it. Building from a checkout also works.

This was a deliberate trade: version numbers that read correctly everywhere a
user actually installs Hermod, against a `go get` path that had never worked in
any published version anyway — the module path was a placeholder until
2026-09-02, so no release before this one could be imported either.

### Why the withdrawn versions are retracted

Two tombstones exist: `v1.7.4` and `v1.8.0`. A tombstone is a tag holding a
`retract` directive and nothing else — no code, no image, no chart. Both are
listed in `.github/tombstones`, which is how the release workflow knows to
publish no artifacts for them.

They exist because Go reads retractions from the go.mod of the **highest release
version**, which makes retracting anything require publishing something above
it. `v1.7.4` was cut to retract the withdrawn `1.x` line. `v1.8.0` was cut
because that turned out not to be enough:

- `v1.8.0-rc.1`, an intermediate candidate withdrawn for the `x/crypto` SSH
  defects, sorts *above* `v1.7.4`, so no directive in `v1.7.4` could reach it.
  `go get github.com/gsoultan/hermod` selected it and installed the vulnerable
  build without complaint.
- `v1.0.0-rc.1` sorts *below* `v1.0.0`, since a pre-release precedes its
  release, so the `[v1.0.0, v1.7.4]` range never covered it either. It needs a
  directive naming it, and that directive has to live in the highest release —
  not in `v1.0.0-rc.1` itself, which is a pre-release and therefore not where Go
  looks.

The retraction is now `[v1.0.0, v1.8.0]` plus `v1.0.0-rc.1` by name, carried by
the `v1.8.0` tombstone. Between them they cover every version the proxy can
serve for this module path.

`v1.0.0-rc.1` — this release — is itself retracted, which is unusual and
deliberate. The proxy holds that version string against an older commit from an
earlier attempt at this reset, and re-tagging cannot displace it, so a Go user
asking for it would receive code without the SSH denial-of-service fix above.
Failing loudly is the better outcome. The image, chart and binaries are freshly
built from this commit, carry no such history, and are published normally.

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

[1.0.0]: https://github.com/gsoultan/hermod/releases/tag/v1.0.0
[1.0.0-rc.2]: https://github.com/gsoultan/hermod/releases/tag/v1.0.0-rc.2
[1.0.0-rc.1]: https://github.com/gsoultan/hermod/releases/tag/v1.0.0-rc.1
