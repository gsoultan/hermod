"""Card fraud: is this card payment fraudulent? (yes/no, about one in a hundred)

One row per card payment a payment processor saw over three months, labelled
afterwards by its chargebacks and investigations. About 1% are fraud, so a
model that answers "no" every time is 99% accurate and worthless: read
balanced accuracy, ROC AUC and the fraud class's recall instead.

Fraud shows up as a pattern of things, none decisive alone: online payments
from a new device, a card used abroad far from home, a burst of payments in
the last hour, an amount far above the card's usual (amount_anomaly_score is
what Hermod's Anomaly Score node writes: the amount's z-score against the
card's recent payments, empty until the card has ten of them), risky merchant
groups at night, and failed CVV checks. Columns that do not apply are empty:
an in-store payment has no device or CVV check; an online one no distance.

card_id, merchant_id and tx_id are IDs, tx_time a time stamp (the hour is its
own column). chargeback_filed is what labels the fraud weeks later: it is the
answer, known only afterwards.

    python3 ml/examples/cases/card_fraud.py --out card_fraud.csv               # 100,000 payments
"""

from __future__ import annotations

import datetime as dt

import numpy as np
import pandas as pd

from casekit import main, sigmoid

NAME = "card_fraud"
ROWS = 100_000
SAMPLE_ROWS = 0  # no sample: a few hundred payments hold a handful of frauds

MCC = np.array(["grocery", "fuel", "restaurants", "electronics", "fashion", "travel", "digital_goods", "gambling",
                "pharmacy", "utilities", "jewellery", "gift_cards"])
MCC_SHARE = np.array([0.22, 0.12, 0.16, 0.06, 0.1, 0.05, 0.08, 0.02, 0.06, 0.08, 0.02, 0.03])
MCC_RISK = np.array([-0.8, -0.4, -0.6, 0.9, 0.1, 0.4, 1.0, 1.1, -0.7, -1.2, 1.2, 1.5])
MCC_AMOUNT = np.array([3.4, 3.7, 3.3, 5.3, 4.3, 5.6, 2.9, 4.0, 3.2, 4.2, 5.8, 4.0])
CHANNELS = np.array(["chip", "contactless", "online", "atm"])
CHANNEL_SHARE = [0.3, 0.32, 0.33, 0.05]
COUNTRIES = np.array(["ID", "SG", "MY", "TH", "AU", "US", "GB", "NL", "NG", "BR"])
START = dt.datetime(2026, 4, 1)


def generate(rows: int = ROWS, seed: int = 7) -> pd.DataFrame:
    rng = np.random.default_rng(seed)
    cards = max(20, rows // 12)
    merchants = max(20, rows // 30)

    card_country = rng.choice(COUNTRIES, cards, p=[0.55, 0.12, 0.1, 0.06, 0.05, 0.04, 0.03, 0.02, 0.01, 0.02])
    card_age = rng.integers(5, 3000, cards)
    card_typical = rng.lognormal(0, 0.4, cards)
    merchant_mcc = rng.choice(len(MCC), merchants, p=MCC_SHARE)
    merchant_country = np.where(rng.random(merchants) < 0.75, "ID", rng.choice(COUNTRIES[1:], merchants))
    merchant_bad = rng.random(merchants) < 0.03  # a few merchants are where stolen cards get tested

    card = rng.integers(0, cards, rows)
    merchant = rng.integers(0, merchants, rows)
    mcc = merchant_mcc[merchant]
    channel = rng.choice(len(CHANNELS), rows, p=CHANNEL_SHARE)
    online = channel == 2
    country = np.where(online, merchant_country[merchant], np.where(rng.random(rows) < 0.06,
                       rng.choice(COUNTRIES, rows), card_country[card]))
    foreign = country != card_country[card]
    tx_time = START + pd.to_timedelta(np.sort(rng.uniform(0, 91 * 24 * 3600, rows)), unit="s")
    hour = tx_time.hour.to_numpy()
    night = (hour < 5) | (hour >= 23)
    new_device = np.where(online, rng.random(rows) < 0.08, False)
    cvv_fail = np.where(online, rng.random(rows) < 0.015, False)
    burst = rng.poisson(0.4, rows)
    age = card_age[card] + (tx_time - START).days.to_numpy()

    risk = (-6.35 + MCC_RISK[mcc] + 1.4 * new_device + 1.3 * foreign + 0.9 * night + 2.0 * cvv_fail
            + 0.45 * np.minimum(burst, 6) + 1.8 * merchant_bad[merchant] + np.where(age < 60, 0.9, 0)
            + 0.5 * online + rng.normal(0, 0.6, rows))
    fraud = rng.random(rows) < sigmoid(risk)

    # A fraudster spends more, more often, and farther from home.
    burst = np.where(fraud, burst + rng.poisson(1.5, rows), burst)
    amount = rng.lognormal(MCC_AMOUNT[mcc], 0.7, rows) * card_typical[card]
    amount = np.where(fraud, amount * rng.lognormal(0.6, 0.9, rows), amount)
    amount = np.round(amount, 2)
    usual = rng.lognormal(MCC_AMOUNT[mcc], 0.5, rows) * card_typical[card]
    zscore = np.round((np.log(amount) - np.log(usual)) / 0.55 + rng.normal(0, 0.3, rows), 2)
    zscore = np.where(rng.random(rows) < 0.08, np.nan, zscore)
    distance = np.where(foreign, rng.uniform(300, 12000, rows), rng.gamma(1.2, 9, rows))
    distance = np.where(fraud & ~foreign, distance * rng.uniform(1, 25, rows), distance)
    distance = np.where(online, np.nan, np.round(distance, 1))

    chargeback = np.where(fraud, rng.random(rows) < 0.995, rng.random(rows) < 0.001)

    return pd.DataFrame({
        "tx_id": [f"TX{i + 1:08d}" for i in range(rows)],
        "tx_time": tx_time.strftime("%Y-%m-%dT%H:%M:%S"),
        "card_id": [f"CARD{c + 1:06d}" for c in card],
        "merchant_id": [f"M{m + 1:05d}" for m in merchant],
        "hour": hour,
        "amount": amount,
        "merchant_group": MCC[mcc],
        "channel": CHANNELS[channel],
        "country": country,
        "card_country": card_country[card],
        "is_foreign": foreign,
        "card_age_days": age,
        "tx_count_last_1h": burst,
        "amount_anomaly_score": zscore,
        "distance_from_home_km": distance,
        "new_device": pd.array(np.where(online, new_device, None), dtype="boolean"),
        "cvv_failed": pd.array(np.where(online, cvv_fail, None), dtype="boolean"),
        "chargeback_filed": chargeback,
        "is_fraud": np.where(fraud, "yes", "no"),
    })


if __name__ == "__main__":
    main(generate, ROWS, SAMPLE_ROWS, NAME)
