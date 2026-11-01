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

## Traps

- The SDK's `go get` bumped shared deps (pgx, modernc sqlite, prometheus,
  aws-sdk). Run `go test -race -short ./...` after any SDK upgrade.
- `golangci-lint` built with Go 1.25 refuses this module's `go 1.27.3`; use a
  lint binary built with 1.27+.
