import json
from datetime import datetime

from tests.helpers import xlsx_bytes

BASE = "/v1/datasets/acme"


def post_rows(client, name, rows, replace=None, vhost="acme"):
    body = {"rows": rows}
    if replace is not None:
        body["replace"] = replace
    return client.post(f"/v1/datasets/{vhost}/{name}/rows", json=body)


def columns(info):
    return {c["name"]: c["type"] for c in info["columns"]}


def test_append_rows_reports_running_total(client, tmp_path):
    r1 = post_rows(client, "orders", [{"a": 1, "b": "x"}, {"a": 2, "b": "y"}])
    assert r1.status_code == 200
    assert r1.json() == {"name": "orders", "rows": 2}
    r2 = post_rows(client, "orders", [{"a": 3, "b": "z"}])
    assert r2.json() == {"name": "orders", "rows": 3}
    parts = sorted(p.name for p in (tmp_path / "data/datasets/acme/orders").iterdir())
    assert parts == ["part-000001.parquet", "part-000002.parquet"]


def test_replace_drops_existing_parts(client, tmp_path):
    post_rows(client, "orders", [{"a": 1}, {"a": 2}])
    resp = post_rows(client, "orders", [{"a": 9}], replace=True)
    assert resp.json() == {"name": "orders", "rows": 1}
    info = client.get(f"{BASE}/orders").json()
    assert info["sample"] == [{"a": 9}]
    parts = list((tmp_path / "data/datasets/acme/orders").iterdir())
    assert len(parts) == 1


def test_empty_rows_with_replace_creates_empty_dataset(client):
    post_rows(client, "orders", [{"a": 1}])
    resp = post_rows(client, "orders", [], replace=True)
    assert resp.json() == {"name": "orders", "rows": 0}
    info = client.get(f"{BASE}/orders").json()
    assert info["rows"] == 0
    assert info["columns"] == []
    assert info["sample"] == []

    resp = post_rows(client, "fresh", [], replace=True)
    assert resp.json() == {"name": "fresh", "rows": 0}
    assert client.get(f"{BASE}/fresh").status_code == 200


def test_value_types_and_json_text_for_objects(client):
    rows = [
        {"n": 1, "f": 1.5, "s": "hi", "b": True, "z": None, "o": {"k": [1, 2]}, "l": [1, "a"]},
        {"n": 2, "f": None, "s": None, "b": False, "z": None, "o": None, "l": []},
    ]
    assert post_rows(client, "t", rows).status_code == 200
    info = client.get(f"{BASE}/t").json()
    assert columns(info) == {"n": "number", "f": "number", "s": "string", "b": "bool", "z": "string", "o": "string", "l": "string"}
    first, second = info["sample"]
    assert first["n"] == 1 and first["f"] == 1.5 and first["s"] == "hi" and first["b"] is True
    assert first["z"] is None
    assert json.loads(first["o"]) == {"k": [1, 2]}
    assert json.loads(first["l"]) == [1, "a"]
    assert second["f"] is None and second["s"] is None and second["b"] is False
    assert second["l"] == "[]"


def test_mixed_types_across_parts_become_string(client):
    post_rows(client, "m", [{"v": 1}, {"v": 2}])
    post_rows(client, "m", [{"v": "three"}])
    info = client.get(f"{BASE}/m").json()
    assert columns(info) == {"v": "string"}
    assert [r["v"] for r in info["sample"]] == ["1", "2", "three"]


def test_columns_missing_from_some_rows_or_parts_are_null(client):
    post_rows(client, "g", [{"a": 1}, {"b": "x"}])
    post_rows(client, "g", [{"c": True}])
    info = client.get(f"{BASE}/g").json()
    assert [c["name"] for c in info["columns"]] == ["a", "b", "c"]
    assert info["sample"] == [
        {"a": 1, "b": None, "c": None},
        {"a": None, "b": "x", "c": None},
        {"a": None, "b": None, "c": True},
    ]


def test_info_shape_and_sample_limited_to_20(client):
    post_rows(client, "big", [{"i": i} for i in range(25)])
    info = client.get(f"{BASE}/big").json()
    assert set(info) == {"name", "rows", "columns", "updated_at", "sample"}
    assert info["rows"] == 25
    assert [r["i"] for r in info["sample"]] == list(range(20))
    # RFC3339 with an explicit UTC offset.
    assert info["updated_at"].endswith("Z")
    datetime.fromisoformat(info["updated_at"].replace("Z", "+00:00"))


def test_list_is_sorted_and_scoped_to_vhost(client):
    post_rows(client, "zeta", [{"a": 1}])
    post_rows(client, "alpha", [{"a": 1}, {"a": 2}])
    post_rows(client, "other", [{"a": 1}], vhost="elsewhere")
    resp = client.get(BASE)
    assert resp.status_code == 200
    data = resp.json()["datasets"]
    assert [d["name"] for d in data] == ["alpha", "zeta"]
    assert set(data[0]) == {"name", "rows", "columns", "updated_at"}
    assert data[0]["rows"] == 2


def test_list_unknown_vhost_is_empty(client):
    assert client.get("/v1/datasets/nobody").json() == {"datasets": []}


def test_get_and_delete_missing_dataset_is_404(client):
    assert client.get(f"{BASE}/missing").status_code == 404
    resp = client.delete(f"{BASE}/missing")
    assert resp.status_code == 404
    assert "error" in resp.json()


def test_delete_dataset(client, tmp_path):
    post_rows(client, "gone", [{"a": 1}])
    resp = client.delete(f"{BASE}/gone")
    assert resp.status_code == 204
    assert resp.content == b""
    assert client.get(f"{BASE}/gone").status_code == 404
    assert not (tmp_path / "data/datasets/acme/gone").exists()


def test_rows_body_validation(client):
    url = f"{BASE}/x/rows"
    assert client.post(url, content=b"not json", headers={"content-type": "application/json"}).status_code == 400
    assert client.post(url, json={"rows": "nope"}).status_code == 400
    assert client.post(url, json={"rows": [1, 2]}).status_code == 400
    assert client.post(url, json={"rows": [], "replace": "yes"}).status_code == 400
    assert client.post(url, json=[]).status_code == 400
    assert client.post(url, json={}).status_code == 400


def test_csv_upload_replaces_dataset(client):
    post_rows(client, "c", [{"old": 1}])
    csv = b"id,price,city,active\n1,9.5,Oslo,true\n2,,Bergen,false\n3,4,,true\n"
    resp = client.put(f"{BASE}/c/file?format=csv", content=csv)
    assert resp.status_code == 200
    info = resp.json()
    assert info["name"] == "c"
    assert info["rows"] == 3
    assert columns(info) == {"id": "number", "price": "number", "city": "string", "active": "bool"}
    sample = client.get(f"{BASE}/c").json()["sample"]
    assert sample[0] == {"id": 1, "price": 9.5, "city": "Oslo", "active": True}
    assert sample[1]["price"] is None
    assert sample[2]["city"] is None


def test_xlsx_upload_reads_first_sheet(client):
    body = xlsx_bytes(
        [["name", "qty", "ok"], ["a", 1, True], ["b", 2.5, False]],
        extra_sheet=[["ignored"], ["x"]],
    )
    resp = client.put(f"{BASE}/x/file?format=xlsx", content=body)
    assert resp.status_code == 200
    info = resp.json()
    assert info["rows"] == 2
    assert columns(info) == {"name": "string", "qty": "number", "ok": "bool"}
    sample = client.get(f"{BASE}/x").json()["sample"]
    assert sample == [{"name": "a", "qty": 1, "ok": True}, {"name": "b", "qty": 2.5, "ok": False}]


def test_file_upload_errors(client):
    assert client.put(f"{BASE}/x/file?format=json", content=b"a\n1\n").status_code == 400
    assert client.put(f"{BASE}/x/file", content=b"a\n1\n").status_code == 400
    assert client.put(f"{BASE}/x/file?format=csv", content=b"").status_code == 400
    assert client.put(f"{BASE}/x/file?format=xlsx", content=b"not a workbook").status_code == 400


def test_upload_too_big_is_413(make_client):
    client = make_client(max_upload_mb=1)
    big = b"a\n" + b"1\n" * (600 * 1024)
    resp = client.put(f"{BASE}/x/file?format=csv", content=big)
    assert resp.status_code == 413
    assert "error" in resp.json()
    rows = {"rows": [{"a": "x" * 1024} for _ in range(1100)]}
    assert client.post(f"{BASE}/x/rows", json=rows).status_code == 413
    # Nothing was stored.
    assert client.get(f"{BASE}/x").status_code == 404
