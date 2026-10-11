"""Recommendation models: training, the split, the metrics and the artifact."""

from __future__ import annotations

import json
import random

import numpy as np
import pytest

from hermod_ml import recommend
from tests.helpers import load, train


def order_rows(people: int = 60, seed: int = 3, with_time: bool = True) -> list[dict]:
    """Orders where each person shops from one of three aisles of ten items,
    plus the odd item from elsewhere, one order per day."""
    rng = random.Random(seed)
    rows = []
    for p in range(people):
        aisle = p % 3
        mine = rng.sample([f"sku-{aisle * 10 + i}" for i in range(10)], 6)
        if rng.random() < 0.3:
            mine.append(f"sku-{rng.randrange(30)}")
        for day, sku in enumerate(mine):
            row = {"customer": f"c{p}", "sku": sku, "qty": rng.randint(1, 4)}
            if with_time:
                row["ordered_at"] = f"2026-03-{day + 1:02d}T10:00:00Z"
            rows.append(row)
    return rows


def recommend_body(**overrides):
    body = {"dataset": "orders", "task": "recommendation", "recommend": {"user": "customer", "item": "sku"}}
    body.update(overrides)
    return body


@pytest.fixture
def orders(client):
    load(client, "orders", order_rows())
    return client


def test_a_recommender_trains_with_defaults(orders, tmp_path):
    resp = train(orders, "recs", **recommend_body())
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    assert meta["model"] == "recs"
    assert meta["version"] == "1"
    assert meta["task"] == "recommendation"
    assert meta["algorithm"] in ("als", "item_similarity")
    assert set(meta["candidates"]) == {"als", "item_similarity"}
    assert meta["recommend"] == {"user": "customer", "item": "sku", "strength": None, "time": None}
    assert meta["features"] == [] and meta["feature_types"] == {} and meta["target"] == ""
    want = {"hit_rate@10", "precision@10", "recall@10", "ndcg@10", "coverage"}
    assert set(meta["metrics"]) == want | {f"baseline_{k}" for k in want} | {"score"}
    assert meta["metrics"]["score"] == meta["metrics"]["ndcg@10"]
    assert all(0 <= v <= 1 for v in meta["metrics"].values())
    assert meta["catalog"] == {"users": 60, "items": 30}
    assert meta["rows"]["train"] + meta["rows"]["test"] == len({(r["customer"], r["sku"]) for r in order_rows()})
    assert len(meta["samples"]) == 3
    assert all(len(s["recommended"]) == 10 and s["picked"] for s in meta["samples"])

    vdir = tmp_path / "data/models/acme/recs/1"
    assert sorted(p.name for p in vdir.iterdir()) == ["meta.json", "model.rec"]
    assert json.loads((vdir / "meta.json").read_text()) == meta


@pytest.mark.parametrize("algorithm", ["als", "item_similarity"])
def test_each_algorithm_beats_the_popular_items_baseline(orders, algorithm):
    meta = train(orders, "recs", **recommend_body(algorithm=algorithm)).json()
    assert meta["algorithm"] == algorithm
    assert "candidates" not in meta
    m = meta["metrics"]
    assert m["hit_rate@10"] > m["baseline_hit_rate@10"], m
    assert m["ndcg@10"] > m["baseline_ndcg@10"], m
    assert m["coverage"] > m["baseline_coverage"], m


def test_with_a_time_column_each_person_is_tested_on_their_latest_picks(client):
    load(client, "orders", order_rows())
    meta = train(client, "recs", **recommend_body(recommend={"user": "customer", "item": "sku", "time": "ordered_at"})).json()
    assert meta["recommend"]["time"] == "ordered_at"
    latest: dict[str, dict[str, str]] = {}
    for r in order_rows():
        mine = latest.setdefault(r["customer"], {})
        mine[r["sku"]] = max(mine.get(r["sku"], ""), r["ordered_at"])
    for sample in meta["samples"]:
        picks = sorted((t, sku) for sku, t in latest[sample["user"]].items())
        held = len(sample["picked"])
        assert held == max(1, -(-len(picks) * 2 // 10))
        assert sample["picked"] == sorted(sku for _, sku in picks[-held:])


def test_without_a_time_column_a_random_share_is_held_back(client):
    load(client, "orders", order_rows(with_time=False))
    a = train(client, "a", **recommend_body(seed=1)).json()
    b = train(client, "b", **recommend_body(seed=1)).json()
    assert a["rows"] == b["rows"]
    assert a["samples"] == b["samples"]
    assert a["rows"]["test"] > 0


def test_strength_must_be_numeric_and_drops_rows_without_one(client):
    rows = order_rows()
    rows[0]["qty"] = None
    rows[1]["qty"] = 0
    load(client, "orders", rows)
    meta = train(client, "recs", **recommend_body(recommend={"user": "customer", "item": "sku", "strength": "qty"})).json()
    assert meta["rows"]["train"] + meta["rows"]["test"] == len({(r["customer"], r["sku"]) for r in rows[2:]})

    resp = train(client, "recs", **recommend_body(recommend={"user": "customer", "item": "sku", "strength": "ordered_at"}))
    assert resp.status_code == 400
    assert "must hold numbers" in resp.json()["error"]


@pytest.mark.parametrize(
    "overrides, fragment",
    [
        ({"recommend": None}, "'recommend' is required"),
        ({"recommend": {"item": "sku"}}, "'recommend.user' is required"),
        ({"recommend": {"user": "customer"}}, "'recommend.item' is required"),
        ({"recommend": {"user": "customer", "item": "customer"}}, "more than once"),
        ({"recommend": {"user": "customer", "item": "sku", "rating": "qty"}}, "unknown keys"),
        ({"recommend": {"user": "customer", "item": "sku", "time": 3}}, "'recommend.time' must be"),
        ({"target": "sku"}, "'target' does not apply"),
        ({"features": ["qty"]}, "'features' does not apply"),
        ({"params": {"epochs": 3}}, "'params' does not apply"),
        ({"algorithm": "random_forest"}, "must be one of auto, als, item_similarity"),
        ({"test_size": 0}, "test_size"),
        ({"recommend": {"user": "nobody", "item": "sku"}}, "'nobody' is not in dataset"),
        ({"recommend": {"user": "customer", "item": "sku", "time": "qty"}}, None),
    ],
)
def test_bad_recommendation_requests_are_400(orders, overrides, fragment):
    resp = train(orders, "recs", **recommend_body(**overrides))
    if fragment is None:
        # A numeric time column is an ordering key, not an error.
        assert resp.status_code == 200, resp.text
        return
    assert resp.status_code == 400, resp.text
    assert fragment in resp.json()["error"]


@pytest.mark.parametrize(
    "body, fragment",
    [
        ({"dataset": "orders", "target": "sku", "recommend": {"user": "customer", "item": "sku"}}, "'recommend' applies only"),
        ({"dataset": "orders", "target": "sku", "algorithm": "als"}, "'algorithm' must be one of"),
        ({"dataset": "orders", "target": "sku", "task": "classification", "algorithm": "item_similarity"}, "'algorithm' must be one of"),
    ],
)
def test_other_tasks_refuse_what_only_a_recommender_takes(orders, body, fragment):
    resp = train(orders, "m", **body)
    assert resp.status_code == 400, resp.text
    assert fragment in resp.json()["error"]


def test_too_few_interactions_is_400(client):
    load(client, "orders", order_rows()[:5])
    resp = train(client, "recs", **recommend_body())
    assert resp.status_code == 400
    assert "at least 10 usable interactions" in resp.json()["error"]


def test_an_oversized_artifact_is_refused_and_nothing_is_saved(orders, tmp_path, monkeypatch):
    monkeypatch.setattr(recommend, "MAX_ARTIFACT_BYTES", 1024)
    resp = train(orders, "recs", **recommend_body(algorithm="als"))
    assert resp.status_code == 400
    assert "over the 0 MB limit" in resp.json()["error"] or "limit" in resp.json()["error"]
    assert not (tmp_path / "data/models/acme/recs").exists() or not any((tmp_path / "data/models/acme/recs").iterdir())


@pytest.mark.parametrize("algorithm", ["als", "item_similarity"])
def test_the_artifact_round_trips_and_serves_what_training_scored(orders, tmp_path, algorithm):
    train(orders, "recs", **recommend_body(algorithm=algorithm))
    raw = (tmp_path / "data/models/acme/recs/1/model.rec").read_bytes()
    assert raw[:4] == b"HREC"
    model = recommend.decode(raw)
    assert model.algorithm == algorithm
    assert len(model.users) == 60 and len(model.items) == 30
    assert (model.user_factors is not None) == (algorithm == "als")
    assert (model.neighbor_items is not None) == (algorithm == "item_similarity")
    assert recommend.encode(model) == raw

    u = model.users.index("c0")
    recs = recommend.recommend_user(model, u, 10)
    seen = set(model.seen.indices[model.seen.indptr[u] : model.seen.indptr[u + 1]].tolist())
    assert len(recs) == 10
    assert not seen & {i for i, _, _ in recs}
    scores = [s for _, s, r in recs if r is None]
    assert scores == sorted(scores, reverse=True)
    similar = recommend.similar_items(model, model.items.index("sku-0"), 5)
    assert 0 < len(similar) <= 5
    assert model.items.index("sku-0") not in {i for i, _, _ in similar}


@pytest.mark.parametrize(
    "raw, fragment",
    [
        (b"", "not a recommendation artifact"),
        (b"ONNX" + bytes(20), "not a recommendation artifact"),
        (b"HREC" + (2).to_bytes(4, "little") + (2).to_bytes(4, "little") + b"{}", "format 2"),
    ],
)
def test_decode_refuses_what_is_not_an_artifact(raw, fragment):
    with pytest.raises(ValueError, match=fragment):
        recommend.decode(raw)


def test_decode_refuses_a_truncated_artifact(orders, tmp_path):
    train(orders, "recs", **recommend_body(algorithm="als"))
    raw = (tmp_path / "data/models/acme/recs/1/model.rec").read_bytes()
    with pytest.raises(ValueError, match="truncated"):
        recommend.decode(raw[:-3])
    with pytest.raises(ValueError, match="after its last section"):
        recommend.decode(raw + b"\0")


def test_the_artifact_and_routes_of_a_recommendation_version(orders, tmp_path):
    train(orders, "recs", **recommend_body())
    resp = orders.get("/v1/models/acme/recs/versions/1/model.rec")
    assert resp.status_code == 200
    assert resp.content == (tmp_path / "data/models/acme/recs/1/model.rec").read_bytes()

    resp = orders.get("/v1/models/acme/recs/versions/1/model.onnx")
    assert resp.status_code == 404
    assert "recommendation model" in resp.json()["error"]
    resp = orders.post("/vhosts/acme/v2/models/recs/infer", json={"inputs": [{"name": "x", "datatype": "FP64", "shape": [1], "data": [1]}]})
    assert resp.status_code == 400
    assert "recommendation model" in resp.json()["error"]
    resp = orders.get("/v1/models/acme/recs/versions/1/export")
    assert resp.status_code == 400
    assert "recommendation" in resp.json()["error"]


def test_the_artifact_route_refuses_a_version_that_is_not_a_recommender(client):
    from tests.helpers import classification_rows

    load(client, "churn", classification_rows())
    assert train(client, "churn", dataset="churn", target="churned").status_code == 200
    resp = client.get("/v1/models/acme/churn/versions/1/model.rec")
    assert resp.status_code == 404


def test_capabilities_offer_recommendations(client):
    caps = client.get("/v1/capabilities").json()
    assert "recommendation" in caps["tasks"]
    assert {"als", "item_similarity"} <= set(caps["algorithms"])


def test_popular_fallback_and_holdout_helpers():
    data = recommend.Interactions(
        users=["a", "b", "c"],
        items=["x", "y", "z"],
        user=np.array([0, 0, 0, 1, 1, 2]),
        item=np.array([0, 1, 2, 0, 1, 0]),
        strength=np.ones(6),
        time=np.array([1.0, 3.0, 2.0, 5.0, 4.0, 1.0]),
    )
    train_rows, test_rows = recommend.holdout(data, np.arange(6), 0.2, 0)
    # a holds back its latest (y at 3), b its latest (x at 5); c has one pick.
    assert test_rows.tolist() == [1, 3]
    assert train_rows.tolist() == [0, 2, 4, 5]
    model = recommend.fit("item_similarity", data, None, 0)
    assert [i for i, _, _ in recommend.popular_items(model, 3)] == [0, 1, 2]
    assert all(reason == "popular" for _, _, reason in recommend.popular_items(model, 3))
    assert recommend.popular_items(model, 3)[0][1] == 1.0
