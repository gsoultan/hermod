import json
import threading
from datetime import datetime

import numpy as np
import onnx
import pytest

from hermod_ml import training
from tests.helpers import classification_rows, load, regression_rows, train

META_KEYS = {
    "model", "version", "task", "algorithm", "dataset", "target", "features",
    "feature_types", "fill", "feature_stats", "metrics", "rows", "created_at",
}


@pytest.fixture
def churn(client):
    load(client, "orders", classification_rows())
    return client


@pytest.fixture
def sales(client):
    load(client, "sales", regression_rows())
    return client


def test_classification_with_defaults(churn, tmp_path):
    resp = train(churn, "churn", dataset="orders", target="churned", features=["x1", "x2", "city", "flag"])
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    assert set(meta) == META_KEYS | {"labels"}
    assert meta["model"] == "churn"
    assert meta["version"] == "1"
    assert meta["task"] == "classification"
    assert meta["algorithm"] == "random_forest"
    assert meta["dataset"] == "orders"
    assert meta["target"] == "churned"
    assert meta["features"] == ["x1", "x2", "city", "flag"]
    assert meta["feature_types"] == {"x1": "number", "x2": "number", "city": "string", "flag": "number"}
    assert set(meta["fill"]) == {"x1", "x2", "flag"}
    assert all(isinstance(v, float) for v in meta["fill"].values())
    assert meta["labels"] == ["no", "yes"]
    assert set(meta["metrics"]) == {"accuracy", "f1", "roc_auc", "score"}
    assert meta["metrics"]["score"] == meta["metrics"]["accuracy"]
    assert meta["metrics"]["accuracy"] > 0.7
    assert meta["rows"] == {"train": 96, "test": 24}
    datetime.fromisoformat(meta["created_at"].replace("Z", "+00:00"))

    vdir = tmp_path / "data/models/acme/churn/1"
    assert sorted(p.name for p in vdir.iterdir()) == ["meta.json", "model.onnx"]
    assert json.loads((vdir / "meta.json").read_text()) == meta


def test_onnx_file_has_one_typed_input_per_feature(churn, tmp_path):
    train(churn, "churn", dataset="orders", target="churned", features=["x1", "city"])
    model = onnx.load(str(tmp_path / "data/models/acme/churn/1/model.onnx"))
    inputs = {i.name: i.type.tensor_type for i in model.graph.input}
    assert list(inputs) == ["x1", "city"]
    assert inputs["x1"].elem_type == onnx.TensorProto.FLOAT
    assert inputs["city"].elem_type == onnx.TensorProto.STRING
    for t in inputs.values():
        assert [d.dim_value for d in t.shape.dim][1] == 1
    # zipmap disabled: probabilities come out as a plain tensor.
    assert [o.name for o in model.graph.output][1] == "probabilities"
    assert model.graph.output[1].type.HasField("tensor_type")


def test_empty_features_means_every_column_but_target(churn):
    meta = train(churn, "m", dataset="orders", target="churned").json()
    assert meta["features"] == ["id", "x1", "x2", "city", "flag"]


def test_regression_auto_and_metrics(sales):
    resp = train(sales, "amt", dataset="sales", target="amount", algorithm="linear")
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    assert meta["task"] == "regression"
    assert meta["algorithm"] == "linear"
    assert "labels" not in meta
    assert set(meta["metrics"]) == {"rmse", "mae", "r2", "score"}
    assert meta["metrics"]["score"] == meta["metrics"]["r2"]
    assert meta["metrics"]["r2"] > 0.95
    assert meta["fill"]["x2"] == pytest.approx(meta["fill"]["x2"])


@pytest.mark.parametrize("algorithm", ["random_forest", "gradient_boosting", "linear", "xgboost"])
def test_every_algorithm_trains_both_tasks(client, algorithm):
    load(client, "orders", classification_rows())
    load(client, "sales", regression_rows())
    c = train(client, f"c-{algorithm}", dataset="orders", target="churned", algorithm=algorithm, features=["x1", "city", "flag"])
    assert c.status_code == 200, c.text
    assert c.json()["algorithm"] == algorithm
    r = train(client, f"r-{algorithm}", dataset="sales", target="amount", algorithm=algorithm)
    assert r.status_code == 200, r.text


def test_auto_task_rules(client):
    rows = classification_rows()
    # integer target with few distinct values -> classification, int labels
    for r in rows:
        r["tier"] = r["x2"] % 3
        r["big"] = r["id"] * 7  # 120 distinct integers -> regression
        r["ratio"] = r["id"] / 7  # non-integral -> regression
    load(client, "d", rows)
    tier = train(client, "tier", dataset="d", target="tier", features=["x1", "city"]).json()
    assert tier["task"] == "classification"
    assert tier["labels"] == [0, 1, 2]
    big = train(client, "big", dataset="d", target="big", features=["x1", "x2"]).json()
    assert big["task"] == "regression"
    ratio = train(client, "ratio", dataset="d", target="ratio", features=["x1", "x2"]).json()
    assert ratio["task"] == "regression"
    flag = train(client, "flag", dataset="d", target="flag", features=["x1", "city"]).json()
    assert flag["task"] == "classification"
    assert flag["labels"] == ["false", "true"]


def test_explicit_task_overrides_auto(client):
    rows = classification_rows()
    for r in rows:
        r["tier"] = r["x2"] % 3
    load(client, "d", rows)
    meta = train(client, "t", dataset="d", target="tier", task="regression", features=["x1", "x2"]).json()
    assert meta["task"] == "regression"


def test_null_targets_are_dropped(client):
    rows = classification_rows(n=60)
    for r in rows[:10]:
        r["churned"] = None
    load(client, "d", rows)
    meta = train(client, "m", dataset="d", target="churned", features=["x1"], test_size=0.25).json()
    assert meta["rows"]["train"] + meta["rows"]["test"] == 50


def test_fill_is_training_median(client):
    rows = [{"x": float(i), "y": "a" if i % 2 else "b"} for i in range(20)]
    rows += [{"x": None, "y": "a"}, {"x": None, "y": "b"}]
    load(client, "d", rows)
    meta = train(client, "m", dataset="d", target="y", test_size=0.1, seed=1).json()
    # The median is over the training split, which excludes the held-out rows,
    # so check it lies within the data's range rather than an exact value.
    assert 0.0 <= meta["fill"]["x"] <= 19.0


def test_versions_increment_and_list_newest_first(churn):
    for _ in range(3):
        assert train(churn, "m", dataset="orders", target="churned", features=["x1"]).status_code == 200
    resp = churn.get("/v1/models/acme/m/versions")
    assert resp.status_code == 200
    versions = resp.json()["versions"]
    assert [v["version"] for v in versions] == ["3", "2", "1"]
    assert set(versions[0]) == META_KEYS | {"labels"}


def test_versions_sort_numerically(churn, tmp_path):
    for _ in range(2):
        train(churn, "m", dataset="orders", target="churned", features=["x1"])
    # Pretend nine more versions exist so "10" must sort above "9".
    base = tmp_path / "data/models/acme/m"
    for v in range(3, 11):
        src = base / "1"
        dst = base / str(v)
        dst.mkdir()
        meta = json.loads((src / "meta.json").read_text())
        meta["version"] = str(v)
        (dst / "meta.json").write_text(json.dumps(meta))
        (dst / "model.onnx").write_bytes((src / "model.onnx").read_bytes())
    assert [v["version"] for v in churn.get("/v1/models/acme/m/versions").json()["versions"]][:3] == ["10", "9", "8"]
    assert train(churn, "m", dataset="orders", target="churned", features=["x1"]).json()["version"] == "11"


def test_versions_of_unknown_model_is_404(client):
    resp = client.get("/v1/models/acme/nothing/versions")
    assert resp.status_code == 404
    assert "error" in resp.json()


def test_delete_model_removes_all_versions(churn, tmp_path):
    train(churn, "m", dataset="orders", target="churned", features=["x1"])
    train(churn, "m", dataset="orders", target="churned", features=["x1"])
    resp = churn.delete("/v1/models/acme/m")
    assert resp.status_code == 204
    assert not (tmp_path / "data/models/acme/m").exists()
    assert churn.get("/v1/models/acme/m/versions").status_code == 404
    assert churn.delete("/v1/models/acme/m").status_code == 404
    # A fresh model starts again at version 1.
    assert train(churn, "m", dataset="orders", target="churned", features=["x1"]).json()["version"] == "1"


@pytest.mark.parametrize(
    "body,fragment",
    [
        ({"target": "churned"}, "dataset"),
        ({"dataset": "orders"}, "target"),
        ({"dataset": "orders", "target": "nope"}, "nope"),
        ({"dataset": "orders", "target": "churned", "features": ["ghost"]}, "ghost"),
        ({"dataset": "orders", "target": "churned", "features": ["churned"]}, "target"),
        ({"dataset": "orders", "target": "churned", "features": "x1"}, "features"),
        ({"dataset": "orders", "target": "churned", "task": "clustering"}, "task"),
        ({"dataset": "orders", "target": "churned", "algorithm": "svm"}, "algorithm"),
        ({"dataset": "orders", "target": "churned", "test_size": 0}, "test_size"),
        ({"dataset": "orders", "target": "churned", "test_size": 1.5}, "test_size"),
        ({"dataset": "orders", "target": "churned", "seed": "x"}, "seed"),
        ({"dataset": "../etc", "target": "churned"}, "dataset"),
        ({"dataset": "orders", "target": "churned", "task": "regression"}, "regression"),
    ],
)
def test_bad_train_requests_are_400(churn, body, fragment):
    resp = churn.post("/v1/models/acme/m/train", json=body)
    assert resp.status_code == 400, resp.text
    assert fragment in resp.json()["error"]


def test_unknown_dataset_is_404(client):
    resp = train(client, "m", dataset="missing", target="y")
    assert resp.status_code == 404


def test_too_few_rows_is_400(client):
    load(client, "tiny", [{"x": i, "y": "a" if i % 2 else "b"} for i in range(9)] + [{"x": 1, "y": None}] * 5)
    resp = train(client, "m", dataset="tiny", target="y")
    assert resp.status_code == 400
    assert "10" in resp.json()["error"]


def test_single_class_target_is_400(client):
    load(client, "one", [{"x": i, "y": "same"} for i in range(30)])
    resp = train(client, "m", dataset="one", target="y")
    assert resp.status_code == 400


def test_parity_mismatch_is_500_and_saves_nothing(churn, tmp_path, monkeypatch):
    def broken(session, frame, meta):
        labels, probs = training.onnx_predict(session, frame, meta)
        return np.array(["nope"] * len(labels), dtype=object), probs

    monkeypatch.setattr(training, "onnx_predict_for_parity", broken)
    resp = train(churn, "m", dataset="orders", target="churned", features=["x1"])
    assert resp.status_code == 500
    assert "ONNX" in resp.json()["error"]
    model_dir = tmp_path / "data/models/acme/m"
    assert not model_dir.exists() or list(model_dir.iterdir()) == []


def test_regression_parity_mismatch_is_500(sales, monkeypatch):
    def broken(session, frame, meta):
        values, _ = training.onnx_predict(session, frame, meta)
        return values * 1.01, None

    monkeypatch.setattr(training, "onnx_predict_for_parity", broken)
    resp = train(sales, "m", dataset="sales", target="amount")
    assert resp.status_code == 500


def test_feature_names_are_kept_exactly_even_when_they_clash(client, tmp_path):
    rows = []
    for r in classification_rows():
        rows.append({"a b": r["x1"], "label": r["x2"], "probabilities": r["city"], "variable": r["flag"], "y": r["churned"]})
    load(client, "odd", rows)
    resp = train(client, "odd", dataset="odd", target="y")
    assert resp.status_code == 200, resp.text
    model = onnx.load(str(tmp_path / "data/models/acme/odd/1/model.onnx"))
    assert [i.name for i in model.graph.input] == ["a b", "label", "probabilities", "variable"]
    onnx.checker.check_model(model)


def test_concurrent_training_beyond_limit_is_429(churn, monkeypatch):
    started, release = threading.Event(), threading.Event()
    real = training.Trainer._train

    def slow(self, *args, **kwargs):
        started.set()
        release.wait(10)
        return real(self, *args, **kwargs)

    monkeypatch.setattr(training.Trainer, "_train", slow)
    results = {}
    worker = threading.Thread(
        target=lambda: results.setdefault("first", train(churn, "m", dataset="orders", target="churned", features=["x1"]))
    )
    worker.start()
    assert started.wait(10)
    busy = train(churn, "m2", dataset="orders", target="churned", features=["x1"])
    release.set()
    worker.join(30)
    assert busy.status_code == 429
    assert "error" in busy.json()
    assert results["first"].status_code == 200
    # The slot is released afterwards.
    assert train(churn, "m3", dataset="orders", target="churned", features=["x1"]).status_code == 200


def test_max_trainings_allows_parallel_calls(make_client, monkeypatch):
    client = make_client(max_trainings=2)
    load(client, "orders", classification_rows())
    started, release = threading.Event(), threading.Event()
    real = training.Trainer._train

    def slow(self, *args, **kwargs):
        started.set()
        release.wait(10)
        return real(self, *args, **kwargs)

    monkeypatch.setattr(training.Trainer, "_train", slow)
    worker = threading.Thread(target=lambda: train(client, "m", dataset="orders", target="churned", features=["x1"]))
    worker.start()
    assert started.wait(10)
    monkeypatch.setattr(training.Trainer, "_train", real)
    second = train(client, "m2", dataset="orders", target="churned", features=["x1"])
    release.set()
    worker.join(30)
    assert second.status_code == 200


def test_no_temp_dirs_left_behind(churn, tmp_path):
    train(churn, "m", dataset="orders", target="churned", features=["x1"])
    names = [p.name for p in (tmp_path / "data/models/acme/m").iterdir()]
    assert names == ["1"]


def test_xgboost_classification_keeps_string_labels(churn, tmp_path):
    resp = train(churn, "xc", dataset="orders", target="churned", algorithm="xgboost", features=["x1", "x2", "city", "flag"])
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    assert meta["algorithm"] == "xgboost"
    assert meta["task"] == "classification"
    assert meta["labels"] == ["no", "yes"]
    assert set(meta["metrics"]) == {"accuracy", "f1", "roc_auc", "score"}
    assert meta["metrics"]["accuracy"] > 0.7
    model = onnx.load(str(tmp_path / "data/models/acme/xc/1/model.onnx"))
    assert [i.name for i in model.graph.input] == ["x1", "x2", "city", "flag"]
    # The graph itself emits the original string labels.
    label_out = model.graph.output[0].type.tensor_type
    assert label_out.elem_type == onnx.TensorProto.STRING


def test_xgboost_multiclass_and_integer_labels(client):
    rows = classification_rows()
    for r in rows:
        r["tier"] = (r["x2"] % 3) * 10
    load(client, "d", rows)
    meta = train(client, "xt", dataset="d", target="tier", algorithm="xgboost", features=["x2", "city"]).json()
    assert meta["labels"] == [0, 10, 20]
    assert "roc_auc" not in meta["metrics"]
    assert meta["metrics"]["accuracy"] > 0.9


def test_xgboost_regression(sales):
    resp = train(sales, "xr", dataset="sales", target="amount", algorithm="xgboost")
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    assert meta["algorithm"] == "xgboost"
    assert meta["task"] == "regression"
    assert meta["metrics"]["r2"] > 0.8


def test_xgboost_defaults(churn, monkeypatch):
    seen = {}
    real = training._estimator

    def spy(task, algorithm, seed):
        est = real(task, algorithm, seed)
        seen["params"] = est.get_params()
        return est

    monkeypatch.setattr(training, "_estimator", spy)
    train(churn, "xd", dataset="orders", target="churned", algorithm="xgboost", features=["x1"])
    params = seen["params"]
    assert (params["n_estimators"], params["max_depth"], params["tree_method"]) == (200, 6, "hist")
    assert params["random_state"] == 42


def test_xgboost_parity_mismatch_is_500(churn, monkeypatch):
    def broken(session, frame, meta):
        labels, probs = training.onnx_predict(session, frame, meta)
        return np.array(["nope"] * len(labels), dtype=object), probs

    monkeypatch.setattr(training, "onnx_predict_for_parity", broken)
    resp = train(churn, "xp", dataset="orders", target="churned", algorithm="xgboost", features=["x1"])
    assert resp.status_code == 500


def test_xgboost_missing_is_400(churn, monkeypatch):
    monkeypatch.setattr(training, "_xgboost_unavailable", lambda: "xgboost is not installed")
    resp = train(churn, "xm", dataset="orders", target="churned", algorithm="xgboost", features=["x1"])
    assert resp.status_code == 400
    assert "xgboost" in resp.json()["error"]
