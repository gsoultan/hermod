"""What a version reports about itself beyond its own predictions.

- metrics: how the model scores on the held-out test rows.
- baseline: the same metrics for a model that knows nothing, always the most
  common class (classification) or the mean (regression), so a reader can tell
  a good score from one any guess would get.
- importance: permutation importance on the held-out rows. Each feature's
  values are shuffled in turn and the drop in score is its weight; weights
  below zero (shuffling helped) count as zero, and the rest are normalised to
  sum to 1. Only the top TOP_FEATURES are kept.
"""

from __future__ import annotations

import math
import warnings
from typing import Any

import numpy as np
import pandas as pd
from sklearn.dummy import DummyClassifier, DummyRegressor
from sklearn.inspection import permutation_importance
from sklearn.metrics import (
    accuracy_score,
    balanced_accuracy_score,
    f1_score,
    mean_absolute_error,
    mean_squared_error,
    r2_score,
    roc_auc_score,
)
from sklearn.pipeline import Pipeline

from .selection import metric_name

TOP_FEATURES = 20
# Shuffles per feature; their mean is the feature's weight.
REPEATS = 5


def finite(metrics: dict[str, float]) -> dict[str, float]:
    """Drop metrics that came out NaN/inf (e.g. r2 on a one-row test split)."""
    return {k: float(v) for k, v in metrics.items() if v is not None and math.isfinite(v)}


def metrics(task: str, y_true: np.ndarray, predicted: np.ndarray, proba: np.ndarray | None, classes: list[Any]) -> dict[str, float]:
    """The metrics of one set of predictions. `proba` (n x classes) gives
    roc_auc for a two-class target; "score" is accuracy or R²."""
    with warnings.catch_warnings():
        # balanced accuracy warns when a class is predicted but absent.
        warnings.simplefilter("ignore")
        if task == "classification":
            out = {
                "accuracy": accuracy_score(y_true, predicted),
                "balanced_accuracy": balanced_accuracy_score(y_true, predicted),
                "f1": f1_score(y_true, predicted, average="macro"),
            }
            if proba is not None and len(classes) == 2 and len(np.unique(y_true)) == 2:
                out["roc_auc"] = roc_auc_score(np.asarray(y_true) == classes[1], proba[:, 1])
            out["score"] = out["accuracy"]
        else:
            out = {
                "rmse": math.sqrt(mean_squared_error(y_true, predicted)),
                "mae": mean_absolute_error(y_true, predicted),
                "r2": r2_score(y_true, predicted) if len(y_true) >= 2 else math.nan,
            }
            out["score"] = out["r2"]
    return finite(out)


def baseline(task: str, X_train: pd.DataFrame, y_train: np.ndarray, X_test: pd.DataFrame, y_test: np.ndarray) -> dict[str, float]:
    """The metrics of a dummy model fitted on the training split."""
    if task == "classification":
        dummy = DummyClassifier(strategy="most_frequent").fit(X_train, y_train)
        return metrics(task, y_test, dummy.predict(X_test), dummy.predict_proba(X_test), list(dummy.classes_))
    dummy = DummyRegressor(strategy="mean").fit(X_train, y_train)
    return metrics(task, y_test, dummy.predict(X_test), None, [])


def importance(pipeline: Pipeline, X: pd.DataFrame, y: np.ndarray, task: str, seed: int) -> list[dict[str, Any]]:
    """Permutation importance of each input column on (X, y), the held-out
    rows: normalised, best first, at most TOP_FEATURES. Empty when no
    feature changes the score."""
    if len(X) < 2:
        return []
    with warnings.catch_warnings():
        warnings.simplefilter("ignore")
        result = permutation_importance(
            pipeline, X, y, scoring=metric_name(task), n_repeats=REPEATS, random_state=seed % 2**32
        )
    weights = np.clip(np.nan_to_num(result.importances_mean, nan=0.0), 0.0, None)
    total = float(weights.sum())
    if total <= 0:
        return []
    # Heaviest first; ties by name, so the same model always lists the same order.
    ranked = sorted(zip(X.columns, weights / total), key=lambda fw: (-fw[1], fw[0]))
    return [{"feature": f, "weight": float(w)} for f, w in ranked[:TOP_FEATURES]]
