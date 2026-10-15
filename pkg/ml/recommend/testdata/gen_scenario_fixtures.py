"""Regenerate the scenario parity fixtures for pkg/ml/recommend.

Run with the hermod-ml worker's dependencies installed (ml/worker/requirements.txt):

    python -I pkg/ml/recommend/testdata/gen_scenario_fixtures.py [NAME ...]

With names (such as products_peers) it writes only those fixtures. The ones
without "_peers" were trained before customer categories and sizes made peer
groups: they stay as they are, so the tests prove an older artifact answers
exactly as it did. Regenerate them only on purpose.

For each scenario it trains a format 2 artifact on a small synthetic
distributor (ml/worker/tests/b2b.py) through the worker's own HTTP API, and
writes scenario/<scenario>.rec and scenario/<scenario>.json: calls and what
the worker's scoring functions (visit_signals and visit_reasons,
product_scores, audience_scores, reorder_due, similar_items) answered for
them, on the day after the artifact's as_of, reading the artifact's own
float32 features as Hermod does. The "_peers" runs map the customers' size
(their headcount) besides their segment, so products and audience add the
peer share; "_near" runs place every customer of a category in one of two
towns, so their peers are the nearby ones (near_km, near_peers), and a
products case says what they buy in its "summary". The "_details" runs map
the products' details (b2b.with_details: brand, subcategory, pack size and
unit, launch and end dates, stock, substitute), and the "_shop" runs the
worker's product details tests' shops, so products, audience and similar
weigh brands and new products, leave out what cannot be offered for its
substitute, and products suggest a quantity (similar_scores). The
"_market" runs map the peer tests' customers' price tier and region besides
their category and size, so peers are of all four, and the audience lists
carry the customers' tier, channel and region. The "_signals" runs add the
details shops' returns and quotes (the worker's returns and quotes tests):
products leave out what a customer returned or lost a quote of and offer an
open quote with its date, and an audience leaves out a lost quote and counts
a returned product half; their cases also ask with what was bought.
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
from tests.b2b import data_roles, generate, with_details  # noqa: E402
from tests.test_scenarios import (  # noqa: E402
    detail_body, detail_tables, market_body, market_customers, near_body, near_tables, peer_body, peer_tables, signal_body, signal_tables,
)

VHOST = "fixture"


def built(model: recommend.Model) -> scenarios.Built:
    """What the worker's scoring reads, from the artifact alone."""
    return scenarios.built_from_artifact(model)


def customer_fields(b, cattrs, u):
    return scenarios._customer_fields(b, cattrs, u)


def item_fields(b, i, u=None):
    return scenarios._item_fields(b, i, u)


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


def cases(scenario: str, model: recommend.Model, customers: tuple[str, ...] = (), products: tuple[str, ...] = (),
          seen: tuple[str, ...] = ()) -> list[dict]:
    """The calls of a fixture and what the worker answered. seen are
    customers (products, for an audience) asked about again with what they
    bought listed too."""
    b = built(model)
    cattrs = {k: v for k, v in model.extra["customer_attrs"].items()}
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
        # The customers with the fewest orders lean on their peers most.
        fewest = [int(u) for u in np.argsort(np.diff(model.seen.indptr), kind="stable")[:2]] + [b.users.index(c) for c in customers]
        for u, k, exclude in [(0, 10, True), (3, 5, True), (11, 10, False), (40, 20, True)] + [(u, 10, True) for u in fewest] + [(12, 100, True), (fewest[0], 100, True), (fewest[-1], 100, True)] + [(b.users.index(c), 16, False) for c in seen]:
            u %= len(b.users)  # a small model has fewer customers
            picks = scenarios.product_scores(b, u, k, exclude)
            items = [scenarios._item(b.items[i], s, r + 1, reasons, item_fields(b, i, u)) for r, (i, s, reasons) in enumerate(picks)]
            case = {"today": today, "customer": b.users[u], "k": k, "exclude_seen": exclude, "items": items}
            if summary := scenarios.nearby_summary(b, u, items):
                case["summary"] = summary
            out.append(case)
    if scenario == "audience":
        for i, k, exclude in [(int(p), 10, True) for p in model.popular[:3]] + [(5, 15, False)] + [(b.items.index(p), 10, True) for p in products] + [(b.items.index(p), 40, False) for p in seen]:
            picks = scenarios.audience_scores(b, i, k, exclude)
            out.append({"today": today, "product": b.items[i], "k": k, "exclude_seen": exclude,
                        "items": [scenarios._item(b.users[u], s, r + 1, reasons, customer_fields(b, cattrs, u)) for r, (u, s, reasons) in enumerate(picks)]})
    if scenario == "similar":
        for i in [int(p) for p in model.popular[:3]] + [9 % len(b.items)] + [b.items.index(p) for p in products]:
            items = [scenarios._item(b.items[j], s, r + 1, reasons, item_fields(b, j)) for r, (j, s, reasons) in enumerate(scenarios.similar_scores(b, i, 10))]
            out.append({"today": today, "product": b.items[i], "k": 10, "items": items})
    return out


def main(names: list[str]) -> None:
    out_dir = HERE / "scenario"
    out_dir.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory() as tmp:
        client = TestClient(create_app(Settings(data_dir=Path(tmp) / "data")))
        tables = generate(seed=11, customers=80, salesmen=4, products=60)
        tables["items_details"] = with_details(tables["items"])
        for dataset, table in (("orders", "orders"), ("customers", "customers"), ("items", "items"), ("visits", "visits"), ("reps", "reps"),
                               ("items_details", "items_details")):
            resp = client.post(f"/v1/datasets/{VHOST}/{dataset}/rows", json={"rows": tables[table], "replace": True})
            assert resp.status_code == 200, resp.text
        runs = [(s, s, {}) for s in scenarios.SCENARIOS] + [("audience_als", "audience", {"algorithm": "als"}),
                                                              ("products_als", "products", {"algorithm": "als"})]
        runs += [(f"{s}_peers", s, {"data": data_roles(size=True)}) for s in ("products", "audience", "reorder")]
        runs += [("audience_peers_als", "audience", {"algorithm": "als", "data": data_roles(size=True)})]
        # Same-tier customers within 40 km, the 8 nearest.
        runs += [("products_peers_wide", "products", {"data": data_roles(size=True), "near_km": 40, "near_peers": 8})]
        # Product details, the new products those launched within 50 days.
        runs += [(f"{s}_details", s, {"data": data_roles(size=True, details=True), "new_days": 50}) for s in ("products", "audience", "similar")]
        details = [it["sku"] for p, it in enumerate(tables["items_details"]) if p % 13 == 4 or p % 17 == 6 or p % 11 == 7][:6]
        for name, scenario, extra in runs:
            if names and name not in names:
                continue
            body = {"task": "recommendation", "scenario": scenario, "data": data_roles(), **extra}
            resp = client.post(f"/v1/models/{VHOST}/{name}/train", json=body)
            assert resp.status_code == 200, resp.text
            meta = resp.json()
            raw = client.get(f"/v1/models/{VHOST}/{name}/versions/{meta['version']}/model.rec").content
            model = recommend.decode(raw)
            (out_dir / f"{name}.rec").write_bytes(raw)
            products = tuple(p for p in details if p in model.items) if name.endswith("_details") else ()
            (out_dir / f"{name}.json").write_text(json.dumps(cases(scenario, model, products=products), indent=1) + "\n")
            print(name, len(raw), "bytes", model.algorithm, {k: round(v, 3) for k, v in meta["metrics"].items()})

        # Customers with one order, whose peers say most: the newcomers of
        # the worker's peer tests.
        tables = peer_tables()
        for dataset, table in (("porders", "orders"), ("pcustomers", "customers")):
            resp = client.post(f"/v1/datasets/{VHOST}/{dataset}/rows", json={"rows": tables[table], "replace": True})
            assert resp.status_code == 200, resp.text
        for name, scenario in (("products_cold", "products"), ("audience_cold", "audience")):
            if names and name not in names:
                continue
            resp = client.post(f"/v1/models/{VHOST}/{name}/train", json=peer_body(scenario, {"segment": "kind", "size": "staff"}))
            assert resp.status_code == 200, resp.text
            meta = resp.json()
            raw = client.get(f"/v1/models/{VHOST}/{name}/versions/{meta['version']}/model.rec").content
            model = recommend.decode(raw)
            (out_dir / f"{name}.rec").write_bytes(raw)
            (out_dir / f"{name}.json").write_text(json.dumps(cases(scenario, model, ("Rl16", "Hs17", "Om12")), indent=1) + "\n")
            print(name, len(raw), "bytes", model.algorithm, {k: round(v, 3) for k, v in meta["metrics"].items()})

        # Minimarkets in two towns, and one without coordinates: the
        # worker's nearby tests.
        tables = near_tables()
        for dataset, table in (("norders", "orders"), ("ncustomers", "customers")):
            resp = client.post(f"/v1/datasets/{VHOST}/{dataset}/rows", json={"rows": tables[table], "replace": True})
            assert resp.status_code == 200, resp.text
        for name, scenario in (("products_near", "products"), ("audience_near", "audience")):
            if names and name not in names:
                continue
            resp = client.post(f"/v1/models/{VHOST}/{name}/train", json=near_body(scenario))
            assert resp.status_code == 200, resp.text
            meta = resp.json()
            raw = client.get(f"/v1/models/{VHOST}/{name}/versions/{meta['version']}/model.rec").content
            model = recommend.decode(raw)
            (out_dir / f"{name}.rec").write_bytes(raw)
            (out_dir / f"{name}.json").write_text(json.dumps(cases(scenario, model, ("A16", "B17", "Anone")), indent=1) + "\n")
            print(name, len(raw), "bytes", model.algorithm, {k: round(v, 3) for k, v in meta["metrics"].items()})

        # Shops of two noodle brands: the worker's product details tests.
        tables = detail_tables()
        for dataset, table in (("dorders", "orders"), ("ditems", "items")):
            resp = client.post(f"/v1/datasets/{VHOST}/{dataset}/rows", json={"rows": tables[table], "replace": True})
            assert resp.status_code == 200, resp.text
        for name, scenario in (("products_shop", "products"), ("audience_shop", "audience"), ("similar_shop", "similar")):
            if names and name not in names:
                continue
            resp = client.post(f"/v1/models/{VHOST}/{name}/train", json=detail_body(scenario))
            assert resp.status_code == 200, resp.text
            meta = resp.json()
            raw = client.get(f"/v1/models/{VHOST}/{name}/versions/{meta['version']}/model.rec").content
            model = recommend.decode(raw)
            (out_dir / f"{name}.rec").write_bytes(raw)
            (out_dir / f"{name}.json").write_text(json.dumps(cases(scenario, model, ("K01", "K02", "K10", "K30"), ("I1", "I4", "I6", "M6")), indent=1) + "\n")
            print(name, len(raw), "bytes", model.algorithm, {k: round(v, 3) for k, v in meta["metrics"].items()})

        # The peer tests' customers with a price tier, channel and region.
        load_rows(client, "mcustomers", market_customers())
        for name, scenario in (("products_market", "products"), ("audience_market", "audience")):
            if names and name not in names:
                continue
            columns = {"segment": "kind", "size": "staff", "price_tier": "pricing", "channel": "chan", "region": "area"}
            write(client, out_dir, name, scenario, market_body(scenario, columns), ("Rl16", "Hs17", "Om12"), ())

        # The details shops' returns and quotes.
        sig = signal_tables()
        load_rows(client, "dreturns", sig["returns"])
        load_rows(client, "dquotes", sig["quotes"])
        returned = sig["returned"][0]
        for name, scenario, customers, products, seen in (
                ("products_signals", "products", ("K02", "K05", "K06", "K07", "K10"), (), ("K02", "K07", "K10")),
                ("audience_signals", "audience", (), ("M1", "M3", returned), (returned, "M1"))):
            if names and name not in names:
                continue
            write(client, out_dir, name, scenario, signal_body(scenario), customers, products, seen)


def load_rows(client: TestClient, dataset: str, rows: list[dict]) -> None:
    resp = client.post(f"/v1/datasets/{VHOST}/{dataset}/rows", json={"rows": rows, "replace": True})
    assert resp.status_code == 200, resp.text


def write(client: TestClient, out_dir: Path, name: str, scenario: str, body: dict, customers: tuple[str, ...], products: tuple[str, ...],
          seen: tuple[str, ...] = ()) -> None:
    resp = client.post(f"/v1/models/{VHOST}/{name}/train", json=body)
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    raw = client.get(f"/v1/models/{VHOST}/{name}/versions/{meta['version']}/model.rec").content
    model = recommend.decode(raw)
    (out_dir / f"{name}.rec").write_bytes(raw)
    (out_dir / f"{name}.json").write_text(json.dumps(cases(scenario, model, customers, products, seen), indent=1) + "\n")
    print(name, len(raw), "bytes", model.algorithm, {k: round(v, 3) for k, v in meta["metrics"].items()})


if __name__ == "__main__":
    main(sys.argv[1:])
