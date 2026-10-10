"""Regenerate the parity fixtures for pkg/ml/onnxscore.

Run with the hermod-ml worker's dependencies installed (ml/worker/requirements.txt):

    python -I pkg/ml/onnxscore/testdata/gen_fixtures.py

Two kinds of fixture are written next to this script:

trained/<algorithm>_<task>/
    Models trained by the worker's own Trainer, so the graphs are exactly
    what hermod-ml exports: model.onnx and meta.json as the worker stores
    them, and cases.json holding request rows and what the worker's
    ModelServer answered for them. The rows go through the same column
    tensors Hermod's OIP client builds (pkg/ml/inference columnInputs).

graphs/<name>/
    Small hand-built graphs for the glue operators no worker export uses
    today (Identity, Cast, FeatureVectorizer, Softmax, ArgMax, Normalizer
    MAX and L2), with input tensors and what onnxruntime computed for them.
    unsupported_*.onnx are graphs the evaluator must refuse to load.

Everything is deterministic for a fixed set of package versions, which are
recorded in manifest.json.
"""

from __future__ import annotations

import json
import random
import shutil
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[3]
sys.path.insert(0, str(REPO / "ml" / "worker"))

import numpy as np  # noqa: E402
import onnx  # noqa: E402
import onnxruntime as ort  # noqa: E402
import onnxmltools  # noqa: E402
import skl2onnx  # noqa: E402
import sklearn  # noqa: E402
import xgboost  # noqa: E402
from onnx import TensorProto, helper  # noqa: E402

from hermod_ml.datasets import DatasetStore, rows_to_table  # noqa: E402
from hermod_ml.serving import ModelServer  # noqa: E402
from hermod_ml.training import ModelStore, Trainer  # noqa: E402

ALGORITHMS = ("linear", "random_forest", "gradient_boosting", "xgboost")
# binary: string labels; multiclass: integer labels; regression: numbers.
KINDS = ("binary", "multiclass", "regression")
TRAIN_ROWS = 40


def training_rows(kind: str, seed: int = 3) -> list[dict]:
    rng = random.Random(seed)
    rows = []
    for i in range(TRAIN_ROWS):
        x1 = round(rng.uniform(-3, 3), 2)
        x2 = rng.randint(0, 9)
        city = rng.choice(["Oslo", "Bergen", "Tromso"])
        flag = rng.random() < 0.5
        s = x1 + (1.5 if city == "Oslo" else 0) + (1 if flag else -1)
        row = {"x1": None if i % 17 == 0 else x1, "x2": x2, "city": None if i % 23 == 0 else city, "flag": flag}
        if kind == "binary":
            row["y"] = "yes" if s > 0.5 else "no"
        elif kind == "multiclass":
            row["y"] = 0 if s < -1 else (1 if s < 1.5 else 2)
        else:
            row["y"] = round(3 * x1 + 2 * x2 + (5 if city == "Oslo" else 0) + rng.uniform(-0.5, 0.5), 3)
        rows.append(row)
    return rows


def request_batches() -> list[list[dict]]:
    """What a caller sends: clean rows, then rows that exercise coercion."""
    rng = random.Random(99)
    clean = [
        {
            "x1": round(rng.uniform(-4, 4), 3),
            "x2": rng.randint(-1, 11),
            "city": rng.choice(["Oslo", "Bergen", "Tromso"]),
            "flag": rng.random() < 0.5,
            "id": i,
        }
        for i in range(25)
    ]
    messy = [
        {"x1": None, "x2": 3, "city": "Oslo", "flag": True},
        {"x1": "1.25", "x2": "7", "city": None, "flag": False},
        {"x1": " -2.5e0 ", "x2": 0, "city": "Paris", "flag": 1},
        {"x1": 0.1, "x2": None, "city": "", "flag": None},
        {"x2": 4.5, "city": 7, "flag": "0"},
        {"x1": 2, "x2": 9, "city": True, "flag": 0, "extra": "ignored"},
    ]
    return [clean, messy]


# -- pkg/ml/inference columnInputs, in Python ---------------------------------


def _datatype(data: list) -> str:
    all_num = all_bool = True
    for v in data:
        if isinstance(v, bool):
            all_num = False
        elif isinstance(v, (int, float)):
            all_bool = False
        else:
            all_num = all_bool = False
    return "FP64" if all_num else ("BOOL" if all_bool else "BYTES")


def column_inputs(rows: list[dict]) -> list[dict]:
    names = sorted({k for r in rows for k in r})
    inputs = []
    for name in names:
        data = [r.get(name) for r in rows]
        inputs.append({"name": name, "shape": [len(rows)], "datatype": _datatype(data), "data": data})
    return inputs


def output_rows(outputs: list[dict], n: int) -> list[dict]:
    rows = [{} for _ in range(n)]
    for out in outputs:
        for i in range(n):
            rows[i][out["name"]] = out["data"][i]
    return rows


# -- trained models -------------------------------------------------------------


def write_trained(out: Path) -> None:
    data_dir = Path(tempfile.mkdtemp())
    try:
        datasets, models = DatasetStore(data_dir), ModelStore(data_dir)
        trainer = Trainer(datasets, models, max_concurrent=1)
        server = ModelServer(models, capacity=4)
        for kind in KINDS:
            datasets.append("fx", kind, rows_to_table(training_rows(kind)), True)
        for algorithm in ALGORITHMS:
            for kind in KINDS:
                name = f"{algorithm}_{kind}"
                task = "regression" if kind == "regression" else "classification"
                meta = trainer.train("fx", name, {"dataset": kind, "target": "y", "task": task, "algorithm": algorithm})
                target = out / name
                target.mkdir(parents=True)
                shutil.copyfile(models.onnx_path("fx", name, meta["version"]), target / "model.onnx")
                (target / "meta.json").write_text(json.dumps(meta, indent=2, sort_keys=True) + "\n")
                cases = []
                for rows in request_batches():
                    body = json.loads(json.dumps({"inputs": column_inputs(rows)}))
                    reply = server.infer("fx", name, None, body)
                    cases.append({"rows": rows, "predictions": output_rows(reply["outputs"], len(rows))})
                (target / "cases.json").write_text(json.dumps(cases, indent=1, sort_keys=True) + "\n")
    finally:
        shutil.rmtree(data_dir, ignore_errors=True)


# -- hand-built graphs ------------------------------------------------------------


def _tensor_json(name: str, array: np.ndarray) -> dict:
    kind = {np.dtype(np.float32): "float", np.dtype(np.int64): "int64"}.get(array.dtype, "string")
    data = array.ravel().tolist()
    if kind == "string":
        data = [str(v) for v in data]
    return {"name": name, "type": kind, "shape": list(array.shape), "data": data}


def _save_graph(out: Path, name: str, graph: onnx.GraphProto, opsets: list, feeds: dict[str, np.ndarray]) -> None:
    model = helper.make_model(graph, opset_imports=opsets, producer_name="hermod-onnxscore-fixtures")
    model.ir_version = 8
    onnx.checker.check_model(model)
    target = out / name
    target.mkdir(parents=True)
    (target / "model.onnx").write_bytes(model.SerializeToString())
    session = ort.InferenceSession(model.SerializeToString(), providers=["CPUExecutionProvider"])
    results = session.run(None, feeds)
    case = {
        "inputs": [_tensor_json(k, v) for k, v in feeds.items()],
        "outputs": [_tensor_json(o.name, np.asarray(r)) for o, r in zip(session.get_outputs(), results)],
    }
    (target / "cases.json").write_text(json.dumps(case, indent=1) + "\n")


def write_graphs(out: Path) -> None:
    ml = helper.make_operatorsetid("ai.onnx.ml", 1)
    rng = np.random.default_rng(5)
    a = rng.normal(size=(7, 2)).astype(np.float32)
    b = rng.integers(-3, 4, size=(7, 1)).astype(np.int64)

    # Cast, FeatureVectorizer, Identity, Scaler (one offset for every
    # column), Softmax and ArgMax.
    glue = helper.make_graph(
        [
            helper.make_node("Cast", ["b"], ["bf"], to=TensorProto.FLOAT),
            helper.make_node("FeatureVectorizer", ["a", "bf"], ["fv"], domain="ai.onnx.ml", inputdimensions=[2, 1]),
            helper.make_node("Identity", ["fv"], ["same"]),
            helper.make_node("Scaler", ["same"], ["scaled"], domain="ai.onnx.ml", offset=[0.5], scale=[1.5]),
            helper.make_node("Softmax", ["scaled"], ["probs"], axis=-1),
            helper.make_node("ArgMax", ["probs"], ["best"], axis=1, keepdims=1),
            helper.make_node("Cast", ["scaled"], ["truncated"], to=TensorProto.INT64),
        ],
        "glue",
        [
            helper.make_tensor_value_info("a", TensorProto.FLOAT, [None, 2]),
            helper.make_tensor_value_info("b", TensorProto.INT64, [None, 1]),
        ],
        [
            helper.make_tensor_value_info("probs", TensorProto.FLOAT, [None, 3]),
            helper.make_tensor_value_info("best", TensorProto.INT64, [None, 1]),
            helper.make_tensor_value_info("truncated", TensorProto.INT64, [None, 3]),
        ],
    )
    _save_graph(out, "glue", glue, [helper.make_operatorsetid("", 13), ml], {"a": a, "b": b})

    x = rng.normal(size=(6, 3)).astype(np.float32)
    x[2] = [0.0, 0.0, 0.0]
    x[3] = [-1.0, -2.0, -0.5]
    norms = helper.make_graph(
        [
            helper.make_node("Normalizer", ["x"], ["l1"], domain="ai.onnx.ml", norm="L1"),
            helper.make_node("Normalizer", ["x"], ["l2"], domain="ai.onnx.ml", norm="L2"),
            helper.make_node("Normalizer", ["x"], ["max"], domain="ai.onnx.ml", norm="MAX"),
        ],
        "normalizers",
        [helper.make_tensor_value_info("x", TensorProto.FLOAT, [None, 3])],
        [helper.make_tensor_value_info(n, TensorProto.FLOAT, [None, 3]) for n in ("l1", "l2", "max")],
    )
    _save_graph(out, "normalizers", norms, [helper.make_operatorsetid("", 13), ml], {"x": x})

    # Refused at load: an operator outside the supported set, and ZipMap,
    # whose output is a sequence of maps rather than a tensor.
    abs_graph = helper.make_graph(
        [helper.make_node("Abs", ["x"], ["y"])],
        "abs",
        [helper.make_tensor_value_info("x", TensorProto.FLOAT, [None, 3])],
        [helper.make_tensor_value_info("y", TensorProto.FLOAT, [None, 3])],
    )
    m = helper.make_model(abs_graph, opset_imports=[helper.make_operatorsetid("", 13)])
    m.ir_version = 8
    (out / "unsupported_op.onnx").write_bytes(m.SerializeToString())

    from sklearn.linear_model import LogisticRegression
    from skl2onnx.common.data_types import FloatTensorType

    X = rng.normal(size=(30, 2)).astype(np.float32)
    y = (X[:, 0] > 0).astype(np.int64)
    zipmap = skl2onnx.convert_sklearn(
        LogisticRegression().fit(X, y), initial_types=[("x", FloatTensorType([None, 2]))], target_opset=13
    )
    (out / "unsupported_zipmap.onnx").write_bytes(zipmap.SerializeToString())


def main() -> None:
    for name in ("trained", "graphs"):
        shutil.rmtree(HERE / name, ignore_errors=True)
    for name in ("unsupported_op.onnx", "unsupported_zipmap.onnx"):
        (HERE / "graphs" / name).unlink(missing_ok=True)
    write_trained(HERE / "trained")
    (HERE / "graphs").mkdir()
    write_graphs(HERE / "graphs")
    manifest = {
        "onnx": onnx.__version__,
        "onnxruntime": ort.__version__,
        "skl2onnx": skl2onnx.__version__,
        "onnxmltools": onnxmltools.__version__,
        "scikit-learn": sklearn.__version__,
        "xgboost": xgboost.__version__,
        "numpy": np.__version__,
    }
    (HERE / "manifest.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
