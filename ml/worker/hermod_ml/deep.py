"""Deep-learning algorithms: a small multilayer perceptron in PyTorch or Keras.

Both are tabular models over the same preprocessing as the other
algorithms (scaled numbers, one-hot strings), so they slot into the same
scikit-learn pipeline as its final step. Each estimator here trains with its
framework and exports only the network to ONNX; training.py exports the
preprocessing with skl2onnx and joins the two into one graph that takes one
tensor per feature, like every other model.

torch and tensorflow are optional: only the "-dl" image installs them
(requirements-dl.txt). They are imported when a training needs them, never at
start-up, so the slim image runs this module without them.
"""

from __future__ import annotations

import importlib.util
import io
import math
import warnings
from typing import Any

import numpy as np
import onnx
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
    unknown = sorted(set(raw) - set(DEFAULT_PARAMS))
    if unknown:
        raise bad_request(f"'params' has unknown keys: {', '.join(unknown)}; allowed are {', '.join(DEFAULT_PARAMS)}.")
    params = {**DEFAULT_PARAMS, **raw}

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
    return {
        "hidden_layers": list(layers),
        "epochs": _int(params, "epochs", 1, MAX_EPOCHS),
        "batch_size": _int(params, "batch_size", 1, MAX_BATCH),
        "learning_rate": float(rate),
        "patience": _int(params, "patience", 1, MAX_PATIENCE),
    }


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
    return cls(task=task, seed=seed, **params)


class _MLP(BaseEstimator):
    """What the two frameworks share: target encoding, the validation split,
    and the scikit-learn methods the pipeline and the metrics call.

    Classification learns class indices and reports the original labels
    through classes_. Regression learns a standardised target; the exported
    graph scales it back, so ONNX returns values in the target's own units.
    """

    def __init__(self, task: str, seed: int, hidden_layers, epochs, batch_size, learning_rate, patience) -> None:
        self.task = task
        self.seed = seed
        self.hidden_layers = hidden_layers
        self.epochs = epochs
        self.batch_size = batch_size
        self.learning_rate = learning_rate
        self.patience = patience

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
