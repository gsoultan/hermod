"""Moving a dataset to a training pool and a trained version back.

Hermod trains on a separate worker pool (custom scripts, GPU) by copying the
dataset there and the trained version back to the worker that serves it.
Hermod only pipes the bytes; the serving worker checks what comes back,
because a pool that runs uploaded code is not trusted.
"""

from __future__ import annotations

import base64
import io

import onnx
import pyarrow as pa
import pyarrow.parquet as pq
import pytest
from fastapi.testclient import TestClient

from hermod_ml.app import create_app
from hermod_ml.settings import Settings
from tests.helpers import classification_rows, load, train


@pytest.fixture
def pair(tmp_path):
    """Two workers: `main` serves, `pool` trains."""
    main = TestClient(create_app(Settings(data_dir=tmp_path / "main")))
    pool = TestClient(create_app(Settings(data_dir=tmp_path / "pool")))
    return main, pool


def test_a_dataset_round_trips_through_export_and_import(pair):
    main, pool = pair
    load(main, "orders", classification_rows())
    exported = main.get("/v1/datasets/acme/orders/export")
    assert exported.status_code == 200
    assert exported.headers["content-type"] == "application/vnd.apache.parquet"

    imported = pool.put("/v1/datasets/acme/job-1/import", content=exported.content)
    assert imported.status_code == 200, imported.text
    a = main.get("/v1/datasets/acme/orders").json()
    b = pool.get("/v1/datasets/acme/job-1").json()
    assert b["rows"] == a["rows"] == 120
    assert b["columns"] == a["columns"]
    assert b["sample"] == a["sample"]


def test_an_unknown_dataset_cannot_be_exported(pair):
    main, _ = pair
    assert main.get("/v1/datasets/acme/nope/export").status_code == 404


@pytest.mark.parametrize(
    "body, fragment",
    [
        (b"not parquet", "parquet"),
        (b"", "parquet"),
    ],
)
def test_import_refuses_what_is_not_parquet(pair, body, fragment):
    _, pool = pair
    resp = pool.put("/v1/datasets/acme/job-1/import", content=body)
    assert resp.status_code == 400
    assert fragment in resp.json()["error"]


def test_import_refuses_column_types_a_dataset_cannot_hold(pair):
    _, pool = pair
    table = pa.table({"tags": pa.array([[1, 2], [3]], type=pa.list_(pa.int64()))})
    buf = io.BytesIO()
    pq.write_table(table, buf)
    resp = pool.put("/v1/datasets/acme/job-1/import", content=buf.getvalue())
    assert resp.status_code == 400
    assert "tags" in resp.json()["error"]


def trained_artifact(pool) -> dict:
    load(pool, "job-1", classification_rows())
    resp = train(pool, "job-1", dataset="job-1", target="churned", features=["x1", "city"])
    assert resp.status_code == 200, resp.text
    art = pool.get("/v1/models/acme/job-1/versions/1/export")
    assert art.status_code == 200, art.text
    return art.json()


def test_a_version_moves_to_the_serving_worker_and_serves_there(pair):
    main, pool = pair
    art = trained_artifact(pool)
    assert set(art) == {"meta", "onnx"}
    for _ in range(2):  # the serving worker numbers versions itself
        resp = main.post("/v1/models/acme/churn/import?dataset=orders", json=art)
        assert resp.status_code == 200, resp.text
    meta = resp.json()
    assert meta["version"] == "2"
    assert meta["model"] == "churn"
    assert meta["dataset"] == "orders"
    assert meta["metrics"] == art["meta"]["metrics"]

    out = main.post(
        "/vhosts/acme/v2/models/churn/infer",
        json={"inputs": [
            {"name": "x1", "datatype": "FP64", "shape": [1], "data": [2.0]},
            {"name": "city", "datatype": "BYTES", "shape": [1], "data": ["Oslo"]},
        ]},
    )
    assert out.status_code == 200, out.text
    assert out.json()["model_version"] == "2"


def test_an_unknown_version_cannot_be_exported(pair):
    _, pool = pair
    trained_artifact(pool)
    assert pool.get("/v1/models/acme/job-1/versions/9/export").status_code == 404


def _graph(art: dict) -> onnx.ModelProto:
    return onnx.load_from_string(base64.b64decode(art["onnx"]))


def _with_graph(art: dict, model: onnx.ModelProto) -> dict:
    return {**art, "onnx": base64.b64encode(model.SerializeToString()).decode()}


def _rename_first_input(art: dict) -> dict:
    model = _graph(art)
    old = model.graph.input[0].name
    model.graph.input[0].name = "renamed"
    for node in model.graph.node:
        for i, name in enumerate(node.input):
            if name == old:
                node.input[i] = "renamed"
    return _with_graph(art, model)


def _external_initializer(art: dict) -> dict:
    model = _graph(art)
    init = model.graph.initializer[0]
    init.ClearField("raw_data")
    init.ClearField("float_data")
    init.ClearField("int64_data")
    init.ClearField("string_data")
    init.data_location = onnx.TensorProto.EXTERNAL
    entry = init.external_data.add()
    entry.key, entry.value = "location", "../../../../etc/passwd"
    return _with_graph(art, model)


@pytest.mark.parametrize(
    "tamper, fragment",
    [
        (lambda a: {**a, "onnx": "!!!"}, "base64"),
        (lambda a: {**a, "onnx": base64.b64encode(b"junk").decode()}, "not a valid ONNX model"),
        (_rename_first_input, "inputs"),
        (_external_initializer, "external data"),
        (lambda a: {**a, "meta": {**a["meta"], "task": "clustering"}}, "task"),
        (lambda a: {**a, "meta": {**a["meta"], "features": ["x1"]}}, "feature"),
        (lambda a: {**a, "meta": {**a["meta"], "labels": ["no", "yes", "maybe"]}}, "3 labels"),
        (lambda a: {**a, "meta": {**a["meta"], "fill": {"x1": "NaN"}}}, "fill"),
        (lambda a: {"meta": a["meta"]}, "onnx"),
    ],
    ids=["bad-base64", "not-onnx", "renamed-input", "external-data", "bad-task", "features-mismatch",
         "labels-mismatch", "bad-fill", "missing-onnx"],
)
def test_the_serving_worker_rejects_a_tampered_version(pair, tamper, fragment, tmp_path):
    main, pool = pair
    art = tamper(trained_artifact(pool))
    resp = main.post("/v1/models/acme/churn/import", json=art)
    assert resp.status_code in (400, 422), resp.text
    assert fragment in resp.json()["error"]
    assert not (tmp_path / "main/models/acme/churn").exists()


def test_import_refuses_a_bad_dataset_name(pair):
    main, pool = pair
    art = trained_artifact(pool)
    resp = main.post("/v1/models/acme/churn/import?dataset=../x", json=art)
    assert resp.status_code == 400
