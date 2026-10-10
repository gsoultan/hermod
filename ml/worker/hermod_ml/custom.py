"""Training with a custom script.

The worker prepares the data exactly as the built-in trainer does (the same
columns, task rules, split and median fill), hands the training rows and a
spec to the script in the sandbox, and treats what comes back as untrusted:
the ONNX is checked against the spec and scored on the held-out rows with
onnxruntime before it is saved. The script's own Python objects never come
back into this process.

The interface a script implements:

    def train(df: pandas.DataFrame, spec: dict) -> model
    def export_onnx(model, spec: dict) -> bytes

`df` holds the feature columns (numbers as float64 with nulls already
filled, strings as str) and the target column. `spec` holds task, target,
features, feature_types, seed and, for a classifier, labels: the
probabilities output has one column per label, in that order.
"""

from __future__ import annotations

import hashlib
import math
import re
import threading
from typing import Any

import numpy as np
import pandas as pd
from sklearn.metrics import accuracy_score, f1_score, mean_absolute_error, mean_squared_error, r2_score, roc_auc_score

from .artifacts import check_onnx, unprocessable
from .datasets import BOOL, NUMBER, STRING, DatasetStore, is_null
from .errors import ApiError, bad_request
from .names import NAME_RE
from .sandbox import SandboxSettings, ScriptFailed, run_script
from .training import MAX_AUTO_CLASSES, MIN_ROWS, ModelStore, Trainer, _finite, numeric_column, parse_request, string_column, utc_now

PREFIX = "custom:"
MAX_SOURCE_BYTES = 256 * 1024
# The log kept in meta.json; the sandbox may capture more while it runs.
MAX_META_LOG = 16 * 1024
SHA256_RE = re.compile(r"[0-9a-f]{64}\Z")


def parse_script(body: dict[str, Any]) -> tuple[str, str, str]:
    """Return (name, sha256, source) after checking the source matches its hash."""
    script = body.get("script")
    if not isinstance(script, dict):
        raise bad_request("'script' is required: an object with name, sha256 and source.")
    name, digest, source = script.get("name"), script.get("sha256"), script.get("source")
    if not isinstance(name, str) or not NAME_RE.match(name):
        raise bad_request(f"Invalid script name {name!r}: use 1-128 letters, digits, '_', '.' or '-'.")
    if not isinstance(source, str) or not source.strip():
        raise bad_request("The script's 'source' is empty.")
    if len(source.encode()) > MAX_SOURCE_BYTES:
        raise bad_request(f"The script is larger than {MAX_SOURCE_BYTES // 1024} KB.")
    if not isinstance(digest, str) or not SHA256_RE.match(digest):
        raise bad_request("The script's 'sha256' must be 64 lowercase hex digits.")
    if hashlib.sha256(source.encode()).hexdigest() != digest:
        raise bad_request("The script's source does not match its sha256.")
    if body.get("algorithm") != PREFIX + name:
        raise bad_request(f"'algorithm' must be {PREFIX + name!r} to run script {name!r}.")
    return name, digest, source


def metrics_for(task: str, y_test: np.ndarray, predicted: np.ndarray, probabilities: np.ndarray | None, labels: list[Any]) -> dict[str, float]:
    """The built-in trainer's metrics, computed from the ONNX graph's predictions."""
    if task == "classification":
        ints = all(isinstance(v, int) for v in labels)

        def norm(v: Any) -> Any:
            v = v.item() if hasattr(v, "item") else v
            return int(v) if ints else str(v)

        got = [norm(v) for v in predicted]
        want = [norm(v) for v in y_test]
        metrics = {"accuracy": accuracy_score(want, got), "f1": f1_score(want, got, average="macro")}
        if len(labels) == 2 and len(set(want)) == 2 and probabilities is not None:
            metrics["roc_auc"] = roc_auc_score([w == norm(labels[1]) for w in want], probabilities[:, 1])
        metrics["score"] = metrics["accuracy"]
    else:
        metrics = {
            "rmse": math.sqrt(mean_squared_error(y_test, predicted)),
            "mae": mean_absolute_error(y_test, predicted),
            "r2": r2_score(y_test, predicted) if len(y_test) >= 2 else math.nan,
        }
        metrics["score"] = metrics["r2"]
    return _finite(metrics)


class CustomTrainer:
    """Runs custom-script trainings, at most `max_concurrent` at a time."""

    def __init__(self, datasets: DatasetStore, models: ModelStore, sandbox: SandboxSettings, max_concurrent: int, on_saved=None) -> None:
        self._datasets = datasets
        self._models = models
        self._sandbox = sandbox
        self._slots = threading.BoundedSemaphore(max_concurrent)
        self._on_saved = on_saved

    def train(self, vhost: str, name: str, body: dict[str, Any]) -> dict[str, Any]:
        if not self._sandbox.enabled:
            raise ApiError(403, "Custom training scripts are off on this worker; set HERMOD_ML_CUSTOM_SCRIPTS=true on a worker pool set aside for them.")
        script_name, digest, source = parse_script(body)
        # The built-in request rules for everything but the algorithm.
        request = parse_request({**body, "algorithm": "auto"})
        if not self._slots.acquire(blocking=False):
            raise ApiError(429, "Training is busy; try again when the current training finishes.")
        try:
            meta = self._train(vhost, name, request, script_name, digest, source)
        finally:
            self._slots.release()
        if self._on_saved is not None:
            self._on_saved(vhost, name)
        return meta

    def _train(self, vhost, name, req, script_name, digest, source) -> dict[str, Any]:
        frame, types = self._datasets.load(vhost, req.dataset)
        if req.target not in types:
            raise bad_request(f"Target column {req.target!r} is not in dataset {req.dataset!r}.")
        features = req.features or [c for c in frame.columns if c != req.target]
        for feature in features:
            if feature == req.target:
                raise bad_request(f"Column {req.target!r} is the target and cannot also be a feature.")
            if feature not in types:
                raise bad_request(f"Feature column {feature!r} is not in dataset {req.dataset!r}.")
        if not features:
            raise bad_request("There are no feature columns to train on.")
        frame = frame[~frame[req.target].map(is_null)]
        if len(frame) < MIN_ROWS:
            raise bad_request(f"Training needs at least {MIN_ROWS} rows with a target value; found {len(frame)}.")

        # Task and target follow the built-in trainer's rules.
        target_type = types[req.target]
        raw_target = frame[req.target]
        integral = target_type == NUMBER and all(float(v).is_integer() for v in raw_target)
        task = req.task
        if task == "auto":
            if target_type in (STRING, BOOL) or (integral and raw_target.nunique() <= MAX_AUTO_CLASSES):
                task = "classification"
            else:
                task = "regression"
        if task == "classification":
            if target_type == STRING:
                y = np.asarray([str(v) for v in raw_target], dtype=object)
            elif target_type == BOOL:
                y = np.asarray(["true" if v else "false" for v in raw_target], dtype=object)
            elif integral:
                y = raw_target.to_numpy(dtype=np.int64)
            else:
                raise bad_request(f"Classification needs a string, bool or integer target; {req.target!r} has fractional numbers.")
            if len(np.unique(y)) < 2:
                raise bad_request(f"Classification needs at least two distinct values in {req.target!r}.")
        else:
            if target_type != NUMBER:
                raise bad_request(f"Task regression needs a numeric target; {req.target!r} is {target_type}.")
            y = raw_target.to_numpy(dtype=np.float64)

        feature_types = {f: STRING if types[f] == STRING else NUMBER for f in features}
        X = pd.DataFrame(
            {f: numeric_column(frame[f]) if feature_types[f] == NUMBER else string_column(frame[f]) for f in features},
            columns=features,
        ).reset_index(drop=True)
        X_train, X_test, y_train, y_test = Trainer._split(X, y, task, req)
        fill: dict[str, float] = {}
        for f in features:
            if feature_types[f] == NUMBER:
                median = X_train[f].median()
                fill[f] = float(np.float32(0.0 if math.isnan(median) else median))
        X_train = X_train.fillna(value=fill).reset_index(drop=True)
        X_test = X_test.fillna(value=fill).reset_index(drop=True)

        spec: dict[str, Any] = {
            "task": task,
            "target": req.target,
            "features": features,
            "feature_types": feature_types,
            "seed": req.seed,
        }
        labels: list[Any] = []
        if task == "classification":
            labels = [v.item() if hasattr(v, "item") else v for v in np.unique(y_train)]
            spec["labels"] = labels
        df = X_train.copy()
        df[req.target] = y_train

        try:
            result = run_script(source, df, spec, self._sandbox)
        except ScriptFailed as exc:
            raise unprocessable(exc.message) from None

        meta: dict[str, Any] = {
            "model": name,
            "task": task,
            "algorithm": PREFIX + script_name,
            "dataset": req.dataset,
            "target": req.target,
            "features": features,
            "feature_types": feature_types,
            "fill": fill,
        }
        if task == "classification":
            meta["labels"] = labels
        predicted, probabilities = check_onnx(result.onnx, meta, X_test)
        meta["metrics"] = metrics_for(task, y_test, predicted, probabilities, labels)
        meta["rows"] = {"train": int(len(X_train)), "test": int(len(X_test))}
        meta["created_at"] = utc_now()
        meta["script"] = {"name": script_name, "sha256": digest}
        meta["log"] = result.log[-MAX_META_LOG:]
        return self._models.save(vhost, name, result.onnx, meta)
