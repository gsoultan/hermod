# hermod-ml

The Hermod ML worker. Stores tabular datasets as parquet, trains
scikit-learn models, exports them to ONNX and serves them per vhost over the
Open Inference Protocol (V2). Only ONNX files are ever loaded; nothing is
pickled.

## Run

```bash
python3 -m venv .venv && . .venv/bin/activate      # or a venv outside the repo
pip install -r requirements.txt
HERMOD_ML_DATA_DIR=/tmp/hermod-ml python -m hermod_ml
```

Docker:

```bash
docker build -t hermod-ml ml/worker
docker run -p 8090:8090 -v hermod-ml-data:/var/lib/hermod-ml -e HERMOD_ML_TOKEN=change-me hermod-ml
```

Tests (from `ml/worker`):

```bash
pip install -r requirements.txt pytest==8.3.4 httpx==0.28.1
pytest -q
```

## Environment

| Variable | Default | Meaning |
|---|---|---|
| `HERMOD_ML_DATA_DIR` | `/var/lib/hermod-ml` | Holds `datasets/` and `models/`. |
| `HERMOD_ML_TOKEN` | unset | When set, every route except `/v2/health/*` needs `Authorization: Bearer <token>`. |
| `HERMOD_ML_MAX_UPLOAD_MB` | `200` | Largest request body; bigger ones get 413. |
| `HERMOD_ML_MAX_TRAININGS` | `1` | Concurrent training calls; extra calls get 429. |
| `HERMOD_ML_MODEL_CACHE` | `32` | Loaded ONNX sessions kept in an LRU cache. |
| `HERMOD_ML_ADDR` | `0.0.0.0:8090` | Listen address. |
| `HERMOD_ML_CUSTOM_SCRIPTS` | `false` | `true` runs custom training scripts (`/train-custom`) in a sandbox. Needs `HERMOD_ML_TOKEN`; only for a dedicated pool. |
| `HERMOD_ML_SANDBOX_CPU_SECONDS` | `600` | CPU seconds a script may use. |
| `HERMOD_ML_SANDBOX_MEMORY_MB` | `2048` | A script's address space; at least 1024. |
| `HERMOD_ML_SANDBOX_TIMEOUT_SECONDS` | `900` | Wall-clock limit; the script's process group is killed. |
| `HERMOD_ML_SANDBOX_FILE_MB` | `256` | Largest file a script may write. |
| `HERMOD_ML_SANDBOX_NPROC` | `512` | Process limit (counts the whole uid). |
| `HERMOD_ML_SANDBOX_OUTPUT_MB` | `100` | Largest ONNX a script may return; at most `FILE_MB`. |
| `HERMOD_ML_SANDBOX_LOG_KB` | `64` | Script output kept; at most `FILE_MB`. |
| `HERMOD_ML_SANDBOX_THREADS` | `1` | BLAS/OpenMP threads for a script. |

## API

Names (vhost, dataset, model, version) must match `^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`.
Errors are always `{"error": "<sentence>"}` with 400, 401, 404, 413, 429 or 500.

| Method | Path | Purpose |
|---|---|---|
| POST | `/v1/datasets/{vhost}/{name}/rows` | Append rows (`{"rows": [...], "replace": false}`) |
| PUT | `/v1/datasets/{vhost}/{name}/file?format=csv\|xlsx` | Replace the dataset with an uploaded file |
| GET | `/v1/datasets/{vhost}` | List datasets |
| GET | `/v1/datasets/{vhost}/{name}` | Dataset info plus a 20-row sample |
| DELETE | `/v1/datasets/{vhost}/{name}` | Delete a dataset |
| POST | `/v1/models/{vhost}/{name}/train` | Train a new version (synchronous) |
| POST | `/v1/models/{vhost}/{name}/train-custom` | Train with a custom script (`script: {name, sha256, source}`); 403 unless `HERMOD_ML_CUSTOM_SCRIPTS` |
| GET | `/v1/datasets/{vhost}/{name}/export` | The dataset as Parquet, to copy it to a pool |
| PUT | `/v1/datasets/{vhost}/{name}/import` | Replace a dataset with Parquet from `export` |
| GET | `/v1/models/{vhost}/{name}/versions/{v}/export` | A version's metadata and ONNX, to copy it back |
| POST | `/v1/models/{vhost}/{name}/import?dataset=` | Keep an exported version as this model's next one, checked again |
| GET | `/v1/models/{vhost}/{name}/versions` | Version metadata, newest first |
| DELETE | `/v1/models/{vhost}/{name}` | Delete every version |
| GET | `/v2/health/live`, `/v2/health/ready` | Health (no auth) |
| GET | `/vhosts/{vhost}/v2/models/{name}[/versions/{v}]` | OIP model metadata |
| POST | `/vhosts/{vhost}/v2/models/{name}[/versions/{v}]/infer` | OIP inference |

| GET | `/v1/capabilities` | `{"tasks", "algorithms", "unavailable": {algorithm: reason}}`: what this image can train |

Algorithms: `random_forest` (default), `gradient_boosting`, `linear` and
`xgboost` (200 trees, depth 6, `hist`; exported to ONNX via `onnxmltools`).
The image uses `xgboost-cpu`, which has the same API as `xgboost` without
the CUDA/NCCL dependency.

`pytorch_mlp` and `keras_mlp` (`hermod_ml/deep.py`) train a multilayer
perceptron on the same preprocessing. A train request may carry
`"params": {"hidden_layers": [64, 32], "epochs": 200, "batch_size": 32,
"learning_rate": 0.001, "patience": 10}` (those are the defaults; any key may
be left out) for them, and only them. The network is exported with
`torch.onnx.export` or `tf2onnx` and merged after the skl2onnx preprocessing
graph, so the model's inputs and outputs are the same as every other
algorithm's. They need `requirements-dl.txt`, which only the `-dl` image
installs:

```bash
docker build --build-arg HERMOD_ML_EXTRAS=dl -t hermod-ml:dl ml/worker
# or, for a local venv (Python 3.12):
pip install -r requirements.txt -r requirements-dl.txt
```

Without those libraries a request for either answers 400 "not available in
this image", and `/v1/capabilities` lists them under `unavailable`. Their
tests skip; `HERMOD_ML_REQUIRE_DL=1 pytest -q` makes the skips failures, as
the CI job that installs the extras does.
