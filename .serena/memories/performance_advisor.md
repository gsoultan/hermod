# The performance advisor (`internal/advisor`)

Answers "what should I change to make this flow faster?" on the Tuning tab's
Recommendations section. Three PRs: #383 (read-only), #384 (apply + measure),
#385 (AI explanation).

## Shape

- **Sampler**: `Registry.runAdvisorSampler` (`internal/engine/registry/registry_advisor.go`)
  reads every running workflow's `GetStatus()` every `advisor.SampleInterval`
  (30s) into `advisor.Advisor`, a ring of 240 samples per flow (2h), in memory
  only. `Retain` drops stopped flows. A `Sample` holds counters and timings,
  never the frame's payload samples.
- **Window**: `currentRun` cuts the window where a counter went backwards
  (engine restart). Rules that compare readings need `window.ready()`:
  5 samples, 2 min, 100 messages. Design rules (`lookup_cache_off`,
  `trace_sampling`) and `stuck_step`/`sink_breaker` report on any window.
- **Rules** (`analyze.go`, thresholds are constants at the top): `bottleneck`,
  `filter_earlier`, `sink_backpressure`, `sink_breaker`, `step_errors`,
  `dead_letters`, `stuck_step`, `source_lag`, `slow_tail`, `lookup_cache_off`,
  `trace_sampling`. `docs/workflows.md` has the table of when each fires; keep
  it in step with the constants.
- **HTTP**: `internal/advisor/transport/http/insights.go`.
  `GET /api/workflows/{id}/insights` (vhost access),
  `POST .../insights/explain` (Editor).

## Run-time histograms

`telemetry.NodeStats.Observe` adds one atomic count to a 24-bucket doubling
histogram (bucket k = (2^(k-1), 2^k] microseconds). Zero allocations; the
allocation budget test is unchanged. It is **not** in the status frame: the
sampler reads `Engine.NodeLatencyHistograms()` and sets `Sample.NodeTimes`.
Quantiles are bucket upper bounds, right to a factor of two, and the finding
text is worded as "95% finish within".

## Fixes are proposals, never direct changes

A finding with `Fix` set can become a self-heal proposal:
`POST /api/workflows/{id}/proposals {rule,node_id}` ->
`selfheal.Service.ProposeFix`. The request names the finding only; the handler
re-reads the current report and refuses (409) if the finding is gone, and
`fixPatch` in `internal/selfheal/fix.go` decides the change. Two kinds:
`enable_lookup_cache` (`/nodes/{id}/config/ttl` "0" -> "5m") and
`set_trace_sampling` (`/trace_sample_rate` 0 -> 0.01). Patchable paths are
whitelisted in `fieldFor` (`internal/selfheal/proposal.go`).

**Sink batch size is advice only on purpose**: it lives on the shared sink
connection, not on the workflow node, so patching it would change every
workflow using that sink.

## Before/after

`ApproveWithBaseline` stores `Proposal.Baseline` (the report's `Summary` at
approval). `List` adds `effect` (via `advisor.Compare`) or `effect_pending` to
the **newest applied** proposal only: readings since an older fix include every
later change. Approval redeploys the engine, so the "since" window starts
fresh. `Compare` judges step time (15% and at least 1 ms) and error rate;
throughput is shown but not judged because it follows the source.

## AI explanation (off by default)

`advisor.Explainer` (`explain.go`), configured only by
`HERMOD_ADVISOR_AI_PROVIDER/_MODEL/_BASE_URL/_API_KEY`; the key must be a
`{{secret("NAME")}}` reference. Sends `brief(report)`: finding text and
numbers, max 8 KB, no payloads. Output is text, max 600 tokens, never parsed.
Charged via `llm.WithScope` to the workflow's vhost and workflow; 429 on a
spent budget. Audit action `AI_EXPLAIN_INSIGHTS`.

## UI gotchas

- `ui/src/pages/workflows/tuning/Recommendations.tsx`, `ui/src/lib/insights.ts`,
  effect table in `proposals/ProposalCard.tsx`.
- A mutation that shows its own inline error must still define `onError`
  (even empty), or `mutationCache` in `ui/src/lib/queryClient.ts` adds an
  "Action failed" toast and the failure is said twice. Tests must build their
  `QueryClient` with that `mutationCache` to catch it.
- In handler tests, a store with no users puts `RbacMiddleware` in first-run
  bypass and every role passes: create a user first.

## Not done

- History is not persisted: a server restart empties every window.
- No `buf`/Playwright/govulncheck run locally for these PRs; CI covers them.
