"""Account spend: what will each software customer spend next quarter? (a number, for a neural network)

One row per business account of a subscription software vendor at the end
of a quarter, over two years, with what the account was billed the quarter
after. Spend is seats times price per seat, plus usage-based API charges, and
moves with how the account uses the product: seats grow where most of them
are active and logins are frequent, shrink where usage fades, and accounts
with many recent support tickets or a low NPS shrink or leave. These are
smooth effects that multiply each other, the kind of shape a neural network
learns well from many rows.

What is known at the end of the quarter: the plan, seats and their price,
this quarter's spend and how it changed, active users, logins and API calls,
support tickets, the discount, the months as a customer, the region and
industry, and the last NPS answer (empty for most accounts, who never
answered the survey).

account_id is an ID and quarter_end a date. invoiced_next_quarter is what
finance billed after the quarter, the answer itself under another name.

    python3 ml/examples/cases/account_spend.py --out account_spend.csv         # 80,000 rows
"""

from __future__ import annotations

import datetime as dt

import numpy as np
import pandas as pd

from casekit import main, missing, sigmoid

NAME = "account_spend"
ROWS = 80_000
SAMPLE_ROWS = 1_300

PLANS = np.array(["starter", "team", "business", "enterprise"])
PLAN_SHARE = [0.4, 0.3, 0.2, 0.1]
PLAN_PRICE = np.array([12.0, 25.0, 45.0, 80.0])
PLAN_SEATS = np.array([1.6, 2.6, 3.6, 5.0])  # log of the usual seats
REGIONS = np.array(["apac", "emea", "north_america", "latam"])
INDUSTRIES = np.array(["software", "finance", "retail", "manufacturing", "healthcare", "education", "media", "logistics"])
QUARTERS = [dt.date(2024, 3, 31), dt.date(2024, 6, 30), dt.date(2024, 9, 30), dt.date(2024, 12, 31),
            dt.date(2025, 3, 31), dt.date(2025, 6, 30), dt.date(2025, 9, 30), dt.date(2025, 12, 31)]


def generate(rows: int = ROWS, seed: int = 7) -> pd.DataFrame:
    rng = np.random.default_rng(seed)
    n = rows
    plan = rng.choice(len(PLANS), n, p=PLAN_SHARE)
    seats = np.maximum(1, np.round(rng.lognormal(PLAN_SEATS[plan], 0.7))).astype(int)
    discount = np.round(np.clip(rng.normal(0.04 * plan + 0.02 * np.log1p(seats), 0.05), 0, 0.4), 2)
    price = np.round(PLAN_PRICE[plan] * (1 - discount), 2)
    active_share = np.round(np.clip(rng.beta(4, 2, n) * rng.uniform(0.6, 1.1, n), 0.02, 1), 3)
    logins = np.round(rng.gamma(3, 4, n) * (0.4 + active_share), 1)
    api_calls = np.round(rng.lognormal(6 + 0.6 * plan, 1.6, n)).astype(int)
    tickets = rng.poisson(0.5 + 0.3 * plan + 2.5 * (active_share < 0.35), n)
    months = rng.integers(1, 96, n)
    growth = np.round(rng.normal(0.02, 0.12, n), 3)
    nps = np.clip(np.round(rng.normal(7 + 2.5 * (active_share - 0.6) - 0.4 * tickets, 1.8)), 0, 10)
    nps = missing(rng, nps, 0.72)
    region = rng.choice(REGIONS, n, p=[0.3, 0.3, 0.3, 0.1])
    industry = rng.choice(INDUSTRIES, n)

    usage_fee = np.round(np.maximum(api_calls - 2000 * (plan + 1), 0) * 0.002 * 3, 2)
    spend_now = np.round(seats * price * 3 + usage_fee, 2)

    # Next quarter: seats move with engagement, usage with API growth, and some accounts leave.
    engagement = 2.2 * (active_share - 0.6) + 0.04 * (logins - 12) - 0.12 * tickets + 0.8 * growth
    engagement = engagement + np.where(np.isnan(nps), 0, 0.06 * (np.nan_to_num(nps) - 7))
    seat_change = np.exp(0.18 * np.tanh(engagement) + 0.05 * np.tanh(months / 24 - 1) + rng.normal(0, 0.06, n))
    next_seats = np.maximum(1, seats * seat_change)
    churn = rng.random(n) < sigmoid(-3.4 - 2.2 * engagement + 0.6 * (months < 6) - 0.4 * plan)
    next_usage = usage_fee * np.exp(0.5 * growth + rng.normal(0, 0.15, n)) * (1 + 0.3 * (active_share > 0.8))
    next_spend = np.where(churn, 0.0, next_seats * price * 3 + next_usage)
    next_spend = np.round(next_spend * rng.lognormal(0, 0.04, n), 2)

    return pd.DataFrame({
        "account_id": [f"ACC{a + 1:06d}" for a in rng.permutation(n)],
        "quarter_end": [QUARTERS[q].isoformat() for q in rng.integers(0, len(QUARTERS), n)],
        "plan": PLANS[plan],
        "region": region,
        "industry": industry,
        "months_as_customer": months,
        "seats": seats,
        "price_per_seat": price,
        "discount": discount,
        "spend_this_quarter": spend_now,
        "spend_growth_qoq": growth,
        "active_user_share": active_share,
        "logins_per_user_30d": logins,
        "api_calls_30d": api_calls,
        "support_tickets_90d": tickets,
        "nps": nps,
        "invoiced_next_quarter": np.round(next_spend * rng.uniform(0.995, 1.005, n), 2),
        "spend_next_quarter": next_spend,
    })


if __name__ == "__main__":
    main(generate, ROWS, SAMPLE_ROWS, NAME)
