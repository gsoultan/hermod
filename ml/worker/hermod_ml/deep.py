"""Deep-learning algorithms: a small multilayer perceptron in PyTorch or Keras.

Both are tabular models over the same preprocessing as the other
algorithms (scaled numbers, one-hot strings), so they slot into the same
scikit-learn pipeline as its final step. Each estimator here trains with its
framework and exports only the network to ONNX; training.py exports the
preprocessing with skl2onnx and joins the two into one graph that takes one
tensor per feature, like every other model.

A network trains in float32 on at most `params.threads` CPU threads. Its
joined graph is optimised offline by ONNX Runtime and, unless float32 is
asked for, quantised to int8 weights (dynamic quantisation of its MatMuls),
which it keeps when it scores within a tolerance of float32 on the rows held
back from training (settle_precision).

torch and tensorflow are optional: only the "-dl" image installs them
(requirements-dl.txt). They are imported when a training needs them, never at
start-up, so the slim image runs this module without them.
"""

from __future__ import annotations

import importlib.util
import io
import math
import os
import tempfile
import warnings
from collections.abc import Callable
from pathlib import Path
from typing import Any

import numpy as np
import onnx
import onnxruntime as ort
from onnx import TensorProto, helper, numpy_helper
from sklearn.base import BaseEstimator

from .errors import bad_request

ALGORITHMS = ("pytorch_mlp", "keras_mlp")

# What each algorithm imports. tf2onnx exports the Keras network to ONNX.
MODULES = {"pytorch_mlp": ("torch",), "keras_mlp": ("tensorflow", "keras", "tf2onnx")}

# The opset every part of a deep-learning graph is exported at, so the
# preprocessing graph and the network graph can be merged.
OPSET = 17

DEFAULT_PARAMS: dict[str, Any] = {
    "hidden_layers": [64, 32],
    "epochs": 200,
    "batch_size": 32,
    "learning_rate": 0.001,
    "patience": 10,
}

# How the network is trained and kept, beside its hyperparameters: the
# precision of its weights (auto, float32, int8) and the CPU threads its
# training uses (0 for every CPU but one). The version records them under
# "efficiency", not "params".
EFFICIENCY_DEFAULTS: dict[str, Any] = {"precision": "auto", "threads": 0}
PRECISIONS = ("auto", "float32", "int8")
MAX_THREADS = 256

# How many values of a text column a network one-hot encodes: the most
# frequent, as the built-in engine does (learn.MaxCategories). Any other
# value encodes as all zeros, like a value never seen in training. It keeps
# an ID-like column from making the network's input, and the matrix it
# trains on, thousands of columns wide.
MAX_CATEGORIES = 100

# The tolerance int8 weights are kept within, the built-in engine's: an
# accuracy at most half a point below float32's, or an RMSE at most 1% above.
ACCURACY_TOLERANCE = 0.005
RMSE_TOLERANCE = 0.01

# Bounds on what a caller may ask for. A training holds a worker slot until it
# finishes, so the largest network and the most epochs are capped.
MAX_LAYERS = 5
MAX_UNITS = 1024
MAX_EPOCHS = 1000
MAX_BATCH = 4096
MAX_PATIENCE = 1000

# Rows held out of the training split to decide when to stop.
VALIDATION_FRACTION = 0.1
MIN_ROWS_FOR_VALIDATION = 10


def unavailable(algorithm: str) -> str | None:
    """Why `algorithm` cannot train in this image, or None when it can."""
    for module in MODULES[algorithm]:
        if importlib.util.find_spec(module) is None:
            return f"the {module} package is not installed"
    return None


# --------------------------------------------------------------------------
# Hyperparameters
# --------------------------------------------------------------------------


def _int(params: dict[str, Any], key: str, low: int, high: int) -> int:
    value = params[key]
    if isinstance(value, bool) or not isinstance(value, int) or not low <= value <= high:
        raise bad_request(f"'params.{key}' must be an integer from {low} to {high}.")
    return value


def parse_params(raw: Any) -> dict[str, Any]:
    """Validate a train request's `params` and fill in the defaults."""
    if raw is None:
        raw = {}
    if not isinstance(raw, dict):
        raise bad_request("'params' must be an object.")
    allowed = {**DEFAULT_PARAMS, **EFFICIENCY_DEFAULTS}
    unknown = sorted(set(raw) - set(allowed))
    if unknown:
        raise bad_request(f"'params' has unknown keys: {', '.join(unknown)}; allowed are {', '.join(allowed)}.")
    params = {**allowed, **raw}

    layers = params["hidden_layers"]
    if (
        not isinstance(layers, list)
        or not 1 <= len(layers) <= MAX_LAYERS
        or not all(isinstance(n, int) and not isinstance(n, bool) and 1 <= n <= MAX_UNITS for n in layers)
    ):
        raise bad_request(
            f"'params.hidden_layers' must list 1 to {MAX_LAYERS} layer sizes, each an integer from 1 to {MAX_UNITS}."
        )
    rate = params["learning_rate"]
    if isinstance(rate, bool) or not isinstance(rate, (int, float)) or not 0 < rate <= 1:
        raise bad_request("'params.learning_rate' must be a number above 0 and at most 1.")
    if params["precision"] not in PRECISIONS:
        raise bad_request(f"'params.precision' must be one of {', '.join(PRECISIONS)}.")
    return {
        "hidden_layers": list(layers),
        "epochs": _int(params, "epochs", 1, MAX_EPOCHS),
        "batch_size": _int(params, "batch_size", 1, MAX_BATCH),
        "learning_rate": float(rate),
        "patience": _int(params, "patience", 1, MAX_PATIENCE),
        "precision": params["precision"],
        "threads": _int(params, "threads", 0, MAX_THREADS),
    }


def top_categories(values) -> list[str]:
    """The MAX_CATEGORIES most frequent values of a text column, sorted.
    Ties at the cut go to the smaller value, so the same rows always encode
    the same."""
    counts: dict[str, int] = {}
    for v in values:
        counts[v] = counts.get(v, 0) + 1
    ranked = sorted(counts, key=lambda c: (-counts[c], c))
    return sorted(ranked[:MAX_CATEGORIES])


def network_params(params: dict[str, Any]) -> dict[str, Any]:
    """The network's hyperparameters, as a version records them."""
    return {k: params[k] for k in DEFAULT_PARAMS}


def estimator_params(params: dict[str, Any]) -> dict[str, Any]:
    """What an estimator takes: the hyperparameters and its threads."""
    return {**network_params(params), "threads": params["threads"]}


def cpu_count() -> int:
    """The CPUs this process may run on (its affinity mask, where the
    platform has one)."""
    if hasattr(os, "sched_getaffinity"):
        return max(1, len(os.sched_getaffinity(0)))
    return os.cpu_count() or 1


def resolve_threads(requested: int, cap: int) -> int:
    """The threads a training uses: those asked for, or every CPU but one,
    and at most `cap` (HERMOD_ML_TRAIN_THREADS; 0 for none)."""
    threads = requested if requested > 0 else cpu_count() - 1
    if cap > 0:
        threads = min(threads, cap)
    return max(1, threads)


# --------------------------------------------------------------------------
# Estimators
# --------------------------------------------------------------------------


def make_estimator(algorithm: str, task: str, params: dict[str, Any], seed: int):
    """A scikit-learn style estimator for `algorithm`, or 400 when this image lacks it."""
    reason = unavailable(algorithm)
    if reason is not None:
        raise bad_request(
            f"Algorithm '{algorithm}' is not available in this image: {reason}. "
            "Run the hermod-ml '-dl' image to train deep-learning models."
        )
    cls = TorchMLP if algorithm == "pytorch_mlp" else KerasMLP
    return cls(task=task, seed=seed, **estimator_params(params))


class _MLP(BaseEstimator):
    """What the two frameworks share: target encoding, the validation split,
    and the scikit-learn methods the pipeline and the metrics call.

    Classification learns class indices and reports the original labels
    through classes_. Regression learns a standardised target; the exported
    graph scales it back, so ONNX returns values in the target's own units.
    """

    def __init__(self, task: str, seed: int, hidden_layers, epochs, batch_size, learning_rate, patience, threads=1) -> None:
        self.task = task
        self.seed = seed
        self.hidden_layers = hidden_layers
        self.epochs = epochs
        self.batch_size = batch_size
        self.learning_rate = learning_rate
        self.patience = patience
        self.threads = threads

    def fit(self, X, y):
        X = np.asarray(X, dtype=np.float32)
        if self.task == "classification":
            self.classes_, codes = np.unique(np.asarray(y), return_inverse=True)
            target = codes.astype(np.int64)
            outputs = len(self.classes_)
        else:
            y = np.asarray(y, dtype=np.float64)
            self.mean_ = float(y.mean())
            std = float(y.std())
            self.std_ = std if std > 0 and math.isfinite(std) else 1.0
            target = ((y - self.mean_) / self.std_).astype(np.float32).reshape(-1, 1)
            outputs = 1

        train_idx, val_idx = self._split(len(X))
        # The rows held back, by position in X: the int8 check scores on them.
        self.validation_rows_ = val_idx
        self.n_inputs_ = X.shape[1]
        self._fit(X[train_idx], target[train_idx], X[val_idx], target[val_idx], outputs)
        return self

    def _split(self, n: int) -> tuple[np.ndarray, np.ndarray]:
        """Training and validation row indices. Small sets train on every row
        for all epochs: a validation set of one or two rows would stop
        training on noise."""
        order = np.random.default_rng(self.seed).permutation(n)
        if n < MIN_ROWS_FOR_VALIDATION:
            return order, order[:0]
        n_val = max(1, int(round(n * VALIDATION_FRACTION)))
        return order[n_val:], order[:n_val]

    def predict_proba(self, X) -> np.ndarray:
        return np.asarray(self._forward(np.asarray(X, dtype=np.float32)), dtype=np.float64)

    def predict(self, X) -> np.ndarray:
        out = self._forward(np.asarray(X, dtype=np.float32))
        if self.task == "classification":
            return self.classes_[np.argmax(out, axis=1)]
        return np.asarray(out, dtype=np.float64).ravel()


class TorchMLP(_MLP):
    def _fit(self, X, y, X_val, y_val, outputs: int) -> None:
        import torch

        # torch's thread count is the process's: set it for this training,
        # then put it back.
        previous = torch.get_num_threads()
        torch.set_num_threads(max(1, int(self.threads)))
        try:
            self._train(X, y, X_val, y_val, outputs)
        finally:
            torch.set_num_threads(previous)

    def _train(self, X, y, X_val, y_val, outputs: int) -> None:
        import torch
        from torch import nn

        torch.manual_seed(self.seed)
        layers: list[nn.Module] = []
        width = X.shape[1]
        for units in self.hidden_layers:
            layers += [nn.Linear(width, units), nn.ReLU()]
            width = units
        layers.append(nn.Linear(width, outputs))
        net = nn.Sequential(*layers)
        loss_fn = nn.CrossEntropyLoss() if self.task == "classification" else nn.MSELoss()
        optimizer = torch.optim.Adam(net.parameters(), lr=self.learning_rate)

        X_t, y_t = torch.from_numpy(X), torch.from_numpy(y)
        X_v, y_v = torch.from_numpy(X_val), torch.from_numpy(y_val)
        generator = torch.Generator().manual_seed(self.seed)
        best_loss, best_state, waited = math.inf, None, 0
        for _ in range(self.epochs):
            net.train()
            for batch in torch.randperm(len(X_t), generator=generator).split(self.batch_size):
                optimizer.zero_grad()
                loss = loss_fn(net(X_t[batch]), y_t[batch])
                loss.backward()
                optimizer.step()
            if len(X_v) == 0:
                continue
            net.eval()
            with torch.no_grad():
                val_loss = float(loss_fn(net(X_v), y_v))
            if val_loss < best_loss:
                best_loss, waited = val_loss, 0
                best_state = {k: v.detach().clone() for k, v in net.state_dict().items()}
            else:
                waited += 1
                if waited >= self.patience:
                    break
        if best_state is not None:
            net.load_state_dict(best_state)
        net.eval()
        self.net_ = net

    def _head(self):
        """The network plus its output step: softmax, or scaling the target back."""
        import torch
        from torch import nn

        task, mean, std = self.task, getattr(self, "mean_", 0.0), getattr(self, "std_", 1.0)

        class Head(nn.Module):
            def __init__(self, net) -> None:
                super().__init__()
                self.net = net

            def forward(self, x):
                out = self.net(x)
                if task == "classification":
                    return torch.softmax(out, dim=1)
                return out * std + mean

        return Head(self.net_).eval()

    def _forward(self, X: np.ndarray) -> np.ndarray:
        import torch

        with torch.no_grad():
            return self._head()(torch.from_numpy(X)).numpy()

    def to_onnx(self) -> onnx.ModelProto:
        import torch

        buffer = io.BytesIO()
        with warnings.catch_warnings():
            # The TorchScript exporter is deprecated in favour of the dynamo
            # one, which needs onnxscript; this MLP needs neither's extras.
            warnings.simplefilter("ignore")
            torch.onnx.export(
                self._head(),
                (torch.zeros(1, self.n_inputs_),),
                buffer,
                input_names=["x"],
                output_names=["y"],
                dynamic_axes={"x": {0: "n"}, "y": {0: "n"}},
                opset_version=OPSET,
                dynamo=False,
            )
        return onnx.load_from_string(buffer.getvalue())


class KerasMLP(_MLP):
    def _fit(self, X, y, X_val, y_val, outputs: int) -> None:
        import keras

        keras.utils.set_random_seed(self.seed)
        classify = self.task == "classification"
        model = keras.Sequential(
            [keras.Input((X.shape[1],))]
            + [keras.layers.Dense(units, activation="relu") for units in self.hidden_layers]
            + [keras.layers.Dense(outputs, activation="softmax" if classify else None)]
        )
        model.compile(
            optimizer=keras.optimizers.Adam(learning_rate=self.learning_rate),
            loss="sparse_categorical_crossentropy" if classify else "mean_squared_error",
        )
        callbacks, validation = [], None
        if len(X_val):
            validation = (X_val, y_val)
            callbacks.append(keras.callbacks.EarlyStopping(patience=self.patience, restore_best_weights=True))
        model.fit(
            X, y, validation_data=validation, epochs=self.epochs, batch_size=self.batch_size,
            shuffle=True, verbose=0, callbacks=callbacks,
        )
        self.model_ = model

    def _forward(self, X: np.ndarray) -> np.ndarray:
        out = np.asarray(self.model_(X, training=False))
        if self.task == "classification":
            return out
        return out * np.float32(self.std_) + np.float32(self.mean_)

    def to_onnx(self) -> onnx.ModelProto:
        import tensorflow as tf
        import tf2onnx

        model, task = self.model_, self.task
        mean, std = np.float32(getattr(self, "mean_", 0.0)), np.float32(getattr(self, "std_", 1.0))

        def head(x):
            out = model(x, training=False)
            return out if task == "classification" else out * std + mean

        spec = [tf.TensorSpec([None, self.n_inputs_], tf.float32, name="x")]
        proto, _ = tf2onnx.convert.from_function(tf.function(head), input_signature=spec, opset=OPSET)
        return proto


# --------------------------------------------------------------------------
# Joining the preprocessing graph and the network
# --------------------------------------------------------------------------


def _align_opsets(a: onnx.ModelProto, b: onnx.ModelProto) -> None:
    """Give both models the same opset imports, as merging requires.

    Domains no node uses are dropped first (tf2onnx always imports
    ai.onnx.ml). skl2onnx imports the lowest opset its operators need, below
    the one asked for; raising it to the network's is safe because the
    preprocessing operators (Scaler, OneHotEncoder, Cast, Concat, Reshape)
    mean the same at every opset from 13 to OPSET.
    """
    versions: dict[str, int] = {}
    for model in (a, b):
        used = {n.domain for n in model.graph.node} | {""}
        for o in model.opset_import:
            if o.domain in used:
                versions[o.domain] = max(versions.get(o.domain, 0), o.version)
    for model in (a, b):
        del model.opset_import[:]
        model.opset_import.extend(helper.make_opsetid(d, v) for d, v in sorted(versions.items()))


def compose(prep: onnx.ModelProto, network: onnx.ModelProto, task: str, labels: list[Any] | None) -> onnx.ModelProto:
    """One graph: preprocessing, then the network, then the usual outputs.

    `prep` takes the feature tensors and gives one float matrix; `network`
    takes that matrix and gives probabilities [n, k] or values [n, 1]. The
    result outputs (label, probabilities) like a scikit-learn classifier or
    (variable) like a regressor. Every internal name is prefixed so nothing
    clashes; training renames the inputs back to the feature names.
    """
    prep = onnx.compose.add_prefix(prep, "prep_")
    network = onnx.compose.add_prefix(network, "nn_")
    _align_opsets(prep, network)
    ir =max(prep.ir_version, network.ir_version)
    prep.ir_version = network.ir_version = ir
    if len(prep.graph.output) != 1 or len(network.graph.input) != 1 or len(network.graph.output) != 1:
        raise ValueError("unexpected graph shape for a deep-learning export")
    merged = onnx.compose.merge_models(
        prep, network, io_map=[(prep.graph.output[0].name, network.graph.input[0].name)]
    )

    graph = merged.graph
    result = graph.output[0].name
    del graph.output[:]
    if task == "classification":
        values = np.asarray(labels)
        if values.dtype.kind in "iu":
            const, elem = numpy_helper.from_array(values.astype(np.int64), "hermod_labels"), TensorProto.INT64
        else:
            const = helper.make_tensor("hermod_labels", TensorProto.STRING, [len(labels)], [str(v).encode() for v in labels])
            elem = TensorProto.STRING
        graph.initializer.append(const)
        graph.node.extend([
            helper.make_node("Identity", [result], ["probabilities"], name="hermod_probabilities"),
            helper.make_node("ArgMax", [result], ["hermod_index"], name="hermod_argmax", axis=1, keepdims=0),
            helper.make_node("Gather", ["hermod_labels", "hermod_index"], ["label"], name="hermod_label", axis=0),
        ])
        graph.output.extend([
            helper.make_tensor_value_info("label", elem, [None]),
            helper.make_tensor_value_info("probabilities", TensorProto.FLOAT, [None, len(labels)]),
        ])
    else:
        graph.node.append(helper.make_node("Identity", [result], ["variable"], name="hermod_variable"))
        graph.output.append(helper.make_tensor_value_info("variable", TensorProto.FLOAT, [None, 1]))
    return merged


# --------------------------------------------------------------------------
# Cheaper graphs: offline optimisation and int8 weights
# --------------------------------------------------------------------------


def _session_options(threads: int = 0) -> ort.SessionOptions:
    options = ort.SessionOptions()
    if threads > 0:
        options.intra_op_num_threads = threads
    return options


def optimize(model_bytes: bytes) -> bytes:
    """The graph after ONNX Runtime's basic optimisations (constant folding,
    redundant nodes removed), saved: it holds standard operators only, so
    any runtime still reads it, and it loads without redoing them."""
    options = _session_options(1)
    options.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_BASIC
    with tempfile.TemporaryDirectory() as tmp:
        path = Path(tmp) / "optimized.onnx"
        options.optimized_model_filepath = str(path)
        ort.InferenceSession(model_bytes, options, providers=["CPUExecutionProvider"])
        optimized = onnx.load_from_string(path.read_bytes())
    # ONNX Runtime saves an import of every domain it knows, some twice;
    # keep one of each domain the graph uses, as the quantiser requires.
    versions: dict[str, int] = {}
    used = {n.domain for n in optimized.graph.node} | {""}
    for o in optimized.opset_import:
        if o.domain in used:
            versions[o.domain] = max(versions.get(o.domain, 0), o.version)
    del optimized.opset_import[:]
    optimized.opset_import.extend(helper.make_opsetid(d, v) for d, v in sorted(versions.items()))
    return optimized.SerializeToString()


def gemm_to_matmul(model: onnx.ModelProto) -> None:
    """Rewrite each Gemm whose weights are a constant (torch exports a
    linear layer as one) as MatMul then Add, which compute the same: ONNX
    Runtime's dynamic quantisation quantises MatMul, not Gemm."""
    inits = {t.name: t for t in model.graph.initializer}
    nodes = []
    for node in model.graph.node:
        attrs = {a.name: helper.get_attribute_value(a) for a in node.attribute}
        plain = attrs.get("alpha", 1.0) == 1.0 and attrs.get("beta", 1.0) == 1.0 and not attrs.get("transA", 0)
        if node.op_type != "Gemm" or node.domain not in ("", "ai.onnx") or not plain or node.input[1] not in inits:
            nodes.append(node)
            continue
        weights = numpy_helper.to_array(inits[node.input[1]])
        if attrs.get("transB", 0):
            weights = weights.T
        name = f"{node.input[1]}_matmul"
        model.graph.initializer.append(numpy_helper.from_array(np.ascontiguousarray(weights), name))
        has_bias = len(node.input) > 2 and node.input[2] != ""
        base = node.name or node.output[0]
        product = f"{node.output[0]}_matmul" if has_bias else node.output[0]
        nodes.append(helper.make_node("MatMul", [node.input[0], name], [product], name=f"{base}_matmul"))
        if has_bias:
            nodes.append(helper.make_node("Add", [product, node.input[2]], [node.output[0]], name=f"{base}_bias"))
    del model.graph.node[:]
    model.graph.node.extend(nodes)
    used = {i for n in model.graph.node for i in n.input}
    kept = [t for t in model.graph.initializer if t.name in used]
    del model.graph.initializer[:]
    model.graph.initializer.extend(kept)


def quantize(model_bytes: bytes) -> bytes:
    """The graph with int8 weights: ONNX Runtime's dynamic quantisation,
    signed, one scale per weight matrix. Activations stay float32 and are
    quantised as each MatMul runs."""
    from onnxruntime.quantization import QuantType, quantize_dynamic

    model = onnx.load_from_string(model_bytes)
    gemm_to_matmul(model)
    with tempfile.TemporaryDirectory() as tmp:
        path = Path(tmp) / "int8.onnx"
        # Shape inference stops at the preprocessing's ai.onnx.ml operators;
        # every tensor a MatMul here takes is float.
        quantize_dynamic(
            model, path, op_types_to_quantize=["MatMul"], per_channel=False, weight_type=QuantType.QInt8,
            extra_options={"DefaultTensorType": TensorProto.FLOAT},
        )
        return path.read_bytes()


def quant_check(task: str, f32: float, i8: float) -> dict[str, Any]:
    """What the int8 graph scored against the float32 one, and the tolerance."""
    if task == "regression":
        return {"metric": "rmse", "float32": f32, "int8": i8, "tolerance": RMSE_TOLERANCE}
    return {"metric": "accuracy", "float32": f32, "int8": i8, "tolerance": ACCURACY_TOLERANCE}


def choose_precision(requested: str, check: dict[str, Any]) -> str:
    """float32 and int8 as asked; for auto, int8 when it scored within the
    tolerance."""
    if requested in ("float32", "int8"):
        return requested
    slack = 1e-12
    if check["metric"] == "rmse":
        ok = check["int8"] <= check["float32"] * (1 + check["tolerance"]) + slack
    else:
        ok = check["int8"] >= check["float32"] - check["tolerance"] - slack
    return "int8" if ok else "float32"


def settle_precision(
    model_bytes: bytes, requested: str, task: str, threads: int, score: Callable[[bytes], float]
) -> tuple[bytes, dict[str, Any]]:
    """The graph to keep and the version's "efficiency": unless float32 was
    asked for, the int8 graph is scored against the float32 one (`score`
    measures a graph on the rows held back) and kept per choose_precision."""
    efficiency: dict[str, Any] = {"requested": requested, "precision": "float32", "threads": threads}
    if requested == "float32":
        return model_bytes, efficiency
    small = quantize(model_bytes)
    check = quant_check(task, float(score(model_bytes)), float(score(small)))
    efficiency.update(check, precision=choose_precision(requested, check))
    return (small if efficiency["precision"] == "int8" else model_bytes), efficiency
