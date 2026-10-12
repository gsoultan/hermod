"""The pre-flight check: what a training would make of a dataset, without
training anything.

It reads the dataset, works out the task and the target the same way a
training does, and gives every column a role:

- target: what the model predicts.
- mostly_empty: more than MOSTLY_EMPTY of its values are missing.
- constant: at most one distinct value.
- id_like: a string or whole-number column with a distinct value on at
  least ID_LIKE of the rows, like an ID; it cannot generalise.
- leak_suspect: it gives the answer away. For classification, a one-column
  decision tree (with as many leaves as there are classes; for a string
  column, its most common class per value) fitted on half of a sample
  predicts the other half with a balanced accuracy of at least LEAK_SCORE.
  For regression, its correlation with the target is at least LEAK_SCORE
  either way.
- use: none of the above.

The same notes, in words, become a trained version's `warnings`.
"""

from __future__ import annotations

from typing import Any

import numpy as np
import pandas as pd
from sklearn.metrics import balanced_accuracy_score
from sklearn.model_selection import train_test_split
from sklearn.tree import DecisionTreeClassifier

from .datasets import BOOL, NUMBER, STRING, is_null, to_text
from .errors import bad_request
from .selection import is_imbalanced

MIN_ROWS = 10
MAX_AUTO_CLASSES = 20  # integer targets with at most this many values are classes
# Fewer usable rows than this trains, but with rough scores.
FEW_ROWS = 100
MOSTLY_EMPTY = 0.6
ID_LIKE = 0.95
LEAK_SCORE = 0.98
# The leak check looks at a sample of at most this many rows, and only at a
# dataset with at least LEAK_MIN_ROWS: on fewer, chance agreement is common.
LEAK_SAMPLE = 5000
LEAK_MIN_ROWS = 20


# --------------------------------------------------------------------------
# Target and task, shared with training
# --------------------------------------------------------------------------


def is_integral(target_type: str, raw: pd.Series) -> bool:
    return target_type == NUMBER and all(float(v).is_integer() for v in raw)


def detect_task(requested: str, target_type: str, raw: pd.Series) -> str:
    """The task for `requested` ("auto" or explicit) and a non-null target."""
    if requested != "auto":
        return requested
    if target_type in (STRING, BOOL):
        return "classification"
    if is_integral(target_type, raw) and raw.nunique() <= MAX_AUTO_CLASSES:
        return "classification"
    return "regression"


def target_values(task: str, target: str, target_type: str, raw: pd.Series) -> np.ndarray:
    """The target as the model learns it: labels or numbers. 400 when the
    target cannot serve the task at all."""
    if task == "classification":
        if target_type == STRING:
            return np.asarray([str(v) for v in raw], dtype=object)
        if target_type == BOOL:
            return np.asarray(["true" if v else "false" for v in raw], dtype=object)
        if is_integral(target_type, raw):
            return raw.to_numpy(dtype=np.int64)
        raise bad_request(f"Classification needs a string, bool or integer target; {target!r} has fractional numbers.")
    if target_type != NUMBER:
        raise bad_request(f"Task regression needs a numeric target; {target!r} is {target_type}.")
    return raw.to_numpy(dtype=np.float64)


# --------------------------------------------------------------------------
# Notes on the target
# --------------------------------------------------------------------------


def _warning(code: str, message: str, column: str | None = None) -> dict[str, Any]:
    out = {"code": code, "message": message}
    if column is not None:
        out["column"] = column
    return out


def target_warnings(task: str, target: str, y: np.ndarray) -> tuple[list[dict[str, Any]], bool]:
    """Warnings about the target's usable values, and whether a training
    would accept them."""
    out: list[dict[str, Any]] = []
    ok = True
    rows = len(y)
    if rows < MIN_ROWS:
        ok = False
        out.append(_warning("few_rows", f"Only {rows} rows have a value in '{target}'. Training needs at least {MIN_ROWS}.", target))
    elif rows < FEW_ROWS:
        out.append(
            _warning(
                "few_rows",
                f"Only {rows} rows have a value in '{target}'. A model learned from so few is rough; more rows give better results.",
                target,
            )
        )
    if task != "classification":
        if rows and len(np.unique(y)) < 2:
            out.append(_warning("constant", f"Every row has the same value in '{target}', so there is nothing to learn.", target))
        return out, ok
    labels, counts = np.unique(y, return_counts=True)
    if len(labels) < 2:
        ok = False
        out.append(
            _warning("constant", f"Every row has the same value in '{target}'. A yes/no or category needs at least two different values.", target)
        )
        return out, ok
    if len(labels) > MAX_AUTO_CLASSES:
        out.append(
            _warning(
                "many_classes",
                f"'{target}' has {len(labels)} different values to choose between. Each needs enough rows to learn from; "
                "grouping rare values together usually helps.",
                target,
            )
        )
    if is_imbalanced(y):
        rare = labels[int(np.argmin(counts))]
        share = counts.min() / counts.sum()
        out.append(
            _warning(
                "imbalanced",
                f"'{target}' is lopsided: '{to_text(rare)}' is only {share:.0%} of the rows. Hermod weighs the values "
                "so the rare ones count, and judges models by balanced accuracy.",
                target,
            )
        )
    return out, ok


# --------------------------------------------------------------------------
# Notes on the features
# --------------------------------------------------------------------------


def _present(series: pd.Series, kind: str) -> list[Any]:
    """The values of a column that are there: not null, and not "" for text."""
    values = [v for v in series if not is_null(v)]
    if kind == STRING:
        values = [v if isinstance(v, str) else to_text(v) for v in values]
        values = [v for v in values if v != ""]
    return values


def _numbers(series: pd.Series) -> np.ndarray:
    return np.asarray([np.nan if is_null(v) else float(v) for v in series], dtype=np.float64)


def _classification_leak(values: pd.Series, kind: str, y: np.ndarray, seed: int) -> bool:
    labels = np.asarray([to_text(v) for v in y], dtype=object)
    if len(np.unique(labels)) < 2:
        return False
    rows = np.arange(len(labels))
    _, counts = np.unique(labels, return_counts=True)
    stratify = labels if counts.min() >= 2 else None
    try:
        fit, score = train_test_split(rows, test_size=0.5, random_state=seed, stratify=stratify)
    except ValueError:
        fit, score = train_test_split(rows, test_size=0.5, random_state=seed)
    if kind == STRING:
        text = np.asarray(["" if is_null(v) else to_text(v) for v in values], dtype=object)
        fitted = pd.DataFrame({"x": text[fit], "y": labels[fit]})
        by_value = fitted.groupby("x")["y"].agg(lambda s: s.value_counts().index[0]).to_dict()
        common = pd.Series(labels[fit]).value_counts().index[0]
        predicted = np.asarray([by_value.get(v, common) for v in text[score]], dtype=object)
    else:
        x = _numbers(values)
        finite = x[np.isfinite(x)]
        # A missing value gets a value of its own, below every real one.
        x = np.where(np.isfinite(x), x, (finite.min() - 1.0) if len(finite) else 0.0).reshape(-1, 1)
        tree = DecisionTreeClassifier(max_leaf_nodes=max(2, len(np.unique(labels))), random_state=seed)
        tree.fit(x[fit], labels[fit])
        predicted = tree.predict(x[score])
    return float(balanced_accuracy_score(labels[score], predicted)) >= LEAK_SCORE


def _regression_leak(values: pd.Series, y: np.ndarray) -> bool:
    x = _numbers(values)
    keep = np.isfinite(x) & np.isfinite(y)
    if keep.sum() < 3 or np.std(x[keep]) == 0 or np.std(y[keep]) == 0:
        return False
    return abs(float(np.corrcoef(x[keep], y[keep])[0, 1])) >= LEAK_SCORE


def feature_role(series: pd.Series, kind: str, task: str, y: np.ndarray, seed: int) -> tuple[str, str]:
    """A feature column's role over the usable rows, and why in words."""
    rows = len(series)
    present = _present(series, kind)
    empty = 1 - len(present) / rows if rows else 1.0
    if empty > MOSTLY_EMPTY:
        return "mostly_empty", f"{empty:.0%} of its values are empty, so there is little to learn from."
    distinct = len(set(present))
    if distinct <= 1:
        return "constant", "Every row has the same value, so it cannot tell rows apart."
    whole = kind == NUMBER and all(float(v).is_integer() for v in present)
    if (kind == STRING or whole) and distinct >= ID_LIKE * rows:
        return "id_like", "Almost every row has its own value, like an ID. It cannot help predict new rows."
    if rows >= LEAK_MIN_ROWS:
        if task == "classification":
            leak = _classification_leak(series, kind, y, seed)
        else:
            leak = kind != STRING and _regression_leak(series, y.astype(np.float64))
        if leak:
            return "leak_suspect", (
                "On its own it predicts the target almost perfectly. It may be the answer in disguise, "
                "something only known afterwards; a model using it will fail on new rows."
            )
    return "use", ""


ROLE_WARNINGS = {
    "mostly_empty": "'{c}' is mostly empty: {note}",
    "constant": "'{c}' has the same value on every row and cannot help predict.",
    "id_like": "'{c}' has a different value on almost every row, like an ID. It cannot help predict and is best left out.",
    "leak_suspect": "'{c}' predicts the target almost perfectly on its own. If it is only known after the fact, leave it out.",
}


def feature_notes(
    frame: pd.DataFrame, types: dict[str, str], features: list[str], task: str, y: np.ndarray, seed: int
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    """(column roles, warnings) for `features` over `frame`, the usable rows.

    The leak check runs on a sample of at most LEAK_SAMPLE rows."""
    sample = np.arange(len(frame))
    if len(sample) > LEAK_SAMPLE:
        sample = np.sort(np.random.default_rng(seed).choice(len(frame), LEAK_SAMPLE, replace=False))
    columns, warnings = [], []
    for feature in features:
        kind = types[feature]
        series = frame[feature].iloc[sample]
        role, note = feature_role(series, kind, task, y[sample], seed)
        columns.append({"name": feature, "type": kind, "role": role, "note": note})
        if role != "use":
            warnings.append(_warning(role, ROLE_WARNINGS[role].format(c=feature, note=note), feature))
    return columns, warnings


# --------------------------------------------------------------------------
# The check
# --------------------------------------------------------------------------


def check(frame: pd.DataFrame, types: dict[str, str], target: str, features: list[str], requested_task: str, seed: int) -> dict[str, Any]:
    """The pre-flight answer for a dataset whose target and features exist."""
    rows = len(frame)
    usable = frame[~frame[target].map(is_null)]
    raw = usable[target]
    task = detect_task(requested_task, types[target], raw)
    y = target_values(task, target, types[target], raw)
    warnings, ok = target_warnings(task, target, y)

    out: dict[str, Any] = {"task": task}
    classes = len(np.unique(y)) if len(y) else 0
    if task == "classification":
        out["target_kind"] = "yes_no" if classes == 2 else "category"
        out["classes"] = classes
    else:
        out["target_kind"] = "number"
    out["rows"] = rows
    out["usable_rows"] = len(usable)

    target_column = {"name": target, "type": types[target], "role": "target", "note": "What the model learns to predict."}
    if len(usable) and (task != "classification" or classes >= 2):
        columns, notes = feature_notes(usable.reset_index(drop=True), types, features, task, y, seed)
    else:
        columns = [{"name": f, "type": types[f], "role": "use", "note": ""} for f in features]
        notes = []
    order = {name: i for i, name in enumerate(frame.columns)}
    out["columns"] = sorted([target_column, *columns], key=lambda c: order[c["name"]])
    out["warnings"] = warnings + notes
    out["ok"] = ok
    return out
