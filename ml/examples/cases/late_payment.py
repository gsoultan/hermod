"""Late payment risk: will this B2B invoice be paid late? (yes/no, lopsided)

One row per invoice a wholesaler issued over two years to about 3,000
business customers, as an accounts-receivable export gives it. About one
invoice in seven is paid after its due date. Whether it is depends mostly on
the customer's own habit (their share of earlier invoices paid late), on how
stretched their credit is, on disputes, on the payment terms and the
customer's segment, and a little on the amount and the month (year-end runs
late). New customers have no history: their history columns are empty.

The export also holds what is only known after the due date, which a model
must not learn from: reminders_sent (dunning letters go out only once an
invoice is overdue) and days_to_pay and paid_date (known once it is paid).
invoice_id, issue_date and due_date are row labels, not signals.

    python3 ml/examples/cases/late_payment.py --out late_payment.csv           # 40,000 invoices
    python3 ml/examples/cases/late_payment.py --rows 2000 --out sample.csv
"""

from __future__ import annotations

import datetime as dt

import numpy as np
import pandas as pd

from casekit import main, missing, sigmoid

NAME = "late_payment"
ROWS = 40_000
SAMPLE_ROWS = 1_300

START = dt.date(2024, 1, 1)
DAYS = 731
SEGMENTS = np.array(["smb", "mid_market", "enterprise", "public_sector"])
SEGMENT_SHARE = [0.55, 0.25, 0.12, 0.08]
SEGMENT_RISK = {"smb": 0.35, "mid_market": 0.0, "enterprise": -0.45, "public_sector": 0.7}
INDUSTRIES = np.array(["construction", "retail", "hospitality", "healthcare", "manufacturing", "logistics",
                       "education", "agriculture", "it_services", "automotive", "energy", "media"])
INDUSTRY_RISK = dict(zip(INDUSTRIES, [0.5, 0.2, 0.45, -0.2, 0.0, 0.1, -0.1, 0.3, -0.3, 0.05, -0.25, 0.25]))
REGIONS = np.array(["north", "south", "east", "west", "central", "coast", "islands", "highlands"])
TERMS = np.array([14, 30, 45, 60])
CURRENCIES = np.array(["EUR", "USD", "GBP"])


def generate(rows: int = ROWS, seed: int = 7) -> pd.DataFrame:
    rng = np.random.default_rng(seed)
    customers = max(50, rows // 13)

    # -- customers: who they are and how they pay -----------------------------
    c_segment = rng.choice(SEGMENTS, customers, p=SEGMENT_SHARE)
    c_industry = rng.choice(INDUSTRIES, customers)
    c_region = rng.choice(REGIONS, customers, p=[0.2, 0.18, 0.15, 0.15, 0.12, 0.1, 0.05, 0.05])
    c_habit = rng.normal(0, 1, customers)  # how late a payer they are, unseen
    c_since = rng.integers(-2500, DAYS - 30, customers)  # first invoice, days from START
    c_terms = rng.choice(TERMS, customers, p=[0.15, 0.5, 0.15, 0.2])
    c_currency = rng.choice(CURRENCIES, customers, p=[0.7, 0.2, 0.1])
    c_size = rng.lognormal(7.0, 1.0, customers) * np.select(
        [c_segment == "enterprise", c_segment == "public_sector", c_segment == "mid_market"], [6, 3, 2.2], 1)
    c_rep = rng.integers(1, 61, customers)
    weight = np.select([c_segment == "enterprise", c_segment == "mid_market"], [4.0, 2.0], 1.0)

    # -- invoices -------------------------------------------------------------
    cust = rng.choice(customers, rows, p=weight / weight.sum())
    day = np.maximum(rng.integers(0, DAYS, rows), np.clip(c_since[cust], 0, DAYS - 1))
    issue = np.array([START + dt.timedelta(days=int(d)) for d in day])
    month = np.array([d.month for d in issue])
    terms = np.where(rng.random(rows) < 0.9, c_terms[cust], rng.choice(TERMS, rows))
    amount = np.round(c_size[cust] * rng.lognormal(0, 0.8, rows), 2)
    outlier = rng.random(rows) < 0.004
    amount[outlier] *= rng.uniform(15, 60, outlier.sum())
    tenure = day - c_since[cust]

    # The customer's record before this invoice: how many, and how many late.
    history = np.minimum(np.maximum(tenure // 21, 0), 400) + rng.integers(0, 3, rows)
    history[tenure < 20] = 0
    p_hist = sigmoid(-2.0 + 1.3 * c_habit[cust])
    late_before = rng.binomial(history, p_hist)
    late_share = np.where(history > 0, late_before / np.maximum(history, 1), np.nan)
    days_late_before = np.where(late_before > 0, np.round(rng.gamma(2.0, 4 + 6 * sigmoid(c_habit[cust])), 1), 0.0)
    days_late_before[history == 0] = np.nan

    utilisation = np.clip(rng.normal(0.55 + 0.15 * c_habit[cust], 0.25), 0, 1.6)
    dispute = rng.random(rows) < 0.04 + 0.03 * sigmoid(c_habit[cust])
    segment = c_segment[cust]
    industry = c_industry[cust]

    risk = (-2.95 + 1.05 * c_habit[cust] + 1.1 * (utilisation - 0.55) + 1.6 * dispute
            + np.vectorize(SEGMENT_RISK.get)(segment) + np.vectorize(INDUSTRY_RISK.get)(industry)
            + np.select([terms == 60, terms == 14], [0.35, -0.25], 0)
            + 0.12 * np.log1p(amount / 1000) + np.where(month == 12, 0.45, 0) + np.where(month == 8, 0.2, 0)
            + np.where(history == 0, 0.6, 0) + rng.normal(0, 0.55, rows))
    late = rng.random(rows) < sigmoid(risk)

    # -- what is known only after the due date --------------------------------
    days_after_due = np.where(late, np.ceil(rng.gamma(1.6, 9 + 8 * sigmoid(c_habit[cust]))),
                              -np.floor(rng.uniform(0, terms * 0.9)))
    days_to_pay = (terms + days_after_due).astype(int)
    reminders = np.where(late, 1 + np.minimum(days_after_due // 14, 5), 0).astype(int)
    courtesy = ~late & (rng.random(rows) < 0.01)
    reminders[courtesy] = 1
    paid = [i + dt.timedelta(days=int(d)) for i, d in zip(issue, days_to_pay)]

    frame = pd.DataFrame({
        "invoice_id": [f"INV-{i + 1:06d}" for i in range(rows)],
        "customer_id": [f"C-{c + 1:05d}" for c in cust],
        "issue_date": [d.isoformat() for d in issue],
        "due_date": [(d + dt.timedelta(days=int(t))).isoformat() for d, t in zip(issue, terms)],
        "issue_month": month,
        "amount": amount,
        "currency": c_currency[cust],
        "payment_terms_days": terms,
        "segment": segment,
        "industry": industry,
        "region": np.where(rng.random(rows) < 0.03, None, c_region[cust]),
        "sales_rep": [f"rep_{r:02d}" for r in c_rep[cust]],
        "customer_tenure_days": np.maximum(tenure, 0),
        "prior_invoices": history,
        "prior_late_share": np.round(late_share, 3),
        "avg_days_late_prior": days_late_before,
        "credit_utilisation": np.round(missing(rng, utilisation, 0.12), 3),
        "has_dispute": dispute,
        "reminders_sent": reminders,
        "days_to_pay": days_to_pay,
        "paid_date": [d.isoformat() for d in paid],
        "paid_late": np.where(late, "yes", "no"),
    })
    frame = frame.sort_values("issue_date", kind="stable").reset_index(drop=True)
    frame["invoice_id"] = [f"INV-{i + 1:06d}" for i in range(rows)]
    return frame


if __name__ == "__main__":
    main(generate, ROWS, SAMPLE_ROWS, NAME)
