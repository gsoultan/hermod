"""Smart Auto, presets, the extra built-in algorithms, imbalance handling and
what every version records about itself (leaderboard, importance, baseline,
warnings)."""

import random

import numpy as np
import pytest

from hermod_ml import deep, selection, training
from tests.helpers import classification_rows, load, regression_rows, train

FEATURES = ["x1", "x2", "city", "flag"]


@pytest.fixture
def churn(client):
    load(client, "orders", classification_rows())
    return client


@pytest.fixture
def sales(client):
    load(client, "sales", regression_rows())
    return client


def lopsided_rows(n: int = 200, seed: int = 5) -> list[dict]:
    """A target that is "yes" for about one row in ten, driven by x1."""
    rng = random.Random(seed)
    rows = []
    for _ in range(n):
        x1 = round(rng.uniform(0, 10), 2)
        rows.append({"x1": x1, "x2": rng.randint(0, 9), "fraud": "yes" if x1 > 9 else "no"})
    return rows


# -- Smart Auto ---------------------------------------------------------------


def test_auto_defaults_to_balanced_and_records_a_leaderboard(churn):
    resp = train(churn, "m", dataset="orders", target="churned", features=FEATURES)
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    assert meta["preset"] == "balanced"
    board = meta["leaderboard"]
    assert [e["algorithm"] for e in board if e["chosen"]] == [meta["algorithm"]]
    assert len(board) == 4
    assert {e["algorithm"] for e in board} == {"linear", "random_forest", "hist_gradient_boosting", "xgboost"}
    assert [e["score"] for e in board] == sorted((e["score"] for e in board), reverse=True)
    assert board[0]["chosen"]
    for e in board:
        assert set(e) == {"algorithm", "score", "metric", "folds", "seconds", "chosen"}
        assert e["metric"] == "balanced_accuracy"
        assert e["folds"] == 3
        assert e["seconds"] >= 0


def test_auto_regression_is_scored_by_r2(sales):
    meta = train(sales, "m", dataset="sales", target="amount").json()
    assert meta["task"] == "regression"
    assert {e["metric"] for e in meta["leaderboard"]} == {"r2"}
    # The data is linear: a linear model should win, or come within reach.
    assert meta["leaderboard"][0]["score"] > 0.9


def test_quick_preset_tries_fast_candidates_without_cross_validation(churn):
    meta = train(churn, "m", dataset="orders", target="churned", features=FEATURES, preset="quick").json()
    assert meta["preset"] == "quick"
    board = meta["leaderboard"]
    assert 1 <= len(board) <= 2
    assert {e["algorithm"] for e in board} <= {"hist_gradient_boosting", "linear"}
    assert {e["folds"] for e in board} == {1}


def test_best_preset_tries_every_candidate_with_five_folds(churn, monkeypatch):
    monkeypatch.setattr(deep, "unavailable", lambda algorithm: "not in this image")
    meta = train(churn, "m", dataset="orders", target="churned", features=FEATURES, preset="best").json()
    assert meta["preset"] == "best"
    assert {e["algorithm"] for e in meta["leaderboard"]} == {
        "linear", "random_forest", "extra_trees", "hist_gradient_boosting", "xgboost",
    }
    assert {e["folds"] for e in meta["leaderboard"]} == {5}


@pytest.mark.parametrize("preset,mlp", [("quick", False), ("balanced", False), ("best", True)])
def test_mlp_is_a_candidate_only_on_best_when_deep_learning_is_installed(monkeypatch, preset, mlp):
    monkeypatch.setattr(deep, "unavailable", lambda algorithm: None if algorithm == "pytorch_mlp" else "missing")
    names = [c.algorithm for c in selection.candidates("classification", preset)]
    assert ("pytorch_mlp" in names) is mlp
    monkeypatch.setattr(deep, "unavailable", lambda algorithm: "missing")
    assert not set(deep.ALGORITHMS) & {c.algorithm for c in selection.candidates("classification", preset)}


def test_xgboost_is_left_out_when_it_is_not_installed():
    names = [c.algorithm for c in selection.candidates("regression", "best", xgboost=False)]
    assert "xgboost" not in names


def test_selection_never_sees_the_test_rows(churn, monkeypatch):
    seen = {}
    real = selection.select

    def spy(X, y, *args, **kwargs):
        seen["rows"] = len(X)
        seen["index"] = set(X.index)
        return real(X, y, *args, **kwargs)

    monkeypatch.setattr(selection, "select", spy)
    meta = train(churn, "m", dataset="orders", target="churned", features=FEATURES).json()
    assert seen["rows"] == meta["rows"]["train"]
    assert seen["rows"] < meta["rows"]["train"] + meta["rows"]["test"]


def test_time_budget_stops_trying_candidates(churn, monkeypatch):
    monkeypatch.setitem(selection.PRESETS, "balanced", selection.PRESETS["balanced"]._replace(budget=0.0))
    meta = train(churn, "m", dataset="orders", target="churned", features=FEATURES).json()
    # The first candidate is always scored, so there is something to choose.
    assert len(meta["leaderboard"]) == 1
    assert meta["leaderboard"][0]["chosen"]
    assert any("time" in w for w in meta["warnings"])


@pytest.mark.parametrize(
    "body,fragment",
    [
        ({"preset": "fast"}, "preset"),
        ({"preset": "quick", "algorithm": "linear"}, "preset"),
        ({"preset": 3}, "preset"),
    ],
)
def test_bad_presets_are_400(churn, body, fragment):
    resp = train(churn, "m", dataset="orders", target="churned", **body)
    assert resp.status_code == 400, resp.text
    assert fragment in resp.json()["error"]


def test_explicit_algorithm_records_no_preset_or_leaderboard(churn):
    meta = train(churn, "m", dataset="orders", target="churned", algorithm="linear", features=FEATURES).json()
    assert "preset" not in meta
    assert "leaderboard" not in meta


# -- the new built-ins ----------------------------------------------------------


@pytest.mark.parametrize("algorithm", ["extra_trees", "hist_gradient_boosting"])
def test_new_algorithms_train_and_export_both_tasks(client, algorithm):
    # Training refuses (500) a graph whose predictions differ from the model's,
    # so a 200 here is the ONNX parity check passing.
    load(client, "orders", classification_rows())
    load(client, "sales", regression_rows())
    c = train(client, "c", dataset="orders", target="churned", algorithm=algorithm, features=FEATURES)
    assert c.status_code == 200, c.text
    assert c.json()["algorithm"] == algorithm
    assert c.json()["metrics"]["accuracy"] > 0.7
    r = train(client, "r", dataset="sales", target="amount", algorithm=algorithm)
    assert r.status_code == 200, r.text
    assert r.json()["metrics"]["r2"] > 0.7


def test_hist_gradient_boosting_learns_from_a_small_table(client):
    # With its default of 20 rows per leaf, 32 training rows give trees with
    # no split at all, and skl2onnx exports such a model's probabilities
    # wrongly. A small table gets smaller leaves.
    rows = classification_rows(n=40)
    load(client, "small", rows)
    resp = train(client, "m", dataset="small", target="churned", algorithm="hist_gradient_boosting", features=FEATURES)
    assert resp.status_code == 200, resp.text


def test_hist_gradient_boosting_that_learns_nothing_is_refused(client):
    load(client, "flat", [{"x": 1.0, "y": "a" if i % 2 else "b"} for i in range(40)])
    resp = train(client, "m", dataset="flat", target="y", algorithm="hist_gradient_boosting")
    assert resp.status_code == 400, resp.text
    assert "nothing" in resp.json()["error"]
    # Auto skips it rather than failing.
    resp = train(client, "a", dataset="flat", target="y")
    assert resp.status_code == 200, resp.text
    assert "hist_gradient_boosting" not in {e["algorithm"] for e in resp.json()["leaderboard"]}


def test_new_algorithms_are_listed_as_capabilities(client):
    body = client.get("/v1/capabilities").json()
    assert {"extra_trees", "hist_gradient_boosting"} <= set(body["algorithms"])


# -- imbalance ----------------------------------------------------------------


def test_imbalanced_target_gets_class_weights():
    y = np.asarray(["no"] * 90 + ["yes"] * 10, dtype=object)
    for algorithm in ("linear", "random_forest", "extra_trees", "hist_gradient_boosting"):
        est = training._estimator("classification", algorithm, 1)
        fit_params = selection.balance(est, "classification", y)
        assert est.get_params()["class_weight"] == "balanced", algorithm
        assert fit_params == {}
    for algorithm in ("xgboost", "gradient_boosting"):
        est = training._estimator("classification", algorithm, 1)
        fit_params = selection.balance(est, "classification", y)
        weights = fit_params["model__sample_weight"]
        assert weights[-1] > weights[0] * 5


def test_balanced_target_and_regression_are_left_alone():
    est = training._estimator("classification", "random_forest", 1)
    assert selection.balance(est, "classification", np.asarray(["a", "b"] * 50, dtype=object)) == {}
    assert est.get_params()["class_weight"] is None
    est = training._estimator("regression", "random_forest", 1)
    assert selection.balance(est, "regression", np.arange(100.0)) == {}


def test_imbalanced_training_warns_and_reports_balanced_accuracy(client):
    load(client, "fraud", lopsided_rows())
    meta = train(client, "m", dataset="fraud", target="fraud").json()
    assert "balanced_accuracy" in meta["metrics"]
    assert any("lopsided" in w or "imbalanced" in w for w in meta["warnings"])
    # A model that only ever says "no" scores 0.5 balanced accuracy.
    assert meta["metrics"]["balanced_accuracy"] > 0.6


# -- importance, baseline, warnings -----------------------------------------------


def test_importance_is_normalised_and_finds_what_matters(client):
    rng = random.Random(3)
    rows = []
    for _ in range(200):
        x1, noise = rng.uniform(-3, 3), rng.uniform(-3, 3)
        rows.append({"x1": x1, "noise": noise, "y": "yes" if x1 > 0 else "no"})
    load(client, "d", rows)
    meta = train(client, "m", dataset="d", target="y", algorithm="random_forest").json()
    importance = meta["importance"]
    assert [i["feature"] for i in importance][0] == "x1"
    assert sum(i["weight"] for i in importance) == pytest.approx(1.0)
    assert all(i["weight"] >= 0 for i in importance)
    assert [i["weight"] for i in importance] == sorted((i["weight"] for i in importance), reverse=True)


def test_importance_keeps_the_top_20(client):
    rng = random.Random(4)
    rows = [{**{f"f{k}": rng.uniform(0, 1) for k in range(25)}, "y": rng.uniform(0, 1)} for _ in range(60)]
    for r in rows:
        r["y"] = sum(r[f"f{k}"] * (k + 1) for k in range(25))
    load(client, "wide", rows)
    meta = train(client, "m", dataset="wide", target="y", algorithm="linear").json()
    assert len(meta["importance"]) == 20


def test_baseline_has_the_metric_keys_of_a_dummy_model(churn, sales):
    c = train(churn, "c", dataset="orders", target="churned", algorithm="linear", features=FEATURES).json()
    assert set(c["baseline"]) == set(c["metrics"])
    assert c["baseline"]["balanced_accuracy"] == pytest.approx(0.5)
    assert c["baseline"]["accuracy"] < c["metrics"]["accuracy"]
    r = train(sales, "r", dataset="sales", target="amount", algorithm="linear").json()
    assert set(r["baseline"]) == set(r["metrics"])
    assert r["baseline"]["r2"] <= 0.0


def test_warnings_name_id_like_feature_columns(churn):
    meta = train(churn, "m", dataset="orders", target="churned", algorithm="linear").json()
    assert any("'id'" in w for w in meta["warnings"])


def test_every_version_records_importance_baseline_and_warnings(churn):
    meta = train(churn, "m", dataset="orders", target="churned", algorithm="extra_trees", features=FEATURES).json()
    assert {"importance", "baseline", "warnings"} <= set(meta)
    assert isinstance(meta["warnings"], list)


def test_snapping_thresholds_keeps_every_decision_on_float32_inputs():
    from sklearn.ensemble import HistGradientBoostingRegressor

    rng = np.random.default_rng(0)
    # Two-decimal values, rounded to float32 as training rounds them.
    X = np.round(rng.uniform(0, 10, size=(400, 2)), 2).astype(np.float32).astype(np.float64)
    y = 3 * X[:, 0] - X[:, 1]
    model = HistGradientBoostingRegressor(random_state=0).fit(X, y)
    before = model.predict(X)
    training.snap_thresholds(model)
    np.testing.assert_array_equal(model.predict(X), before)
    for trees in model._predictors:
        for tree in trees:
            t = tree.nodes["num_threshold"]
            np.testing.assert_array_equal(t.astype(np.float32).astype(np.float64), t)
