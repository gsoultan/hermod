"""Per-feature statistics of the rows a model was trained on.

Hermod compares what a live model is sent against these to measure drift
(the population stability index, PSI), so the shapes here are a contract
with Hermod's monitor (internal/ml/monitor):

- numeric: `edges` are the training split's decile cut points, deduplicated
  and increasing; bin i holds the values v with edges[i-1] < v <= edges[i],
  the last bin everything above the last edge. `fractions` is each bin's
  share of the non-missing values.
- categorical: `top` holds the most frequent values with their share of the
  non-missing values, and `other_fraction` the rest.

`null_fraction` is the share of rows with no usable value: null, NaN or
infinite for a number, null or "" for a string (the worker feeds a missing
string feature as "").
"""

from __future__ import annotations

from typing import Any

import numpy as np
import pandas as pd

from .datasets import NUMBER, is_null, to_text

BINS = 10
TOP_K = 20


def feature_stats(frame: pd.DataFrame, feature_types: dict[str, str]) -> dict[str, dict[str, Any]]:
    """Stats for every feature of `frame`, by its type."""
    return {
        feature: numeric_stats(frame[feature]) if kind == NUMBER else categorical_stats(frame[feature])
        for feature, kind in feature_types.items()
    }


def numeric_stats(series: pd.Series) -> dict[str, Any]:
    total = len(series)
    values = np.asarray([float(v) for v in series if not is_null(v)], dtype=np.float64)
    values = values[np.isfinite(values)]
    out: dict[str, Any] = {
        "kind": "numeric",
        "count": total,
        "null_fraction": _share(total - len(values), total),
        "edges": [],
        "fractions": [],
    }
    if len(values) == 0:
        return out
    edges = np.unique(np.quantile(values, np.linspace(0, 1, BINS + 1)[1:-1]))
    counts = np.bincount(np.searchsorted(edges, values, side="left"), minlength=len(edges) + 1)
    out.update(
        mean=float(values.mean()),
        std=float(values.std()),
        min=float(values.min()),
        max=float(values.max()),
        edges=[float(e) for e in edges],
        fractions=[float(c) / len(values) for c in counts],
    )
    return out


def categorical_stats(series: pd.Series, top_k: int = TOP_K) -> dict[str, Any]:
    total = len(series)
    values = [v if isinstance(v, str) else to_text(v) for v in series if not is_null(v)]
    values = [v for v in values if v != ""]
    counts: dict[str, int] = {}
    for v in values:
        counts[v] = counts.get(v, 0) + 1
    # Most frequent first; ties by value, so retraining on the same rows keeps
    # the same categories.
    ranked = sorted(counts.items(), key=lambda kv: (-kv[1], kv[0]))[:top_k]
    top = [{"value": v, "fraction": c / len(values)} for v, c in ranked]
    other = 1.0 - sum(t["fraction"] for t in top) if values else 0.0
    return {
        "kind": "categorical",
        "count": total,
        "null_fraction": _share(total - len(values), total),
        "top": top,
        "other_fraction": max(0.0, other),
    }


def _share(part: int, whole: int) -> float:
    return part / whole if whole else 0.0
