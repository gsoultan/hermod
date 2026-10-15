"""Weekly demand: how many units will each product sell next week in each warehouse? (a number, per series)

Hermod has no separate forecasting model: a forecast is a number to predict,
so it is a regression on a table built for it. One row per product, warehouse
and week, over two years: 100 products in 10 categories, stocked in five
warehouses. The row for week W holds what is known at the end of week W —
the last weeks' sales, the same week last year, and what is already planned
for week W+1 (its price, a promotion, a public holiday) — and the target is
the units sold in week W+1.

Sales follow each series' own level, a yearly season per category, a slow
trend, promotions (a lift of a third to double), price changes and holidays,
with Poisson noise. When a warehouse runs out, the week's sales are cut short
(stockout_last_week), so the lags understate demand.

week_start is a date (a label, not a signal: the week of the year is its own
column), series_id is product and warehouse together, an ID. revenue_next_week
is units_next_week times the price, which is only known after the week.
Rows with less than a year of history have no same_week_last_year.

    python3 ml/examples/cases/weekly_demand.py --out weekly_demand.csv        # about 50,000 rows
"""

from __future__ import annotations

import datetime as dt

import numpy as np
import pandas as pd

from casekit import main

NAME = "weekly_demand"
ROWS = 50_000
SAMPLE_ROWS = 1_300

WEEKS = 104
WARMUP = 13  # weeks before the first row, so the lags are filled
WAREHOUSES = np.array(["north", "south", "east", "west", "central"])
CATEGORIES = np.array(["beverages", "snacks", "dairy", "frozen", "bakery", "cleaning", "paper", "canned",
                       "personal_care", "pet"])
START = dt.date(2024, 9, 2)  # a Monday, WARMUP weeks before the first row


def generate(rows: int = ROWS, seed: int = 7) -> pd.DataFrame:
    """About rows rows: as many series as it takes, each with WEEKS rows."""
    rng = np.random.default_rng(seed)
    series = max(1, round(rows / WEEKS))
    total = WARMUP + WEEKS + 1

    products = max(1, (series + len(WAREHOUSES) - 1) // len(WAREHOUSES))
    p_category = rng.integers(0, len(CATEGORIES), products)
    p_level = rng.lognormal(3.0, 0.9, products)
    p_price = np.round(rng.lognormal(1.6, 0.6, products), 2)
    p_elasticity = rng.uniform(1.0, 2.5, products)
    c_phase = rng.uniform(0, 2 * np.pi, len(CATEGORIES))
    c_amplitude = rng.uniform(0.05, 0.45, len(CATEGORIES))
    w_size = np.array([1.3, 1.0, 0.8, 0.9, 1.1])

    week = np.arange(total)
    dates = np.array([START + dt.timedelta(weeks=int(w)) for w in week])
    woy = np.array([d.isocalendar()[1] for d in dates])
    holiday = np.isin(woy, [1, 14, 15, 22, 33, 52])

    out = []
    for s in range(series):
        p, w = s // len(WAREHOUSES), s % len(WAREHOUSES)
        cat = p_category[p]
        level = p_level[p] * w_size[w] * rng.lognormal(0, 0.3)
        trend = 1 + rng.normal(0, 0.15) * week / 52
        season = 1 + c_amplitude[cat] * np.sin(2 * np.pi * week / 52.18 + c_phase[cat])
        promo = rng.random(total) < 0.08
        lift = np.where(promo, rng.uniform(1.3, 2.0, total), 1.0)
        discount = np.where(promo, np.round(rng.uniform(0.1, 0.35, total), 2), 0.0)
        drift = np.exp(np.cumsum(rng.normal(0, 0.01, total)))
        price = np.round(p_price[p] * drift * (1 - discount), 2)
        price_effect = (price / (p_price[p] * drift)) ** -p_elasticity[p] / lift ** 0.5
        mean = level * np.maximum(trend, 0.3) * season * lift * price_effect * np.where(holiday, 1.35, 1.0)
        demand = rng.poisson(np.maximum(mean, 0.05))
        stock = rng.random(total) < 0.03
        sold = np.where(stock, np.floor(demand * rng.uniform(0.2, 0.7, total)), demand).astype(int)

        for t in range(WARMUP, WARMUP + WEEKS):
            # The row for week t: what is known at its end, and next week's sales.
            out.append((
                dates[t].isoformat(), f"P{p + 1:03d}-{WAREHOUSES[w]}", f"P{p + 1:03d}", WAREHOUSES[w], CATEGORIES[cat],
                int(woy[t + 1]), bool(holiday[t + 1]), bool(promo[t + 1]), discount[t + 1], price[t + 1],
                int(sold[t]), int(sold[t - 1]), round(float(sold[t - 3:t + 1].mean()), 2),
                round(float(sold[t - 12:t + 1].mean()), 2),
                int(sold[t - 51]) if t - 51 >= 0 else None, bool(stock[t]),
                round(float(sold[t + 1] * price[t + 1]), 2), int(sold[t + 1]),
            ))
    columns = ["week_start", "series_id", "product_id", "warehouse", "category", "week_of_year_next", "holiday_next_week",
               "promo_next_week", "discount_next_week", "price_next_week", "units_last_week", "units_2_weeks_ago",
               "avg_units_last_4_weeks", "avg_units_last_13_weeks", "same_week_last_year", "stockout_last_week",
               "revenue_next_week", "units_next_week"]
    frame = pd.DataFrame(out, columns=columns)
    frame["same_week_last_year"] = frame["same_week_last_year"].astype("Int64")
    return frame.sort_values(["week_start", "series_id"], kind="stable").reset_index(drop=True)


if __name__ == "__main__":
    main(generate, ROWS, SAMPLE_ROWS, NAME)
