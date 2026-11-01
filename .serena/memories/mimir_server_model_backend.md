# mimir-server as a model backend

Since the mimir integration's phase 1 (branch `feat/mimir-server-models`). A
model on a [mimir-server](https://github.com/gsoultan/mimir-server) is a third
kind of external model server beside Open Inference and MLflow, so every
existing caller (Predict node, REST/gRPC serving, MCP tool, monitor) got it at
once.

## The seam

- `pkg/ml/inference/mimir.go`: `BackendMimir = "mimir"`. Hermod talks to the
  server through `github.com/gsoultan/mimir-server-sdk` (Connect over
  `httpclient.DataClient`, so private addresses are allowed on purpose). The
  SDK pulls `github.com/gsoultan/mimir-server` (its generated proto types) and
  `connectrpc.com/connect` into go.mod.
- `Target.Model` is the model's **opaque id** on the server, not its name;
  `Target.Token` is the `mimir_...` API key and is required (`mimirClient`
  refuses an empty one). `InputName` is refused: a mimir model is asked by
  column name.
- `predictMimir` sends one `PredictFields` call per row, up to
  `mimirConcurrency` (4) in flight, keeping row order. Values go as text
  (`mimirText`); with `Features` set only those columns go and a missing one
  goes `""`, which the server reads as the column's fill value.
- The answer row is always an object: `prediction`, `confidence`, `guesses[]`
  and `prediction_id` for a label model; `prediction` and `prediction_id` for a
  number model. Keep `prediction_id`: phase 2's Feedback node sends it back
  with the real outcome.
- `ListMimirModels(ctx, url, token)` → `[]RemoteModel` (id, name, architecture,
  ready/status, accuracy, labels, columns). `internal/ml.Service.ListMimirModels`
  resolves the vhost secret first and wraps a missing or malformed one in
  `ErrBadMimirRequest` (HTTP 400). Route:
  `POST /api/vhosts/{vhost}/ml/mimir/models` (Editor), body
  `{"url","token_secret"}`; the key is never returned.
- `storage.ValidateMLModel` requires `TokenSecret` for a mimir model.

## UI

`ui/src/pages/ml/ModelFormModal.tsx` defaults *Kind of server* to mimir.
`MimirModelPicker.tsx` holds Fetch models + the Select (not-ready models are
listed but disabled, "Enter the id instead" falls back to a text input); a pick
fills `remoteModel`, `features` (from columns), description and, when empty,
the Hermod name (`hermodName` slugifies). Mantine Select in tests: query the
`combobox` role and options with `hidden: true`.

## Phase 2: feedback and explanations (branch `feat/mimir-feedback-explain`)

- `inference.Client.SendFeedback(ctx, t, predictionID, actual)` (numbers →
  `SendFeedbackValue`, anything else → label text) and
  `Explain(ctx, t, row, label)` → `{label, number, value, typical, exact,
  contributions[]{column, value, typical, effect}}`. Both return
  `inference.ErrNotMimir` for another backend.
- `internal/ml.Service.Feedback` / `.Explain`, `Registry.MLFeedback` /
  `MLExplain` (`registry_ml.go`): the transformers find them through
  `hermod.RegistryKey` as small interfaces (`feedbackCaller`, `explainer`).
- `pkg/comm/transformer/ml/feedback.go`: `ml_feedback` node, config `model`,
  `predictionIdField` (default `prediction.prediction_id`), `actualField`,
  `outputField` (default `feedback`) → `{right, prediction_id}`.
- `predict.go`: `explain` option (bool or "true") adds `explanation` to the
  prediction object, or `<outputField>_explanation` for a bare value.
- Route `POST /api/vhosts/{vhost}/ml/models/{name}/explain` (Editor), body
  `{"instance", "label"}`; `ErrNotMimir` → 400, unknown model → 404.
- Lineage: `ml_feedback` is `predicts` against the model, like `ml_predict`
  (`internal/lineage/nodes.go`, `docs/lineage.md`). Builder description in
  `internal/workflow/builder/descriptions.go`.
- UI: `MLFeedbackConfig.tsx` (non-mimir models disabled in the Select),
  "Explain each answer" checkbox in `MLPredictConfig.tsx` for a mimir model,
  `explain()` + `Explanation` in `lib/mlModels.ts`, "Why?" + `WhyList` in
  `ModelTestModal.tsx`. Tests: `mlFeedbackConfig.test.tsx`,
  `mlModelTestModal.test.tsx`, `mlPredictConfig.test.tsx`.

## Phase 3: mimir events on the webhook source (branch `feat/mimir-webhook-events`)

- `internal/webhooks/transport/http/mimir.go`: source config `preset: "mimir"`
  switches `HandleWebhook` to `mimirwebhook.Parse` (SDK `webhook` package)
  on the `Mimir-Signature` header with the source's `secret`; the plain
  `X-Hub-Signature-256` check steps aside (`authenticateWebhook` returns OK
  for the preset). No secret → 503 `msgMimirNeedsSecret`.
- De-dup: `recentEvents` on `WebhookHandler.seen`, keyed path+Event.ID,
  25 h TTL, 10k cap, per process. Repeat → `200 {"status":"duplicate"}`;
  unwanted type (`events` comma list) → `200 {"status":"ignored"}`. Both 2xx
  on purpose: mimir retries anything else for a day.
- Message: body as sent, metadata `mimir_event` / `mimir_event_id`. Replay
  (`internal/source/transport/http` ReplayWebhookRequest) bypasses all of it.
- UI: `OtherSourceConfig.tsx` **Sender** Select (`preset`), `MIMIR_EVENTS`
  MultiSelect (`events`, saved comma-joined), **Webhook secret** required,
  API key / HTTP method hidden for the preset. Tests in
  `webhookCredentials.test.tsx`. Docs: `docs/api-sources.md` "mimir-server
  events".

## Phase 4: documents, datasets, chat (branch `feat/mimir-documents-datasets-chat`)

- `pkg/comm/transformer/genai/mimirask`: `mimir_ask` transformer. Config
  `url`, `apiKey` (both `ResolveTemplateScoped`), `collection`, `mode`
  (`ask`|`search`), `question`/`questionField`, `sources`, `maxTokens`,
  `limit`, `targetField` (default `answer`), `timeout`. Uses
  `client.Documents.AskText` / `.Search`. Blank imports in
  `internal/workflow/builder/catalogue.go` and `cmd/hermod/main.go`;
  description in `descriptions.go`. Test fake embeds
  `mimirv1connect.UnimplementedDocumentServiceHandler`; Ask is a server
  stream.
- Dataset to mimir: `POST /api/vhosts/{vhost}/ml/datasets/{name}/mimir`
  (Editor) → `Service.SendDatasetToMimir` (`internal/ml/mimir_dataset.go`):
  worker `ExportDataset` (parquet) → `parquetToCSV` (`parquetcsv.go`, flat
  columns, 4096-row chunks) → `inference.Client.UploadMimirDataset`
  (`pkg/ml/inference/mimir_data.go`, `Datasets.UploadCSV`). 200 MB cap.
  **mimir has no dataset append**: each send is a new dataset there; the
  design doc's "mimir destination on the ml_dataset sink" was replaced by
  this one-shot action for that reason.
- LLM provider `mimir` in `pkg/llm/connect` (`Kinds()`): OpenAI-compatible
  under `<BaseURL>/v1` (`client.OpenAIBaseURL()`), `JSONObject` mode,
  `LegacyMaxTokens`, key required (not keyless). UI `aiProviders.ts`
  (`requiresBaseUrl`, `showsBaseUrl` include mimir),
  `AIConnectionSection.tsx` placeholder/description.
- UI: `configs/ai-retrieve/MimirAskConfig.tsx` (palette "Ask Documents",
  grape, `IconFileSearch`), `pages/ml/datasets/SendToMimirModal.tsx`
  (`IconSend` action in `DatasetsPanel.tsx`; prefills from connected mimir
  models via `useVHostModels`), `lib/mlModels.ts` `sendDatasetToMimir`.
  Tests: `mimirAskConfig.test.tsx`, `mlSendToMimir.test.tsx`,
  `aiConnection.test.ts`, `paletteAI.test.tsx`.
- Docs: `docs/ml.md` (Send to mimir-server, Ask documents node),
  `docs/ai-automation.md` (provider table, `mimir_ask` row).

## Traps

- CI's `check_pr_base.sh` fails a PR that targets another PR's branch
  unless it carries the `stacked` label; retarget at `main` once the base
  merges.
- golangci-lint (CI v2.14.0) findings seen so far: errorlint wants `%w`
  for every error in an `fmt.Errorf`; gosec G101 flags any constant whose
  name ends in `Secret`; funlen > 50 lines; gocognit > 20.

- The SDK's `go get` bumped shared deps (pgx, modernc sqlite, prometheus,
  aws-sdk). Run `go test -race -short ./...` after any SDK upgrade.
- `golangci-lint` built with an older Go refuses this module's `go 1.27.3`.
  What works: `GOTOOLCHAIN=go1.27.3 GOFLAGS=-mod=mod go run
  github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
  --new-from-rev=origin/main ./...`.
