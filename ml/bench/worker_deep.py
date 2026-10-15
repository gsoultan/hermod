"""Measure the hermod-ml worker's deep-learning path on a generated table.

The table is ml/bench/gomlp's: 30 numbers (5% missing), 8 text columns of 3
to 10 values, 2 ID-like text columns of 500 and 2,000 values, and a yes/no
target that depends on some of each (drawn with numpy, so not the same rows
as the Go benchmark's, only the same shape).

    # Train pytorch_mlp through the worker's Trainer and serve it (needs the
    # "-dl" image's torch). One configuration per process: peak memory is the
    # process's.
    python ml/bench/worker_deep.py train --rows 200000 --precision auto

    # No torch needed: the graph a network of the default shape (64, 32) has
    # on this table, float32 against int8, one-hot of every value against the
    # top 100, scored on 1 serving thread and on ONNX Runtime's default. The
    # weights are random, so it measures size and speed, not quality.
    python ml/bench/worker_deep.py serve

Run from ml/worker (or with it on PYTHONPATH). Prints one JSON object.
"""

from __future__ import annotations

import argparse
import json
import resource
import sys
import tempfile
import time
from pathlib import Path

import numpy as np
import pandas as pd

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "worker"))

from hermod_ml import deep, training  # noqa: E402
from hermod_ml.datasets import DatasetStore, frame_to_table  # noqa: E402


def dataset(n: int, seed: int) -> pd.DataFrame:
    rng = np.random.default_rng(seed)
    cols: dict[str, object] = {}
    for j in range(30):
        v = rng.standard_normal(n)
        v[rng.random(n) < 0.05] = np.nan
        cols[f"n{j}"] = v
    for j in range(8):
        cols[f"s{j}"] = np.char.add("v", rng.integers(3 + j, size=n).astype(str))
    a, b = rng.integers(500, size=n), rng.integers(2000, size=n)
    cols["s8"], cols["s9"] = np.char.add("a", a.astype(str)), np.char.add("b", b.astype(str))
    effect = rng.standard_normal(2000)
    x = {k: np.nan_to_num(cols[f"n{k}"]) for k in range(4)}
    z = x[0] + x[1] * x[2] + np.sin(2 * x[3]) + 0.5 * effect[a] + 0.5 * effect[b]
    z += np.where(np.char.endswith(cols["s0"], "0"), 0.7, 0) + 0.5 * rng.standard_normal(n)
    cols["y"] = np.where(z > 0, "yes", "no")
    return pd.DataFrame(cols)


def cpu_seconds() -> float:
    ru = resource.getrusage(resource.RUSAGE_SELF)
    return ru.ru_utime + ru.ru_stime


def peak_rss_mb() -> float:
    return round(resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024, 1)


def percentiles(samples: list[float]) -> tuple[float, float]:
    s = sorted(samples)
    return round(s[len(s) // 2], 1), round(s[len(s) * 95 // 100], 1)


def train(args) -> dict:
    from hermod_ml.serving import ModelServer
    from hermod_ml.training import ModelStore, Trainer

    out: dict = {"rows": args.rows, "precision_asked": args.precision}
    with tempfile.TemporaryDirectory() as tmp:
        datasets, models = DatasetStore(Path(tmp)), ModelStore(Path(tmp))
        datasets.append("bench", "t", frame_to_table(dataset(args.rows, args.seed)), replace=True)
        out["rss_before_train_mb"] = peak_rss_mb()
        trainer = Trainer(datasets, models, max_concurrent=1, train_threads=args.train_threads)
        params = {"epochs": args.epochs, "precision": args.precision}
        cpu0, wall0 = cpu_seconds(), time.perf_counter()
        meta = trainer.train("bench", "m", {"dataset": "t", "target": "y", "algorithm": "pytorch_mlp",
                                            "seed": args.seed, "params": params})
        out["train_wall_s"] = round(time.perf_counter() - wall0, 1)
        out["train_cpu_s"] = round(cpu_seconds() - cpu0, 1)
        out["peak_rss_mb"] = peak_rss_mb()
        out["artifact_bytes"] = models.onnx_path("bench", "m", meta["version"]).stat().st_size
        out["efficiency"] = meta.get("efficiency")
        out["accuracy"] = round(meta["metrics"]["accuracy"], 4)
        out["roc_auc"] = round(meta["metrics"].get("roc_auc", float("nan")), 4)
        out["baseline_accuracy"] = round(meta["baseline"]["accuracy"], 4)

        server = ModelServer(models, capacity=4, threads=args.serve_threads)
        frame, _ = datasets.load("bench", "t")
        sample = frame.drop(columns=["y"]).head(args.latency_rows)

        def body(rows: pd.DataFrame) -> dict:
            inputs = []
            for c in rows.columns:
                text = rows[c].dtype == object
                data = [None if (not text and np.isnan(v)) else v for v in rows[c].tolist()]
                inputs.append({"name": c, "shape": [len(rows)], "datatype": "BYTES" if text else "FP64", "data": data})
            return {"inputs": inputs}

        server.infer("bench", "m", None, body(sample.head(1)))
        lat = []
        for i in range(len(sample)):
            one = body(sample.iloc[i : i + 1])
            t = time.perf_counter()
            server.infer("bench", "m", None, one)
            lat.append((time.perf_counter() - t) * 1e6)
        out["latency_p50_us"], out["latency_p95_us"] = percentiles(lat)
        batch = body(frame.drop(columns=["y"]).head(20000))
        t = time.perf_counter()
        server.infer("bench", "m", None, batch)
        out["batch_rows_per_s"] = round(20000 / (time.perf_counter() - t))
    return out


def network(width: int, outputs: int, seed: int):
    """A torch-shaped network (Gemm and Relu layers, softmax), random weights."""
    from onnx import TensorProto, helper, numpy_helper

    rng = np.random.default_rng(seed)
    sizes = [width, 64, 32, outputs]
    nodes, inits, last = [], [], "x"
    for i, (n_in, n_out) in enumerate(zip(sizes, sizes[1:])):
        bound = 1 / np.sqrt(n_in)
        inits += [numpy_helper.from_array(rng.uniform(-bound, bound, (n_out, n_in)).astype(np.float32), f"w{i}"),
                  numpy_helper.from_array(rng.uniform(-bound, bound, n_out).astype(np.float32), f"b{i}")]
        nodes.append(helper.make_node("Gemm", [last, f"w{i}", f"b{i}"], [f"z{i}"], transB=1))
        last = f"z{i}"
        if i < 2:
            nodes.append(helper.make_node("Relu", [last], [f"a{i}"]))
            last = f"a{i}"
    nodes.append(helper.make_node("Softmax", [last], ["y"], axis=1))
    graph = helper.make_graph(nodes, "net", [helper.make_tensor_value_info("x", TensorProto.FLOAT, [None, width])],
                              [helper.make_tensor_value_info("y", TensorProto.FLOAT, [None, outputs])], inits)
    return helper.make_model(graph, opset_imports=[helper.make_opsetid("", deep.OPSET)], ir_version=8)


def serve(args) -> dict:
    import onnxruntime as ort
    from skl2onnx import convert_sklearn
    from skl2onnx.common.data_types import FloatTensorType, StringTensorType
    from sklearn.compose import ColumnTransformer
    from sklearn.preprocessing import OneHotEncoder, StandardScaler

    frame = dataset(args.rows, args.seed)
    features = [c for c in frame.columns if c != "y"]
    numeric = [c for c in features if c.startswith("n")]
    strings = [c for c in features if c.startswith("s")]
    X = pd.DataFrame({f: training.numeric_column(frame[f]) if f in numeric else training.string_column(frame[f]) for f in features})
    X = X.fillna({f: float(X[f].median()) for f in numeric})
    meta = {"task": "classification", "features": features, "feature_types": {f: "number" if f in numeric else "string" for f in features}}
    out: dict = {"rows_fitted": args.rows}
    for cap in ("every value", "top 100"):
        cats = [deep.top_categories(X[f]) for f in strings] if cap == "top 100" else "auto"
        prep = ColumnTransformer([("num", StandardScaler(), numeric),
                                  ("str", OneHotEncoder(handle_unknown="ignore", categories=cats), strings)], sparse_threshold=0)
        width = prep.fit(X).transform(X.head(1)).shape[1]
        types = [(f, FloatTensorType([None, 1]) if f in numeric else StringTensorType([None, 1])) for f in features]
        prep_graph = convert_sklearn(prep, initial_types=types, target_opset={"": deep.OPSET, "ai.onnx.ml": 3})
        graph = deep.compose(prep_graph, network(width, 2, args.seed), "classification", ["no", "yes"])
        training._rename_inputs(graph, features)
        f32 = deep.optimize(graph.SerializeToString())
        i8 = deep.quantize(f32)
        feed_one = training.make_feed(X.head(1), meta)
        feed_many = training.make_feed(X.head(20000), meta)
        row: dict = {"input_width": width}
        probs = {}
        for name, raw in (("float32", f32), ("int8", i8)):
            row[f"{name}_bytes"] = len(raw)
            for threads in (1, 0):
                options = ort.SessionOptions()
                if threads:
                    options.intra_op_num_threads = threads
                session = ort.InferenceSession(raw, options, providers=["CPUExecutionProvider"])
                session.run(None, feed_one)
                lat, cpu0 = [], cpu_seconds()
                for _ in range(args.latency_rows):
                    t = time.perf_counter()
                    session.run(None, feed_one)
                    lat.append((time.perf_counter() - t) * 1e6)
                key = f"{name}_threads_{threads or 'default'}"
                row[f"{key}_p50_us"], row[f"{key}_p95_us"] = percentiles(lat)
                row[f"{key}_cpu_us_per_row"] = round((cpu_seconds() - cpu0) / args.latency_rows * 1e6, 1)
                t = time.perf_counter()
                probs[name] = session.run(None, feed_many)[1]
                row[f"{key}_batch_rows_per_s"] = round(20000 / (time.perf_counter() - t))
        row["int8_max_probability_change"] = round(float(np.abs(probs["int8"] - probs["float32"]).max()), 4)
        out[cap] = row
    return out


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("mode", choices=["train", "serve"])
    parser.add_argument("--rows", type=int, default=200_000)
    parser.add_argument("--epochs", type=int, default=30)
    parser.add_argument("--precision", default="auto", choices=deep.PRECISIONS)
    parser.add_argument("--train-threads", type=int, default=0)
    parser.add_argument("--serve-threads", type=int, default=1)
    parser.add_argument("--latency-rows", type=int, default=2000)
    parser.add_argument("--seed", type=int, default=1)
    args = parser.parse_args()
    print(json.dumps(train(args) if args.mode == "train" else serve(args), indent=2))


if __name__ == "__main__":
    main()
