# The Playwright coverage suite (ui/__tests__/coverage/)

Scenario catalogue: docs/testing/e2e-scenarios.md. CI job: `e2e-coverage` in
.github/workflows/ci.yml, three shards (`--shard=N/3`), each starting its own
SQLite stack with `./scripts/dev.sh --sqlite --reset --detach`.

## Layout

- `catalog.ts` parses the lists from the app's own source (palette from
  nodeCategories.ts, SOURCE_TYPES/SINK_TYPES from SourceForm/SinkForm,
  expressionFunctions.json, routes from router.tsx), each with an `atLeast`
  floor. Never hand-list connectors or functions in a spec.
- `helpers.ts`: `api`/`apiRaw` (session + CSRF via support/auth.ts),
  `watchErrors` (fails on pageerror, console errors outside BENIGN_CONSOLE,
  /api/ 5xx), `Cleanup`, `createSource/Sink/Workflow`, `Receiver` (HTTP server
  on 127.0.0.1 recording POST/PUT, answering 200 to every method because the
  HTTP sink pings before start; `serve(path, body)` for a polling source),
  `startWorkflow` (toggle then poll status), `webhookPath` (full
  `/api/webhooks/<name>` — the config path must be the full path).
- Specs: pages (P), palette (N), connectors (C), functions (F), conditions (K),
  secrets_env (S), workflow_features (W), live (L).

## Things that bit while writing it

- Connector wizard: Next is gated by `missingConnectionFields`
  (src/lib/connectorRequirements.ts); `requirementsFor` exposes the list with
  examples. C2 asserts every required label is an input with the same
  accessible name (case-insensitive) — a label drift is a real defect (the
  tooltip names a field that is not on the page).
- Test Connection against example hostnames hangs on DNS offline; the spec
  rewrites examples to 127.0.0.1:1 (`onThisMachine`).
- Each type's fields are a lazy chunk: wait for "Loading config fields..." to go.
- Mantine notifications cover the wizard buttons; dismiss them.
- `acceptConfirm` uses getByRole('dialog') and breaks when a confirm opens over
  another modal (Version history → Roll back); name the dialog instead.
- React Flow edge drag can miss while the canvas fits itself to view; retry
  until the edge count rises.
- Editing ui/src while a run is going makes Vite re-optimise and a lazy route
  fails with "TypeError: Failed to fetch" in MatchInnerImpl — a flake caused by
  the edit, not the page.
- vitest only includes src/__tests__/**.
- S4 (env() fallback to HERMOD_ENV_*) runs only when HERMOD_E2E_ENV_VALUE is set
  and the server has HERMOD_ENV_E2E_REGION; CI sets both.

## Findings it produced (fixed in the same change)

- If/Switch Live Preview dropped `branch` and said "returned the message
  unchanged" — the "array_len(source.users) > 0 is always false" report. The
  engine was right in preview, simulation and live. PreviewPanel now shows
  `data-testid="preview-branch"`. `expressionWarning` (functionCatalog.ts) warns
  on unknown function names and bare field arguments in If/Filter/Switch.
- Filter ignored list-shaped `conditions` (API/import shape) and kept every
  record; now uses evaluator.ParseConditions.
- Fresh install: vhost picker empty on Add source/sink (`default` is not a row);
  see src/lib/vhostChoices.ts.
- factory.go split comma lists without trimming (hosts, brokers, addresses,
  to); now `splitList`.
- FilterEditor rows had no accessible names; now "Condition N field/operator/value".
- Editor used a list-shaped stored sample as the message: Preview sent
  {"0": row} (conditions false), Test sent an array the backend refused.
  `ownPayloadOf` (sampleCapture.ts) now takes the newest object row (K6).
- SalesforceSinkConfig took a Mantine `form` prop nothing passes and crashed
  Add sink; now config/updateConfig like every sink form.
- ClickHouse sink requirements demanded host/port but the form and factory use
  `addr`: Next could never enable. Eleven connectors' requirement labels did not
  match their field labels; C2 now catches any new drift.
- OtherSourceConfig inputs were `value={config.x}` (undefined on a new source):
  React's uncontrolled→controlled error. Use `?? ''`.

## Open as of this change

Gembit reported (2026-11-14) "the If branch, when the user tries simulation,
always goes to false". Not reproduced: an If built in the editor on
array_len(source.users) > 0 goes true in Test for an object sample, a JSON-string
`users`, a CDC `after` envelope and (after the fix) a list sample. Asked which
source feeds it and whether a step sits between. Suspects: the live sample
(`nodeSamples`, the last real message) takes precedence over the stored sample
in Test; or an upstream step changes the record.
