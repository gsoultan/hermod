"""Regenerate the scenario parity fixtures for pkg/ml/recommend.

Run with the hermod-ml worker's dependencies installed (ml/worker/requirements.txt):

    python -I pkg/ml/recommend/testdata/gen_scenario_fixtures.py

For each scenario it trains a format 2 artifact on a small synthetic
distributor (ml/worker/tests/b2b.py) through the worker's own HTTP API, and
writes scenario/<scenario>.rec and scenario/<scenario>.json: calls and what
the worker's scoring functions (visit_signals and visit_reasons,
product_scores, audience_scores, reorder_due, similar_items) answered for
them, on the day after the artifact's as_of, reading the artifact's own
float32 features as Hermod does.
"""

from __future__ import annotations

import json
import math
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[3]
sys.path.insert(0, str(REPO / "ml" / "worker"))

import numpy as np  # noqa: E402
from fastapi.testclient import TestClient  # noqa: E402

from hermod_ml import recommend, scenarios  # noqa: E402
from hermod_ml.app import create_app  # noqa: E402
from hermod_ml.settings import Settings  # noqa: E402
from tests.b2b import data_roles, generate  # noqa: E402

VHOST = "fixture"


def built(model: recommend.Model) -> scenarios.Built:
    """What the worker's scoring reads, from the artifact alone."""
    extra = model.extra
    today = int((np.datetime64(extra["as_of"]) - np.datetime64("1970-01-01")).astype(int)) + 1
    has_opportunity = model.customer_neighbors is not None and model.customer_neighbors.shape[1] > 0
    return scenarios.Built(
        today=today, users=list(model.users), items=list(model.items),
        user_index={u: i for i, u in enumerate(model.users)}, item_index={p: i for i, p in enumerate(model.items)},
        customer_features=model.customer_features.astype(np.float64), item_features=model.item_features.astype(np.float64),
        model=model, global_gap=extra["global_gap"], global_visit_gap=extra["global_visit_gap"],
        has_visits=extra["has_visits"], has_opportunity=has_opportunity, has_margin=extra["has_margin"],
    )


def customer_fields(b, cattrs, u):
    return scenarios._customer_fields(b, cattrs, u)


def item_fields(iattrs, i):
    return scenarios._item_fields(iattrs, i)


def ranked_pool(b, cattrs, scenario: str, rep: str) -> list[dict]:
    salesman = cattrs["salesman"]
    blocked = cattrs.get("blocked", [None] * len(b.users))
    weights = scenarios.normalised_weights(b.model.extra["weights"], b.has_visits, b.has_opportunity)
    ranked = []
    for u in range(len(b.users)):
        if salesman[u] != rep or blocked[u] == "true":
            continue
        feat = b.feature(u)
        if scenario == "visits":
            signals = scenarios.visit_signals(feat, b.today, b.global_gap, b.global_visit_gap, b.has_visits)
            ranked.append((float(np.float32(scenarios.visit_score(signals, weights))), u,
                           scenarios.visit_reasons(feat, signals, b.today, b.global_gap, b.global_visit_gap, weights), None))
        else:
            due = scenarios.reorder_due(b, u)
            if due is not None:
                ranked.append((float(np.float32(due[1])), u, scenarios._reorder_reasons(b, u), scenarios.day_text(math.ceil(due[0]))))
    ranked.sort(key=lambda t: (-t[0], t[1]))
    return [scenarios._item(b.users[u], s, r + 1, reasons, customer_fields(b, cattrs, u), due)
            for r, (s, u, reasons, due) in enumerate(ranked)]


def cases(scenario: str, model: recommend.Model) -> list[dict]:
    b = built(model)
    cattrs = {k: v for k, v in model.extra["customer_attrs"].items()}
    iattrs = {k: v for k, v in model.extra["item_attrs"].items()}
    out = []
    today = scenarios.day_text(b.today)
    if scenario in ("visits", "reorder"):
        for rep in sorted({s for s in cattrs["salesman"] if s is not None}):
            out.append({"today": today, "salesman": rep, "items": ranked_pool(b, cattrs, scenario, rep)})
    if scenario == "reorder":
        for u in (0, 1, 7):
            due = scenarios.reorder_due(b, u)
            items = [] if due is None else [scenarios._item(b.users[u], float(np.float32(due[1])), 1, scenarios._reorder_reasons(b, u),
                                                           customer_fields(b, cattrs, u), scenarios.day_text(math.ceil(due[0])))]
            out.append({"today": today, "customer": b.users[u], "items": items})
    if scenario == "products":
        for u, k, exclude in ((0, 10, True), (3, 5, True), (11, 10, False), (40, 20, True)):
            picks = scenarios.product_scores(b, u, k, exclude)
            out.append({"today": today, "customer": b.users[u], "k": k, "exclude_seen": exclude,
                        "items": [scenarios._item(b.items[i], s, r + 1, reasons, item_fields(iattrs, i)) for r, (i, s, reasons) in enumerate(picks)]})
    if scenario == "audience":
        for i, k, exclude in [(int(p), 10, True) for p in model.popular[:3]] + [(5, 15, False)]:
            picks = scenarios.audience_scores(b, i, k, exclude)
            out.append({"today": today, "product": b.items[i], "k": k, "exclude_seen": exclude,
                        "items": [scenarios._item(b.users[u], s, r + 1, reasons, customer_fields(b, cattrs, u)) for r, (u, s, reasons) in enumerate(picks)]})
    if scenario == "similar":
        for i in [int(p) for p in model.popular[:3]] + [9]:
            items = []
            for r, (j, s, reason) in enumerate(recommend.similar_items(model, i, 10)):
                reasons = ([{"code": "popular", "text": "One of the most popular products"}] if reason == "popular"
                           else [{"code": "bought_together", "text": f"Often bought by the same customers as {b.items[i]}"}])
                items.append(scenarios._item(b.items[j], s, r + 1, reasons, item_fields(iattrs, j)))
            out.append({"today": today, "product": b.items[i], "k": 10, "items": items})
    return out


def main() -> None:
    out_dir = HERE / "scenario"
    out_dir.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory() as tmp:
        client = TestClient(create_app(Settings(data_dir=Path(tmp) / "data")))
        tables = generate(seed=11, customers=80, salesmen=4, products=60)
        for dataset, table in (("orders", "orders"), ("customers", "customers"), ("items", "items"), ("visits", "visits"), ("reps", "reps")):
            resp = client.post(f"/v1/datasets/{VHOST}/{dataset}/rows", json={"rows": tables[table], "replace": True})
            assert resp.status_code == 200, resp.text
        runs = [(s, s, {}) for s in scenarios.SCENARIOS] + [("audience_als", "audience", {"algorithm": "als"}),
                                                              ("products_als", "products", {"algorithm": "als"})]
        for name, scenario, extra in runs:
            body = {"task": "recommendation", "scenario": scenario, "data": data_roles(), **extra}
            resp = client.post(f"/v1/models/{VHOST}/{name}/train", json=body)
            assert resp.status_code == 200, resp.text
            meta = resp.json()
            raw = client.get(f"/v1/models/{VHOST}/{name}/versions/{meta['version']}/model.rec").content
            model = recommend.decode(raw)
            (out_dir / f"{name}.rec").write_bytes(raw)
            (out_dir / f"{name}.json").write_text(json.dumps(cases(scenario, model), indent=1) + "\n")
            print(name, len(raw), "bytes", model.algorithm, {k: round(v, 3) for k, v in meta["metrics"].items()})


if __name__ == "__main__":
    main()
