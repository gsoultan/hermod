"""The worked cases of docs/ml-cases.md (generators in ml/examples/cases).

Every generator gives the same file for the same rows and seed, and the
samples kept in the repository are what the generators write, so the Go
tests that train on them (pkg/ml/builtin/cases_test.go) and the guide's
numbers stay in step with the code. The cases that need the worker train
here on their full-size data: card fraud with XGBoost, the account spend
network with Keras (skipped without the "-dl" libraries), and late payment
with Auto, which compares the worker's own algorithms.
"""

from __future__ import annotations

import gzip
import importlib
import sys
from pathlib import Path

import pytest

from tests.helpers import require_dl, train

CASES_DIR = Path(__file__).resolve().parents[2] / "examples" / "cases"
CASES = ["late_payment", "ticket_routing", "delivery_time", "weekly_demand", "account_spend", "card_fraud"]


def case(name: str):
    """A case's generator module."""
    if str(CASES_DIR) not in sys.path:
        sys.path.insert(0, str(CASES_DIR))
    return importlib.import_module(name)


def upload(client, name: str, rows: int | None = None, vhost: str = "acme") -> None:
    """Upload a case's generated file as the dataset of the same name, the way the console does."""
    module, kit = case(name), case("casekit")
    body = kit.to_csv(module.generate(rows or module.ROWS, 7))
    resp = client.put(f"/v1/datasets/{vhost}/{name}/file?format=csv", content=body)
    assert resp.status_code == 200, resp.text


def roles(client, dataset: str, target: str, vhost: str = "acme") -> dict[str, str]:
    resp = client.post(f"/v1/train-check/{vhost}", json={"dataset": dataset, "target": target})
    assert resp.status_code == 200, resp.text
    return {c["name"]: c["role"] for c in resp.json()["columns"]}


@pytest.mark.parametrize("name", CASES)
def test_a_generator_gives_the_same_file_for_the_same_seed(name):
    module, kit = case(name), case("casekit")
    first = kit.to_csv(module.generate(400, 7))
    assert first == kit.to_csv(module.generate(400, 7))
    assert first != kit.to_csv(module.generate(400, 8))
    assert first.count(b"\n") == 401 or name == "weekly_demand"  # whole weeks of each series


@pytest.mark.parametrize("name", [c for c in CASES if case(c).SAMPLE_ROWS])
def test_the_committed_sample_is_what_the_generator_writes(name):
    module, kit = case(name), case("casekit")
    sample = gzip.decompress((kit.SAMPLE_DIR / f"{name}.csv.gz").read_bytes())
    assert sample == kit.to_csv(module.generate(module.SAMPLE_ROWS, 7)), (
        f"samples/{name}.csv.gz is out of date: run python3 ml/examples/cases/{name}.py --sample"
    )


def test_card_fraud_with_xgboost_beats_its_baseline(client):
    upload(client, "card_fraud")
    found = roles(client, "card_fraud", "is_fraud")
    assert found["tx_id"] == "id_like"
    assert found["chargeback_filed"] == "leak_suspect"
    assert found["new_device"] == "mostly_empty"

    features = ["hour", "amount", "merchant_group", "channel", "country", "card_country", "is_foreign", "card_age_days",
                "tx_count_last_1h", "amount_anomaly_score", "distance_from_home_km", "new_device", "cvv_failed"]
    resp = train(client, "card_fraud", dataset="card_fraud", target="is_fraud", features=features, algorithm="xgboost")
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    print("card_fraud", meta["metrics"], meta["baseline"], meta["importance"][:5], meta["warnings"])
    assert meta["labels"] == ["no", "yes"]
    # Always "no" is 99% accurate, with a balanced accuracy and ROC AUC of 0.5.
    assert meta["baseline"]["accuracy"] > 0.98
    assert meta["metrics"]["roc_auc"] >= 0.88
    assert meta["metrics"]["balanced_accuracy"] - meta["baseline"]["balanced_accuracy"] >= 0.2

    inputs = [
        {"name": "hour", "shape": [2], "datatype": "FP64", "data": [2, 13]},
        {"name": "amount", "shape": [2], "datatype": "FP64", "data": [1240.0, 32.5]},
        {"name": "merchant_group", "shape": [2], "datatype": "BYTES", "data": ["gift_cards", "grocery"]},
        {"name": "channel", "shape": [2], "datatype": "BYTES", "data": ["online", "contactless"]},
        {"name": "country", "shape": [2], "datatype": "BYTES", "data": ["NG", "ID"]},
        {"name": "card_country", "shape": [2], "datatype": "BYTES", "data": ["ID", "ID"]},
        {"name": "is_foreign", "shape": [2], "datatype": "BOOL", "data": [True, False]},
        {"name": "card_age_days", "shape": [2], "datatype": "FP64", "data": [40, 1900]},
        {"name": "tx_count_last_1h", "shape": [2], "datatype": "FP64", "data": [5, 0]},
        {"name": "amount_anomaly_score", "shape": [2], "datatype": "FP64", "data": [4.1, 0.2]},
        {"name": "distance_from_home_km", "shape": [2], "datatype": "FP64", "data": [None, 3.2]},
        {"name": "new_device", "shape": [2], "datatype": "BOOL", "data": [True, None]},
        {"name": "cvv_failed", "shape": [2], "datatype": "BOOL", "data": [True, None]},
    ]
    resp = client.post("/vhosts/acme/v2/models/card_fraud/infer", json={"inputs": inputs})
    assert resp.status_code == 200, resp.text
    outputs = {o["name"]: o["data"] for o in resp.json()["outputs"]}
    print("card_fraud infer", outputs)
    assert outputs["label"] == ["yes", "no"]


def test_late_payment_auto_on_the_worker_beats_its_baseline(client):
    upload(client, "late_payment")
    features = ["issue_month", "amount", "currency", "payment_terms_days", "segment", "industry", "region",
                "customer_tenure_days", "prior_invoices", "prior_late_share", "avg_days_late_prior", "credit_utilisation",
                "has_dispute"]
    resp = train(client, "late_payment", dataset="late_payment", target="paid_late", features=features, preset="quick")
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    print("late_payment", meta["algorithm"], meta["metrics"], meta["baseline"], meta["leaderboard"])
    assert meta["metrics"]["roc_auc"] >= 0.72
    assert meta["metrics"]["balanced_accuracy"] - meta["baseline"]["balanced_accuracy"] >= 0.15
    assert [entry["metric"] for entry in meta["leaderboard"]] == ["balanced_accuracy"] * len(meta["leaderboard"])


def test_account_spend_with_keras_beats_its_baseline(client):
    require_dl("keras_mlp")
    upload(client, "account_spend", rows=20_000)
    features = ["plan", "region", "industry", "months_as_customer", "seats", "price_per_seat", "discount",
                "spend_this_quarter", "spend_growth_qoq", "active_user_share", "logins_per_user_30d", "api_calls_30d",
                "support_tickets_90d"]
    params = {"hidden_layers": [128, 64, 32], "epochs": 300, "batch_size": 256, "learning_rate": 0.001, "patience": 20}
    resp = train(client, "account_spend", dataset="account_spend", target="spend_next_quarter", features=features,
                 algorithm="keras_mlp", params=params)
    assert resp.status_code == 200, resp.text
    meta = resp.json()
    print("account_spend", meta["metrics"], meta["baseline"])
    assert meta["metrics"]["r2"] >= 0.8
    assert 1 - meta["metrics"]["mae"] / meta["baseline"]["mae"] >= 0.6
