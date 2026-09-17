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
5. `useNodeContext` walks edges back to the nearest upstream payload, preferring
   `testResult.payload` → `lastSample` → `nodeSamples[id]` →
   `sources.find(s => s.id === node.data.ref_id).sample`, then
   `preparePayload` + `getAllFieldsWithTypes` build `availableFields`.

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
- **A non-2xx sample is only a toast.** `apiFetch` throws and notifies;
  `fetchSample`'s catch sets `sampleError`, which only `SamplePanel` renders.
  So a failing sample looks like "the field list is empty", with no persistent
  error anywhere in the editor.

## Driving it in a test

`ui/__tests__/batch_sql_available_fields_e2e.spec.ts` is the worked example:
seed sources/workflow over the API, open the source node, **Next Step**, click
**Test Connection**, wait on the `/api/sources/sample` response, then open the
downstream node and read `getByTestId('available-fields-panel')`. Scope field
assertions to that panel — the same paths also render in the Key Field
autocomplete, and an unscoped `getByText` matches both.

Related: [[use_cdc_is_opt_out]], [[message_payload_decoding]],
[[reachability_tests]].
