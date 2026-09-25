# How the editor captures a source sample, and what reads it

Every "Available Fields" list in the workflow editor traces back to one stored
sample on the **upstream source**. If a node shows no fields, the node is almost
never the bug — the source's sample is missing.

## The chain, in order

1. **Test Connection** in the source node's wizard (step 2) is what fires
   sampling. `useSourceForm.testMutation.onSuccess` calls `fetchSample(source)`
   — the sample is a *side effect* of a successful connection test, not a
   separate user action.
2. `fetchSample` picks the table from `selectedSampleTable || config.tables`,
   then `POST /api/sources/sample` with `{source, table}`.
   `selectedSampleTable` is never assigned anywhere (only reset to `''`), so in
   practice the table always comes from `config.tables`.
3. `SourceHandler.SampleSourceTable` → `DiscoveryService.sampleTable` →
   the source's `hermod.Sampler` (else `hermod.Browser`).
4. `onSampleReady` persists it with `PUT /api/sources/{id}` as `sample`
   (a JSON *string*) and mirrors it into the node's `lastSample`.
5. `useNodeContext` reads the last simulation first (`testResults`, the
   upstream node's *output*), else walks edges back to the nearest node with
   a payload: its simulated output first, then what it holds itself
   (`sampleCapture.ownPayloadOf`, freshest first: `testResult.payload` →
   `nodeSamples[id]` (live engine) → the source record's stored `sample` →
   `lastSample`). `preparePayload` + `getAllFieldsWithTypes` build
   `availableFields`.

## How one refresh reaches every node downstream

`handleRefreshFields` (`useWorkflowMutations.ts`) captures a fresh sample for
the selected node's own source, drops the old `testResults`, and re-runs
`POST /api/workflows/test` with `partial: true` and `messages` — one sample per
**source node id**, built by `sampleCapture.simulationInputs` from the same
`ownPayloadOf` the field list uses. Each node's fields come from its
predecessor's step in that run, and `TransformationForm`'s Live Preview re-runs
on its own whenever `previewKey` (type + config + `incomingPayload`) changes.
So one click moves every node's fields and preview. The only thing that stops
it is a node failing on the new data; the refresh notification names that node,
and the nodes after it read the nearest output the run did produce.

`partial` is `Registry.SimulateWorkflow` with `checkWorkflowGraph` only (a
source, no cycle, no dangling edge). The toolbar Test stays strict
(`ValidateWorkflow`, so it needs a reachable sink) on purpose: it must not pass
a workflow the engine will not start. Both seed per source node now, so a
two-source workflow is no longer previewed with one branch's data on both.

## Traps

- **`SamplePanel` is dead code.** It owns "3. LIVE PREVIEW" / "Fetch Sample Now"
  and is the only consumer of `validateSourceForSampling` and
  `isNonDestructiveSample` in `sourceSampling.ts` — and nothing imports it.
  Reasoning about the sampling gate from that file describes a UI the user never
  sees. Check `grep -rn SamplePanel ui/src` before blaming its pre-flight rules.
- **A source type whose config has no `tables` key samples with `table: ""`.**
  That is how `batch_sql` broke: `Sample` built `SELECT * FROM <table> LIMIT 1`
  and produced `SELECT * FROM  LIMIT 1`. Any `Sampler` must decide what an empty
  table means rather than interpolating it.
- **A sample with an operation set does not merge its columns into the root.**
  `DefaultMessage.MarshalJSON` takes the CDC branch whenever `operation != ""`,
  so a `SetOperation(OpSnapshot)` + `SetData(col, v)` message serialises as
  `{"operation":"snapshot","after":{cols…}}`. The editor's `preparePayload`
  hoists `after.*` back to the root, so fields appear both ways.
- **`lastSample` is a persisted copy, and it used to win.** Test Connection
  writes it into the source node and saving the workflow stores it in the
  node's config, but the refresh icon and auto-capture write only the source
  record. Checked first, it made a refresh change nothing downstream. Now it
  is the last resort. Two workflows sharing one source each keep their own copy
  (seen in `hermod_metadata`).
- **Creating a workflow without a sink only warns; running one is refused.**
  `workflow_validation.go` treats a missing sink as a warning, and
  `Registry.ValidateWorkflow` rejects it ("no sink node reachable from any
  source"). That is why the refresh asks for `partial`. Any other caller that
  previews an unfinished workflow has to ask for `partial` too.
- **The simulation request carries no workflow `id`, and that is load-bearing.**
  A `sequential` sink node calls `GetSink(workflowID, nodeID)` during the run.
  With no id it finds no engine and writes nothing. `POST /api/workflows/{id}/test`
  *does* pass an id, so on a running workflow a sequential sink should write.
  That is from reading the code (`TestWorkflowByID` → `SinkExecutor`); it has
  not been exercised.
- **A non-2xx sample is only a toast.** `apiFetch` throws and notifies;
  `fetchSample`'s catch sets `sampleError`, which only `SamplePanel` renders.
  So a failing sample looks like "the field list is empty", with no persistent
  error anywhere in the editor.

## Driving it in a test

`ui/__tests__/available_fields_refresh_next_node_e2e.spec.ts` drives the whole
chain: a sinkless S → A(set) → B(set) → C, stale `lastSample` on S, one refresh
on A, then each node's panel and `getByTestId('live-preview')`. It also covers a
two-source workflow. Open the sink with a single `click()`: its drawer is
narrower, and the second click of a `dblclick()` lands on the overlay and
closes it.

`ui/__tests__/batch_sql_available_fields_e2e.spec.ts` is the worked example:
seed sources/workflow over the API, open the source node, **Next Step**, click
**Test Connection**, wait on the `/api/sources/sample` response, then open the
downstream node and read `getByTestId('available-fields-panel')`. Scope field
assertions to that panel — the same paths also render in the Key Field
autocomplete, and an unscoped `getByText` matches both.

Related: [[use_cdc_is_opt_out]], [[message_payload_decoding]],
[[reachability_tests]].
