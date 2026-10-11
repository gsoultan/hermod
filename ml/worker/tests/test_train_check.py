"""POST /v1/train-check/{vhost}: the pre-flight check before a training."""

import random

import pytest

from tests.helpers import classification_rows, load, regression_rows

ROLES = {"use", "id_like", "constant", "mostly_empty", "leak_suspect", "target"}
CODES = {"few_rows", "imbalanced", "id_like", "constant", "mostly_empty", "leak_suspect", "many_classes"}


def check(client, vhost: str = "acme", **body):
    return client.post(f"/v1/train-check/{vhost}", json=body)


def roles(result: dict) -> dict[str, str]:
    return {c["name"]: c["role"] for c in result["columns"]}


def codes(result: dict) -> set[str]:
    return {w["code"] for w in result["warnings"]}


def test_classification_with_problem_columns(client):
    rows = classification_rows(n=200)
    for i, r in enumerate(rows):
        r["code"] = f"C-{i:05d}"  # one value per row
        r["region"] = "EU"  # the same everywhere
        r["note"] = "x" if i % 4 == 0 else None  # 75% empty
        r["answer"] = 1 if r["churned"] == "yes" else 0  # gives the target away
    load(client, "orders", rows)
    resp = check(client, dataset="orders", target="churned")
    assert resp.status_code == 200, resp.text
    out = resp.json()
    assert out["ok"] is True
    assert out["task"] == "classification"
    assert out["target_kind"] == "yes_no"
    assert out["classes"] == 2
    assert out["rows"] == 200
    assert out["usable_rows"] == 200
    got = roles(out)
    assert got["churned"] == "target"
    assert got["id"] == "id_like"
    assert got["code"] == "id_like"
    assert got["region"] == "constant"
    assert got["note"] == "mostly_empty"
    assert got["answer"] == "leak_suspect"
    assert got["x1"] == "use"
    assert got["city"] == "use"
    assert set(got.values()) <= ROLES
    for c in out["columns"]:
        assert set(c) == {"name", "type", "role", "note"}
        assert (c["note"] == "") == (c["role"] in ("use",))
    types = {c["name"]: c["type"] for c in out["columns"]}
    assert types["x1"] == "number"
    assert types["city"] == "string"
    assert types["flag"] == "bool"
    assert {"id_like", "constant", "mostly_empty", "leak_suspect"} <= codes(out)
    assert codes(out) <= CODES
    leak = next(w for w in out["warnings"] if w["code"] == "leak_suspect")
    assert leak["column"] == "answer"
    assert leak["message"]


def test_only_the_named_features_are_judged(client):
    load(client, "orders", classification_rows())
    out = check(client, dataset="orders", target="churned", features=["x1", "city"]).json()
    assert set(roles(out)) == {"x1", "city", "churned"}


def test_regression_leak_is_a_strong_correlation(client):
    rows = regression_rows()
    for r in rows:
        r["amount_cents"] = r["amount"] * 100
    load(client, "sales", rows)
    out = check(client, dataset="sales", target="amount").json()
    assert out["task"] == "regression"
    assert out["target_kind"] == "number"
    assert "classes" not in out
    got = roles(out)
    assert got["amount_cents"] == "leak_suspect"
    assert got["x1"] == "use"


def test_category_target_and_many_classes(client):
    rng = random.Random(1)
    load(client, "d", [{"x": rng.uniform(0, 1), "y": rng.choice("abc")} for _ in range(60)])
    out = check(client, dataset="d", target="y").json()
    assert out["target_kind"] == "category"
    assert out["classes"] == 3
    load(client, "many", [{"x": rng.uniform(0, 1), "y": f"c{i % 30}"} for i in range(300)])
    out = check(client, dataset="many", target="y").json()
    assert out["ok"] is True
    assert "many_classes" in codes(out)


def test_imbalanced_target_is_noted(client):
    load(client, "d", [{"x": i % 7, "y": "rare" if i % 20 == 0 else "common"} for i in range(200)])
    out = check(client, dataset="d", target="y").json()
    assert "imbalanced" in codes(out)
    assert out["ok"] is True


def test_few_rows_warns_and_too_few_refuses(client):
    load(client, "small", [{"x": i, "y": i % 2} for i in range(40)])
    out = check(client, dataset="small", target="y").json()
    assert out["ok"] is True
    assert "few_rows" in codes(out)
    load(client, "tiny", [{"x": i, "y": "a" if i % 2 else "b"} for i in range(6)] + [{"x": 1, "y": None}] * 5)
    out = check(client, dataset="tiny", target="y").json()
    assert out["ok"] is False
    assert out["rows"] == 11
    assert out["usable_rows"] == 6
    assert "few_rows" in codes(out)


def test_target_with_one_value_refuses(client):
    load(client, "one", [{"x": i, "y": "same"} for i in range(30)])
    out = check(client, dataset="one", target="y").json()
    assert out["ok"] is False
    assert any(w["code"] == "constant" and w.get("column") == "y" for w in out["warnings"])


def test_ignores_name_go_live_and_params_and_trains_nothing(client, tmp_path):
    load(client, "orders", classification_rows())
    resp = check(client, dataset="orders", target="churned", name="m", go_live={"mode": "always"}, params={"epochs": 5})
    assert resp.status_code == 200, resp.text
    assert not (tmp_path / "data/models").exists()


@pytest.mark.parametrize(
    "body,status,fragment",
    [
        ({"target": "churned"}, 400, "dataset"),
        ({"dataset": "orders"}, 400, "target"),
        ({"dataset": "orders", "target": "nope"}, 400, "nope"),
        ({"dataset": "orders", "target": "churned", "features": ["ghost"]}, 400, "ghost"),
        ({"dataset": "orders", "target": "churned", "preset": "quick", "algorithm": "linear"}, 400, "preset"),
        ({"dataset": "missing", "target": "churned"}, 404, "missing"),
    ],
)
def test_bad_requests(client, body, status, fragment):
    load(client, "orders", classification_rows())
    resp = check(client, **body)
    assert resp.status_code == status, resp.text
    assert fragment in resp.json()["error"]


def test_bad_vhost_is_400(client):
    assert check(client, vhost="..", dataset="orders", target="y").status_code in (400, 404)


def test_needs_the_token(make_client):
    client = make_client(token="s3cret")
    assert check(client, dataset="orders", target="y").status_code == 401
