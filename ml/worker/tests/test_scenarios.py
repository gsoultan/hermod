"""Recommendation scenarios: data roles, the health check, features, the five
scenarios, time-split evaluation against named baselines, and the v2 artifact."""

from __future__ import annotations

import copy
import math

import numpy as np
import pytest
from fastapi.testclient import TestClient

from hermod_ml import recommend, scenarios
from hermod_ml.app import create_app
from hermod_ml.settings import Settings
from tests.b2b import data_roles, generate
from tests.helpers import load

VHOST = "acme"


@pytest.fixture(scope="module")
def b2b(tmp_path_factory) -> TestClient:
    """A worker holding the synthetic distributor's five tables."""
    client = TestClient(create_app(Settings(data_dir=tmp_path_factory.mktemp("b2b") / "data")))
    tables = generate()
    for dataset, table in (("orders", "orders"), ("customers", "customers"), ("items", "items"), ("visits", "visits"), ("reps", "reps")):
        load(client, dataset, tables[table], VHOST)
    return client


def body(scenario: str, **overrides) -> dict:
    out = {"task": "recommendation", "scenario": scenario, "data": data_roles()}
    out.update(overrides)
    return out


def check(client: TestClient, payload: dict):
    return client.post(f"/v1/recommend-check/{VHOST}", json=payload)


def train(client: TestClient, model: str, payload: dict):
    return client.post(f"/v1/models/{VHOST}/{model}/train", json=payload)


def codes(result: dict) -> dict[str, str]:
    return {c["code"]: c["status"] for c in result["checks"]}


# --------------------------------------------------------------------------
# Data health
# --------------------------------------------------------------------------


def test_the_check_measures_the_distributor(b2b):
    resp = check(b2b, body("visits"))
    assert resp.status_code == 200, resp.text
    out = resp.json()
    assert out["ok"] is True
    stats = out["stats"]
    assert stats["customers"] == 300 and stats["products"] == 200
    assert stats["transactions"] == len(generate()["orders"])
    assert 17.5 < stats["months_of_history"] < 18.5
    assert stats["tx_per_customer"] > 3
    got = codes(out)
    # 300 customers is below the 500 churn and timing models want.
    assert got["few_customers"] == "amber"
    assert got["short_history"] == "green"
    assert got["future_dates"] == "green" and got["negative_quantities"] == "green"
    assert got["unmatched_ids"] == "green" and got["missing_role"] == "green"
    for c in out["checks"]:
        assert set(c) == {"code", "status", "message", "fix"}
        assert c["status"] in ("green", "amber", "red")
        assert c["message"]


def test_products_want_a_thousand_customers(b2b):
    out = check(b2b, body("products")).json()
    few = next(c for c in out["checks"] if c["code"] == "few_customers")
    assert few["status"] == "amber"
    assert "1,000" in few["message"] or "1000" in few["message"]
    assert few["fix"]


def test_visits_without_a_salesman_column_cannot_train(b2b):
    data = data_roles()
    del data["customers"]["salesman"]
    out = check(b2b, body("visits", data=data)).json()
    assert out["ok"] is False
    missing = [c for c in out["checks"] if c["code"] == "missing_role"]
    assert missing and missing[0]["status"] == "red"
    assert "salesman" in missing[0]["message"]

    resp = train(b2b, "visits_no_rep", body("visits", data=data))
    assert resp.status_code == 400
    assert "salesman" in resp.json()["error"]


def test_products_need_a_product_column(b2b):
    data = data_roles()
    del data["transactions"]["product"]
    out = check(b2b, body("products", data=data)).json()
    assert out["ok"] is False
    assert codes(out)["missing_role"] == "red"
    # Reorder does not.
    assert check(b2b, body("reorder", data=data)).json()["ok"] is True


def test_a_dataset_that_does_not_exist_is_a_red_missing_role(b2b):
    data = data_roles()
    data["visits"]["dataset"] = "no_such_visits"
    out = check(b2b, body("visits", data=data)).json()
    # Visits are optional for a visit plan: without them it cannot say who
    # was neglected, but it can still plan.
    assert out["ok"] is True
    bad = next(c for c in out["checks"] if c["code"] == "missing_role")
    assert bad["status"] == "amber" and "no_such_visits" in bad["message"]


def test_wrong_types_bad_ids_and_odd_rows_are_reported(client):
    orders = [
        {"cust": "a", "sku": "x", "day": "2026-01-01", "qty": 2},
        {"cust": "a", "sku": "x", "day": "2026-01-01", "qty": 2},
        {"cust": "b", "sku": "y", "day": "2099-01-01", "qty": 1},
        {"cust": "c", "sku": "y", "day": "2026-02-01", "qty": -3},
        {"cust": "ghost", "sku": "y", "day": "2026-02-03", "qty": 1},
    ] + [{"cust": f"k{i}", "sku": f"p{i % 4}", "day": f"2026-03-{i % 28 + 1:02d}", "qty": 1} for i in range(20)]
    load(client, "o", orders)
    load(client, "c", [{"id": c, "lat": "north"} for c in ["a", "b", "c"] + [f"k{i}" for i in range(20)]])
    payload = {
        "task": "recommendation", "scenario": "reorder",
        "data": {"transactions": {"dataset": "o", "customer": "cust", "product": "sku", "date": "day", "quantity": "qty"},
                 "customers": {"dataset": "c", "id": "id", "lat": "lat"}},
    }
    out = client.post("/v1/recommend-check/acme", json=payload).json()
    got = codes(out)
    assert got["duplicates"] == "amber"
    assert got["future_dates"] == "amber"
    assert got["negative_quantities"] == "amber"
    assert got["unmatched_ids"] == "amber"
    unmatched = next(c for c in out["checks"] if c["code"] == "unmatched_ids")
    assert "customers" in unmatched["message"]
    assert got["bad_column_type"] == "red"
    bad = next(c for c in out["checks"] if c["code"] == "bad_column_type")
    assert "lat" in bad["message"]
    assert out["ok"] is False


def test_the_request_shape_is_checked(client):
    for payload, words in [
        ({"task": "recommendation", "scenario": "dance", "data": {}}, "scenario"),
        ({"task": "recommendation", "scenario": "visits"}, "data"),
        ({"task": "recommendation", "scenario": "visits", "data": {"orders": {"dataset": "o"}}}, "role"),
        ({"task": "recommendation", "scenario": "visits", "data": {"transactions": {"dataset": "o", "customer": "c", "colour": "x"}}}, "colour"),
        ({"task": "recommendation", "scenario": "visits", "data": {"transactions": {"customer": "c"}}}, "dataset"),
        ({"task": "recommendation", "scenario": "visits", "data": data_roles(), "weights": {"value": 2}}, "weight"),
        ({"task": "recommendation", "scenario": "visits", "data": data_roles(), "weights": {"luck": 0.5}}, "luck"),
        ({"task": "recommendation", "scenario": "visits", "data": data_roles(), "recommend": {"user": "a", "item": "b"}}, "recommend"),
    ]:
        resp = client.post("/v1/recommend-check/acme", json=payload)
        assert resp.status_code == 400, (payload, resp.text)
        assert words in resp.json()["error"], resp.json()["error"]
    # A check ignores rules; a training reads them.
    resp = client.post("/v1/models/acme/m/train", json={"task": "recommendation", "scenario": "visits", "data": data_roles(), "rules": "none"})
    assert resp.status_code == 400 and "rules" in resp.json()["error"]


# --------------------------------------------------------------------------
# Features and visit scoring
# --------------------------------------------------------------------------


def test_cadence_features_say_what_the_orders_say():
    days = np.array([0, 30, 60, 95, 120], dtype=np.int64)
    f = scenarios.cadence(days, amounts=np.array([10.0, 10, 10, 10, 50]), today=160)
    assert f["last_order"] == 120 and f["first_order"] == 0
    assert f["orders"] == 5
    assert f["median_gap"] == pytest.approx(30.0)
    # Gaps 30, 30, 35, 25: all within 1.5x the median.
    assert f["on_time_share"] == pytest.approx(1.0)
    assert f["monetary"] == pytest.approx(90.0)
    # 60 in the last 90 days (days 70 to 159) against 30 in the 90 before.
    assert f["trend"] == pytest.approx((60 - 30) / 30)

    single = scenarios.cadence(np.array([5]), amounts=np.array([3.0]), today=10)
    assert math.isnan(single["median_gap"]) and math.isnan(single["on_time_share"])


def test_visit_scores_are_a_weighted_sum_of_named_signals():
    weights = scenarios.normalised_weights(None, has_visits=True, has_opportunity=True)
    assert sum(weights.values()) == pytest.approx(1.0)
    assert weights == pytest.approx({k: v for k, v in scenarios.DEFAULT_WEIGHTS.items()})
    # Thirty days usually, sixty since the last order: overdue x2, so the
    # order is due but fading into a churn risk.
    feat = {"last_order": 100.0, "median_gap": 30.0, "value_pct": 0.9, "whitespace_pct": 0.5, "last_visit": 130.0, "visit_gap": 20.0}
    signals = scenarios.visit_signals(feat, today=160, global_gap=30, global_visit_gap=30, has_visits=True)
    assert signals["churn_risk"] == pytest.approx((2 - 1.5) / 1.5)
    assert signals["reorder_due"] == pytest.approx(1 - signals["churn_risk"])
    assert signals["value"] == pytest.approx(0.9)
    assert signals["opportunity"] == pytest.approx(0.5)
    assert signals["neglect"] == pytest.approx((30 / 20 - 0.5) / 1.5)
    score = scenarios.visit_score(signals, weights)
    assert score == pytest.approx(sum(weights[k] * signals[k] for k in weights))

    # Twenty days into a thirty-day cycle: due by the end of a 7-day plan
    # (27/30 = 0.9 of the gap), not by the end of a 1-day one (21/30 = 0.7).
    feat["last_order"] = 140.0
    assert scenarios.visit_signals(feat, 160, 30, 30, True, period=7)["reorder_due"] == pytest.approx((0.9 - 0.5) / 0.5)
    assert scenarios.visit_signals(feat, 160, 30, 30, True, period=1)["reorder_due"] == pytest.approx((0.7 - 0.5) / 0.5)
    # Never ordered: neither due nor at risk; never visited: fully neglected.
    blank = scenarios.visit_signals({}, 160, 30, 30, True)
    assert blank["reorder_due"] == blank["churn_risk"] == 0 and blank["neglect"] == 1

    # Without visits the neglect weight is dropped and the rest renormalised.
    no_visits = scenarios.normalised_weights({"reorder_due": 1, "neglect": 1}, has_visits=False, has_opportunity=True)
    assert "neglect" not in no_visits and no_visits["reorder_due"] == pytest.approx(1.0)


def test_reason_texts_carry_numbers():
    feat = {"last_order": 100.0, "median_gap": 30.0, "value_pct": 0.93, "whitespace": 7.0, "whitespace_pct": 0.8,
            "last_visit": 115.0, "visit_gap": 30.0}
    signals = scenarios.visit_signals(feat, today=141, global_gap=30, global_visit_gap=30, has_visits=True)
    texts = {r["code"]: r["text"] for r in scenarios.visit_reasons(feat, signals, today=141, global_gap=30, global_visit_gap=30)}
    assert texts["reorder_due"] == "Usually orders every 30 days; last order 41 days ago"
    assert texts["high_value"] == "Top 7% of customers by revenue"
    assert texts["not_visited"] == "Not visited for 26 days (target every 30)"


# --------------------------------------------------------------------------
# Training the scenarios
# --------------------------------------------------------------------------


def item_shape(item: dict) -> None:
    assert {"id", "score", "rank", "reasons", "fields"} <= set(item), item
    for r in item["reasons"]:
        assert set(r) == {"code", "text"} and r["code"] in scenarios.REASON_CODES and r["text"]


def test_a_visit_plan_trains_and_is_measured_against_baselines(b2b):
    resp = train(b2b, "visit_plan", body("visits", rules=[{"kind": "filter", "field": "customers.salesman", "op": "eq", "from_request": "salesman"}]))
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    assert meta["task"] == "recommendation" and meta["scenario"] == "visits"
    assert meta["roles"] == data_roles()
    assert meta["rules"][0]["kind"] == "filter"
    assert meta["weights"] == pytest.approx(scenarios.DEFAULT_WEIGHTS)
    assert set(meta["metrics"]) >= {"ordered_in_period@k", "revenue_share@k", "score"}
    assert set(meta["baseline"]) == {"ordered_in_period@k", "revenue_share@k"}
    assert [b["name"] for b in meta["baselines_named"]] == ["most_overdue", "biggest", "popular"]
    assert all(set(b["metrics"]) == {"ordered_in_period@k", "revenue_share@k"} for b in meta["baselines_named"])
    assert meta["health"]["ok"] is True
    assert meta["evaluation"]["period_days"] == 7
    assert 1 <= len(meta["samples"]) <= 3
    for sample in meta["samples"]:
        assert sample["for"] and sample["items"]
        for it in sample["items"]:
            item_shape(it)


def test_the_v2_artifact_holds_features_attributes_and_neighbours(b2b):
    meta = train(b2b, "visits_art", body("visits")).json()
    raw = b2b.get(f"/v1/models/{VHOST}/visits_art/versions/{meta['version']}/model.rec")
    assert raw.status_code == 200
    model = recommend.decode(raw.content)
    assert raw.content[4:8] == (2).to_bytes(4, "little")
    x = model.extra
    assert x["scenario"] == "visits" and x["as_of"] == "2026-09-30"
    assert len(model.users) == 300
    assert set(scenarios.CUSTOMER_FEATURES) == set(x["customer_features"])
    assert model.customer_features.shape == (300, len(x["customer_features"]))
    assert set(x["customer_attrs"]) >= {"salesman", "territory", "segment", "blocked", "name"}
    assert len(x["customer_attrs"]["salesman"]) == 300
    assert set(x["salesmen"]) == {f"R{i:02d}" for i in range(1, 13)}
    assert x["salesmen"]["R05"] == {"visits_per_day": pytest.approx(x["salesmen"]["R05"]["visits_per_day"]), "working_days": ["mon", "tue", "wed", "thu"]}
    assert model.customer_neighbors.shape[0] == 300
    assert model.seen_last_day.shape == (model.seen.nnz,)
    assert model.item_features.shape == (200, len(x["item_features"]))


def test_products_scenario_reports_ranking_metrics(b2b):
    meta = train(b2b, "offers", body("products")).json()
    assert meta["scenario"] == "products"
    for key in ("hit_rate@10", "ndcg@10", "coverage"):
        assert 0 <= meta["metrics"][key] <= 1
    assert set(meta["baseline"]) >= {"hit_rate@10", "ndcg@10", "coverage"}
    assert [b["name"] for b in meta["baselines_named"]] == ["popular"]
    assert meta["metrics"]["hit_rate@10"] > meta["baseline"]["hit_rate@10"], meta["metrics"]
    assert meta["algorithm"] in ("als", "item_similarity")
    for sample in meta["samples"]:
        for it in sample["items"]:
            item_shape(it)


def test_reorder_needs_no_products_and_gives_due_dates(b2b):
    data = data_roles()
    del data["transactions"]["product"]
    del data["products"]
    meta = train(b2b, "due", body("reorder", data=data)).json()
    assert meta["scenario"] == "reorder" and meta["algorithm"] == "none"
    assert meta["metrics"]["mae_days"] >= 0 and 0 <= meta["metrics"]["on_time_share"] <= 1
    assert set(meta["baseline"]) == {"mae_days", "on_time_share"}
    assert meta["samples"] and all("due_date" in it for s in meta["samples"] for it in s["items"])
    model = recommend.decode(b2b.get(f"/v1/models/{VHOST}/due/versions/{meta['version']}/model.rec").content)
    assert model.items == [] and meta["catalog"]["items"] == 0


def test_reorder_with_products_keeps_each_customers_rhythm_per_product(b2b):
    """Hermod answers "which customers will buy this product in the next N
    days" from each customer and product's last day and median gap, so a
    reorder model trained with transactions.product keeps its products and
    their pair cadence, still without a recommender."""
    meta = train(b2b, "due_products", body("reorder")).json()
    assert meta["algorithm"] == "none" and meta["catalog"]["items"] > 0
    assert meta["metrics"]["mae_days"] >= 0 and meta["samples"]
    model = recommend.decode(b2b.get(f"/v1/models/{VHOST}/due_products/versions/{meta['version']}/model.rec").content)
    assert model.algorithm == "none" and len(model.items) == meta["catalog"]["items"]
    assert model.seen.nnz > 0 and len(model.seen_gap) == model.seen.nnz
    assert np.isfinite(model.seen_gap).any()
    assert (model.seen_last_day != recommend.EMPTY).all()


@pytest.mark.parametrize("scenario", ["audience", "similar"])
def test_audience_and_similar_train(b2b, scenario):
    resp = train(b2b, f"m_{scenario}", body(scenario))
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    assert meta["scenario"] == scenario
    assert "hit_rate@10" in meta["metrics"]


def test_the_legacy_shape_still_trains_a_format_1_products_model(client):
    rows = [{"customer": f"c{p}", "sku": f"sku-{(p * 3 + i) % 12}"} for p in range(20) for i in range(4)]
    load(client, "orders", rows)
    meta = client.post("/v1/models/acme/legacy/train", json={
        "dataset": "orders", "task": "recommendation", "recommend": {"user": "customer", "item": "sku"}}).json()
    assert meta["scenario"] == "products"
    raw = client.get("/v1/models/acme/legacy/versions/1/model.rec").content
    assert raw[4:8] == (1).to_bytes(4, "little")


def test_a_v1_artifact_still_decodes_and_v2_round_trips(b2b):
    meta = train(b2b, "roundtrip", body("products")).json()
    raw = b2b.get(f"/v1/models/{VHOST}/roundtrip/versions/{meta['version']}/model.rec").content
    model = recommend.decode(raw)
    assert recommend.encode(model) == raw
    v1 = copy.copy(model)
    v1.extra = None
    old = recommend.encode(v1)
    assert old[4:8] == (1).to_bytes(4, "little")
    assert recommend.decode(old).extra is None


# --------------------------------------------------------------------------
# Customer category and size
# --------------------------------------------------------------------------


def test_a_numeric_size_is_cut_into_tertiles_and_a_label_is_kept():
    edges = scenarios.size_edges(np.array([1.0, 2, 3, 4, 5, 6, 7, 8, 9, math.nan]))
    assert edges == pytest.approx([float(np.quantile(np.arange(1, 10), 1 / 3)), float(np.quantile(np.arange(1, 10), 2 / 3))])
    assert [scenarios.size_label(v, edges) for v in (1, 3.6, 3.7, 6.3, 6.4, 9)] == ["small", "small", "medium", "medium", "large", "large"]
    assert scenarios.size_label(math.nan, edges) is None
    assert scenarios.size_edges(np.array([math.nan])) is None
    # A label is a label: the three known ones in lower case, any other as given.
    assert [scenarios.size_text(v) for v in ("Large", " small ", "Enterprise", "", None)] == ["large", "small", "Enterprise", None, None]


GROUPS = [(seg, size) for seg in ("Retail", "Horeca", "Office") for size in ("small", "medium", "large")]
EMPLOYEES = {"small": (5, 15), "medium": (40, 60), "large": (200, 400)}
STAPLES = ("S1", "S2", "S3")


def peer_tables(seed: int = 3) -> dict[str, list[dict]]:
    """Customers whose category and size decide what they buy: every group
    has six products of its own, and everyone buys the staples. The
    established customers order every few weeks for a year; the newcomers
    ordered one staple ten days before the last month and then buy their
    group's products, which only their peers can tell; the two fresh ones
    per group have ordered one staple, at the very end."""
    import datetime as dt
    import random

    rng = random.Random(seed)
    start = dt.date(2025, 10, 1)
    last = 364
    orders, customers = [], []
    for g, (seg, size) in enumerate(GROUPS):
        own = [f"G{g}_{p}" for p in range(6)]
        for c in range(18):
            cid = f"{seg[0]}{size[0]}{c:02d}"
            customers.append({"id": cid, "kind": seg, "staff": rng.randint(*EMPLOYEES[size])})
            if c < 12:
                likes = rng.sample(own, 4)
                day = rng.randrange(20)
                while day <= last:
                    for sku in [rng.choice(STAPLES)] + rng.sample(likes, 2):
                        orders.append({"cust": cid, "sku": sku, "day": (start + dt.timedelta(days=day)).isoformat(), "qty": 1})
                    day += rng.randint(15, 25)
            elif c < 16:
                orders.append({"cust": cid, "sku": rng.choice(STAPLES), "day": (start + dt.timedelta(days=last - 40)).isoformat(), "qty": 1})
                for day in (last - 20, last - 5):
                    for sku in rng.sample(own, 2):
                        orders.append({"cust": cid, "sku": sku, "day": (start + dt.timedelta(days=day)).isoformat(), "qty": 1})
            else:
                orders.append({"cust": cid, "sku": rng.choice(STAPLES), "day": (start + dt.timedelta(days=last - 3)).isoformat(), "qty": 1})
    return {"orders": orders, "customers": customers}


@pytest.fixture(scope="module")
def peers(tmp_path_factory) -> TestClient:
    client = TestClient(create_app(Settings(data_dir=tmp_path_factory.mktemp("peers") / "data")))
    tables = peer_tables()
    load(client, "porders", tables["orders"], VHOST)
    load(client, "pcustomers", tables["customers"], VHOST)
    return client


def peer_body(scenario: str, customers: dict | None = None) -> dict:
    data = {"transactions": {"dataset": "porders", "customer": "cust", "product": "sku", "date": "day", "quantity": "qty"}}
    if customers is not None:
        data["customers"] = {"dataset": "pcustomers", "id": "id", **customers}
    return {"task": "recommendation", "scenario": scenario, "data": data, "algorithm": "item_similarity"}


def artifact(client: TestClient, name: str, meta: dict) -> recommend.Model:
    return recommend.decode(client.get(f"/v1/models/{VHOST}/{name}/versions/{meta['version']}/model.rec").content)


def test_category_and_size_go_in_the_artifact_with_the_size_edges(peers):
    resp = train(peers, "peer_products", peer_body("products", {"segment": "kind", "size": "staff"}))
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    x = artifact(peers, "peer_products", meta).extra
    assert x["has_peers"] is True
    lo, hi = x["size_edges"]
    assert 15 <= lo <= 40 and 60 <= hi <= 200
    attrs = x["customer_attrs"]
    users = artifact(peers, "peer_products", meta).users
    assert attrs["segment"][users.index("Rl00")] == "Retail"
    assert attrs["size"][users.index("Rl00")] == "large"
    assert attrs["size"][users.index("Hs03")] == "small"
    assert meta["evaluation"]["peer_groups"] == len(GROUPS)


def test_without_category_or_size_a_model_has_no_peers(peers):
    meta = train(peers, "no_peers", peer_body("products")).json()
    x = artifact(peers, "no_peers", meta).extra
    assert x["has_peers"] is False and x["size_edges"] is None
    assert "peer_groups" not in meta["evaluation"]
    for sample in meta["samples"]:
        for it in sample["items"]:
            assert all(r["code"] != "peer_group" for r in it["reasons"])


def test_peers_recommend_what_a_newcomers_group_buys(peers):
    meta = train(peers, "peer_products2", peer_body("products", {"segment": "kind", "size": "staff"})).json()
    model = artifact(peers, "peer_products2", meta)
    b = scenarios.built_from_artifact(model)
    # A fresh customer has ordered one staple: its peers say the rest.
    u = model.users.index("Rl16")
    g = GROUPS.index(("Retail", "large"))
    picks = scenarios.product_scores(b, u, 10)
    ids = [model.items[i] for i, _, _ in picks]
    # Its group's products and the staples lead the list.
    assert set(ids[:6]) <= {f"G{g}_{p}" for p in range(6)} | set(STAPLES), ids
    texts = [r["text"] for _, _, rs in picks for r in rs if r["code"] == "peer_group"]
    assert texts and texts[0].startswith("Bought by ") and texts[0].endswith(" of 17 Retail · large customers"), texts

    # The audience of a group's product is led by that group's customers.
    item = model.items.index(f"G{g}_0")
    audience = scenarios.audience_scores(b, item, 5)
    assert {model.users[u][:2] for u, _, _ in audience} == {"Rl"}, [model.users[u] for u, _, _ in audience]
    assert any(r["code"] == "peer_group" for _, _, rs in audience for r in rs)


def test_peers_lift_the_evaluated_metrics(peers):
    without = train(peers, "eval_plain", peer_body("products")).json()["metrics"]
    with_peers = train(peers, "eval_peers", peer_body("products", {"segment": "kind", "size": "staff"})).json()["metrics"]
    assert with_peers["ndcg@10"] > without["ndcg@10"], (with_peers, without)
    assert with_peers["hit_rate@10"] >= without["hit_rate@10"]


def test_customer_lists_carry_category_and_size(peers):
    meta = train(peers, "peer_audience", peer_body("audience", {"segment": "kind", "size": "staff"})).json()
    fields = [it["fields"] for s in meta["samples"] for it in s["items"]]
    assert fields and all(f["segment"] in ("Retail", "Horeca", "Office") and f["size"] in ("small", "medium", "large") for f in fields)


# --------------------------------------------------------------------------
# Nearby customers of the same category
# --------------------------------------------------------------------------

# Two towns about 78 km apart.
TOWNS = {"A": (52.0, 13.0), "B": (52.7, 13.0)}


def near_tables(seed: int = 5) -> dict[str, list[dict]]:
    """Minimarkets in two towns: the established ones of a town buy that
    town's products besides the staples; three fresh ones per town have
    ordered one staple, at the very end. One more fresh minimarket, in town
    A, has no coordinates."""
    import datetime as dt
    import random

    rng = random.Random(seed)
    start = dt.date(2025, 10, 1)
    last = 364
    orders, customers = [], []
    for town, (lat, lon) in TOWNS.items():
        own = [f"{town}_{p}" for p in range(6)]
        for c in range(18):
            cid = f"{town}{c:02d}"
            customers.append({"id": cid, "kind": "Minimarket", "lat": round(lat + rng.gauss(0, 0.006), 5),
                              "lon": round(lon + rng.gauss(0, 0.01), 5)})
            if c < 15:
                likes = rng.sample(own, 4)
                day = rng.randrange(20)
                while day <= last:
                    for sku in [rng.choice(STAPLES)] + rng.sample(likes, 2):
                        orders.append({"cust": cid, "sku": sku, "day": (start + dt.timedelta(days=day)).isoformat(), "qty": 1})
                    day += rng.randint(15, 25)
            else:
                orders.append({"cust": cid, "sku": rng.choice(STAPLES), "day": (start + dt.timedelta(days=last - 3)).isoformat(), "qty": 1})
    customers.append({"id": "Anone", "kind": "Minimarket", "lat": None, "lon": None})
    orders.append({"cust": "Anone", "sku": STAPLES[0], "day": (start + dt.timedelta(days=last - 3)).isoformat(), "qty": 1})
    return {"orders": orders, "customers": customers}


@pytest.fixture(scope="module")
def near(tmp_path_factory) -> TestClient:
    client = TestClient(create_app(Settings(data_dir=tmp_path_factory.mktemp("near") / "data")))
    tables = near_tables()
    load(client, "norders", tables["orders"], VHOST)
    load(client, "ncustomers", tables["customers"], VHOST)
    return client


def near_body(scenario: str, **extra) -> dict:
    data = {"transactions": {"dataset": "norders", "customer": "cust", "product": "sku", "date": "day", "quantity": "qty"},
            "customers": {"dataset": "ncustomers", "id": "id", "segment": "kind", "lat": "lat", "lon": "lon"}}
    return {"task": "recommendation", "scenario": scenario, "data": data, "algorithm": "item_similarity", **extra}


def test_nearby_settings_are_checked():
    for bad in ({"near_km": 0}, {"near_km": 501}, {"near_km": True}, {"near_km": "5"},
                {"near_peers": 2}, {"near_peers": 201}, {"near_peers": 5.5}):
        with pytest.raises(Exception, match="near_"):
            scenarios.parse_request(near_body("products", **bad), 0.2, 0)
    req = scenarios.parse_request(near_body("products"), 0.2, 0)
    assert (req.near_km, req.near_peers) == (5.0, 20)
    req = scenarios.parse_request(near_body("products", near_km=2.5, near_peers=8), 0.2, 0)
    assert (req.near_km, req.near_peers) == (2.5, 8)


def test_the_nearby_settings_go_in_the_artifact(near, peers):
    meta = train(near, "near_products", near_body("products", near_km=3))
    assert meta.status_code == 200, meta.text
    x = artifact(near, "near_products", meta.json()).extra
    assert (x["near_km"], x["near_peers"]) == (3.0, 20)
    # Without a category or size there are no peers, near or not.
    x = artifact(peers, "no_peers_near", train(peers, "no_peers_near", peer_body("products")).json()).extra
    assert x["near_km"] is None and x["near_peers"] is None


def test_a_newcomer_is_offered_what_minimarkets_nearby_buy(near):
    meta = train(near, "near_products2", near_body("products")).json()
    model = artifact(near, "near_products2", meta)
    b = scenarios.built_from_artifact(model)
    u = model.users.index("A16")
    peers = scenarios.peer_group(b, u)
    # Its town's other 17 minimarkets, not the 18 in the other town.
    assert peers is not None and peers.of == 17 and peers.within == 5.0
    picks = scenarios.product_scores(b, u, 10)
    ids = [model.items[i] for i, _, _ in picks]
    assert set(ids[:6]) <= {f"A_{p}" for p in range(6)} | set(STAPLES), ids
    assert not any(i.startswith("B_") for i in ids[:6]), ids
    texts = [r["text"] for _, _, rs in picks for r in rs if r["code"] == "peer_group"]
    assert texts and all(t.endswith(" of 17 Minimarket customers within 5 km") for t in texts), texts
    items = [scenarios._item(model.items[i], s, r + 1, rs, {}) for r, (i, s, rs) in enumerate(picks)]
    nearby = [it["id"] for it in items if any(r["code"] == "peer_group" for r in it["reasons"])][:3]
    assert scenarios.nearby_summary(b, u, items) == f"Minimarket customers within 5 km buy: {', '.join(nearby)}"

    # Without coordinates its peers are every other minimarket.
    lone = model.users.index("Anone")
    peers = scenarios.peer_group(b, lone)
    assert peers is not None and peers.within is None and peers.of == 36
    assert scenarios.nearby_summary(b, lone, items) is None

    # The audience of a town's product is that town's customers.
    audience = scenarios.audience_scores(b, model.items.index("A_0"), 5)
    assert {model.users[v][0] for v, _, _ in audience} == {"A"}, [model.users[v] for v, _, _ in audience]
    assert any(r["text"].endswith("Minimarket customers within 5 km") for _, _, rs in audience for r in rs)


def test_a_wider_radius_is_capped_at_near_peers(near):
    meta = train(near, "near_wide", near_body("products", near_km=100, near_peers=10)).json()
    b = scenarios.built_from_artifact(artifact(near, "near_wide", meta))
    peers = scenarios.peer_group(b, b.users.index("A16"))
    assert peers.of == 10 and peers.within == 100.0
    assert peers.reason(4) == "Bought by 4 of 10 Minimarket customers within 100 km"
    # Too few nearby falls back to the whole category.
    meta = train(near, "near_tiny", near_body("products", near_km=0.1)).json()
    b = scenarios.built_from_artifact(artifact(near, "near_tiny", meta))
    peers = scenarios.peer_group(b, b.users.index("A16"))
    assert peers.within is None and peers.of == 36


def test_product_samples_say_what_nearby_customers_buy(near):
    meta = train(near, "near_samples", near_body("products")).json()
    summaries = [s.get("summary") for s in meta["samples"]]
    assert any(s and s.startswith("Minimarket customers within 5 km buy: ") for s in summaries), summaries
    assert meta["evaluation"]["nearby_customers"] > 0


# --------------------------------------------------------------------------
# Product details: brand, subcategory, pack size, lifecycle and stock
# --------------------------------------------------------------------------

# The day the details world's history ends, and its products: two noodle
# brands of one subcategory, a water brand of another.
DETAIL_START = "2025-10-01"
DETAIL_LAST = 364


def detail_items() -> list[dict]:
    """Indomie I1-I6 and Mie Sedaap M1-M6 are instant noodles, Aqua W1-W4
    water. I4 is out of stock with I5 as its substitute; M6 was
    discontinued 30 days before the end; I6 was launched 20 days before
    it. Noodles come in cartons of 40, water in boxes of 12."""
    import datetime as dt

    start = dt.date.fromisoformat(DETAIL_START)
    rows = []
    for brand, prefix, sub, cat, n in (("Indomie", "I", "Instant noodles", "noodles", 6), ("Mie Sedaap", "M", "Instant noodles", "noodles", 6),
                                        ("Aqua", "W", "Water", "drinks", 4)):
        for p in range(1, n + 1):
            sku = f"{prefix}{p}"
            rows.append({"sku": sku, "name": f"{brand} {p}", "cat": cat, "sub": sub, "brand": brand,
                         "pack": 12 if prefix == "W" else 40, "unit": "box" if prefix == "W" else "carton",
                         "launched": (start + dt.timedelta(days=DETAIL_LAST - 20)).isoformat() if sku == "I6" else "2020-01-01",
                         "ends": (start + dt.timedelta(days=DETAIL_LAST - 30)).isoformat() if sku == "M6" else None,
                         "stock": 0 if sku == "I4" else 50, "instead": "I5" if sku == "I4" else None})
    return rows


def detail_tables(seed: int = 9) -> dict[str, list[dict]]:
    """Forty shops: the first twenty spend most on Indomie, the others on Mie
    Sedaap, and everyone buys some water. Each shop orders every two to
    three weeks for a year: two of its brand's first three products, a
    hundred pieces of each, and a box of water. Half of each kind also tried
    their brand's fourth product, and one Sedaap shop I5 once; a few Indomie
    shops bought the new I6 in its first days, and a few Sedaap shops M6
    before it ended."""
    import datetime as dt
    import random

    rng = random.Random(seed)
    start = dt.date.fromisoformat(DETAIL_START)
    orders = []
    for c in range(40):
        cid = f"K{c:02d}"
        prefix = "I" if c < 20 else "M"
        likes = rng.sample([f"{prefix}1", f"{prefix}2", f"{prefix}3"], 2)
        extra = [f"{prefix}4"] if c % 2 == 0 else []
        day = rng.randrange(15)
        while day <= DETAIL_LAST:
            when = (start + dt.timedelta(days=day)).isoformat()
            for sku in likes:
                orders.append({"cust": cid, "sku": sku, "day": when, "qty": 100, "total": 300.0})
            orders.append({"cust": cid, "sku": f"W{rng.randint(1, 4)}", "day": when, "qty": 12, "total": 24.0})
            if extra and rng.random() < 0.3:
                orders.append({"cust": cid, "sku": rng.choice(extra), "day": when, "qty": 40, "total": 120.0})
            day += rng.randint(14, 21)
        if prefix == "I" and c < 4:
            orders.append({"cust": cid, "sku": "I6", "day": (start + dt.timedelta(days=DETAIL_LAST - 5)).isoformat(), "qty": 40, "total": 140.0})
        if c == 39:
            orders.append({"cust": cid, "sku": "I5", "day": (start + dt.timedelta(days=DETAIL_LAST - 90)).isoformat(), "qty": 40, "total": 120.0})
        if prefix == "M" and c < 26:
            orders.append({"cust": cid, "sku": "M6", "day": (start + dt.timedelta(days=DETAIL_LAST - 60)).isoformat(), "qty": 40, "total": 120.0})
    return {"orders": orders, "items": detail_items()}


@pytest.fixture(scope="module")
def details(tmp_path_factory) -> TestClient:
    client = TestClient(create_app(Settings(data_dir=tmp_path_factory.mktemp("details") / "data")))
    tables = detail_tables()
    load(client, "dorders", tables["orders"], VHOST)
    load(client, "ditems", tables["items"], VHOST)
    return client


DETAIL_COLUMNS = {"brand": "brand", "subcategory": "sub", "pack_size": "pack", "unit": "unit", "launch_date": "launched",
                  "end_date": "ends", "stock": "stock", "substitute": "instead"}


def detail_body(scenario: str, columns: dict | None = None, **extra) -> dict:
    products = {"dataset": "ditems", "id": "sku", "category": "cat", "name": "name", **(DETAIL_COLUMNS if columns is None else columns)}
    data = {"transactions": {"dataset": "dorders", "customer": "cust", "product": "sku", "date": "day", "quantity": "qty", "amount": "total"},
            "products": products}
    return {"task": "recommendation", "scenario": scenario, "data": data, "algorithm": "item_similarity", **extra}


def detail_model(client: TestClient, name: str, scenario: str, columns: dict | None = None, **extra) -> tuple[recommend.Model, scenarios.Built]:
    resp = train(client, name, detail_body(scenario, columns, **extra))
    assert resp.status_code == 200, resp.text
    model = artifact(client, name, resp.json())
    return model, scenarios.built_from_artifact(model)


def test_product_details_are_roles_columns_and_new_days_is_checked():
    for key in ("brand", "subcategory", "pack_size", "unit", "launch_date", "end_date", "stock", "substitute"):
        assert key in scenarios.ROLE_KEYS["products"]
    assert ("products", "pack_size") in scenarios.NUMBER_KEYS and ("products", "stock") in scenarios.NUMBER_KEYS
    assert scenarios.parse_request(detail_body("products"), 0.2, 0).new_days == 60
    assert scenarios.parse_request(detail_body("products", new_days=30), 0.2, 0).new_days == 30
    for bad in (0, 366, True, "30", 2.5):
        with pytest.raises(Exception, match="new_days"):
            scenarios.parse_request(detail_body("products", new_days=bad), 0.2, 0)


def test_product_details_go_in_the_artifact(details):
    model, b = detail_model(details, "det_art", "products")
    x = model.extra
    assert x["new_days"] == 60 and x["pair_details"] is True
    assert x["item_features"][: len(scenarios.ITEM_FEATURES)] == list(scenarios.ITEM_FEATURES)
    assert set(x["item_features"]) >= {"pack_size", "launch_day", "end_day", "stock"}
    attrs = x["item_attrs"]
    i4 = model.items.index("I4")
    assert attrs["brand"][i4] == "Indomie" and attrs["subcategory"][i4] == "Instant noodles"
    assert attrs["unit"][i4] == "carton" and attrs["substitute"][i4] == "I5"
    assert len(model.seen_qty) == model.seen.nnz and len(model.seen_spend) == model.seen.nnz
    assert recommend.encode(model) == recommend.encode(recommend.decode(recommend.encode(model)))


def test_without_product_details_the_artifact_is_as_before(details):
    model, _ = detail_model(details, "det_none", "products", columns={})
    x = model.extra
    assert "new_days" not in x and "pair_details" not in x
    assert x["item_features"] == list(scenarios.ITEM_FEATURES)
    assert set(x["item_attrs"]) <= set(scenarios.ITEM_ATTRS) and "brand" not in x["item_attrs"]
    assert model.seen_qty is None and model.seen_spend is None


def ids_and_reasons(b: scenarios.Built, picks) -> tuple[list[str], dict[str, list[dict]]]:
    return [b.items[i] for i, _, _ in picks], {b.items[i]: rs for i, _, rs in picks}


def test_a_customer_is_offered_more_of_the_brand_it_spends_most_on(details):
    _, b = detail_model(details, "det_brand", "products")
    # K02 buys Indomie and water; its other Indomie products come first.
    u = b.users.index("K02")
    ids, reasons = ids_and_reasons(b, scenarios.product_scores(b, u, 10))
    noodles = [i for i in ids if i[0] in "IM"]
    assert noodles and noodles[0].startswith("I"), ids
    texts = [r["text"] for r in reasons[noodles[0]] if r["code"] == "brand"]
    assert texts and texts[0].startswith("Buys a lot of Indomie ("), reasons[noodles[0]]
    share = scenarios.spend_shares(b, u, "brand")
    assert share["Indomie"] > 0.8 and texts[0] == f"Buys a lot of Indomie ({round(share['Indomie'] * 100)}% of spend)"


def test_discontinued_and_out_of_stock_products_are_never_offered(details):
    _, b = detail_model(details, "det_life", "products")
    for c in ("K00", "K01", "K21", "K30"):
        ids, _ = ids_and_reasons(b, scenarios.product_scores(b, b.users.index(c), 20, exclude_seen=False))
        assert "M6" not in ids and "I4" not in ids, (c, ids)
    sim_ids = [b.items[j] for j, _, _ in scenarios.similar_scores(b, b.items.index("M3"), 20)]
    assert "M6" not in sim_ids and "I4" not in sim_ids
    # Nobody is the audience of a product that is not for sale.
    assert scenarios.audience_scores(b, b.items.index("M6"), 10) == []
    assert scenarios.audience_scores(b, b.items.index("I4"), 10) == []


def test_an_out_of_stock_product_is_offered_as_its_substitute(details):
    _, b = detail_model(details, "det_sub", "products")
    # K01 never bought I4 or I5: I5 takes I4's place.
    ids, reasons = ids_and_reasons(b, scenarios.product_scores(b, b.users.index("K01"), 20))
    assert "I4" not in ids and "I5" in ids, ids
    texts = [r["text"] for rs in reasons.values() for r in rs if r["code"] == "substitute"]
    assert texts == ["Instead of Indomie 4 (out of stock)"], reasons


def test_a_new_product_is_offered_to_its_likely_buyers(details):
    _, b = detail_model(details, "det_new", "products")
    # K10 buys Indomie but has not tried the new I6.
    ids, reasons = ids_and_reasons(b, scenarios.product_scores(b, b.users.index("K10"), 10))
    assert "I6" in ids, ids
    assert {"code": "new_product", "text": "New, launched 2026-09-10"} in reasons["I6"]
    # A shop that buys no instant noodles would have no reason to want it;
    # a Sedaap shop does buy them.
    ids, reasons = ids_and_reasons(b, scenarios.product_scores(b, b.users.index("K30"), 10))
    assert {"code": "new_product", "text": "New, launched 2026-09-10"} in reasons.get("I6", []), reasons
    # Its audience is led by the Indomie shops, each told it is new.
    audience = scenarios.audience_scores(b, b.items.index("I6"), 10)
    assert audience and all(b.users[u] < "K20" for u, _, _ in audience), [b.users[u] for u, _, _ in audience]
    assert all({"code": "new_product", "text": "New, launched 2026-09-10"} in rs for _, _, rs in audience)
    # With a shorter window it is no longer new.
    _, short = detail_model(details, "det_new10", "products", new_days=10)
    ids, reasons = ids_and_reasons(short, scenarios.product_scores(short, short.users.index("K10"), 10))
    assert all(r["code"] != "new_product" for rs in reasons.values() for r in rs)


def test_similar_products_share_a_subcategory_or_brand(details):
    _, b = detail_model(details, "det_similar", "similar")
    picks = scenarios.similar_scores(b, b.items.index("I1"), 20)
    ids, reasons = ids_and_reasons(b, picks)
    # Its brand's products bought with it come first, then what is bought
    # with it, then the noodles nobody buys with it.
    assert ids[0][0] == "I", ids
    assert reasons[ids[0]][-1] == {"code": "same_kind", "text": "Same brand and subcategory (Indomie, Instant noodles)"}
    assert {"code": "same_kind", "text": "Same subcategory (Instant noodles)"} in reasons["M1"], reasons
    plain = scenarios.similar_scores(detail_model(details, "det_similar0", "similar", columns={})[1], b.items.index("I1"), 20)
    assert all(r["code"] != "same_kind" for _, _, rs in plain for r in rs)


def test_a_suggested_quantity_is_rounded_up_to_whole_packs(details):
    _, b = detail_model(details, "det_qty", "products")
    u = b.users.index("K02")
    i1 = next(i for i in range(len(b.items)) if b.items[i] in ("I1", "I2", "I3") and scenarios.pair_quantity(b, u, i) is not None)
    # A hundred pieces an order, forty a carton.
    assert scenarios.pair_quantity(b, u, i1) == 100
    fields = scenarios._item_fields(b, i1, u)
    assert fields["suggested_qty"] == 3 and fields["suggested_text"] == "3 cartons (100 pcs)"
    assert fields["brand"] == "Indomie" and fields["subcategory"] == "Instant noodles"
    assert fields["unit"] == "carton" and fields["pack_size"] == 40
    # What the customer never bought has no history to suggest from.
    never = b.items.index("M1")
    assert "suggested_qty" not in scenarios._item_fields(b, never, u)


def test_products_without_details_score_as_before(details):
    """The details are optional: without their columns the same history
    trains the same artifact and answers the same."""
    plain = train(details, "det_plain", detail_body("products", columns={})).json()
    again = train(details, "det_plain2", detail_body("products", columns={})).json()
    a = details.get(f"/v1/models/{VHOST}/det_plain/versions/{plain['version']}/model.rec").content
    b2 = details.get(f"/v1/models/{VHOST}/det_plain2/versions/{again['version']}/model.rec").content
    assert a == b2 and plain["metrics"] == again["metrics"]
    for sample in plain["samples"]:
        for it in sample["items"]:
            assert set(it["fields"]) <= {"name", "category"}
            assert all(r["code"] not in ("brand", "new_product", "substitute", "same_kind") for r in it["reasons"])


# --------------------------------------------------------------------------
# Customer price tier, channel and region
# --------------------------------------------------------------------------


def market_customers() -> list[dict]:
    """The peer tests' customers with a price tier (Gold for Retail, Silver
    otherwise), a channel (their kind) and a region (N for an even number, S
    for an odd one)."""
    out = []
    for row in peer_tables()["customers"]:
        c = int(row["id"][2:])
        out.append({**row, "chan": row["kind"], "pricing": "Gold" if row["kind"] == "Retail" else "Silver", "area": "N" if c % 2 == 0 else "S"})
    return out


@pytest.fixture(scope="module")
def market(tmp_path_factory) -> TestClient:
    client = TestClient(create_app(Settings(data_dir=tmp_path_factory.mktemp("market") / "data")))
    load(client, "porders", peer_tables()["orders"], VHOST)
    load(client, "mcustomers", market_customers(), VHOST)
    return client


def market_body(scenario: str, customers: dict) -> dict:
    out = peer_body(scenario, customers)
    out["data"]["customers"]["dataset"] = "mcustomers"
    return out


def test_price_tier_channel_and_region_are_customer_columns():
    for key in ("price_tier", "channel", "region"):
        assert key in scenarios.ROLE_KEYS["customers"] and key in scenarios.CUSTOMER_ATTRS
    req = scenarios.parse_request(market_body("products", {"price_tier": "pricing", "channel": "chan", "region": "area"}), 0.2, 0)
    assert req.roles["customers"]["region"] == "area"


def test_a_channel_alone_makes_peer_groups(market):
    meta = train(market, "chan_products", market_body("products", {"channel": "chan"}))
    assert meta.status_code == 200, meta.text
    model = artifact(market, "chan_products", meta.json())
    x = model.extra
    assert x["has_peers"] is True and x["customer_attrs"]["channel"][model.users.index("Rl16")] == "Retail"
    b = scenarios.built_from_artifact(model)
    peers = scenarios.peer_group(b, model.users.index("Rl16"))
    # Every other Retail customer, of any size.
    assert peers is not None and peers.of == 53 and peers.label == "Retail"


def test_the_peer_key_joins_category_size_tier_channel_and_region(market):
    resp = train(market, "key_products", market_body("products", {"segment": "kind", "size": "staff", "price_tier": "pricing", "region": "area"}))
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    model = artifact(market, "key_products", meta)
    b = scenarios.built_from_artifact(model)
    u = model.users.index("Rl16")
    peers = scenarios.peer_group(b, u)
    # Retail, large, Gold and in region N: the other eight of its group there.
    assert peers is not None and peers.of == 8 and peers.label == "Retail · large · Gold · N"
    texts = [r["text"] for _, _, rs in scenarios.product_scores(b, u, 10) for r in rs if r["code"] == "peer_group"]
    assert texts and all(t.endswith(" of 8 Retail · large · Gold · N customers") for t in texts), texts
    # Nine categories and sizes, each in two regions.
    assert meta["evaluation"]["peer_groups"] == 18


def test_customer_lists_carry_price_tier_channel_and_region(market):
    meta = train(market, "market_audience", market_body("audience", {"segment": "kind", "price_tier": "pricing", "channel": "chan",
                                                                      "region": "area"})).json()
    fields = [it["fields"] for s in meta["samples"] for it in s["items"]]
    assert fields and all(f["price_tier"] in ("Gold", "Silver") and f["channel"] == f["segment"] and f["region"] in ("N", "S") for f in fields)


def test_unmapped_market_columns_change_nothing(market, peers):
    """A customers table may hold a tier, channel and region: mapped only by
    category and size, it trains the artifact a table without them does."""
    a = train(market, "market_plain", market_body("products", {"segment": "kind", "size": "staff"})).json()
    b2 = train(peers, "peer_plain", peer_body("products", {"segment": "kind", "size": "staff"})).json()
    raw_a = market.get(f"/v1/models/{VHOST}/market_plain/versions/{a['version']}/model.rec").content
    raw_b = peers.get(f"/v1/models/{VHOST}/peer_plain/versions/{b2['version']}/model.rec").content
    assert raw_a == raw_b and a["metrics"] == b2["metrics"] and a["samples"] == b2["samples"]


# --------------------------------------------------------------------------
# Returns and quotes
# --------------------------------------------------------------------------


def bought(orders: list[dict], cust: str) -> dict[str, float]:
    out: dict[str, float] = {}
    for o in orders:
        if o["cust"] == cust:
            out[o["sku"]] = out.get(o["sku"], 0.0) + o["qty"]
    return out


def signal_tables() -> dict[str, list[dict]]:
    """The details shops' returns and quotes. K02 sent back 60% of what it
    bought of its first noodle and 10% of its second. K10 lost a quote for
    M1 a month ago and one for M2 seven months ago; K05 was quoted M3 and
    K06 won a quote for M4, neither ordered yet; K07 was quoted its own
    noodle in July and has ordered it since."""
    orders = detail_tables()["orders"]
    k02 = sorted(s for s in bought(orders, "K02") if s in ("I1", "I2", "I3"))
    k07 = sorted(s for s in bought(orders, "K07") if s in ("I1", "I2", "I3"))
    have = bought(orders, "K02")
    returns = [{"cust": "K02", "sku": k02[0], "day": "2026-06-01", "qty": -round(0.6 * have[k02[0]])},
               {"cust": "K02", "sku": k02[1], "day": "2026-06-01", "qty": round(0.1 * have[k02[1]])}]
    quotes = [{"cust": "K10", "sku": "M1", "day": "2026-09-01", "result": "Lost", "why": "price"},
              {"cust": "K10", "sku": "M2", "day": "2026-03-01", "result": "lost", "why": "price"},
              {"cust": "K05", "sku": "M3", "day": "2026-09-20", "result": None, "why": None},
              {"cust": "K06", "sku": "M4", "day": "2026-09-25", "result": "won", "why": None},
              {"cust": "K07", "sku": k07[0], "day": "2026-07-01", "result": None, "why": None}]
    return {"returns": returns, "quotes": quotes, "returned": [k02[0]], "kept": [k02[1]], "k07": [k07[0]]}


@pytest.fixture(scope="module")
def signals(details) -> TestClient:
    tables = signal_tables()
    load(details, "dreturns", tables["returns"], VHOST)
    load(details, "dquotes", tables["quotes"], VHOST)
    return details


def signal_body(scenario: str, returns: bool = True, quotes: bool = True, **extra) -> dict:
    out = detail_body(scenario, **extra)
    if returns:
        out["data"]["returns"] = {"dataset": "dreturns", "customer": "cust", "product": "sku", "date": "day", "quantity": "qty"}
    if quotes:
        out["data"]["quotes"] = {"dataset": "dquotes", "customer": "cust", "product": "sku", "date": "day", "outcome": "result", "reason": "why"}
    return out


def signal_model(client: TestClient, name: str, scenario: str, **kw) -> tuple[recommend.Model, scenarios.Built, dict]:
    resp = train(client, name, signal_body(scenario, **kw))
    assert resp.status_code == 200, resp.text
    model = artifact(client, name, resp.json())
    return model, scenarios.built_from_artifact(model), resp.json()


def test_returns_and_quotes_are_roles():
    assert scenarios.ROLE_KEYS["returns"] == ("customer", "product", "date", "quantity")
    assert scenarios.ROLE_KEYS["quotes"] == ("customer", "product", "date", "outcome", "reason")
    req = scenarios.parse_request(signal_body("products"), 0.2, 0)
    assert set(req.roles) >= {"returns", "quotes"}
    body = signal_body("products")
    del body["data"]["quotes"]["date"]
    with pytest.raises(Exception, match="data.quotes.date"):
        scenarios.parse_request(body, 0.2, 0)


def test_returns_and_quotes_go_in_the_artifact(signals):
    model, b, meta = signal_model(signals, "sig_art", "products")
    tables = signal_tables()
    n = model.extra["signals"]
    assert model.signals.shape == (n, 4)
    kinds = {(model.users[u], model.items[i]): (scenarios.day_text(d), k) for u, i, d, k in model.signals.tolist()}
    assert kinds[("K02", tables["returned"][0])] == ("2026-06-01", scenarios.RETURNED)
    assert ("K02", tables["kept"][0]) not in kinds
    assert kinds[("K10", "M1")] == ("2026-09-01", scenarios.LOST)
    assert kinds[("K05", "M3")] == ("2026-09-20", scenarios.QUOTED) and kinds[("K06", "M4")] == ("2026-09-25", scenarios.QUOTED)
    # Older than 90 days, or ordered since: neither is kept.
    assert ("K10", "M2") not in kinds and ("K07", tables["k07"][0]) not in kinds
    assert recommend.encode(model) == recommend.encode(recommend.decode(recommend.encode(model)))
    assert meta["evaluation"]["returns"] == 1


def test_without_returns_or_quotes_the_artifact_is_as_before(signals):
    model, _, meta = signal_model(signals, "sig_none", "products", returns=False, quotes=False)
    assert "signals" not in model.extra and model.signals is None
    assert "returns" not in meta["evaluation"]
    plain = train(signals, "sig_plain", detail_body("products")).json()
    a = signals.get(f"/v1/models/{VHOST}/sig_none/versions/{meta['version']}/model.rec").content
    b2 = signals.get(f"/v1/models/{VHOST}/sig_plain/versions/{plain['version']}/model.rec").content
    assert a == b2 and meta["metrics"] == plain["metrics"] and meta["samples"] == plain["samples"]


def test_a_mostly_returned_product_is_not_offered_again(signals):
    tables = signal_tables()
    _, b, _ = signal_model(signals, "sig_ret", "products")
    _, plain, _ = signal_model(signals, "sig_ret0", "products", returns=False, quotes=False)
    u = b.users.index("K02")
    before = {b.items[i]: s for i, s, _ in scenarios.product_scores(plain, u, 16, exclude_seen=False)}
    after = {b.items[i]: s for i, s, _ in scenarios.product_scores(b, u, 16, exclude_seen=False)}
    returned, kept = tables["returned"][0], tables["kept"][0]
    assert returned in before and returned not in after, (before, after)
    # What it kept is offered as before, and so is everything else.
    assert after[kept] == before[kept]
    assert {p: s for p, s in after.items()} == {p: s for p, s in before.items() if p != returned}
    # In an audience, the customer who returned it counts half.
    item = b.items.index(returned)
    was = {v: s for v, s, _ in scenarios.audience_scores(plain, item, 40, exclude_seen=False)}
    now = {v: s for v, s, _ in scenarios.audience_scores(b, item, 40, exclude_seen=False)}
    assert now[u] == pytest.approx(was[u] * scenarios.RETURNED_AUDIENCE, rel=1e-6)
    assert {v: s for v, s in now.items() if v != u} == {v: s for v, s in was.items() if v != u}


def test_a_lost_quote_keeps_the_product_from_the_customer(signals):
    _, b, _ = signal_model(signals, "sig_lost", "products")
    u = b.users.index("K10")
    ids = [b.items[i] for i, _, _ in scenarios.product_scores(b, u, 16)]
    assert "M1" not in ids, ids
    assert u not in [v for v, _, _ in scenarios.audience_scores(b, b.items.index("M1"), 40)]
    # A quote lost seven months ago no longer counts.
    _, plain, _ = signal_model(signals, "sig_lost0", "products", returns=False, quotes=False)
    plain_ids = [plain.items[i] for i, _, _ in scenarios.product_scores(plain, u, 16)]
    assert "M1" in plain_ids and ("M2" in ids) == ("M2" in plain_ids), plain_ids


def test_an_open_quote_is_offered_with_its_date(signals):
    tables = signal_tables()
    _, b, _ = signal_model(signals, "sig_open", "products")
    for cust, sku, day in (("K05", "M3", "2026-09-20"), ("K06", "M4", "2026-09-25")):
        picks = scenarios.product_scores(b, b.users.index(cust), 10)
        reasons = {b.items[i]: rs for i, _, rs in picks}
        assert {"code": "quoted", "text": f"Quoted on {day}, not ordered yet"} in reasons.get(sku, []), (cust, reasons)
    # It ranks higher than it would unquoted.
    _, plain, _ = signal_model(signals, "sig_open0", "products", returns=False, quotes=False)
    u = b.users.index("K05")
    rank = [b.items[i] for i, _, _ in scenarios.product_scores(b, u, 16)].index("M3")
    unquoted = [plain.items[i] for i, _, _ in scenarios.product_scores(plain, u, 16)]
    assert "M3" not in unquoted or rank < unquoted.index("M3")
    # A quote ordered since says nothing.
    picks = scenarios.product_scores(b, b.users.index("K07"), 16, exclude_seen=False)
    assert all(r["code"] != "quoted" for _, _, rs in picks for r in rs)
    assert tables["k07"][0] in [b.items[i] for i, _, _ in picks]


# --------------------------------------------------------------------------
# Training settings
# --------------------------------------------------------------------------


def test_a_version_remembers_its_nearby_and_new_product_settings(near, details):
    meta = train(near, "near_kept", near_body("products", near_km=2.5, near_peers=8)).json()
    assert (meta["near_km"], meta["near_peers"], meta["new_days"]) == (2.5, 8, 60)
    meta = train(details, "new_kept", detail_body("products", new_days=30)).json()
    assert (meta["near_km"], meta["near_peers"], meta["new_days"]) == (5.0, 20, 30)
