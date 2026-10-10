"""Model serving over the Open Inference Protocol (V2), one namespace per vhost.

Requests carry one column tensor per feature. Each tensor is coerced to the
type the model was trained with (number or string), nulls in numeric
features get the training median, and the ONNX session does the rest.

Loaded sessions live in a small LRU cache keyed by (vhost, model, version).
Only `model.onnx` files are ever loaded.
"""

from __future__ import annotations

import math
import threading
from collections import OrderedDict
from typing import Any

import numpy as np
import onnxruntime as ort

from .datasets import NUMBER, to_text
from .errors import bad_request
from .training import ModelStore, run_model

NUMERIC_DATATYPES = {"FP64", "FP32", "INT64", "INT32"}
DATATYPES = NUMERIC_DATATYPES | {"BOOL", "BYTES"}

CacheKey = tuple[str, str, str]


class ModelCache:
    """LRU cache of (session, meta) pairs with a fixed capacity."""

    def __init__(self, capacity: int) -> None:
        self.capacity = capacity
        self._items: OrderedDict[CacheKey, tuple[ort.InferenceSession, dict[str, Any]]] = OrderedDict()
        self._lock = threading.Lock()

    def get(self, key: CacheKey, load) -> tuple[ort.InferenceSession, dict[str, Any]]:
        with self._lock:
            if key in self._items:
                self._items.move_to_end(key)
                return self._items[key]
        # Load outside the lock so a slow load does not stall cache hits.
        value = load()
        with self._lock:
            if key not in self._items:
                self._items[key] = value
                while len(self._items) > self.capacity:
                    self._items.popitem(last=False)
            self._items.move_to_end(key)
            return self._items[key]

    def evict_model(self, vhost: str, name: str) -> None:
        with self._lock:
            for key in [k for k in self._items if k[0] == vhost and k[1] == name]:
                del self._items[key]

    def keys(self) -> list[CacheKey]:
        """Keys from least to most recently used."""
        with self._lock:
            return list(self._items)


def _label_datatype(meta: dict[str, Any]) -> str:
    labels = meta.get("labels") or []
    return "INT64" if labels and all(isinstance(v, int) and not isinstance(v, bool) for v in labels) else "BYTES"


def _flatten(data: list[Any], feature: str) -> list[Any]:
    """Accept flat data or [[v], [v], ...] for an [n, 1] tensor."""
    flat = []
    for value in data:
        if isinstance(value, list):
            if len(value) != 1:
                raise bad_request(f"Input {feature!r} must be a column: each row holds one value.")
            value = value[0]
        flat.append(value)
    return flat


def _number(value: Any, datatype: str, feature: str, row: int, fill: float) -> float:
    """Coerce one value for a numeric feature, or raise 400 naming feature and row."""
    if value is None:
        return fill
    if datatype in NUMERIC_DATATYPES:
        if isinstance(value, (int, float)) and not isinstance(value, bool):
            return float(value)
    elif datatype == "BOOL":
        if isinstance(value, bool):
            return 1.0 if value else 0.0
        if isinstance(value, int) and value in (0, 1):
            return float(value)
    elif datatype == "BYTES":
        # A BYTES tensor in JSON may mix types: a caller that sends a column
        # holding a null as BYTES sends its numbers as numbers.
        if isinstance(value, bool):
            return 1.0 if value else 0.0
        if isinstance(value, (int, float)) and math.isfinite(value):
            return float(value)
        if isinstance(value, str):
            try:
                number = float(value.strip())
            except ValueError:
                number = math.nan
            if math.isfinite(number):
                return number
    raise bad_request(f"Input {feature!r} row {row}: {value!r} is not a valid number for a {datatype} tensor.")


def _text(value: Any) -> str:
    if value is None:
        return ""
    return value if isinstance(value, str) else to_text(value)


class ModelServer:
    """Answers OIP metadata and infer calls for models in a ModelStore."""

    def __init__(self, models: ModelStore, capacity: int) -> None:
        self.models = models
        self.cache = ModelCache(capacity)

    def evict(self, vhost: str, name: str) -> None:
        self.cache.evict_model(vhost, name)

    def _session(self, vhost: str, name: str, version: str) -> tuple[ort.InferenceSession, dict[str, Any]]:
        def load():
            meta = self.models.meta(vhost, name, version)
            path = self.models.onnx_path(vhost, name, version)
            session = ort.InferenceSession(str(path), providers=["CPUExecutionProvider"])
            return session, meta

        return self.cache.get((vhost, name, version), load)

    # -- metadata ------------------------------------------------------------

    def metadata(self, vhost: str, name: str, version: str | None) -> dict[str, Any]:
        resolved = self.models.resolve(vhost, name, version)
        versions = self.models.version_names(vhost, name) if version is None else [resolved]
        meta = self.models.meta(vhost, name, resolved)
        inputs = [
            {"name": f, "datatype": "FP64" if meta["feature_types"][f] == NUMBER else "BYTES", "shape": [-1]}
            for f in meta["features"]
        ]
        if meta["task"] == "classification":
            outputs = [
                {"name": "label", "datatype": _label_datatype(meta), "shape": [-1]},
                {"name": "probability", "datatype": "FP64", "shape": [-1]},
            ]
        else:
            outputs = [{"name": "value", "datatype": "FP64", "shape": [-1]}]
        return {"name": name, "versions": versions, "platform": "onnx", "inputs": inputs, "outputs": outputs}

    # -- infer ---------------------------------------------------------------

    def infer(self, vhost: str, name: str, version: str | None, body: dict[str, Any]) -> dict[str, Any]:
        resolved = self.models.resolve(vhost, name, version)
        session, meta = self._session(vhost, name, resolved)
        tensors, n = self._parse_inputs(body.get("inputs"), set(meta["features"]))

        feed: dict[str, np.ndarray] = {}
        for feature in meta["features"]:
            if feature not in tensors:
                raise bad_request(f"Input {feature!r} is missing; the model needs it as a feature.")
            datatype, values = tensors[feature]
            if meta["feature_types"][feature] == NUMBER:
                fill = float(meta["fill"].get(feature, 0.0))
                column = [_number(v, datatype, feature, row, fill) for row, v in enumerate(values)]
                feed[feature] = np.asarray(column, dtype=np.float32).reshape(-1, 1)
            else:
                feed[feature] = np.asarray([_text(v) for v in values], dtype=object).reshape(-1, 1)

        result, probabilities = run_model(session, feed, meta)
        if meta["task"] == "classification":
            label_type = _label_datatype(meta)
            index = {label: i for i, label in enumerate(meta["labels"])}
            labels = [int(v) if label_type == "INT64" else str(v) for v in result.tolist()]
            probability = [float(probabilities[row, index[label]]) for row, label in enumerate(labels)]
            outputs = [
                {"name": "label", "datatype": label_type, "shape": [n], "data": labels},
                {"name": "probability", "datatype": "FP64", "shape": [n], "data": probability},
            ]
        else:
            outputs = [{"name": "value", "datatype": "FP64", "shape": [n], "data": [float(v) for v in result]}]

        response: dict[str, Any] = {"model_name": name, "model_version": resolved, "outputs": outputs}
        if isinstance(body.get("id"), str):
            response["id"] = body["id"]
        return response

    @staticmethod
    def _parse_inputs(inputs: Any, features: set[str]) -> tuple[dict[str, tuple[str, list[Any]]], int]:
        """Validate the request tensors. Returns ({feature: (datatype, values)}, n).

        Every input must name itself and give a shape whose first dimension
        is the shared row count. Only inputs that are features are checked in
        full; the rest are ignored.
        """
        if not isinstance(inputs, list) or not inputs:
            raise bad_request("'inputs' must be a non-empty array of tensors.")
        tensors: dict[str, tuple[str, list[Any]]] = {}
        seen: set[str] = set()
        n: int | None = None
        for i, tensor in enumerate(inputs):
            if not isinstance(tensor, dict) or not isinstance(tensor.get("name"), str):
                raise bad_request(f"Input {i} must be an object with a 'name'.")
            name = tensor["name"]
            if name in seen:
                raise bad_request(f"Input {name!r} appears more than once.")
            seen.add(name)
            shape = tensor.get("shape")
            if (
                not isinstance(shape, list)
                or not shape
                or not all(isinstance(d, int) and not isinstance(d, bool) and d >= 0 for d in shape)
            ):
                raise bad_request(f"Input {name!r} needs a 'shape' of non-negative integers.")
            if n is None:
                n = shape[0]
            elif shape[0] != n:
                raise bad_request(f"Input {name!r} has {shape[0]} rows but earlier inputs have {n}; all inputs must have the same length.")
            if name not in features:
                continue
            if len(shape) > 2 or (len(shape) == 2 and shape[1] != 1):
                raise bad_request(f"Input {name!r} must have shape [n] or [n, 1].")
            datatype = tensor.get("datatype")
            if datatype not in DATATYPES:
                raise bad_request(f"Input {name!r} has unsupported datatype {datatype!r}; use one of {', '.join(sorted(DATATYPES))}.")
            data = tensor.get("data")
            if not isinstance(data, list):
                raise bad_request(f"Input {name!r} needs a 'data' array.")
            values = _flatten(data, name)
            if len(values) != shape[0]:
                raise bad_request(f"Input {name!r} has {len(values)} values but its shape says {shape[0]}.")
            tensors[name] = (datatype, values)
        if not n:
            raise bad_request("The request holds no rows.")
        return tensors, n
