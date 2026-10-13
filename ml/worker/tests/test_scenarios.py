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
