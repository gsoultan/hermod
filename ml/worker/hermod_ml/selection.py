"""Smart Auto: choose the algorithm for `algorithm: "auto"`.

Auto scores a few candidate algorithms on the training split only, by
cross-validation, and keeps the best. The held-out test rows are never
looked at here: they stay for the metrics the version reports, so those
metrics are not flattered by the choice.

A preset says how much effort Auto spends:

- quick: two fast candidates, scored on one validation split carved from
  the training rows (no cross-validation).
- balanced (the default): four candidates, 3-fold cross-validation.
- best: every candidate, 5-fold cross-validation and a small search over
  each one's settings.

Every preset has a time budget. Once it runs out no further candidate (or
fold) is started, though the first candidate is always scored so there is
something to choose. A classifier is scored by balanced accuracy, so on a
lopsided target a model that only ever predicts the common class scores 0.5,
not the share of that class; a regression by R².

The module knows nothing about how a pipeline is built: the trainer passes a
`build(algorithm, params)` callable, so the candidates are exactly the
pipelines an explicit training of that algorithm would fit.
"""

from __future__ import annotations

import math
import time
import warnings
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Any, NamedTuple

import numpy as np
import pandas as pd
from sklearn.metrics import balanced_accuracy_score, r2_score
from sklearn.model_selection import KFold, StratifiedKFold, train_test_split
from sklearn.pipeline import Pipeline
from sklearn.utils.class_weight import compute_sample_weight
from sklearn.utils.validation import has_fit_parameter

from . import deep

PRESET_NAMES = ("quick", "balanced", "best")
DEFAULT_PRESET = "balanced"

# The deep-learning candidate, which stands for the first deep algorithm this
# image can train.
MLP = "mlp"


class Preset(NamedTuple):
    # Candidates in the order they are tried; the first is always scored.
    algorithms: tuple[str, ...]
    # Cross-validation folds; 1 means a single validation split.
    folds: int
    # Whether to try the alternative settings in TRIALS too.
    search: bool
    # Seconds Auto may spend choosing.
    budget: float


PRESETS: dict[str, Preset] = {
    "quick": Preset(("hist_gradient_boosting", "linear"), 1, False, 30.0),
    "balanced": Preset(("hist_gradient_boosting", "random_forest", "linear", "xgboost"), 3, False, 120.0),
    "best": Preset(
        ("hist_gradient_boosting", "random_forest", "extra_trees", "xgboost", "linear", MLP), 5, True, 600.0
    ),
}

# The share of the rows a quick preset holds back to score its candidates.
VALIDATION_FRACTION = 0.25

# A classification target is lopsided when its rarest class has at most this
# many rows for each row of its most common class.
IMBALANCE_RATIO = 0.25

# The alternative settings "best" tries per algorithm, beside the defaults.
# Settings an estimator does not have (C on a plain linear regression) are
# skipped.
TRIALS: dict[str, list[dict[str, Any]]] = {
    "random_forest": [{"n_estimators": 300}, {"min_samples_leaf": 3}],
    "extra_trees": [{"n_estimators": 300}, {"min_samples_leaf": 3}],
    "hist_gradient_boosting": [{"learning_rate": 0.05, "max_iter": 300}, {"max_leaf_nodes": 15, "l2_regularization": 1.0}],
    "xgboost": [{"max_depth": 4, "learning_rate": 0.1}, {"max_depth": 8, "subsample": 0.8}],
    "linear": [{"C": 0.1}, {"C": 10.0}],
}


@dataclass(frozen=True)
class Candidate:
    algorithm: str
    # The settings to try, the defaults ({}) first.
    trials: tuple[dict[str, Any], ...] = ({},)


@dataclass
class Selection:
    """What Auto chose and why."""

    algorithm: str
    params: dict[str, Any]
    leaderboard: list[dict[str, Any]]
    warnings: list[str] = field(default_factory=list)


def metric_name(task: str) -> str:
    return "balanced_accuracy" if task == "classification" else "r2"


def _metric(task: str, y_true: np.ndarray, y_pred: np.ndarray) -> float:
    with warnings.catch_warnings():
        # balanced accuracy warns when a fold predicts a class it lacks.
        warnings.simplefilter("ignore")
        if task == "classification":
            return float(balanced_accuracy_score(y_true, y_pred))
        return float(r2_score(y_true, y_pred)) if len(y_true) >= 2 else math.nan


def _deep_candidate() -> str | None:
    """The first deep-learning algorithm this image can train, if any."""
    return next((a for a in deep.ALGORITHMS if deep.unavailable(a) is None), None)


def candidates(task: str, preset: str, xgboost: bool = True) -> list[Candidate]:
    """The candidates `preset` tries, given what this image can train.

    Without xgboost, balanced tries extra trees in its place, so it still
    compares four.
    """
    spec = PRESETS[preset]
    names: list[str] = []
    for name in spec.algorithms:
        if name == "xgboost" and not xgboost:
            if "extra_trees" not in spec.algorithms:
                names.append("extra_trees")
            continue
        if name == MLP:
            found = _deep_candidate()
            if found is not None:
                names.append(found)
            continue
        names.append(name)
    out = []
    for name in names:
        trials: tuple[dict[str, Any], ...] = ({},)
        if spec.search and name in TRIALS:
            trials = ({}, *TRIALS[name])
        out.append(Candidate(name, trials))
    return out


def is_imbalanced(y: np.ndarray) -> bool:
    _, counts = np.unique(y, return_counts=True)
    return len(counts) >= 2 and counts.min() <= IMBALANCE_RATIO * counts.max()


def balance(estimator: Any, task: str, y: np.ndarray) -> dict[str, Any]:
    """Weigh the classes of a lopsided classification target equally.

    An estimator that takes class_weight gets "balanced"; one that takes only
    per-row weights (xgboost, gradient boosting) gets them as the fit
    parameters returned here, keyed for the pipeline's "model" step. Anything
    else, and every regression or evenly spread target, is left alone.
    """
    if task != "classification" or not is_imbalanced(y):
        return {}
    if "class_weight" in estimator.get_params():
        estimator.set_params(class_weight="balanced")
        return {}
    if has_fit_parameter(estimator, "sample_weight"):
        return {"model__sample_weight": compute_sample_weight("balanced", y)}
    return {}


def learned_nothing(estimator: Any) -> bool:
    """Whether a fitted histogram gradient boosting model has no split in
    any tree: it always answers the same, and skl2onnx exports such a model's
    probabilities wrongly."""
    predictors = getattr(estimator, "_predictors", None)
    if predictors is None:
        return False
    return all(len(tree.nodes) == 1 for trees in predictors for tree in trees)


def _splits(X: pd.DataFrame, y: np.ndarray, task: str, folds: int, seed: int) -> list[tuple[np.ndarray, np.ndarray]]:
    """Train/score row positions: one validation split, or k folds
    (stratified where every class has a row in each fold)."""
    rows = np.arange(len(y))
    if folds <= 1:
        stratify = None
        if task == "classification":
            _, counts = np.unique(y, return_counts=True)
            stratify = y if counts.min() >= 2 else None
        try:
            fit, score = train_test_split(rows, test_size=VALIDATION_FRACTION, random_state=seed, stratify=stratify)
        except ValueError:
            fit, score = train_test_split(rows, test_size=VALIDATION_FRACTION, random_state=seed)
        return [(fit, score)]
    k = max(2, min(folds, len(y)))
    if task == "classification":
        _, counts = np.unique(y, return_counts=True)
        if counts.min() >= k:
            return list(StratifiedKFold(k, shuffle=True, random_state=seed).split(X, y))
    return list(KFold(k, shuffle=True, random_state=seed).split(X))


class _OutOfTime(Exception):
    pass


def select(
    X: pd.DataFrame,
    y: np.ndarray,
    task: str,
    preset: str,
    seed: int,
    build: Callable[[str, dict[str, Any]], Pipeline],
    xgboost: bool = True,
) -> Selection:
    """Score every candidate of `preset` on (X, y), the training split, and
    return the best with the leaderboard of what was tried."""
    spec = PRESETS[preset]
    pool = candidates(task, preset, xgboost)
    splits = _splits(X, y, task, spec.folds, seed)
    metric = metric_name(task)
    started = time.monotonic()
    notes: list[str] = []
    board: list[dict[str, Any]] = []
    best_params: dict[str, dict[str, Any]] = {}
    skipped: list[str] = []

    def out_of_time() -> bool:
        return time.monotonic() - started >= spec.budget

    for candidate in pool:
        began = time.monotonic()
        best: tuple[float, dict[str, Any]] | None = None
        try:
            for params in candidate.trials:
                scores = []
                for fit_rows, score_rows in splits:
                    # The first candidate's default settings always finish, so
                    # there is something to choose; after that the budget rules.
                    if (board or best is not None) and out_of_time():
                        raise _OutOfTime
                    pipeline = build(candidate.algorithm, params)
                    y_fit = y[fit_rows]
                    fit_params = balance(pipeline.named_steps["model"], task, y_fit)
                    pipeline.fit(X.iloc[fit_rows], y_fit, **fit_params)
                    if learned_nothing(pipeline.named_steps["model"]):
                        raise ValueError("no feature splits the rows")
                    scores.append(_metric(task, y[score_rows], pipeline.predict(X.iloc[score_rows])))
                mean = float(np.mean(scores))
                if math.isfinite(mean) and (best is None or mean > best[0]):
                    best = (mean, params)
        except _OutOfTime:
            if best is None:
                skipped.append(candidate.algorithm)
                continue
        except Exception as exc:  # one candidate failing must not fail the training
            notes.append(f"Auto skipped {candidate.algorithm}: it could not be trained on this data ({exc}).")
            continue
        if best is None:
            notes.append(f"Auto skipped {candidate.algorithm}: its score could not be measured on this data.")
            continue
        best_params[candidate.algorithm] = best[1]
        board.append(
            {
                "algorithm": candidate.algorithm,
                "score": best[0],
                "metric": metric,
                "folds": len(splits),
                "seconds": round(time.monotonic() - began, 3),
                "chosen": False,
            }
        )

    if skipped:
        notes.append(f"Auto ran out of its {spec.budget:g}-second time budget and did not try: {', '.join(skipped)}.")
    if not board:
        notes.append("Auto could not score any candidate, so it trained a random forest.")
        return Selection("random_forest", {}, [], notes)
    # Best first; a tie keeps the order the candidates were tried in.
    board.sort(key=lambda e: -e["score"])
    board[0]["chosen"] = True
    winner = board[0]["algorithm"]
    return Selection(winner, best_params[winner], board, notes)
