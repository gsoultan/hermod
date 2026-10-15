"""Parquet uploads, and the sample a dataset's profile is computed from."""

import io
import os
from datetime import date, datetime, timezone
from decimal import Decimal
from pathlib import Path

import pyarrow as pa
import pyarrow.parquet as pq

from hermod_ml.datasets import SAMPLE_MAX_TEXT

BASE = "/v1/datasets/acme"
# The Go engine reads the same file (pkg/ml/builtin/table/parquet_test.go).
FLAT = Path(__file__).resolve().parents[3] / "pkg/ml/builtin/table/testdata/flat.parquet"


def parquet_bytes(table: pa.Table) -> bytes:
    buf = io.BytesIO()
    pq.write_table(table, buf)
    return buf.getvalue()


def columns(info):
    return {c["name"]: c["type"] for c in info["columns"]}


def test_parquet_upload_keeps_types_as_the_builtin_engine_does(client):
    resp = client.put(f"{BASE}/p/file?format=parquet", content=FLAT.read_bytes())
    assert resp.status_code == 200, resp.text
    info = resp.json()
    assert info["rows"] == 3
    types = columns(info)
    # A column with no value at all is typed by each engine's own rule for
    # one (string here, number there), whatever file it came from.
    types.pop("empty")
    assert types == {
        "qty": "number", "small": "number", "price": "number", "ok": "bool", "sku": "string", "day": "string",
        "at": "string", "at_utc": "string", "amount": "number", "plan": "string",
    }
    first, second, _ = client.get(f"{BASE}/p").json()["sample"]
    assert first["day"] == "2026-09-01"
    assert first["at"] == "2026-09-01T10:30:00"
    assert second["at"] == "2026-09-01T10:30:00.250000"
    assert first["at_utc"] == "2026-09-01T10:30:00+00:00"
    assert first["amount"] == 12.5 and second["amount"] == -3.05
    assert first["plan"] == "basic" and first["qty"] == 3 and second["qty"] is None


def test_parquet_upload_errors(client):
    nested = parquet_bytes(pa.table({"id": [1, 2], "tags": [["a"], ["b", "c"]]}))
    resp = client.put(f"{BASE}/p/file?format=parquet", content=nested)
    assert resp.status_code == 400
    assert "'tags' is a list, map or nested group" in resp.json()["error"]
    resp = client.put(f"{BASE}/p/file?format=parquet", content=b"a,b\n1,2\n")
    assert resp.status_code == 400
    assert "Could not read the Parquet file" in resp.json()["error"]
    assert client.put(f"{BASE}/p/file?format=parquet", content=b"").status_code == 400
    assert "'parquet'" in client.put(f"{BASE}/p/file?format=json", content=b"[]").json()["error"]
    assert client.get(f"{BASE}/p").status_code == 404


def test_parquet_upload_respects_the_size_limit(make_client):
    client = make_client(max_upload_mb=1)
    body = parquet_bytes(pa.table({"t": [os.urandom(32).hex() for _ in range(40_000)]}))
    assert len(body) > 1 << 20
    assert client.put(f"{BASE}/p/file?format=parquet", content=body).status_code == 413


def test_sample_spreads_rows_over_every_part(client):
    rows = [{"n": i, "s": f"r{i}", "ok": i % 2 == 0} for i in range(100)]
    client.post(f"{BASE}/d/rows", json={"rows": rows[:60]})
    client.post(f"{BASE}/d/rows", json={"rows": rows[60:]})
    resp = client.get(f"{BASE}/d/sample?rows=10")
    assert resp.status_code == 200, resp.text
    s = resp.json()
    assert s["name"] == "d" and s["rows"] == 100 and s["sample_rows"] == 10
    assert [c["name"] for c in s["columns"]] == ["n", "s", "ok"]
    assert s["values"]["n"] == list(range(0, 100, 10))
    assert s["values"]["s"][5] == "r50"
    assert s["values"]["ok"][1] is True
    assert client.get(f"{BASE}/d/sample?rows=1000").json()["sample_rows"] == 100


def test_sample_is_bounded(client):
    client.post(f"{BASE}/d/rows", json={"rows": [{"note": "é" * (SAMPLE_MAX_TEXT + 50), "gap": None}]})
    s = client.get(f"{BASE}/d/sample?rows=9999999").json()
    assert s["values"]["note"][0] == "é" * SAMPLE_MAX_TEXT
    assert s["values"]["gap"] == [None]
    wide = {f"c{i}": i for i in range(200)}
    client.post(f"{BASE}/w/rows", json={"rows": [wide] * 6000})
    assert client.get(f"{BASE}/w/sample?rows=50000").json()["sample_rows"] == 5000


def test_sample_errors(client):
    assert client.get(f"{BASE}/missing/sample?rows=10").status_code == 404
    client.post(f"{BASE}/d/rows", json={"rows": [{"a": 1}]})
    assert client.get(f"{BASE}/d/sample?rows=0").status_code == 400
    assert client.get(f"{BASE}/d/sample?rows=x").status_code == 400
