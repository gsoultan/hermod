# Machine learning

Hermod trains machine-learning models on your data and calls them from
workflows and other applications. A model belongs to one vhost and is used
three ways: the **Predict** node in a workflow, the REST API, and gRPC.

A model is either **trained in Hermod**, on a dataset, by the `hermod-ml`
worker; or **served elsewhere** (KServe, Triton, MLServer, MLflow) and
registered by its address. Both are called the same way. Collecting training
data from a running workflow (the Collect Dataset sink) follows in a later
release; it is drawn dashed below.

```mermaid
flowchart LR
  subgraph A["1. Train a model (e.g. churn), weekly"]
    A1["Cron or manual trigger"] --> A3["<b>Train Model</b><br/>refill from SQL, train,<br/>go live if score >= 0.80"] --> A5["Slack / email<br/>version and metrics"]
  end
  subgraph B["2. Use the model live (e.g. fraud) on every new order"]
    B1["Postgres CDC<br/>orders"] --> B2["<b>Predict</b>"] --> B3{"Router<br/>fraud score"}
    B3 -- "> 0.9" --> B4["Approval<br/>hold the order"]
    B3 -- "else" --> B5["Postgres sink<br/>save with score"]
  end
  subgraph C["3. Same model outside workflows"]
    C1["REST POST /api/ml/serve/{vhost}/{name}"]
    C2["gRPC hermod.ml.v1.InferenceService/Predict"]
  end
  D["Datasets<br/>CSV / Excel upload<br/>or a SQL query"] --> A3
  B6["<b>Collect Dataset</b> sink<br/>coming"] -. "fresh rows" .-> D
  A3 -. "live version" .-> B2
  B2 --- C1
  B2 --- C2
```

## Train a model

Training runs in **hermod-ml**, a Python worker beside Hermod (scikit-learn,
XGBoost, exported to ONNX and served with ONNX Runtime; it never loads a
pickle). Run it and point Hermod at it:

| Setting | On | Meaning |
|---|---|---|
| `HERMOD_ML_WORKER_URL` | Hermod | The worker's address, e.g. `http://hermod-ml:8090`. Unset means training is off. |
| `HERMOD_ML_WORKER_TOKEN` | Hermod | Sent as a bearer token; must equal the worker's `HERMOD_ML_TOKEN`. |
| `HERMOD_ML_TRAIN_TIMEOUT` | Hermod | How long one training may take, default `30m`. |
| `HERMOD_ML_TOKEN` | worker | Required token. Without it the worker is open: never run it so outside a private network. |
| `HERMOD_ML_DATA_DIR` | worker | Datasets and model versions, default `/var/lib/hermod-ml`. Keep it on a volume. |
| `HERMOD_ML_MAX_TRAININGS` | worker | Trainings at once, default 1; another gets "busy". |

With Helm: `--set mlWorker.enabled=true --set mlWorker.token=…` (or
`mlWorker.existingSecret`). The chart refuses to run the worker without a token.

```bash
docker run -d --name hermod-ml -p 8090:8090 -e HERMOD_ML_TOKEN=change-me \
  -v hermod-ml:/var/lib/hermod-ml ghcr.io/gsoultan/hermod-ml:latest
```

### Datasets

On the **Models** page, under **Datasets**:

- **Upload a file**: CSV, or Excel `.xlsx` (first sheet, header row), up to 200 MB.
- **From a database**: a `SELECT` on one of the vhost's database sources
  (Postgres, MySQL/MariaDB, SQL Server, Oracle, SQLite, ClickHouse, DB2), run
  read-only with the source's own credentials, at most 1,000,000 rows unless you
  set another cap.

Filling a dataset again replaces its rows.

### Training

**Train a model** asks for a dataset, the column to predict, and optionally the
columns to learn from (empty means all the others). The task (classify, or
predict a number) is chosen from the target unless you pick it; the algorithm
is random forest unless you pick gradient boosting, linear/logistic regression
or XGBoost. A fifth of the rows are held back to score the model:

- classification: `accuracy`, `f1` (macro), `roc_auc` (two classes); `score` is accuracy
- numbers: `rmse`, `mae`, `r2`; `score` is R²

Every training makes a new, immutable version. Whether it goes live is your
rule: **only when I put it live**, **always**, or **if it scores at least** a
minimum on a metric. A version that is not put live is kept; **Versions** on
the model lists them all and puts any one live, which is also how you roll back.

### Train Model node

*Machine Learning → Train Model* does the same from a workflow: each message
that reaches it trains a new version, and the message carries the result under
`training` (version, metrics, `live`, and why). Optionally it first refills the
dataset from a database source with a query, so a weekly cron workflow keeps
the model fresh. Put it in a workflow that runs on a schedule or on demand,
not one that sees every change to a table.

A trained model is called like any other: the Predict node sends a record's
fields, one per feature, and gets back `label` and `probability` for a
classifier, or `value` for a number.

## Register a model served elsewhere

Open **Models** in the sidebar (Editor or Admin) and add a model:

| Field | Meaning |
|---|---|
| Name | How workflows and callers name it: letters, digits, `-`, `_`, `.` |
| Backend | `oip` for the Open Inference Protocol (KServe V2: KServe, Triton, Seldon MLServer, BentoML, TorchServe's V2 API) or `mlflow` for an MLflow scoring server (`/invocations`) |
| URL | The model server's base URL |
| Remote model / version | The model's name and version on that server (`oip` only) |
| Token secret | A vhost secret holding a bearer token for the server, if it needs one |
| Input name | Send every feature as one FP32 matrix tensor of this name (`oip` only). Empty sends one tensor per feature |
| Features | The features the model takes, in order. The Predict node offers a row for each |
| Timeout | Per call, default 30 s |

**Test** sends sample rows and shows the answer.

## Predict node

*Machine Learning → Predict.* Choose the model, map each feature to a record
field (or map nothing to send the whole record), and name the output field
(`prediction` by default). A model with one output writes the value; one with
several writes an object. A mapped field the record lacks fails the record
rather than sending a null; the node's On Error setting (fail, continue, drop)
decides what happens next.

## Serving to other applications

Serving is off until you make a serving key on the model (**Serving key →
Rotate**). The key is shown once; Hermod keeps only its SHA-256. Rotating
replaces it, and **Disable** removes it.

REST:

```bash
curl -X POST https://hermod.example.com/api/ml/serve/default/fraud \
  -H "X-API-Key: hml_…" \
  -d '{"instances": [{"amount": 912.5, "country": "ID"}]}'
# {"model":"fraud","predictions":[{"score":0.93}]}
```

`Authorization: Bearer hml_…` works too. A wrong key, a model without
serving, and a model that does not exist all answer the same 401.

gRPC (`pkg/ml/proto/inference.proto`), on Hermod's gRPC port with the key in
the `x-api-key` metadata:

```
hermod.ml.v1.InferenceService/Predict
  { vhost: "default", model: "fraud", instances: [{amount: 912.5, country: "ID"}] }
```

One call takes at most 1000 rows.

## MCP clients

A model can be offered to Claude, ChatGPT and other MCP clients as a tool on
Hermod's MCP server (`/api/mcp`, see [ai-automation.md](ai-automation.md#mcp-server)).
It is off for every model until an Editor turns on **Expose to MCP** on the
Models page (`PUT /api/vhosts/{vhost}/ml/models/{name}/mcp` with
`{"exposed": true}`). Editing the model keeps the setting.

An exposed model is the tool `predict_<model>`, listed only to a caller who may
use the model's vhost. When an Administrator sees two vhosts with a model of
the same name, each tool is named `predict_<model>__<vhost>`. The tool takes
one record: one argument per feature, all required and nothing else. A model
trained in Hermod types each argument (number, string or boolean) from its
dataset; a model served elsewhere takes its features untyped, and one that
declares no features takes any object. The tool answers
`{"model", "vhost", "prediction"}`. It is read-only and runs like the Predict
node, under the vhost's prediction quota. The MCP server takes the caller's
own session or worker token; a serving key (`hml_…`) does not open it.

An `ai_agent` node can call a model too, with an `ml_predict` tool: set
`config.model` to the model's name and declare the model's features as the
tool's `parameters`, which the agent fills. The prediction is the tool's
result. See [ai-automation.md](ai-automation.md#the-agent).

## Quotas

Each vhost can be held to ML quotas. A vhost has none unless one is set, either
for the vhost or as a server default:

| Quota | Env var for the server default | Counts |
|---|---|---|
| `max_datasets` | `HERMOD_ML_MAX_DATASETS` | Datasets the vhost keeps on the ML worker. |
| `max_dataset_rows` | `HERMOD_ML_MAX_DATASET_ROWS` | Rows in one dataset. |
| `max_dataset_bytes` | `HERMOD_ML_MAX_DATASET_BYTES` | Bytes of one dataset: the uploaded file, or the JSON a query sends. |
| `max_models` | `HERMOD_ML_MAX_MODELS` | Models, trained here or registered by address. |
| `max_concurrent_trainings` | `HERMOD_ML_MAX_CONCURRENT_TRAININGS` | Trainings running at once, **per Hermod replica**. |
| `max_predictions_per_second` | `HERMOD_ML_MAX_PREDICTIONS_PER_SECOND` | Rows predicted per second (Predict node, REST, gRPC, MCP), **per Hermod replica**. |

A vhost's own value wins; a quota it leaves unset uses the server default; 0
means no limit. An env var that is not a non-negative number is ignored.
Predictions per second is a token bucket per vhost on each replica, with a
burst of one second's worth: three replicas behind a load balancer let a vhost
predict up to three times its quota. One call holding more rows than a
second's worth is refused outright; split it. The dataset and model counts are
checked when one is created, so two replicas creating at the same moment can
pass the count by one each.

A refusal names the vhost and the quota. Over REST it is **429** with
`Retry-After: 1` when waiting is enough (predictions per second, concurrent
trainings) and **403** otherwise; over gRPC it is `RESOURCE_EXHAUSTED`. An
uploaded file over the byte quota is refused while it is read; one over the row
quota is uploaded, counted and removed. A dataset read with a query is refused
when it passes either quota.

| Endpoint | Role |
|---|---|
| `GET /api/vhosts/{vhost}/ml/quotas` | Any role on the vhost. Answers the vhost's own quotas (`quotas`, null where unset), the server `defaults` and the limits in force (`effective`). |
| `PUT /api/vhosts/{vhost}/ml/quotas` | Administrator. Replaces the vhost's quotas; a quota left out or null falls back to the server default. |

The Models page shows the quotas under **ML quotas**, editable by an
Administrator. Quotas are kept in the `ml_quotas` table (SQL) or collection
(MongoDB) and removed with the vhost.

## Metrics

- `hermod_ml_predictions_total{vhost,model,outcome}`
- `hermod_ml_prediction_rows_total{vhost,model}`
- `hermod_ml_prediction_duration_seconds{vhost,model}`
- `hermod_ml_quota_refusals_total{vhost,quota}`
