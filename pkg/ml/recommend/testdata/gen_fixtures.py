"""Regenerate the parity fixtures for pkg/ml/recommend.

Run with the hermod-ml worker's dependencies installed (ml/worker/requirements.txt):

    python -I pkg/ml/recommend/testdata/gen_fixtures.py

For each algorithm it writes <algorithm>/model.rec, an artifact trained by
the worker's own recommend.train, and <algorithm>/cases.json: calls and what
the worker's scoring functions (recommend_user, similar_items,
popular_items) answered for them. Everything is deterministic for a fixed set
of package versions, which are recorded in manifest.json.
"""

from __future__ import annotations

import json
import random
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[3]
sys.path.insert(0, str(REPO / "ml" / "worker"))

import numpy as np  # noqa: E402
import pandas as pd  # noqa: E402
import scipy  # noqa: E402

from hermod_ml import recommend  # noqa: E402
from hermod_ml.datasets import NUMBER, STRING  # noqa: E402


def interactions() -> pd.DataFrame:
    """Shoppers in four aisles of fifteen items, with numeric customer ids
    so the fixtures cover ids that were numbers in the dataset."""
    rng = random.Random(5)
    rows = []
    for p in range(80):
        aisle = p % 4
        mine = rng.sample([f"sku-{aisle * 15 + i}" for i in range(15)], rng.randint(2, 8))
        if rng.random() < 0.4:
            mine.append(f"sku-{rng.randrange(60)}")
        for day, sku in enumerate(mine):
            rows.append({"customer": float(1000 + p), "sku": sku, "qty": float(rng.randint(1, 3)), "day": float(day)})
    return pd.DataFrame(rows)


def item(model: recommend.Model, entry: tuple[int, float, str | None]) -> dict:
    i, score, reason = entry
    out = {"item": model.items[i], "score": score}
    if reason:
        out["reason"] = reason
    return out


def cases(model: recommend.Model) -> list[dict]:
    out = []
    for user, k, exclude in [("1000", 10, True), ("1001", 5, True), ("1002", 10, False), ("1079", 20, True), ("1037", 1, True)]:
        u = model.users.index(user)
        out.append({"user": user, "k": k, "exclude_seen": exclude,
                    "items": [item(model, e) for e in recommend.recommend_user(model, u, k, exclude)]})
    out.append({"user": "stranger", "k": 7, "exclude_seen": True, "items": [item(model, e) for e in recommend.popular_items(model, 7)]})
    for sku, k in [("sku-0", 10), ("sku-31", 5), ("sku-59", 20)]:
        out.append({"item": sku, "k": k, "items": [item(model, e) for e in recommend.similar_items(model, model.items.index(sku), k)]})
    out.append({"item": "sku-unknown", "k": 3, "items": [item(model, e) for e in recommend.popular_items(model, 3)]})
    return out


def main() -> None:
    frame = interactions()
    types = {"customer": NUMBER, "sku": STRING, "qty": NUMBER, "day": NUMBER}
    for algorithm in ("als", "item_similarity"):
        req = recommend.RecommendRequest("orders", "customer", "sku", "qty", "day", algorithm, 0.2, 42)
        artifact, meta = recommend.train(frame, types, req, "fixture")
        model = recommend.decode(artifact)
        out = HERE / algorithm
        out.mkdir(exist_ok=True)
        (out / "model.rec").write_bytes(artifact)
        (out / "cases.json").write_text(json.dumps(cases(model), indent=1) + "\n")
        print(algorithm, len(artifact), "bytes", {k: round(v, 3) for k, v in meta["metrics"].items() if not k.startswith("baseline")})
    manifest = {"numpy": np.__version__, "pandas": pd.__version__, "scipy": scipy.__version__}
    (HERE / "manifest.json").write_text(json.dumps(manifest, indent=1) + "\n")


if __name__ == "__main__":
    main()
