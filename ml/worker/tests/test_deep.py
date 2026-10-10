"""Deep-learning algorithms: pytorch_mlp and keras_mlp.

Tests that train need torch or tensorflow/keras/tf2onnx, which only the "-dl"
image installs; they skip without them (and fail under HERMOD_ML_REQUIRE_DL=1).
Validation, availability and capability tests run everywhere.
"""

import numpy as np
import onnx
import onnxruntime as ort
import pandas as pd
import pytest
from sklearn.compose import ColumnTransformer
from sklearn.pipeline import Pipeline
from sklearn.preprocessing import OneHotEncoder, StandardScaler

from hermod_ml import deep, training
from tests.helpers import classification_rows, load, regression_rows, require_dl, train

DL = ["pytorch_mlp", "keras_mlp"]
DEFAULT_PARAMS = {"hidden_layers": [64, 32], "epochs": 200, "batch_size": 32, "learning_rate": 0.001, "patience": 10}


def col(name, data, datatype="FP64"):
    return {"name": name, "shape": [len(data)], "datatype": datatype, "data": data}


def infer(client, model, inputs, vhost="acme"):
    return client.post(f"/vhosts/{vhost}/v2/models/{model}/infer", json={"inputs": inputs})


# --------------------------------------------------------------------------
# Training through the API
# --------------------------------------------------------------------------


@pytest.mark.parametrize("algorithm", DL)
def test_dl_classification_keeps_the_model_contract(client, tmp_path, algorithm):
    require_dl(algorithm)
    load(client, "orders", classification_rows())
    resp = train(client, "c", dataset="orders", target="churned", algorithm=algorithm, features=["x1", "x2", "city", "flag"])
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    assert meta["algorithm"] == algorithm
    assert meta["task"] == "classification"
    assert meta["labels"] == ["no", "yes"]
    assert meta["params"] == DEFAULT_PARAMS
    assert set(meta["metrics"]) == {"accuracy", "f1", "roc_auc", "score"}
    assert meta["metrics"]["accuracy"] > 0.7

    model = onnx.load(str(tmp_path / "data/models/acme/c/1/model.onnx"))
    onnx.checker.check_model(model)
    inputs = {i.name: i.type.tensor_type for i in model.graph.input}
    assert list(inputs) == ["x1", "x2", "city", "flag"]
    assert inputs["x1"].elem_type == onnx.TensorProto.FLOAT
    assert inputs["city"].elem_type == onnx.TensorProto.STRING
    # Same outputs as the scikit-learn models: string labels, then probabilities.
    assert [o.name for o in model.graph.output] == ["label", "probabilities"]
    assert model.graph.output[0].type.tensor_type.elem_type == onnx.TensorProto.STRING
    assert model.graph.output[1].type.tensor_type.elem_type == onnx.TensorProto.FLOAT


@pytest.mark.parametrize("algorithm", DL)
def test_dl_regression(client, tmp_path, algorithm):
    require_dl(algorithm)
    load(client, "sales", regression_rows())
    resp = train(client, "r", dataset="sales", target="amount", algorithm=algorithm)
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    assert meta["task"] == "regression"
    assert "labels" not in meta
    assert meta["metrics"]["r2"] > 0.9
    model = onnx.load(str(tmp_path / "data/models/acme/r/1/model.onnx"))
    assert [i.name for i in model.graph.input] == ["x1", "x2", "city"]
    assert [o.name for o in model.graph.output] == ["variable"]


@pytest.mark.parametrize("algorithm", DL)
def test_dl_integer_labels_and_multiclass(client, tmp_path, algorithm):
    require_dl(algorithm)
    rows = classification_rows()
    for r in rows:
        r["tier"] = (r["x2"] % 3) * 10
    load(client, "d", rows)
    resp = train(client, "t", dataset="d", target="tier", algorithm=algorithm, features=["x2", "city"],
                 params={"epochs": 400, "patience": 50, "learning_rate": 0.01})
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    assert meta["labels"] == [0, 10, 20]
    assert "roc_auc" not in meta["metrics"]
    model = onnx.load(str(tmp_path / "data/models/acme/t/1/model.onnx"))
    assert model.graph.output[0].type.tensor_type.elem_type == onnx.TensorProto.INT64
    out = infer(client, "t", [col("x2", [0, 1, 2, 4]), col("city", ["Oslo"] * 4, "BYTES")])
    assert out.status_code == 200, out.text
    label = out.json()["outputs"][0]
    assert label["datatype"] == "INT64"
    assert set(label["data"]) <= {0, 10, 20}


@pytest.mark.parametrize("algorithm", DL)
def test_dl_models_serve_like_the_others(client, algorithm):
    require_dl(algorithm)
    load(client, "orders", classification_rows())
    load(client, "sales", regression_rows())
    assert train(client, "c", dataset="orders", target="churned", algorithm=algorithm, features=["x1", "city", "flag"]).status_code == 200
    assert train(client, "r", dataset="sales", target="amount", algorithm=algorithm).status_code == 200

    meta = client.get("/vhosts/acme/v2/models/c").json()
    assert meta["outputs"] == [
        {"name": "label", "datatype": "BYTES", "shape": [-1]},
        {"name": "probability", "datatype": "FP64", "shape": [-1]},
    ]
    resp = infer(client, "c", [col("x1", [2.9, None, -2.9]), col("city", ["Oslo", "Bergen", None], "BYTES"), col("flag", [True, False, False], "BOOL")])
    assert resp.status_code == 200, resp.text
    label, prob = resp.json()["outputs"]
    assert label["data"][0] == "yes"
    assert label["data"][2] == "no"
    assert all(0.5 <= p <= 1.0 for p in prob["data"])

    resp = infer(client, "r", [col("x1", [4.0]), col("x2", [None]), col("city", ["Oslo"], "BYTES")])
    assert resp.status_code == 200, resp.text
    fill = client.get("/v1/models/acme/r/versions").json()["versions"][0]["fill"]["x2"]
    assert resp.json()["outputs"][0]["data"][0] == pytest.approx(3 * 4 + 2 * fill + 5, abs=3.0)


@pytest.mark.parametrize("algorithm", DL)
def test_dl_params_are_used_and_recorded(client, tmp_path, algorithm):
    require_dl(algorithm)
    load(client, "orders", classification_rows())
    params = {"hidden_layers": [8], "epochs": 5, "batch_size": 16, "learning_rate": 0.01, "patience": 2}
    resp = train(client, "c", dataset="orders", target="churned", algorithm=algorithm, features=["x1", "city"], params=params)
    assert resp.status_code == 200, resp.text
    assert resp.json()["params"] == params
    # One hidden layer of 8 units: the graph holds an 8 x n_inputs (or n_inputs x 8)
    # weight matrix and an 8 x 2 (or 2 x 8) output matrix.
    model = onnx.load(str(tmp_path / "data/models/acme/c/1/model.onnx"))
    shapes = sorted(tuple(t.dims) for t in model.graph.initializer if len(t.dims) == 2)
    n_inputs = 1 + 3  # x1, plus one-hot city (Oslo, Bergen, Tromso and the empty string)
    assert sorted(tuple(sorted(s)) for s in shapes) == sorted([tuple(sorted((8, n_inputs + 1))), (2, 8)])


@pytest.mark.parametrize("algorithm", DL)
def test_dl_training_is_repeatable_with_a_seed(client, algorithm):
    require_dl(algorithm)
    load(client, "sales", regression_rows())
    first = train(client, "a", dataset="sales", target="amount", algorithm=algorithm, seed=7, params={"epochs": 20}).json()
    second = train(client, "b", dataset="sales", target="amount", algorithm=algorithm, seed=7, params={"epochs": 20}).json()
    assert first["metrics"]["rmse"] == pytest.approx(second["metrics"]["rmse"], rel=1e-5)


@pytest.mark.parametrize("algorithm", DL)
def test_dl_feature_names_are_kept_exactly_even_when_they_clash(client, tmp_path, algorithm):
    require_dl(algorithm)
    rows = [
        {"a b": r["x1"], "label": r["x2"], "probabilities": r["city"], "variable": r["flag"], "y": r["churned"]}
        for r in classification_rows()
    ]
    load(client, "odd", rows)
    resp = train(client, "odd", dataset="odd", target="y", algorithm=algorithm, params={"epochs": 5})
    assert resp.status_code == 200, resp.text
    model = onnx.load(str(tmp_path / "data/models/acme/odd/1/model.onnx"))
    assert [i.name for i in model.graph.input] == ["a b", "label", "probabilities", "variable"]
    onnx.checker.check_model(model)


@pytest.mark.parametrize("algorithm", DL)
def test_dl_parity_mismatch_is_500(client, monkeypatch, algorithm):
    require_dl(algorithm)
    load(client, "orders", classification_rows())

    def broken(session, frame, meta):
        labels, probs = training.onnx_predict(session, frame, meta)
        return np.array(["nope"] * len(labels), dtype=object), probs

    monkeypatch.setattr(training, "onnx_predict_for_parity", broken)
    resp = train(client, "m", dataset="orders", target="churned", algorithm=algorithm, features=["x1"], params={"epochs": 5})
    assert resp.status_code == 500
    assert "ONNX" in resp.json()["error"]


# --------------------------------------------------------------------------
# ONNX round trip against the framework's own predictions
# --------------------------------------------------------------------------


def _frame(rows, features, types):
    frame = pd.DataFrame(rows)
    data = {}
    for f in features:
        if types[f] == "number":
            data[f] = training.numeric_column(frame[f]).fillna(0.0)
        else:
            data[f] = training.string_column(frame[f])
    return pd.DataFrame(data, columns=features)


@pytest.mark.parametrize("algorithm", DL)
@pytest.mark.parametrize("task", ["classification", "regression"])
def test_onnx_round_trip_matches_the_framework(algorithm, task):
    require_dl(algorithm)
    if task == "classification":
        rows, target, features = classification_rows(), "churned", ["x1", "x2", "city", "flag"]
        types = {"x1": "number", "x2": "number", "city": "string", "flag": "number"}
        y = np.asarray([r[target] for r in rows], dtype=object)
        fresh = classification_rows(n=50, seed=99)
    else:
        rows, target, features = regression_rows(), "amount", ["x1", "x2", "city"]
        types = {"x1": "number", "x2": "number", "city": "string"}
        y = np.asarray([r[target] for r in rows], dtype=np.float64)
        fresh = regression_rows(n=50, seed=99)
    for r in fresh:  # a category the model never saw
        r["city"] = "Reykjavik" if r is fresh[0] else r["city"]
    X = _frame(rows, features, types)
    numeric = [f for f in features if types[f] == "number"]
    strings = [f for f in features if types[f] == "string"]
    prep = ColumnTransformer(
        [("num", StandardScaler(), numeric), ("str", OneHotEncoder(handle_unknown="ignore"), strings)], sparse_threshold=0
    )
    estimator = deep.make_estimator(algorithm, task, deep.parse_params(None), seed=3)
    pipeline = Pipeline([("prep", prep), ("model", estimator)]).fit(X, y)

    model = training.export_onnx(pipeline, features, types, task, algorithm)
    session = ort.InferenceSession(model.SerializeToString(), providers=["CPUExecutionProvider"])
    meta = {"task": task, "features": features, "feature_types": types}
    X_new = _frame(fresh, features, types)
    got, probabilities = training.onnx_predict(session, X_new, meta)

    if task == "classification":
        np.testing.assert_allclose(probabilities, pipeline.predict_proba(X_new), atol=1e-5)
        assert got.tolist() == pipeline.predict(X_new).tolist()
    else:
        np.testing.assert_allclose(got, pipeline.predict(X_new), rtol=1e-4, atol=1e-3)


# --------------------------------------------------------------------------
# Validation and availability: no deep-learning libraries needed
# --------------------------------------------------------------------------


@pytest.mark.parametrize(
    "params,fragment",
    [
        ("fast", "params"),
        ({"epochs": 0}, "epochs"),
        ({"epochs": 1001}, "epochs"),
        ({"epochs": 2.5}, "epochs"),
        ({"epochs": True}, "epochs"),
        ({"hidden_layers": []}, "hidden_layers"),
        ({"hidden_layers": [0]}, "hidden_layers"),
        ({"hidden_layers": [1025]}, "hidden_layers"),
        ({"hidden_layers": [8, 8, 8, 8, 8, 8]}, "hidden_layers"),
        ({"hidden_layers": "64,32"}, "hidden_layers"),
        ({"batch_size": 0}, "batch_size"),
        ({"batch_size": 4097}, "batch_size"),
        ({"learning_rate": 0}, "learning_rate"),
        ({"learning_rate": 1.5}, "learning_rate"),
        ({"learning_rate": "0.1"}, "learning_rate"),
        ({"patience": 0}, "patience"),
        ({"patience": 1001}, "patience"),
        ({"dropout": 0.5}, "dropout"),
    ],
)
@pytest.mark.parametrize("algorithm", DL)
def test_bad_params_are_400(client, algorithm, params, fragment):
    load(client, "orders", classification_rows())
    resp = train(client, "m", dataset="orders", target="churned", algorithm=algorithm, params=params)
    assert resp.status_code == 400, resp.text
    assert fragment in resp.json()["error"]


@pytest.mark.parametrize("algorithm", ["auto", "random_forest", "xgboost"])
def test_params_are_refused_for_other_algorithms(client, algorithm):
    load(client, "orders", classification_rows())
    resp = train(client, "m", dataset="orders", target="churned", algorithm=algorithm, params={"epochs": 5})
    assert resp.status_code == 400
    assert "params" in resp.json()["error"]


def test_labels_may_differ_only_on_float32_ties():
    proba = np.array([[0.50002, 0.49998], [0.9, 0.1]])
    expected = np.array(["a", "a"], dtype=object)
    # Row 0 is a tie within PROBABILITY_ATOL: ONNX may break it the other way.
    assert training._labels_agree(np.array(["b", "a"], dtype=object), expected, proba)
    # Row 1 is not, so a different label there is a real mismatch.
    assert not training._labels_agree(np.array(["a", "b"], dtype=object), expected, proba)
    # Without probabilities (the scikit-learn models) labels must match exactly.
    assert not training._labels_agree(np.array(["b", "a"], dtype=object), expected, None)
    assert not training._labels_agree(np.array(["a"], dtype=object), expected, proba)


def test_params_resolve_with_defaults():
    assert deep.parse_params(None) == DEFAULT_PARAMS
    assert deep.parse_params({"epochs": 3}) == {**DEFAULT_PARAMS, "epochs": 3}
    assert deep.parse_params({"learning_rate": 1}) == {**DEFAULT_PARAMS, "learning_rate": 1.0}


@pytest.mark.parametrize(
    "algorithm,missing",
    [("pytorch_mlp", "torch"), ("keras_mlp", "tensorflow")],
)
def test_missing_dl_library_is_400_not_available_in_this_image(client, monkeypatch, algorithm, missing):
    real = deep.importlib.util.find_spec
    monkeypatch.setattr(deep.importlib.util, "find_spec", lambda name, *a: None if name == missing else real(name, *a))
    load(client, "orders", classification_rows())
    resp = train(client, "m", dataset="orders", target="churned", algorithm=algorithm, features=["x1"])
    assert resp.status_code == 400
    error = resp.json()["error"]
    assert algorithm in error and "not available in this image" in error and missing in error
    assert not (client.app.state.models.root / "acme" / "m").exists()


def test_capabilities_list_the_algorithms_this_image_can_train(client, monkeypatch):
    real = deep.importlib.util.find_spec
    monkeypatch.setattr(deep.importlib.util, "find_spec", lambda name, *a: None if name == "torch" else real(name, *a))
    monkeypatch.setattr(training, "_xgboost_unavailable", lambda: None)
    resp = client.get("/v1/capabilities")
    assert resp.status_code == 200, resp.text
    body = resp.json()
    assert body["tasks"] == ["auto", "classification", "regression"]
    assert "pytorch_mlp" not in body["algorithms"]
    assert body["algorithms"][:5] == ["auto", "random_forest", "gradient_boosting", "linear", "xgboost"]
    assert "torch" in body["unavailable"]["pytorch_mlp"]
    assert set(body["algorithms"]) | set(body["unavailable"]) == set(training.ALGORITHMS)


def test_capabilities_need_the_token(make_client):
    client = make_client(token="s3cret")
    assert client.get("/v1/capabilities").status_code == 401
    assert client.get("/v1/capabilities", headers={"Authorization": "Bearer s3cret"}).status_code == 200
