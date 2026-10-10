import pytest

from tests.helpers import classification_rows, load, regression_rows, train


def col(name, data, datatype="FP64"):
    return {"name": name, "shape": [len(data)], "datatype": datatype, "data": data}


@pytest.fixture
def churn(client):
    load(client, "orders", classification_rows())
    resp = train(client, "churn", dataset="orders", target="churned", features=["x1", "city", "flag"])
    assert resp.status_code == 200, resp.text
    return client


@pytest.fixture
def sales(client):
    load(client, "sales", regression_rows())
    resp = train(client, "amt", dataset="sales", target="amount", algorithm="linear")
    assert resp.status_code == 200, resp.text
    return client


def infer(client, model, inputs, version=None, vhost="acme"):
    path = f"/vhosts/{vhost}/v2/models/{model}"
    if version is not None:
        path += f"/versions/{version}"
    return client.post(path + "/infer", json={"inputs": inputs})


def test_health_endpoints(client):
    assert client.get("/v2/health/live").status_code == 200
    assert client.get("/v2/health/ready").status_code == 200


def test_metadata_for_classifier(churn):
    resp = churn.get("/vhosts/acme/v2/models/churn")
    assert resp.status_code == 200
    assert resp.json() == {
        "name": "churn",
        "versions": ["1"],
        "platform": "onnx",
        "inputs": [
            {"name": "x1", "datatype": "FP64", "shape": [-1]},
            {"name": "city", "datatype": "BYTES", "shape": [-1]},
            {"name": "flag", "datatype": "FP64", "shape": [-1]},
        ],
        "outputs": [
            {"name": "label", "datatype": "BYTES", "shape": [-1]},
            {"name": "probability", "datatype": "FP64", "shape": [-1]},
        ],
    }


def test_metadata_for_regressor_and_int_labels(client):
    load(client, "sales", regression_rows())
    train(client, "amt", dataset="sales", target="amount")
    meta = client.get("/vhosts/acme/v2/models/amt").json()
    assert meta["outputs"] == [{"name": "value", "datatype": "FP64", "shape": [-1]}]
    rows = classification_rows()
    for r in rows:
        r["tier"] = r["x2"] % 3
    load(client, "d", rows)
    train(client, "tier", dataset="d", target="tier", features=["x1"])
    meta = client.get("/vhosts/acme/v2/models/tier").json()
    assert meta["outputs"][0] == {"name": "label", "datatype": "INT64", "shape": [-1]}


def test_metadata_versions(churn):
    train(churn, "churn", dataset="orders", target="churned", features=["x1"])
    assert churn.get("/vhosts/acme/v2/models/churn").json()["versions"] == ["2", "1"]
    v1 = churn.get("/vhosts/acme/v2/models/churn/versions/1").json()
    assert v1["versions"] == ["1"]
    assert [i["name"] for i in v1["inputs"]] == ["x1", "city", "flag"]
    newest = churn.get("/vhosts/acme/v2/models/churn").json()
    assert [i["name"] for i in newest["inputs"]] == ["x1"]
    assert churn.get("/vhosts/acme/v2/models/churn/versions/9").status_code == 404
    assert churn.get("/vhosts/acme/v2/models/churn/versions/abc").status_code == 404
    assert churn.get("/vhosts/acme/v2/models/nope").status_code == 404
    assert churn.get("/vhosts/other/v2/models/churn").status_code == 404


def test_hermod_style_classification_request(churn):
    # Column tensors as Hermod sends them: numbers as FP64, a null numeric,
    # a BYTES string feature, a BOOL, and extra inputs that are not features.
    inputs = [
        col("x1", [2.5, None, -2.9]),
        {"name": "city", "shape": [3, 1], "datatype": "BYTES", "data": ["Oslo", "Bergen", None]},
        col("flag", [True, False, False], "BOOL"),
        col("id", [1, 2, 3], "INT64"),
        col("note", ["a", "b", "c"], "BYTES"),
    ]
    resp = infer(churn, "churn", inputs)
    assert resp.status_code == 200, resp.text
    body = resp.json()
    assert body["model_name"] == "churn"
    assert body["model_version"] == "1"
    label, prob = body["outputs"]
    assert label["name"] == "label" and label["datatype"] == "BYTES" and label["shape"] == [3]
    assert prob["name"] == "probability" and prob["datatype"] == "FP64" and prob["shape"] == [3]
    assert label["data"][0] == "yes"
    assert label["data"][2] == "no"
    assert all(set(label["data"]) <= {"yes", "no"} for _ in [0])
    assert all(0.5 <= p <= 1.0 for p in prob["data"])


def test_null_numeric_uses_saved_median(sales):
    meta = sales.get("/v1/models/acme/amt/versions").json()["versions"][0]
    median = meta["fill"]["x2"]
    with_null = infer(sales, "amt", [col("x1", [4.0]), col("x2", [None]), col("city", ["Oslo"], "BYTES")]).json()
    with_median = infer(sales, "amt", [col("x1", [4.0]), col("x2", [median]), col("city", ["Oslo"], "BYTES")]).json()
    assert with_null["outputs"] == with_median["outputs"]
    out = with_null["outputs"][0]
    assert out["name"] == "value" and out["datatype"] == "FP64" and out["shape"] == [1]
    assert out["data"][0] == pytest.approx(3 * 4 + 2 * median + 5, abs=1.0)


@pytest.mark.parametrize("datatype,values", [("FP32", [4.0]), ("INT64", [4]), ("INT32", [4]), ("BYTES", ["4"]), ("BYTES", [" 4.0 "])])
def test_numeric_feature_accepts_numeric_datatypes(sales, datatype, values):
    resp = infer(sales, "amt", [col("x1", values, datatype), col("x2", [1.0]), col("city", ["Bergen"], "BYTES")])
    assert resp.status_code == 200, resp.text
    assert resp.json()["outputs"][0]["data"][0] == pytest.approx(3 * 4 + 2, abs=1.0)


def test_numeric_feature_in_a_mixed_bytes_tensor(sales):
    # Hermod sends a column holding a null as BYTES, so its numbers arrive as
    # JSON numbers inside a BYTES tensor.
    resp = infer(sales, "amt", [col("x1", [4, None], "BYTES"), col("x2", [1.0, 1.0]), col("city", ["Bergen", "Oslo"], "BYTES")])
    assert resp.status_code == 200, resp.text
    assert resp.json()["outputs"][0]["data"][0] == pytest.approx(3 * 4 + 2, abs=1.0)


def test_string_feature_accepts_any_value(sales):
    resp = infer(sales, "amt", [col("x1", [1.0, 1.0]), col("x2", [1.0, 1.0]), col("city", [7, None], "INT64")])
    assert resp.status_code == 200, resp.text


def test_missing_feature_is_400_naming_it(sales):
    resp = infer(sales, "amt", [col("x1", [1.0]), col("city", ["Oslo"], "BYTES")])
    assert resp.status_code == 400
    assert "x2" in resp.json()["error"]


@pytest.mark.parametrize(
    "x1",
    [
        col("x1", ["1.0", "abc"], "BYTES"),
        col("x1", ["1.0", "nan"], "BYTES"),
        col("x1", [1.0, {"a": 1}], "FP64"),
        col("x1", [1.0, "2"], "FP64"),
        col("x1", [True, 2], "BOOL"),
    ],
)
def test_uncoercible_numeric_is_400_naming_feature_and_row(sales, x1):
    resp = infer(sales, "amt", [x1, col("x2", [1.0, 1.0]), col("city", ["Oslo", "Oslo"], "BYTES")])
    assert resp.status_code == 400
    error = resp.json()["error"]
    assert "x1" in error and "row 1" in error


@pytest.mark.parametrize(
    "body",
    [
        {},
        {"inputs": "x"},
        {"inputs": [{"name": "x1"}]},
        {"inputs": [{"name": "x1", "shape": [1], "datatype": "FP16", "data": [1.0]}]},
        {"inputs": [{"name": "x1", "shape": [2], "datatype": "FP64", "data": [1.0]}]},
        {"inputs": [{"name": "x1", "shape": [1, 2], "datatype": "FP64", "data": [1.0, 2.0]}]},
        {"inputs": [{"name": "x1", "shape": [], "datatype": "FP64", "data": []}]},
    ],
)
def test_malformed_infer_requests_are_400(sales, body):
    resp = sales.post("/vhosts/acme/v2/models/amt/infer", json=body)
    assert resp.status_code == 400, resp.text
    assert "error" in resp.json()


def test_inputs_must_share_n(sales):
    resp = infer(sales, "amt", [col("x1", [1.0, 2.0]), col("x2", [1.0]), col("city", ["Oslo", "Oslo"], "BYTES")])
    assert resp.status_code == 400


def test_nested_data_for_n_by_1_shape(sales):
    x1 = {"name": "x1", "shape": [2, 1], "datatype": "FP64", "data": [[1.0], [2.0]]}
    resp = infer(sales, "amt", [x1, col("x2", [1.0, 1.0]), col("city", ["Oslo", "Oslo"], "BYTES")])
    assert resp.status_code == 200, resp.text
    assert resp.json()["outputs"][0]["shape"] == [2]


def test_version_selection(client):
    load(client, "sales", regression_rows())
    train(client, "m", dataset="sales", target="amount", features=["x1"], algorithm="linear")
    train(client, "m", dataset="sales", target="amount", features=["x1", "x2", "city"], algorithm="linear")
    x1_only = [col("x1", [5.0])]
    # Newest (v2) needs x2 and city; v1 only x1.
    assert infer(client, "m", x1_only).status_code == 400
    v1 = infer(client, "m", x1_only, version="1")
    assert v1.status_code == 200
    assert v1.json()["model_version"] == "1"
    full = x1_only + [col("x2", [0.0]), col("city", ["Oslo"], "BYTES")]
    assert infer(client, "m", full).json()["model_version"] == "2"
    assert infer(client, "m", full, version="2").json()["model_version"] == "2"
    assert infer(client, "m", full, version="3").status_code == 404
    assert infer(client, "nope", full).status_code == 404


def test_integer_labels_are_int64(client):
    rows = classification_rows()
    for r in rows:
        r["tier"] = 10 if r["churned"] == "yes" else 20
    load(client, "d", rows)
    train(client, "tier", dataset="d", target="tier", features=["x1", "city", "flag"])
    resp = infer(client, "tier", [col("x1", [2.5]), col("city", ["Oslo"], "BYTES"), col("flag", [1], "INT64")])
    label = resp.json()["outputs"][0]
    assert label["datatype"] == "INT64"
    assert label["data"] == [10]


def test_bool_target_labels_are_true_false(client):
    rows = classification_rows()
    for r in rows:
        r["won"] = r["churned"] == "yes"
    load(client, "d", rows)
    train(client, "won", dataset="d", target="won", features=["x1", "city", "flag"])
    resp = infer(client, "won", [col("x1", [2.5]), col("city", ["Oslo"], "BYTES"), col("flag", [True], "BOOL")])
    label = resp.json()["outputs"][0]
    assert label["datatype"] == "BYTES"
    assert label["data"] == ["true"]


def test_cache_is_lru_and_bounded(make_client):
    client = make_client(model_cache=2)
    load(client, "sales", regression_rows())
    for m in ("a", "b", "c"):
        train(client, m, dataset="sales", target="amount", features=["x1"], algorithm="linear")
    cache = client.app.state.server.cache
    x = [col("x1", [1.0])]
    infer(client, "a", x)
    infer(client, "b", x)
    assert cache.keys() == [("acme", "a", "1"), ("acme", "b", "1")]
    infer(client, "a", x)  # touch a so b is least recently used
    infer(client, "c", x)
    assert cache.keys() == [("acme", "a", "1"), ("acme", "c", "1")]


def test_delete_model_evicts_from_cache(sales):
    x = [col("x1", [1.0]), col("x2", [1.0]), col("city", ["Oslo"], "BYTES")]
    assert infer(sales, "amt", x).status_code == 200
    cache = sales.app.state.server.cache
    assert ("acme", "amt", "1") in cache.keys()
    assert sales.delete("/v1/models/acme/amt").status_code == 204
    assert cache.keys() == []
    assert infer(sales, "amt", x).status_code == 404


def test_retrained_model_after_delete_is_not_served_stale(client):
    load(client, "sales", regression_rows())
    train(client, "m", dataset="sales", target="amount", features=["x1"], algorithm="linear")
    x = [col("x1", [1.0])]
    assert infer(client, "m", x).status_code == 200
    client.delete("/v1/models/acme/m")
    train(client, "m", dataset="sales", target="amount", features=["x2"], algorithm="linear")
    # Version "1" now has a different feature; the old session must be gone.
    resp = infer(client, "m", x)
    assert resp.status_code == 400
    assert "x2" in resp.json()["error"]


def test_onnx_output_matches_training_parity(churn):
    # Every prediction should be one of the training labels with a probability.
    rows = classification_rows(n=40, seed=99)
    inputs = [
        col("x1", [r["x1"] for r in rows]),
        col("city", [r["city"] for r in rows], "BYTES"),
        col("flag", [r["flag"] for r in rows], "BOOL"),
    ]
    out = infer(churn, "churn", inputs).json()["outputs"]
    assert len(out[0]["data"]) == 40
    agree = sum(a == r["churned"] for a, r in zip(out[0]["data"], rows))
    assert agree / 40 > 0.7


def test_xgboost_models_serve_original_labels(client):
    load(client, "orders", classification_rows())
    load(client, "sales", regression_rows())
    assert train(client, "xc", dataset="orders", target="churned", algorithm="xgboost", features=["x1", "city", "flag"]).status_code == 200
    assert train(client, "xr", dataset="sales", target="amount", algorithm="xgboost").status_code == 200
    resp = infer(client, "xc", [col("x1", [2.5, -2.9]), col("city", ["Oslo", None], "BYTES"), col("flag", [True, False], "BOOL")])
    assert resp.status_code == 200, resp.text
    label, prob = resp.json()["outputs"]
    assert label["datatype"] == "BYTES"
    assert label["data"] == ["yes", "no"]
    assert all(0.5 <= p <= 1.0 for p in prob["data"])
    resp = infer(client, "xr", [col("x1", [4.0]), col("x2", [None]), col("city", ["Oslo"], "BYTES")])
    assert resp.status_code == 200, resp.text
    assert resp.json()["outputs"][0]["data"][0] == pytest.approx(3 * 4 + 5, abs=5.0)
