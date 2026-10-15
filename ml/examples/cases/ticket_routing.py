"""Ticket routing: which team should take this support ticket? (six classes, lopsided)

One row per ticket an online retailer's help desk received over a year, as
the ticketing system exports it, with the team that finally solved it:
billing, shipping, returns, technical, account or sales. Billing and shipping
get most tickets; sales gets about one in twenty-five.

What is known when a ticket arrives: the channel, the customer's tier, the
product line, whether an order is quoted (and its value and age), and the
words of the subject, turned into a few yes/no keyword columns the way a SQL
query can (subject ILIKE '%refund%'). The subject itself is free text, almost
unique per ticket: as a column it cannot help, so it is dropped once the
keywords are taken from it. A keyword is a hint, not the answer: a customer
who writes "invoice" may want a refund, and many subjects say nothing useful.

The export also holds what is only known once a team has worked on the
ticket: resolution_code (each team's codes start with its own prefix) and
handled_by (each agent sits in one team). Both give the answer away.

    python3 ml/examples/cases/ticket_routing.py --out ticket_routing.csv       # 30,000 tickets
"""

from __future__ import annotations

import datetime as dt

import numpy as np
import pandas as pd

from casekit import main, missing

NAME = "ticket_routing"
ROWS = 30_000
SAMPLE_ROWS = 1_300

TEAMS = np.array(["billing", "shipping", "returns", "technical", "account", "sales"])
TEAM_SHARE = [0.29, 0.26, 0.18, 0.14, 0.09, 0.04]
CHANNELS = np.array(["email", "chat", "phone", "web_form", "social"])
# How each team's tickets arrive (rows: teams in TEAMS order).
CHANNEL_MIX = np.array([
    [0.45, 0.20, 0.15, 0.15, 0.05],
    [0.25, 0.40, 0.10, 0.15, 0.10],
    [0.30, 0.25, 0.10, 0.30, 0.05],
    [0.35, 0.30, 0.20, 0.10, 0.05],
    [0.30, 0.35, 0.15, 0.15, 0.05],
    [0.40, 0.15, 0.30, 0.10, 0.05],
])
TIERS = np.array(["guest", "standard", "plus", "business"])
TIER_MIX = np.array([
    [0.15, 0.55, 0.20, 0.10],
    [0.25, 0.50, 0.20, 0.05],
    [0.20, 0.55, 0.20, 0.05],
    [0.10, 0.45, 0.25, 0.20],
    [0.30, 0.45, 0.20, 0.05],
    [0.05, 0.25, 0.20, 0.50],
])
PRODUCT_LINES = np.array(["phones", "laptops", "home_appliances", "furniture", "fashion", "toys", "groceries",
                          "beauty", "software", "gift_cards"])
# Which product lines each team hears about; technical is about devices and software.
LINE_MIX = np.array([
    [0.10, 0.10, 0.10, 0.08, 0.15, 0.07, 0.12, 0.08, 0.10, 0.10],
    [0.08, 0.08, 0.16, 0.18, 0.15, 0.10, 0.12, 0.08, 0.02, 0.03],
    [0.10, 0.10, 0.12, 0.12, 0.25, 0.10, 0.03, 0.10, 0.03, 0.05],
    [0.25, 0.25, 0.20, 0.02, 0.01, 0.04, 0.01, 0.02, 0.19, 0.01],
    [0.10, 0.08, 0.05, 0.05, 0.12, 0.05, 0.10, 0.10, 0.15, 0.20],
    [0.15, 0.20, 0.15, 0.20, 0.05, 0.05, 0.05, 0.05, 0.08, 0.02],
])
# How often a ticket of each team quotes an order.
ORDER_RATE = [0.75, 0.92, 0.95, 0.55, 0.20, 0.15]
# Keywords, and how often each team's subjects hold them.
KEYWORDS = ["invoice", "refund", "tracking", "broken", "password", "price", "return", "charged_twice"]
KEYWORD_RATE = np.array([
    [0.40, 0.25, 0.02, 0.02, 0.02, 0.10, 0.03, 0.18],
    [0.04, 0.05, 0.55, 0.06, 0.01, 0.01, 0.03, 0.01],
    [0.05, 0.40, 0.08, 0.20, 0.01, 0.02, 0.50, 0.02],
    [0.02, 0.04, 0.02, 0.45, 0.10, 0.01, 0.05, 0.01],
    [0.05, 0.03, 0.01, 0.02, 0.55, 0.02, 0.01, 0.04],
    [0.15, 0.02, 0.02, 0.02, 0.01, 0.55, 0.02, 0.01],
])
SUBJECT_WORDS = {
    "invoice": "invoice", "refund": "refund", "tracking": "tracking number", "broken": "not working",
    "password": "password reset", "price": "price quote", "return": "return label", "charged_twice": "charged twice",
}
FILLER = ["Question about my order", "Need help", "Urgent", "Hello", "Please call me back", "Problem", "Re: your message",
          "Order issue", "Follow-up", "Complaint"]
AGENTS_PER_TEAM = 8
START = dt.datetime(2025, 10, 1)


def generate(rows: int = ROWS, seed: int = 7) -> pd.DataFrame:
    rng = np.random.default_rng(seed)
    team = rng.choice(len(TEAMS), rows, p=TEAM_SHARE)

    def pick(mix: np.ndarray, values: np.ndarray) -> np.ndarray:
        u = rng.random(rows)[:, None]
        return values[(u > np.cumsum(mix[team], axis=1)).sum(axis=1)]

    channel = pick(CHANNEL_MIX, CHANNELS)
    tier = pick(TIER_MIX, TIERS)
    line = pick(LINE_MIX, PRODUCT_LINES)

    has_order = rng.random(rows) < np.array(ORDER_RATE)[team]
    value = rng.lognormal(4.0, 1.0, rows) * np.where(tier == "business", 5, 1)
    value = np.where(has_order, np.round(value, 2), np.nan)
    # Shipping tickets come while the parcel is on its way; returns weeks later.
    age_scale = np.array([12.0, 4.0, 18.0, 25.0, 30.0, 10.0])[team]
    days_since_order = np.where(has_order, np.floor(rng.gamma(1.5, age_scale)), np.nan)

    flags = rng.random((rows, len(KEYWORDS))) < KEYWORD_RATE[team]
    # Some subjects are vague: no keyword at all.
    flags[rng.random(rows) < 0.18] = False

    subjects = []
    for i in range(rows):
        words = [SUBJECT_WORDS[k] for k, on in zip(KEYWORDS, flags[i]) if on]
        head = rng.choice(FILLER) if not words or rng.random() < 0.4 else ""
        number = f"#{rng.integers(100000, 999999)}" if has_order[i] and rng.random() < 0.7 else ""
        subjects.append(" ".join(p for p in [head, ", ".join(words), number] if p).strip())

    created = START + pd.to_timedelta(np.sort(rng.uniform(0, 365 * 24 * 60, rows)), unit="m")
    hour = created.hour.to_numpy()
    weekend = created.dayofweek.to_numpy() >= 5

    attachments = rng.poisson(np.array([0.6, 0.3, 1.2, 1.0, 0.2, 0.4])[team])
    previous = rng.poisson(np.where(tier == "business", 2.5, 0.8))

    codes = np.array(["BIL", "SHP", "RET", "TEC", "ACC", "SAL"])
    resolution = [f"{codes[t]}-{rng.integers(1, 5):02d}" for t in team]
    agent = [f"agent_{t * AGENTS_PER_TEAM + rng.integers(0, AGENTS_PER_TEAM) + 1:02d}" for t in team]

    frame = pd.DataFrame({
        "ticket_id": [f"T-{i + 100001}" for i in range(rows)],
        "created_at": created.strftime("%Y-%m-%dT%H:%M:%S"),
        "hour": hour,
        "weekend": weekend,
        "channel": channel,
        "customer_tier": np.where(rng.random(rows) < 0.04, None, tier),
        "product_line": line,
        "has_order_ref": has_order,
        "order_value": value,
        "days_since_order": days_since_order,
        "attachments": attachments,
        "previous_tickets_90d": previous,
        "subject": subjects,
        **{f"kw_{k}": flags[:, j] for j, k in enumerate(KEYWORDS)},
        "customer_satisfaction": np.round(missing(rng, rng.integers(1, 6, rows), 0.7)),
        "resolution_code": resolution,
        "handled_by": agent,
        "team": TEAMS[team],
    })
    return frame


if __name__ == "__main__":
    main(generate, ROWS, SAMPLE_ROWS, NAME)
