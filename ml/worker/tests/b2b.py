"""A synthetic B2B distributor, for the recommendation scenario tests and the
example in docs/ml-recommendations-data-guide.md.

About 300 customers in four territories, served by 12 salesmen (three per
territory), buying from 200 products in 10 categories over 18 months. Each
customer reorders a core basket on its own cycle (weekly to every two months),
now and then tries what other customers of its kind buy, and is visited by its
salesman on a rhythm of its own. About one in eight customers stops ordering
somewhere in the last six months, and a few start late. Everything follows
from the seed.

    python -I ml/worker/tests/b2b.py OUT_DIR     # writes the five tables as CSV
"""

from __future__ import annotations

import csv
import datetime as dt
import math
import random
import sys
from pathlib import Path

START = dt.date(2025, 4, 1)
END = dt.date(2026, 9, 30)
TERRITORIES = {"north": (52.5, 13.4), "south": (48.1, 11.6), "east": (51.0, 13.7), "west": (50.9, 6.9)}
CATEGORIES = ["beverages", "snacks", "dairy", "frozen", "bakery", "cleaning", "paper", "canned", "spices", "personal"]
SEGMENTS = ("A", "B", "C")
CYCLES = (7, 14, 21, 30, 45, 60)


def generate(seed: int = 7, customers: int = 300, salesmen: int = 12, products: int = 200) -> dict[str, list[dict]]:
    """The distributor's five tables: orders, customers, items, visits, reps."""
    rng = random.Random(seed)
    days = (END - START).days + 1

    items = []
    for p in range(products):
        category = CATEGORIES[p % len(CATEGORIES)]
        price = round(rng.uniform(2, 120), 2)
        items.append({
            "sku": f"S{p + 1:03d}",
            "name": f"{category.title()} item {p // len(CATEGORIES) + 1}",
            "cat": category,
            "price": price,
            "margin": round(rng.uniform(0.08, 0.45), 3),
            "active": rng.random() > 0.05,
        })
    by_category: dict[str, list[dict]] = {}
    for item in items:
        by_category.setdefault(item["cat"], []).append(item)

    reps = []
    names = list(TERRITORIES)
    for r in range(salesmen):
        reps.append({
            "rep": f"R{r + 1:02d}",
            "territory": names[r % len(names)],
            "cap": rng.choice((2, 3, 3, 4)),
            "days": "mon,tue,wed,thu" if r % 5 == 4 else "mon,tue,wed,thu,fri",
        })

    custs, orders, visits = [], [], []
    for c in range(customers):
        rep = reps[c % salesmen]
        centre = TERRITORIES[rep["territory"]]
        segment = rng.choices(SEGMENTS, weights=(1, 2, 3))[0]
        size = {"A": 6.0, "B": 2.5, "C": 1.0}[segment] * math.exp(rng.gauss(0, 0.4))
        cycle = rng.choice(CYCLES)
        # Two or three categories make up a customer's kind; its core basket
        # comes from them, and so do most of the products it tries.
        kind = rng.sample(CATEGORIES, rng.choice((2, 3)))
        pool = [i for cat in kind for i in by_category[cat]]
        core = rng.sample(pool, rng.randint(4, 10))
        first = 0 if rng.random() > 0.08 else rng.randrange(60, 300)
        churn_at = days if rng.random() > 0.12 else rng.randrange(days - 180, days - 20)
        cid = f"C{c + 1:04d}"
        custs.append({
            "cust_id": cid,
            "name": f"Customer {c + 1}",
            "rep": rep["rep"],
            "region": rep["territory"],
            "tier": segment,
            "lat": round(centre[0] + rng.gauss(0, 0.25), 5),
            "lon": round(centre[1] + rng.gauss(0, 0.35), 5),
            "credit_hold": rng.random() < 0.03,
        })

        day = first + rng.randrange(cycle)
        while day < min(days, churn_at):
            date = START + dt.timedelta(days=day)
            basket = [i for i in core if rng.random() < 0.7] or [rng.choice(core)]
            if rng.random() < 0.25:
                basket.append(rng.choice(pool))
            for item in {i["sku"]: i for i in basket}.values():
                qty = max(1, round(size * rng.uniform(1, 6)))
                orders.append({
                    "cust_id": cid, "sku": item["sku"], "order_date": date.isoformat(),
                    "qty": qty, "total": round(qty * item["price"], 2),
                })
            # The next order comes about one cycle later, rarely much later.
            day += max(1, round(cycle * rng.uniform(0.75, 1.3) + (cycle if rng.random() < 0.05 else 0)))

        every = rng.choice((14, 21, 30, 45))
        vday = rng.randrange(every)
        while vday < days:
            date = START + dt.timedelta(days=vday)
            if date.weekday() < 5:
                visits.append({
                    "cust_id": cid, "visit_date": date.isoformat(), "rep": rep["rep"],
                    "result": "order" if rng.random() < 0.4 else "no_order",
                })
            vday += max(1, round(every * rng.uniform(0.8, 1.25)))

    return {"orders": orders, "customers": custs, "items": items, "visits": visits, "reps": reps}


def data_roles() -> dict[str, dict[str, str]]:
    """The training body's "data" for these tables, as the contract shows it."""
    return {
        "transactions": {"dataset": "orders", "customer": "cust_id", "product": "sku", "date": "order_date", "quantity": "qty", "amount": "total"},
        "customers": {"dataset": "customers", "id": "cust_id", "salesman": "rep", "territory": "region", "segment": "tier",
                      "lat": "lat", "lon": "lon", "blocked": "credit_hold", "name": "name"},
        "products": {"dataset": "items", "id": "sku", "category": "cat", "price": "price", "margin": "margin", "active": "active", "name": "name"},
        "visits": {"dataset": "visits", "customer": "cust_id", "date": "visit_date", "salesman": "rep", "outcome": "result"},
        "salesmen": {"dataset": "reps", "id": "rep", "visits_per_day": "cap", "working_days": "days"},
    }


def write_csv(out: Path, seed: int = 7) -> None:
    out.mkdir(parents=True, exist_ok=True)
    for name, rows in generate(seed).items():
        with (out / f"{name}.csv").open("w", newline="") as f:
            writer = csv.DictWriter(f, fieldnames=list(rows[0]))
            writer.writeheader()
            writer.writerows(rows)


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit("usage: b2b.py OUT_DIR")
    write_csv(Path(sys.argv[1]))
