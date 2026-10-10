import math

import numpy as np
import pandas as pd
import pytest

from hermod_ml import stats
from tests.helpers import classification_rows, load, train


def test_numeric_stats_bin_the_values_at_their_deciles():
    series = pd.Series([float(v) for v in range(1, 101)] + [math.nan] * 25)
    got = stats.numeric_stats(series)

    assert got["kind"] == "numeric"
    assert got["count"] == 125
    assert got["null_fraction"] == pytest.approx(0.2)
    assert got["mean"] == pytest.approx(50.5)
    assert got["std"] == pytest.approx(np.std(np.arange(1, 101)))
    assert got["min"] == 1.0 and got["max"] == 100.0
    assert len(got["edges"]) == stats.BINS - 1
    assert got["edges"] == sorted(got["edges"])
    assert len(got["fractions"]) == len(got["edges"]) + 1
    assert sum(got["fractions"]) == pytest.approx(1.0)
    # Deciles of 1..100 put ten values in each bin.
    assert got["fractions"] == pytest.approx([0.1] * stats.BINS)


def test_numeric_bins_are_value_le_edge_like_hermods():
    # Hermod bins a live value with sort.SearchFloat64s: the first edge >= v.
    # Training must count a value equal to an edge in the same bin.
    got = stats.numeric_stats(pd.Series([1.0, 1.0, 1.0, 2.0, 3.0]))
    edges, fractions = got["edges"], got["fractions"]
    assert edges[0] == 1.0
    assert fractions[0] == pytest.approx(0.6)


def test_a_constant_column_has_one_edge_and_two_bins():
    got = stats.numeric_stats(pd.Series([5.0] * 20))
    assert got["edges"] == [5.0]
    assert got["fractions"] == [1.0, 0.0]
    assert got["std"] == 0.0


def test_an_all_null_numeric_column_has_no_bins():
    got = stats.numeric_stats(pd.Series([math.nan, math.nan]))
    assert got["null_fraction"] == 1.0
    assert got["edges"] == [] and got["fractions"] == []
    assert "mean" not in got


def test_infinite_values_count_as_missing():
    got = stats.numeric_stats(pd.Series([1.0, 2.0, math.inf, -math.inf]))
    assert got["null_fraction"] == pytest.approx(0.5)
    assert math.isfinite(got["mean"]) and math.isfinite(got["max"])


def test_categorical_stats_keep_the_top_values_and_the_rest():
    values = ["a"] * 50 + ["b"] * 30 + [f"rare{i}" for i in range(20)] + [""] * 25
    got = stats.categorical_stats(pd.Series(values, dtype=object), top_k=3)

    assert got["kind"] == "categorical"
    assert got["count"] == 125
    # The worker feeds a missing string feature as "", so "" is a missing value.
    assert got["null_fraction"] == pytest.approx(0.2)
    assert [t["value"] for t in got["top"]] == ["a", "b", "rare0"]
    assert [t["fraction"] for t in got["top"]] == pytest.approx([0.5, 0.3, 0.01])
    assert got["other_fraction"] == pytest.approx(0.19)


def test_categorical_ties_are_ordered_by_value_so_versions_are_stable():
    got = stats.categorical_stats(pd.Series(["z", "y", "x"], dtype=object), top_k=2)
    assert [t["value"] for t in got["top"]] == ["x", "y"]


def test_feature_stats_follow_the_feature_types():
    frame = pd.DataFrame({"age": [30.0, 40.0, math.nan], "plan": ["pro", "", "free"]})
    got = stats.feature_stats(frame, {"age": "number", "plan": "string"})
    assert set(got) == {"age", "plan"}
    assert got["age"]["kind"] == "numeric"
    assert got["plan"]["kind"] == "categorical"


def test_a_trained_version_carries_its_training_stats(client):
    load(client, "orders", classification_rows())
    resp = train(client, "churn", dataset="orders", target="churned", features=["x1", "city", "flag"])
    assert resp.status_code == 200, resp.text
    fs = resp.json()["feature_stats"]

    assert set(fs) == {"x1", "city", "flag"}
    assert fs["x1"]["kind"] == "numeric"
    assert fs["flag"]["kind"] == "numeric"
    assert fs["city"]["kind"] == "categorical"
    # Stats describe the training split, not the held-back rows.
    assert fs["x1"]["count"] == 96
    assert {t["value"] for t in fs["city"]["top"]} <= {"Oslo", "Bergen", "Tromso"}

    listed = client.get("/v1/models/acme/churn/versions").json()["versions"][0]
    assert listed["feature_stats"] == fs
