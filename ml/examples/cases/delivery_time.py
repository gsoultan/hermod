"""Delivery time: how many hours from order to doorstep? (a number, skewed, with outliers)

One row per parcel a retailer shipped over a year from six warehouses with
five carriers, as the shipping system exports it. Most parcels arrive in one
to four days; the hours run long to the right (remote islands, heavy freight,
customs), and about one parcel in seventy goes astray for a week or more before
it turns up: outliers no column explains, which an average is pulled by and
a model should not chase.

What is known when the order is placed: the warehouse, carrier and service,
where it goes and how far, the weight and number of parcels, cash on
delivery, whether it crosses a border, the hour and day it was placed, peak
season, and how the carrier did on that lane over the last 30 days (empty
for a lane with no recent parcels).

The export also holds what is known only once the parcel has arrived:
delivered_at, delivery_days (the target in days, rounded) and scan_events
(the tracking scans, which pile up the longer a parcel travels).

    python3 ml/examples/cases/delivery_time.py --out delivery_time.csv         # 40,000 parcels
"""

from __future__ import annotations

import datetime as dt

import numpy as np
import pandas as pd

from casekit import main, missing

NAME = "delivery_time"
ROWS = 40_000
SAMPLE_ROWS = 1_300

WAREHOUSES = np.array(["WH-JKT", "WH-SBY", "WH-BDG", "WH-MDN", "WH-MKS", "WH-DPS"])
WAREHOUSE_DELAY = np.array([0.0, 2.0, 1.0, 4.0, 6.0, 3.0])  # hours to pick and pack
CARRIERS = np.array(["swift", "nusantara_post", "kilat", "cargo_one", "local_courier"])
CARRIER_SPEED = np.array([1.0, 1.25, 0.9, 1.4, 1.1])
CARRIER_SPREAD = np.array([0.1, 0.2, 0.13, 0.18, 0.24])
SERVICES = np.array(["same_day", "express", "standard", "economy"])
SERVICE_SHARE = [0.05, 0.25, 0.5, 0.2]
SERVICE_FACTOR = np.array([0.25, 0.6, 1.0, 1.6])
REGIONS = 34
START = dt.datetime(2025, 9, 1)


def generate(rows: int = ROWS, seed: int = 7) -> pd.DataFrame:
    rng = np.random.default_rng(seed)

    # Each region's distance from each warehouse, and whether it is remote.
    region_distance = rng.uniform(20, 2600, (REGIONS, len(WAREHOUSES)))
    region_remote = rng.random(REGIONS) < 0.2
    region_names = np.array([f"region_{r + 1:02d}" for r in range(REGIONS)])

    wh = rng.choice(len(WAREHOUSES), rows, p=[0.32, 0.22, 0.16, 0.1, 0.1, 0.1])
    carrier = rng.choice(len(CARRIERS), rows, p=[0.3, 0.25, 0.2, 0.1, 0.15])
    service = rng.choice(len(SERVICES), rows, p=SERVICE_SHARE)
    # Most parcels go to nearby, busy regions.
    region = np.minimum(rng.geometric(0.09, rows) - 1, REGIONS - 1)
    distance = np.round(region_distance[region, wh] * rng.uniform(0.9, 1.1, rows), 1)
    remote = region_remote[region] & (rng.random(rows) < 0.8)
    weight = np.round(rng.lognormal(0.3, 1.1, rows), 2)
    parcels = 1 + rng.poisson(np.minimum(weight / 15, 4))
    cod = rng.random(rows) < 0.22
    customs = rng.random(rows) < 0.04

    placed = START + pd.to_timedelta(np.sort(rng.uniform(0, 365 * 24 * 60, rows)), unit="m")
    hour = placed.hour.to_numpy()
    dow = placed.dayofweek.to_numpy()
    month, day = placed.month.to_numpy(), placed.day.to_numpy()
    peak = ((month == 11) & (day >= 8)) | (month == 12) | ((month == 3) & (day >= 15))

    # -- the hours it takes ---------------------------------------------------
    cutoff_wait = np.where(hour >= 15, 24 - hour + 9, 0) + np.where(dow == 6, 24, 0) + np.where(dow == 5, 10, 0)
    travel = (8 + distance / 38) * CARRIER_SPEED[carrier] * SERVICE_FACTOR[service]
    hours = (WAREHOUSE_DELAY[wh] + cutoff_wait * np.where(service == 0, 0.3, 1.0) + travel
             + np.where(remote, rng.gamma(2, 14, rows), 0) + 2.5 * np.log1p(weight) * (1 + (weight > 30))
             + np.where(cod, 4, 0) + np.where(customs, rng.gamma(2, 30, rows), 0))
    hours = hours * np.where(peak, rng.uniform(1.15, 1.6, rows), 1.0)
    hours = hours * rng.lognormal(0, CARRIER_SPREAD[carrier], rows)
    astray = rng.random(rows) < 0.015
    hours = np.where(astray, hours + rng.uniform(120, 480, rows), hours)
    hours = np.round(np.maximum(hours, 1.5), 1)

    # How the carrier did on the lane lately: the lane's hours, blurred.
    lane = np.round(((8 + distance / 38) * CARRIER_SPEED[carrier] + WAREHOUSE_DELAY[wh]) * rng.lognormal(0, 0.12, rows), 1)
    lane = missing(rng, lane, 0.07)

    delivered = placed + pd.to_timedelta(hours, unit="h")
    scans = np.maximum(2, np.round(2 + hours / 12 + rng.normal(0, 2.5, rows))).astype(int)

    return pd.DataFrame({
        "shipment_id": [f"SHP{i + 1:07d}" for i in range(rows)],
        "order_id": [f"ORD{i * 7 + int(rng.integers(0, 7)) + 1000000}" for i in range(rows)],
        "placed_at": placed.strftime("%Y-%m-%dT%H:%M:%S"),
        "placed_hour": hour,
        "placed_weekday": dow,
        "peak_season": peak,
        "warehouse": WAREHOUSES[wh],
        "carrier": CARRIERS[carrier],
        "service_level": SERVICES[service],
        "destination_region": region_names[region],
        "remote_area": remote,
        "distance_km": distance,
        "weight_kg": weight,
        "parcels": parcels,
        "cash_on_delivery": cod,
        "cross_border": customs,
        "lane_avg_hours_30d": lane,
        "delivered_at": delivered.strftime("%Y-%m-%dT%H:%M:%S"),
        "scan_events": scans,
        "delivery_days": np.round(hours / 24, 1),
        "delivery_hours": hours,
    })


if __name__ == "__main__":
    main(generate, ROWS, SAMPLE_ROWS, NAME)
