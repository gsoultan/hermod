"""Small helpers shared by tests."""

from __future__ import annotations

import io

from openpyxl import Workbook


def xlsx_bytes(rows: list[list], extra_sheet: list[list] | None = None) -> bytes:
    """Build an in-memory workbook; the first sheet holds `rows` (header first)."""
    wb = Workbook()
    ws = wb.active
    ws.title = "first"
    for row in rows:
        ws.append(row)
    if extra_sheet is not None:
        other = wb.create_sheet("second")
        for row in extra_sheet:
            other.append(row)
    buf = io.BytesIO()
    wb.save(buf)
    return buf.getvalue()


def classification_rows(n: int = 120, seed: int = 7) -> list[dict]:
    """Rows whose string target `churned` depends on x1, city and flag."""
    import random

    rng = random.Random(seed)
    rows = []
    for i in range(n):
        x1 = round(rng.uniform(-3, 3), 2)
        x2 = rng.randint(0, 9)
        city = rng.choice(["Oslo", "Bergen", "Tromso"])
        flag = rng.random() < 0.5
        score = x1 + (1.5 if city == "Oslo" else 0) + (1 if flag else -1)
        rows.append(
            {
                "id": i,
                "x1": None if i % 17 == 0 else x1,
                "x2": x2,
                "city": None if i % 23 == 0 else city,
                "flag": flag,
                "churned": "yes" if score > 0.5 else "no",
            }
        )
    return rows


def regression_rows(n: int = 120, seed: int = 11) -> list[dict]:
    """Rows whose numeric target `amount` is linear in x1/x2 plus a city effect."""
    import random

    rng = random.Random(seed)
    rows = []
    for i in range(n):
        x1 = round(rng.uniform(0, 10), 2)
        x2 = round(rng.uniform(-5, 5), 2)
        city = rng.choice(["Oslo", "Bergen"])
        amount = 3 * x1 + 2 * x2 + (5 if city == "Oslo" else 0) + rng.uniform(-0.5, 0.5)
        rows.append({"x1": x1, "x2": None if i % 13 == 0 else x2, "city": city, "amount": round(amount, 3)})
    return rows


def load(client, name: str, rows: list[dict], vhost: str = "acme") -> None:
    resp = client.post(f"/v1/datasets/{vhost}/{name}/rows", json={"rows": rows, "replace": True})
    assert resp.status_code == 200, resp.text


def train(client, model: str, vhost: str = "acme", **body):
    return client.post(f"/v1/models/{vhost}/{model}/train", json=body)
