"""Training with a custom script: POST /v1/models/{vhost}/{name}/train-custom.

The script runs in the sandbox (test_sandbox.py covers its limits); these
tests cover the API around it: the switch, the request, the checks on the
ONNX the script returns, and that an accepted model serves like a built-in.
"""

from __future__ import annotations

import json

import pytest

from hermod_ml.sandbox import SandboxSettings
from tests.helpers import classification_rows, load, regression_rows
from tests.scripts import EXTERNAL_DATA, EXTRA_CLASS, GOOD, NOT_ONNX, ONE_CLASS, WRONG_INPUTS, ZIPMAP, script, sha, with_body

ON = SandboxSettings(enabled=True, timeout_seconds=120)


@pytest.fixture
def custom(make_client):
    client = make_client(token="t", sandbox=ON)
    client.headers["Authorization"] = "Bearer t"
    load(client, "orders", classification_rows())
    load(client, "sales", regression_rows())
    return client


def train_custom(client, model: str, source: str = GOOD, name: str = "trees", vhost: str = "acme", **body):
    body.setdefault("dataset", "orders")
    body.setdefault("target", "churned")
    body.setdefault("algorithm", f"custom:{name}")
    body.setdefault("script", script(name, source))
    return client.post(f"/v1/models/{vhost}/{model}/train-custom", json=body)


def infer(client, model: str, inputs: list[dict], vhost: str = "acme"):
    return client.post(f"/vhosts/{vhost}/v2/models/{model}/infer", json={"inputs": inputs})


def test_custom_training_is_off_by_default(make_client):
    client = make_client()
    load(client, "orders", classification_rows())
    resp = train_custom(client, "churn")
    assert resp.status_code == 403
    assert "HERMOD_ML_CUSTOM_SCRIPTS" in resp.json()["error"]


def test_a_good_classification_script_trains_and_serves(custom, tmp_path):
    resp = train_custom(custom, "churn", features=["x1", "x2", "city", "flag"])
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    assert meta["version"] == "1"
    assert meta["algorithm"] == "custom:trees"
    assert meta["script"] == {"name": "trees", "sha256": sha(GOOD)}
    assert meta["task"] == "classification"
    assert meta["labels"] == ["no", "yes"]
    assert meta["features"] == ["x1", "x2", "city", "flag"]
    assert meta["feature_types"] == {"x1": "number", "x2": "number", "city": "string", "flag": "number"}
    assert {"accuracy", "f1", "roc_auc", "score"} <= set(meta["metrics"])
    assert meta["metrics"]["accuracy"] > 0.6
    assert meta["rows"] == {"train": 96, "test": 24}
    assert "training on 96 rows" in meta["log"]
    assert json.loads((tmp_path / "data/models/acme/churn/1/meta.json").read_text()) == meta

    out = infer(
        custom,
        "churn",
        [
            {"name": "x1", "datatype": "FP64", "shape": [2], "data": [2.5, -2.5]},
            {"name": "x2", "datatype": "INT64", "shape": [2], "data": [1, 1]},
            {"name": "city", "datatype": "BYTES", "shape": [2], "data": ["Oslo", "Bergen"]},
            {"name": "flag", "datatype": "BOOL", "shape": [2], "data": [True, False]},
        ],
    )
    assert out.status_code == 200, out.text
    outputs = {o["name"]: o["data"] for o in out.json()["outputs"]}
    assert all(label in ("no", "yes") for label in outputs["label"])
    assert all(-1e-6 <= p <= 1 + 1e-6 for p in outputs["probability"])


def test_a_good_regression_script_trains_and_serves(custom):
    resp = train_custom(custom, "amount", dataset="sales", target="amount")
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    assert meta["task"] == "regression"
    assert {"rmse", "mae", "r2", "score"} <= set(meta["metrics"])
    out = infer(
        custom,
        "amount",
        [
            {"name": "x1", "datatype": "FP64", "shape": [1], "data": [5]},
            {"name": "x2", "datatype": "FP64", "shape": [1], "data": [None]},
            {"name": "city", "datatype": "BYTES", "shape": [1], "data": ["Oslo"]},
        ],
    )
    assert out.status_code == 200, out.text
    assert isinstance(out.json()["outputs"][0]["data"][0], float)


@pytest.mark.parametrize(
    "source, fragment",
    [
        (WRONG_INPUTS, "inputs"),
        (ZIPMAP, "probabilities"),
        (NOT_ONNX, "not a valid ONNX model"),
        (EXTRA_CLASS, "2 labels"),
        (ONE_CLASS, "crashed onnxruntime"),
        (EXTERNAL_DATA, "external data"),
    ],
    ids=["wrong-inputs", "zipmap", "not-onnx", "wrong-class-count", "aborts-onnxruntime", "external-data"],
)
def test_a_bad_onnx_is_rejected(custom, tmp_path, source, fragment):
    resp = train_custom(custom, "churn", source=source)
    assert resp.status_code == 422, resp.text
    assert fragment in resp.json()["error"]
    assert not (tmp_path / "data/models/acme/churn").exists()


def test_a_failing_script_reports_its_error(custom):
    resp = train_custom(custom, "churn", source=with_body("raise RuntimeError('no luck today')"))
    assert resp.status_code == 422
    assert "RuntimeError: no luck today" in resp.json()["error"]


@pytest.mark.parametrize(
    "patch, fragment",
    [
        ({"script": {"name": "trees", "sha256": "0" * 64, "source": GOOD}}, "sha256"),
        ({"algorithm": "custom:other"}, "custom:trees"),
        ({"algorithm": "random_forest"}, "custom:trees"),
        ({"script": {"name": "../x", "sha256": sha(GOOD), "source": GOOD}}, "script name"),
        ({"script": "print(1)"}, "'script'"),
        ({"script": {"name": "trees", "sha256": sha(""), "source": ""}}, "empty"),
        ({"target": "nope"}, "nope"),
    ],
)
def test_a_bad_request_is_refused_before_anything_runs(custom, patch, fragment):
    resp = train_custom(custom, "churn", **patch)
    assert resp.status_code == 400, resp.text
    assert fragment in resp.json()["error"]


def test_a_script_larger_than_the_limit_is_refused(custom):
    big = "# " + "x" * (300 * 1024) + "\n" + GOOD
    resp = train_custom(custom, "churn", source=big)
    assert resp.status_code == 400
    assert "256 KB" in resp.json()["error"]
