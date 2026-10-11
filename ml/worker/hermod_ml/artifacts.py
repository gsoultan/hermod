"""Checks on a model version the worker did not build itself.

Two kinds of version arrive from outside the built-in trainer: the ONNX a
custom script exported, and a version imported from a training pool. Both
must hold to the contract the serving path relies on before they are saved:

- meta.json: a task the server knows, features with types, numeric fill
  values, and labels for a classifier;
- the graph: one input per feature, named exactly as the feature, float for
  a number and string for a string, shaped [n, 1]; outputs as a built-in
  model has them (label then probabilities [n, k], or one value);
- no tensor whose data lives in an external file, which onnxruntime would
  read from disk when it loads the graph;
- a smoke prediction in onnxruntime that returns that shape.

The prediction runs in a separate process (onnx_child.py): onnxruntime
aborts the process on some malformed graphs, and a graph from outside must
not be able to take the worker down with it. Only ONNX is ever loaded;
nothing here unpickles anything.
"""

from __future__ import annotations

import json
import math
import shutil
import signal
import subprocess
import sys
import tempfile
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import numpy as np
import onnx
import pandas as pd

from .datasets import NUMBER, STRING
from .errors import ApiError, bad_request

TASKS = ("classification", "regression")
MAX_FEATURES = 4096
SMOKE_ROWS = 2
ONNX_CHILD = Path(__file__).with_name("onnx_child.py")
# Limits for the process that runs a graph from outside.
RUN_CPU_SECONDS = 600
RUN_MEMORY_MB = 4096
RUN_TIMEOUT_SECONDS = 900

_FLOATS = {onnx.TensorProto.FLOAT, onnx.TensorProto.DOUBLE}
_INTS = {onnx.TensorProto.INT64, onnx.TensorProto.INT32}


def unprocessable(message: str) -> ApiError:
    return ApiError(422, message)


# --------------------------------------------------------------------------
# meta.json
# --------------------------------------------------------------------------


def _finite_number(value: Any) -> bool:
    return isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value)


def check_meta(meta: Any) -> dict[str, Any]:
    """Validate a version's meta; raise 400 naming the first problem.

    Keys the serving path does not read are kept as they are.
    """
    if not isinstance(meta, dict):
        raise bad_request("'meta' must be an object.")
    task = meta.get("task")
    if task not in TASKS:
        raise bad_request(f"meta 'task' must be one of {', '.join(TASKS)}.")

    features = meta.get("features")
    if (
        not isinstance(features, list)
        or not 0 < len(features) <= MAX_FEATURES
        or not all(isinstance(f, str) and f for f in features)
        or len(set(features)) != len(features)
    ):
        raise bad_request(f"meta 'features' must list 1 to {MAX_FEATURES} distinct feature names.")
    types = meta.get("feature_types")
    if not isinstance(types, dict) or set(types) != set(features):
        raise bad_request("meta 'feature_types' must give a type for exactly each feature.")
    if not all(t in (NUMBER, STRING) for t in types.values()):
        raise bad_request(f"meta 'feature_types' values must be {NUMBER!r} or {STRING!r}.")

    fill = meta.get("fill", {})
    if not isinstance(fill, dict) or not all(
        types.get(k) == NUMBER and _finite_number(v) for k, v in fill.items()
    ):
        raise bad_request("meta 'fill' must map numeric features to finite numbers.")

    if task == "classification":
        labels = meta.get("labels")
        if not isinstance(labels, list) or not labels or len(set(map(repr, labels))) != len(labels):
            raise bad_request("meta 'labels' must list the classifier's distinct labels.")
        ints = all(isinstance(v, int) and not isinstance(v, bool) for v in labels)
        if not ints and not all(isinstance(v, str) for v in labels):
            raise bad_request("meta 'labels' must be all strings or all integers.")

    metrics = meta.get("metrics", {})
    if not isinstance(metrics, dict) or not all(isinstance(k, str) and _finite_number(v) for k, v in metrics.items()):
        raise bad_request("meta 'metrics' must map names to finite numbers.")
    for key in ("algorithm", "dataset", "target", "created_at"):
        if key in meta and not isinstance(meta[key], str):
            raise bad_request(f"meta {key!r} must be a string.")
    return meta


# --------------------------------------------------------------------------
# The graph
# --------------------------------------------------------------------------


def _graph_tensors(graph: onnx.GraphProto) -> Iterator[onnx.TensorProto]:
    """Every tensor a graph holds, including those in subgraphs and attributes."""
    yield from graph.initializer
    for sparse in graph.sparse_initializer:
        yield sparse.values
        yield sparse.indices
    for node in graph.node:
        yield from _node_tensors(node)


def _node_tensors(node: onnx.NodeProto) -> Iterator[onnx.TensorProto]:
    for attr in node.attribute:
        if attr.HasField("t"):
            yield attr.t
        yield from attr.tensors
        if attr.HasField("sparse_tensor"):
            yield attr.sparse_tensor.values
            yield attr.sparse_tensor.indices
        for sparse in attr.sparse_tensors:
            yield sparse.values
            yield sparse.indices
        if attr.HasField("g"):
            yield from _graph_tensors(attr.g)
        for sub in attr.graphs:
            yield from _graph_tensors(sub)


def _all_tensors(model: onnx.ModelProto) -> Iterator[onnx.TensorProto]:
    yield from _graph_tensors(model.graph)
    for function in model.functions:
        for node in function.node:
            yield from _node_tensors(node)


def _tensor_type(value: onnx.ValueInfoProto) -> onnx.TypeProto.Tensor | None:
    return value.type.tensor_type if value.type.HasField("tensor_type") else None


def _check_inputs(model: onnx.ModelProto, meta: dict[str, Any]) -> None:
    initializers = {t.name for t in model.graph.initializer}
    inputs = [i for i in model.graph.input if i.name not in initializers]
    names = [i.name for i in inputs]
    if sorted(names) != sorted(meta["features"]):
        raise unprocessable(
            f"The model's inputs {names} must be exactly the features {meta['features']}, one input per feature."
        )
    for value in inputs:
        tensor = _tensor_type(value)
        want = onnx.TensorProto.FLOAT if meta["feature_types"][value.name] == NUMBER else onnx.TensorProto.STRING
        if tensor is None or tensor.elem_type != want:
            kind = "float" if want == onnx.TensorProto.FLOAT else "string"
            raise unprocessable(f"The model's input {value.name!r} must be a {kind} tensor.")
        dims = tensor.shape.dim
        if tensor.HasField("shape") and (len(dims) != 2 or (dims[1].HasField("dim_value") and dims[1].dim_value != 1)):
            raise unprocessable(f"The model's input {value.name!r} must have shape [n, 1].")


def _check_outputs(model: onnx.ModelProto, meta: dict[str, Any]) -> None:
    outputs = list(model.graph.output)
    if meta["task"] == "classification":
        if len(outputs) < 2:
            raise unprocessable("A classifier must output a label tensor and then a probabilities tensor.")
        label, proba = _tensor_type(outputs[0]), _tensor_type(outputs[1])
        ints = all(isinstance(v, int) for v in meta["labels"])
        want = _INTS if ints else {onnx.TensorProto.STRING}
        if label is None or label.elem_type not in want:
            raise unprocessable(f"The model's first output must be a {'integer' if ints else 'string'} label tensor.")
        if proba is None or proba.elem_type not in _FLOATS:
            raise unprocessable(
                "The model's second output must be a float tensor of probabilities [n, labels], "
                "not a map (export a classifier without ZipMap)."
            )
    else:
        if not outputs or (value := _tensor_type(outputs[0])) is None or value.elem_type not in _FLOATS:
            raise unprocessable("A regressor's first output must be a float tensor of values.")


def _smoke_frame(meta: dict[str, Any]) -> pd.DataFrame:
    fill = meta.get("fill", {})
    return pd.DataFrame(
        {
            f: [float(fill.get(f, 0.0))] * SMOKE_ROWS if meta["feature_types"][f] == NUMBER else [""] * SMOKE_ROWS
            for f in meta["features"]
        }
    )


def check_onnx(
    model_bytes: bytes, meta: dict[str, Any], frame: pd.DataFrame | None = None
) -> tuple[np.ndarray, np.ndarray | None]:
    """Check the graph against meta and run it on `frame` (or two filler rows).

    Returns what run_model returns for those rows. Raises 422 when the graph
    breaks the contract; meta is assumed to have passed check_meta.
    """
    try:
        model = onnx.load_from_string(model_bytes)
    except Exception:  # protobuf raises DecodeError and friends
        raise unprocessable("The model is not a valid ONNX model.") from None
    if not model.HasField("graph"):
        raise unprocessable("The model is not a valid ONNX model: it has no graph.")
    for tensor in _all_tensors(model):
        if tensor.data_location == onnx.TensorProto.EXTERNAL or len(tensor.external_data) > 0:
            raise unprocessable(f"The model's tensor {tensor.name!r} keeps its data in an external file; external data is not allowed.")
    try:
        onnx.checker.check_model(model)
    except Exception as exc:
        raise unprocessable(f"The model is not a valid ONNX model: {exc}") from None
    _check_inputs(model, meta)
    _check_outputs(model, meta)

    if frame is None:
        frame = _smoke_frame(meta)
    n = len(frame)
    labels, probabilities = run_isolated(model_bytes, frame, meta)

    if len(labels) != n:
        raise unprocessable(f"The model returned {len(labels)} predictions for {n} rows.")
    if meta["task"] == "classification":
        k = len(meta["labels"])
        if probabilities.ndim != 2 or probabilities.shape != (n, k):
            raise unprocessable(
                f"The model's probabilities have shape {list(probabilities.shape)}; "
                f"with {k} labels they must be [{n}, {k}], one column per label in spec['labels'] order."
            )
        if not np.all(np.isfinite(probabilities)):
            raise unprocessable("The model's probabilities are not all finite.")
        known = {str(v) for v in meta["labels"]}
        if not all(str(v.item() if hasattr(v, "item") else v) in known for v in labels):
            raise unprocessable("The model predicted a label that is not in spec['labels'].")
    elif labels.dtype.kind not in "fiu" or not np.all(np.isfinite(labels)):
        raise unprocessable("The model's predictions are not all finite numbers.")
    return labels, probabilities


def run_isolated(model_bytes: bytes, frame: pd.DataFrame, meta: dict[str, Any]) -> tuple[np.ndarray, np.ndarray | None]:
    """Run the graph on `frame` in onnx_child.py; return (labels, probabilities)."""
    workdir = Path(tempfile.mkdtemp(prefix="hermod-onnx-"))
    try:
        (workdir / "model.onnx").write_bytes(model_bytes)
        job = {
            "limits": {"cpu_seconds": RUN_CPU_SECONDS, "memory_mb": RUN_MEMORY_MB},
            "task": meta["task"],
            "features": meta["features"],
            "numeric": {f: meta["feature_types"][f] == NUMBER for f in meta["features"]},
            "columns": {f: frame[f].tolist() for f in meta["features"]},
        }
        (workdir / "job.json").write_text(json.dumps(job), encoding="utf-8")
        try:
            proc = subprocess.run(
                [sys.executable, "-I", "-B", str(ONNX_CHILD), str(workdir)],
                stdin=subprocess.DEVNULL,
                capture_output=True,
                cwd=workdir,
                env={"PATH": "/usr/local/bin:/usr/bin:/bin", "LANG": "C.UTF-8", "OMP_NUM_THREADS": "1"},
                timeout=RUN_TIMEOUT_SECONDS,
            )
        except subprocess.TimeoutExpired:
            raise unprocessable(f"The model did not finish a prediction in {RUN_TIMEOUT_SECONDS} seconds.") from None
        if proc.returncode != 0:
            if proc.returncode < 0:
                raise unprocessable(f"The model crashed onnxruntime ({signal.Signals(-proc.returncode).name}).")
            last = (proc.stderr.decode("utf-8", errors="replace").strip().splitlines() or ["no output"])[-1]
            raise unprocessable(f"The model does not run in onnxruntime: {last[-500:]}")
        result = json.loads((workdir / "result.json").read_text(encoding="utf-8"))
    finally:
        shutil.rmtree(workdir, ignore_errors=True)
    labels = np.asarray(result["labels"], dtype=object if meta["task"] == "classification" else np.float64)
    probabilities = None
    if meta["task"] == "classification":
        try:
            probabilities = np.asarray(result["probabilities"], dtype=np.float64)
        except ValueError:
            raise unprocessable("The model's probabilities are not a rectangular [n, labels] array.") from None
    return labels, probabilities
