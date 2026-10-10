"""Model training, ONNX export and the on-disk model store.

A training call is synchronous: load the dataset, fit a scikit-learn
pipeline, export it with skl2onnx, prove the ONNX graph predicts what the
pipeline predicts, then save the version atomically.

Layout on disk:

    DATA_DIR/models/{vhost}/{name}/{version}/model.onnx
                                             meta.json

Only `model.onnx` and `meta.json` are ever written or read; nothing is
pickled.
"""

from __future__ import annotations

import importlib.util
import json
import math
import os
import re
import shutil
import threading
import uuid
from collections.abc import Callable
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import numpy as np
import onnx
import onnxruntime as ort
import pandas as pd
from sklearn.compose import ColumnTransformer
from sklearn.ensemble import (
    GradientBoostingClassifier,
    GradientBoostingRegressor,
    RandomForestClassifier,
    RandomForestRegressor,
)
from sklearn.linear_model import LinearRegression, LogisticRegression
from sklearn.metrics import (
    accuracy_score,
    f1_score,
    mean_absolute_error,
    mean_squared_error,
    r2_score,
    roc_auc_score,
)
from sklearn.model_selection import train_test_split
from sklearn.pipeline import Pipeline
from sklearn.preprocessing import OneHotEncoder, StandardScaler
from skl2onnx import convert_sklearn, get_latest_tested_opset_version
from skl2onnx.common.data_types import FloatTensorType, StringTensorType

from .datasets import BOOL, NUMBER, STRING, DatasetStore, is_null, to_text
from .errors import ApiError, bad_request, not_found
from .names import validate_name

MIN_ROWS = 10
MAX_AUTO_CLASSES = 20  # integer targets with at most this many values are classes
REGRESSION_RTOL = 1e-3

TASKS = ("auto", "classification", "regression")
ALGORITHMS = ("auto", "random_forest", "gradient_boosting", "linear", "xgboost")
VERSION_RE = re.compile(r"[0-9]+\Z")


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


# --------------------------------------------------------------------------
# Model store
# --------------------------------------------------------------------------


class ModelStore:
    """Versioned model files under DATA_DIR/models. Thread-safe."""

    def __init__(self, data_dir: Path) -> None:
        self.root = Path(data_dir) / "models"
        self._lock = threading.Lock()

    def _dir(self, vhost: str, name: str) -> Path:
        return self.root / validate_name(vhost, "vhost") / validate_name(name, "model")

    def _numbers(self, model_dir: Path) -> list[int]:
        """Committed version numbers, newest first. Temp dirs are skipped."""
        if not model_dir.is_dir():
            return []
        return sorted(
            (int(e.name) for e in model_dir.iterdir() if VERSION_RE.match(e.name) and e.is_dir()),
            reverse=True,
        )

    def version_names(self, vhost: str, name: str) -> list[str]:
        """Version strings newest first; 404 if the model has none."""
        numbers = self._numbers(self._dir(vhost, name))
        if not numbers:
            raise not_found(f"Model {name!r} has no versions in vhost {vhost!r}.")
        return [str(n) for n in numbers]

    def resolve(self, vhost: str, name: str, version: str | None) -> str:
        """Return `version` if it exists, or the newest version when None."""
        if version is not None:
            validate_name(version, "version")
        versions = self.version_names(vhost, name)
        if version is None:
            return versions[0]
        if not VERSION_RE.match(version) or str(int(version)) not in versions:
            raise not_found(f"Model {name!r} has no version {version!r}.")
        return str(int(version))

    def meta(self, vhost: str, name: str, version: str) -> dict[str, Any]:
        path = self._dir(vhost, name) / validate_name(version, "version") / "meta.json"
        try:
            return json.loads(path.read_text())
        except FileNotFoundError:
            raise not_found(f"Model {name!r} has no version {version!r}.") from None

    def onnx_path(self, vhost: str, name: str, version: str) -> Path:
        return self._dir(vhost, name) / validate_name(version, "version") / "model.onnx"

    def versions(self, vhost: str, name: str) -> list[dict[str, Any]]:
        return [self.meta(vhost, name, v) for v in self.version_names(vhost, name)]

    def save(self, vhost: str, name: str, model_bytes: bytes, meta: dict[str, Any]) -> dict[str, Any]:
        """Write a new version and return its meta (with "version" filled in).

        Files go into a hidden temp dir first; a single rename publishes the
        version, so a crash never leaves a half-written version behind.
        """
        model_dir = self._dir(vhost, name)
        with self._lock:
            model_dir.mkdir(parents=True, exist_ok=True)
            numbers = self._numbers(model_dir)
            version = str(numbers[0] + 1 if numbers else 1)
            # Keep "model" and "version" first so meta.json reads naturally.
            meta = {"model": meta.get("model"), "version": version, **{k: v for k, v in meta.items() if k != "model"}}
            tmp = model_dir / f".tmp-{uuid.uuid4().hex}"
            tmp.mkdir()
            try:
                (tmp / "model.onnx").write_bytes(model_bytes)
                (tmp / "meta.json").write_text(json.dumps(meta, indent=2))
                os.rename(tmp, model_dir / version)
            finally:
                shutil.rmtree(tmp, ignore_errors=True)
        return meta

    def delete(self, vhost: str, name: str) -> None:
        model_dir = self._dir(vhost, name)
        with self._lock:
            if not model_dir.is_dir():
                raise not_found(f"Model {name!r} does not exist in vhost {vhost!r}.")
            trash = model_dir.parent / f".trash-{uuid.uuid4().hex}"
            os.rename(model_dir, trash)
            shutil.rmtree(trash, ignore_errors=True)


# --------------------------------------------------------------------------
# Request parsing
# --------------------------------------------------------------------------


@dataclass(frozen=True)
class TrainRequest:
    dataset: str
    target: str
    features: list[str]
    task: str
    algorithm: str
    test_size: float
    seed: int


def parse_request(body: dict[str, Any]) -> TrainRequest:
    dataset = body.get("dataset")
    if not isinstance(dataset, str):
        raise bad_request("'dataset' is required and must be a string.")
    validate_name(dataset, "dataset")

    target = body.get("target")
    if not isinstance(target, str) or not target:
        raise bad_request("'target' is required and must be a non-empty string.")

    features = body.get("features", [])
    if features is None:
        features = []
    if not isinstance(features, list) or not all(isinstance(f, str) and f for f in features):
        raise bad_request("'features' must be an array of column names.")
    if len(set(features)) != len(features):
        raise bad_request("'features' lists a column more than once.")

    task = body.get("task", "auto")
    if task not in TASKS:
        raise bad_request(f"'task' must be one of {', '.join(TASKS)}.")

    algorithm = body.get("algorithm", "auto")
    if algorithm not in ALGORITHMS:
        raise bad_request(f"'algorithm' must be one of {', '.join(ALGORITHMS)}.")

    test_size = body.get("test_size", 0.2)
    if isinstance(test_size, bool) or not isinstance(test_size, (int, float)) or not 0 < test_size < 1:
        raise bad_request("'test_size' must be a number between 0 and 1 (exclusive).")

    seed = body.get("seed", 42)
    if isinstance(seed, bool) or not isinstance(seed, int) or not 0 <= seed < 2**32:
        raise bad_request("'seed' must be an integer between 0 and 4294967295.")

    return TrainRequest(dataset, target, list(features), task, algorithm, float(test_size), seed)


# --------------------------------------------------------------------------
# Feature preparation, shared with serving
# --------------------------------------------------------------------------


def numeric_column(series: pd.Series) -> pd.Series:
    """bool/number column -> float64 with NaN for null, rounded to float32.

    ONNX inputs are float32, so training on float32-representable values
    keeps sklearn and the exported graph looking at exactly the same numbers.
    """
    values = [math.nan if is_null(v) else float(v) for v in series]
    return pd.Series(np.asarray(values, dtype=np.float32).astype(np.float64), index=series.index)


def string_column(series: pd.Series) -> pd.Series:
    """string column -> str values, null -> ""."""
    return pd.Series(["" if is_null(v) else (v if isinstance(v, str) else to_text(v)) for v in series], index=series.index, dtype=object)


def make_feed(frame: pd.DataFrame, meta: dict[str, Any]) -> dict[str, np.ndarray]:
    """Turn a prepared (already filled) frame into ONNX inputs, one per feature."""
    feed = {}
    for feature in meta["features"]:
        if meta["feature_types"][feature] == NUMBER:
            feed[feature] = frame[feature].to_numpy(dtype=np.float32).reshape(-1, 1)
        else:
            feed[feature] = frame[feature].to_numpy(dtype=object).reshape(-1, 1)
    return feed


def run_model(session: ort.InferenceSession, feed: dict[str, np.ndarray], meta: dict[str, Any]) -> tuple[np.ndarray, np.ndarray | None]:
    """Run the graph. Returns (labels, probabilities[n, k]) or (values, None).

    Outputs are taken by position (label, probabilities / variable) because
    their names may have been changed to make room for feature names.
    """
    outputs = session.run(None, feed)
    if meta["task"] == "classification":
        return outputs[0].ravel(), np.asarray(outputs[1], dtype=np.float64)
    return np.asarray(outputs[0], dtype=np.float64).ravel(), None


def onnx_predict(session: ort.InferenceSession, frame: pd.DataFrame, meta: dict[str, Any]) -> tuple[np.ndarray, np.ndarray | None]:
    return run_model(session, make_feed(frame, meta), meta)


# The parity check calls through this name so tests can substitute a broken
# predictor and prove that a mismatch is caught.
onnx_predict_for_parity = onnx_predict


# --------------------------------------------------------------------------
# ONNX export helpers
# --------------------------------------------------------------------------


def _rename_inputs(model: onnx.ModelProto, features: list[str]) -> None:
    """Give graph inputs the exact feature names.

    skl2onnx sanitises input names ("a b" -> "a_b") and renames inputs that
    clash with its own output names ("label" -> "label1"). The contract says
    each input is named exactly as its feature, so rename them back. Any
    internal tensor that already uses a wanted name is moved aside first.
    """
    graph = model.graph
    current = [i.name for i in graph.input]
    if len(current) != len(features):
        raise ApiError(500, "ONNX export produced an unexpected number of inputs.")
    taken: set[str] = set(current)
    for node in graph.node:
        taken.update(node.input)
        taken.update(node.output)
    taken.update(t.name for t in graph.initializer)
    taken.update(o.name for o in graph.output)
    taken.update(v.name for v in graph.value_info)

    wanted = set(features)
    mapping: dict[str, str] = {}
    for name in sorted(taken - set(current)):
        if name in wanted:
            candidate, n = f"{name}__internal", 1
            while candidate in taken or candidate in wanted:
                n += 1
                candidate = f"{name}__internal{n}"
            taken.add(candidate)
            mapping[name] = candidate
    for old, new in zip(current, features):
        if old != new:
            mapping[old] = new
    if not mapping:
        return

    def sub(n: str) -> str:
        return mapping.get(n, n)

    for value in list(graph.input) + list(graph.output) + list(graph.value_info):
        value.name = sub(value.name)
    for tensor in graph.initializer:
        tensor.name = sub(tensor.name)
    for node in graph.node:
        for k, n in enumerate(node.input):
            node.input[k] = sub(n)
        for k, n in enumerate(node.output):
            node.output[k] = sub(n)


XGB_PARAMS = {"n_estimators": 200, "max_depth": 6, "tree_method": "hist"}

_xgb_lock = threading.Lock()
_xgb_classes: tuple[type, type] | None = None


def _xgboost_unavailable() -> str | None:
    """Why xgboost cannot be used, or None when it can.

    Training needs xgboost itself and onnxmltools, which supplies the ONNX
    converter that skl2onnx lacks.
    """
    for module in ("xgboost", "onnxmltools"):
        if importlib.util.find_spec(module) is None:
            return f"the {module} package is not installed"
    return None


def _xgboost_classes() -> tuple[type, type]:
    """Return (classifier, regressor) classes, registering their ONNX converters once.

    The classifier is a thin XGBClassifier subclass: xgboost only accepts
    labels 0..k-1, so it label-encodes the target in fit() and exposes the
    original labels through classes_ and predict(). The onnxmltools
    converter reads classes_, so the exported graph emits the original
    labels too (strings or integers).
    """
    global _xgb_classes
    with _xgb_lock:
        if _xgb_classes is not None:
            return _xgb_classes

        from onnxmltools.convert.xgboost.operator_converters.XGBoost import convert_xgboost
        from skl2onnx import update_registered_converter
        from skl2onnx.common.shape_calculator import (
            calculate_linear_classifier_output_shapes,
            calculate_linear_regressor_output_shapes,
        )
        from sklearn.preprocessing import LabelEncoder
        from xgboost import XGBClassifier, XGBRegressor

        class LabeledXGBClassifier(XGBClassifier):
            # Tells the onnxmltools converter to treat this as a classifier.
            operator_name = "XGBClassifier"

            def fit(self, X, y, **kwargs):
                self._labels = None  # classes_ must read 0..k-1 while xgboost fits
                encoder = LabelEncoder().fit(y)
                super().fit(X, encoder.transform(y), **kwargs)
                self._labels = encoder.classes_
                return self

            @property
            def classes_(self):
                labels = getattr(self, "_labels", None)
                return np.arange(self.n_classes_) if labels is None else labels

            def predict(self, X, **kwargs):
                return self.classes_[np.asarray(super().predict(X, **kwargs), dtype=np.int64)]

        update_registered_converter(
            LabeledXGBClassifier,
            "HermodXGBClassifier",
            calculate_linear_classifier_output_shapes,
            convert_xgboost,
            options={"nocl": [True, False], "zipmap": [True, False, "columns"]},
        )
        update_registered_converter(
            XGBRegressor, "HermodXGBRegressor", calculate_linear_regressor_output_shapes, convert_xgboost
        )
        _xgb_classes = (LabeledXGBClassifier, XGBRegressor)
        return _xgb_classes


def _estimator(task: str, algorithm: str, seed: int):
    if algorithm == "xgboost":
        reason = _xgboost_unavailable()
        if reason is not None:
            raise bad_request(f"Algorithm 'xgboost' is not available: {reason}.")
        classifier, regressor = _xgboost_classes()
        cls = classifier if task == "classification" else regressor
        return cls(random_state=seed, **XGB_PARAMS)
    classify = task == "classification"
    if algorithm == "random_forest":
        return RandomForestClassifier(random_state=seed) if classify else RandomForestRegressor(random_state=seed)
    if algorithm == "gradient_boosting":
        return GradientBoostingClassifier(random_state=seed) if classify else GradientBoostingRegressor(random_state=seed)
    if algorithm == "linear":
        return LogisticRegression(max_iter=1000) if classify else LinearRegression()
    raise bad_request(f"Unknown algorithm {algorithm!r}.")  # unreachable after parse_request


def _finite(metrics: dict[str, float]) -> dict[str, float]:
    """Drop metrics that came out NaN/inf (e.g. r2 on a one-row test split)."""
    return {k: float(v) for k, v in metrics.items() if v is not None and math.isfinite(v)}


# --------------------------------------------------------------------------
# Trainer
# --------------------------------------------------------------------------


class Trainer:
    """Runs training calls, at most `max_concurrent` at a time."""

    def __init__(
        self,
        datasets: DatasetStore,
        models: ModelStore,
        max_concurrent: int,
        on_saved: Callable[[str, str], None] | None = None,
    ) -> None:
        self._datasets = datasets
        self._models = models
        self._slots = threading.BoundedSemaphore(max_concurrent)
        self._on_saved = on_saved

    def train(self, vhost: str, name: str, body: dict[str, Any]) -> dict[str, Any]:
        """Validate the request, take a slot (or 429) and train."""
        request = parse_request(body)
        if not self._slots.acquire(blocking=False):
            raise ApiError(429, "Training is busy; try again when the current training finishes.")
        try:
            meta = self._train(vhost, name, request)
        finally:
            self._slots.release()
        if self._on_saved is not None:
            self._on_saved(vhost, name)
        return meta

    def _train(self, vhost: str, name: str, req: TrainRequest) -> dict[str, Any]:
        frame, types = self._datasets.load(vhost, req.dataset)

        # -- columns ---------------------------------------------------------
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

        # -- target and task -------------------------------------------------
        target_type = types[req.target]
        raw_target = frame[req.target]
        integral = target_type == NUMBER and all(float(v).is_integer() for v in raw_target)
        task = req.task
        if task == "auto":
            if target_type in (STRING, BOOL):
                task = "classification"
            elif integral and raw_target.nunique() <= MAX_AUTO_CLASSES:
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

        # -- features --------------------------------------------------------
        feature_types = {f: STRING if types[f] == STRING else NUMBER for f in features}
        numeric = [f for f in features if feature_types[f] == NUMBER]
        strings = [f for f in features if feature_types[f] == STRING]
        X = pd.DataFrame(
            {f: numeric_column(frame[f]) if f in numeric else string_column(frame[f]) for f in features},
            columns=features,
        ).reset_index(drop=True)

        X_train, X_test, y_train, y_test = self._split(X, y, task, req)

        # Medians come from the training split only and are rounded to float32
        # so inference fills exactly the value the model was checked with.
        fill: dict[str, float] = {}
        for f in numeric:
            median = X_train[f].median()
            fill[f] = float(np.float32(0.0 if math.isnan(median) else median))
        X_train = X_train.fillna(value=fill)
        X_test = X_test.fillna(value=fill)

        # -- fit -------------------------------------------------------------
        algorithm = "random_forest" if req.algorithm == "auto" else req.algorithm
        estimator = _estimator(task, algorithm, req.seed)
        transformers = []
        if numeric:
            transformers.append(("num", StandardScaler() if algorithm == "linear" else "passthrough", numeric))
        if strings:
            transformers.append(("str", OneHotEncoder(handle_unknown="ignore"), strings))
        # sparse_threshold=0 keeps the transformed matrix dense, which is what
        # the ONNX graph produces too.
        pipeline = Pipeline([("prep", ColumnTransformer(transformers, sparse_threshold=0)), ("model", estimator)])
        pipeline.fit(X_train, y_train)

        # -- export ----------------------------------------------------------
        initial_types = [
            (f, FloatTensorType([None, 1]) if feature_types[f] == NUMBER else StringTensorType([None, 1]))
            for f in features
        ]
        options = {id(estimator): {"zipmap": False}} if task == "classification" else None
        target_opset = None
        if algorithm == "xgboost":
            # The onnxmltools xgboost converter supports ai.onnx.ml up to v3.
            target_opset = {"": get_latest_tested_opset_version(), "ai.onnx.ml": 3}
        onnx_model = convert_sklearn(pipeline, initial_types=initial_types, options=options, target_opset=target_opset)
        _rename_inputs(onnx_model, features)
        onnx.checker.check_model(onnx_model)
        model_bytes = onnx_model.SerializeToString()

        meta: dict[str, Any] = {
            "model": name,
            "task": task,
            "algorithm": algorithm,
            "dataset": req.dataset,
            "target": req.target,
            "features": features,
            "feature_types": feature_types,
            "fill": fill,
        }
        if task == "classification":
            meta["labels"] = [c.item() if hasattr(c, "item") else c for c in pipeline.classes_]

        # -- parity: the ONNX graph must agree with sklearn on the test split --
        session = ort.InferenceSession(model_bytes, providers=["CPUExecutionProvider"])
        expected = pipeline.predict(X_test)
        got, _ = onnx_predict_for_parity(session, X_test, meta)
        if task == "classification":
            same = len(got) == len(expected) and all(a == b for a, b in zip(got.tolist(), expected.tolist()))
            if not same:
                raise ApiError(500, "ONNX export check failed: the exported model's labels differ from the trained model's.")
        else:
            expected = np.asarray(expected, dtype=np.float64)
            tolerance = REGRESSION_RTOL * np.maximum(np.abs(expected), 1.0)
            if len(got) != len(expected) or not np.all(np.abs(got - expected) <= tolerance):
                raise ApiError(500, "ONNX export check failed: the exported model's values differ from the trained model's by more than 0.1%.")

        # -- metrics ---------------------------------------------------------
        if task == "classification":
            metrics = {
                "accuracy": accuracy_score(y_test, expected),
                "f1": f1_score(y_test, expected, average="macro"),
            }
            classes = list(pipeline.classes_)
            if len(classes) == 2 and len(np.unique(y_test)) == 2:
                proba = pipeline.predict_proba(X_test)[:, 1]
                metrics["roc_auc"] = roc_auc_score(np.asarray(y_test) == classes[1], proba)
            metrics["score"] = metrics["accuracy"]
        else:
            metrics = {
                "rmse": math.sqrt(mean_squared_error(y_test, expected)),
                "mae": mean_absolute_error(y_test, expected),
                "r2": r2_score(y_test, expected) if len(y_test) >= 2 else math.nan,
            }
            metrics["score"] = metrics["r2"]
        meta["metrics"] = _finite(metrics)
        meta["rows"] = {"train": int(len(X_train)), "test": int(len(X_test))}
        meta["created_at"] = utc_now()

        return self._models.save(vhost, name, model_bytes, meta)

    @staticmethod
    def _split(X: pd.DataFrame, y: np.ndarray, task: str, req: TrainRequest):
        """Hold out `test_size` of the rows; stratify classes when possible."""
        stratify = None
        if task == "classification":
            _, counts = np.unique(y, return_counts=True)
            if counts.min() >= 2:
                stratify = y
        try:
            try:
                return train_test_split(X, y, test_size=req.test_size, random_state=req.seed, stratify=stratify)
            except ValueError:
                if stratify is None:
                    raise
                # Too few rows per class for the requested split; fall back.
                return train_test_split(X, y, test_size=req.test_size, random_state=req.seed)
        except ValueError as exc:
            raise bad_request(f"Cannot split the data with test_size={req.test_size}: {exc}") from None
