"""The recommendation cases of docs/ml-recommendation-cases.md: every
scenario trained on each of the three businesses of tests/cases.py, and what
the walkthrough says each one does."""

from __future__ import annotations

import datetime as dt

import pytest
from fastapi.testclient import TestClient

from hermod_ml import recommend, scenarios
from hermod_ml.app import create_app
from hermod_ml.settings import Settings
from tests import cases
from tests.helpers import load

VHOST = "cases"
QUOTE_FROM = (cases.TODAY - dt.timedelta(days=scenarios.QUOTE_DAYS)).isoformat()


class Case:
    """A worker holding one case's tables, and the models trained on them,
    each trained once when first asked for."""

    def __init__(self, client: TestClient, name: str):
        self.client, self.name = client, name
        self.tables = cases.generate(name)
        self.roles = cases.roles(name)
        self.models: dict[tuple[str, bool], tuple[scenarios.Built, dict]] = {}
        for dataset, rows in self.tables.items():
            load(client, f"{name}_{dataset}", rows, VHOST)

    def data(self, signals: bool = True) -> dict:
        return {role: {**cols, "dataset": f"{self.name}_{cols['dataset']}"} for role, cols in self.roles.items()
                if signals or role not in ("returns", "quotes")}

    def train(self, scenario: str, signals: bool = True) -> tuple[scenarios.Built, dict]:
        """The model of a scenario, with or without the returns and quotes."""
        if (scenario, signals) not in self.models:
            model = f"{self.name}_{scenario}_{int(signals)}"
            resp = self.client.post(f"/v1/models/{VHOST}/{model}/train",
                                    json={"task": "recommendation", "scenario": scenario, "data": self.data(signals)})
            assert resp.status_code == 200, resp.text
            meta = resp.json()
            raw = self.client.get(f"/v1/models/{VHOST}/{model}/versions/{meta['version']}/model.rec").content
            self.models[(scenario, signals)] = (scenarios.built_from_artifact(recommend.decode(raw)), meta)
        return self.models[(scenario, signals)]

    def column(self, role: str, key: str) -> str:
        return self.roles[role][key]

    def bought(self) -> dict[tuple[str, str], float]:
        """What each customer bought of each product, all time."""
        cust, prod, qty = (self.column("transactions", k) for k in ("customer", "product", "quantity"))
        out: dict[tuple[str, str], float] = {}
        for row in self.tables[self.roles["transactions"]["dataset"]]:
            out[(row[cust], row[prod])] = out.get((row[cust], row[prod]), 0) + row[qty]
        return out

    def unavailable(self) -> set[str]:
        """The products past their end date or out of stock."""
        cols = self.roles["products"]
        out = set()
        for row in self.tables[cols["dataset"]]:
            if (cols.get("end_date") and row[cols["end_date"]]) or (cols.get("stock") and row[cols["stock"]] == 0):
                out.add(row[cols["id"]])
        return out


@pytest.fixture(scope="module")
def worker(tmp_path_factory) -> TestClient:
    return TestClient(create_app(Settings(data_dir=tmp_path_factory.mktemp("cases") / "data")))


@pytest.fixture(scope="module")
def loaded(worker) -> dict[str, Case]:
    return {name: Case(worker, name) for name in cases.CASES}


def ids(b: scenarios.Built, picks) -> list[str]:
    return [b.items[i] for i, _, _ in picks]


def reasons(picks, code: str) -> list[str]:
    return [r["text"] for _, _, rs in picks for r in rs if r["code"] == code]


# --------------------------------------------------------------------------
# Every scenario on every case
# --------------------------------------------------------------------------


@pytest.mark.parametrize("name", cases.CASES)
@pytest.mark.parametrize("scenario", scenarios.SCENARIOS)
def test_every_scenario_trains_and_is_no_worse_than_its_baseline(loaded, name, scenario):
    _, meta = loaded[name].train(scenario)
    assert meta["health"]["ok"] is True
    metrics, baseline = meta["metrics"], meta["baseline"]
    if scenario == "visits":
        assert metrics["ordered_in_period@k"] >= baseline["ordered_in_period@k"]
    elif scenario == "reorder":
        assert metrics["mae_days"] < baseline["mae_days"] and metrics["on_time_share"] >= baseline["on_time_share"]
    else:
        assert metrics["ndcg@10"] >= baseline["ndcg@10"] and metrics["hit_rate@10"] >= baseline["hit_rate@10"]
    assert meta["samples"]


def test_a_visit_plan_is_each_salesmans_own_customers(loaded):
    for name in cases.CASES:
        _, meta = loaded[name].train("visits")
        for sample in meta["samples"]:
            assert sample["items"] and {it["fields"]["salesman"] for it in sample["items"]} == {sample["for"]}, name


# --------------------------------------------------------------------------
# What every case shares
# --------------------------------------------------------------------------


@pytest.mark.parametrize("name", cases.CASES)
def test_what_cannot_be_sold_is_never_recommended(loaded, name):
    case = loaded[name]
    gone = case.unavailable()
    b, _ = case.train("products")
    for u in range(len(b.users)):
        assert not set(ids(b, scenarios.product_scores(b, u, 20, exclude_seen=False))) & gone, b.users[u]
    s, _ = case.train("similar")
    for i in range(len(s.items)):
        assert not set(ids(s, scenarios.similar_scores(s, i, 10))) & gone, s.items[i]
    a, _ = case.train("audience")
    for p in gone & set(a.items):
        assert scenarios.audience_scores(a, a.items.index(p), 100) == [], p


@pytest.mark.parametrize("name", cases.CASES)
def test_a_mostly_returned_product_is_not_offered_again(loaded, name):
    case = loaded[name]
    cust, prod, qty = (case.column("returns", k) for k in ("customer", "product", "quantity"))
    bought = case.bought()
    b, meta = case.train("products")
    plain, _ = case.train("products", signals=False)
    mostly = [(r[cust], r[prod]) for r in case.tables["returns"] if r[qty] >= scenarios.RETURN_SHARE * bought[(r[cust], r[prod])]]
    assert mostly and meta["evaluation"]["returns"] == len(set(mostly))
    changed = 0
    for c, p in mostly:
        u = b.users.index(c)
        after = ids(b, scenarios.product_scores(b, u, 20, exclude_seen=False))
        before = ids(plain, scenarios.product_scores(plain, u, 20, exclude_seen=False))
        assert p not in after, (c, p)
        changed += p in before
    assert changed >= 2
    # In the product's audience the customer counts half.
    a, _ = case.train("audience")
    pa, _ = case.train("audience", signals=False)
    c, p = mostly[0]
    was = {pa.users[v]: s for v, s, _ in scenarios.audience_scores(pa, pa.items.index(p), 500, exclude_seen=False)}
    now = {a.users[v]: s for v, s, _ in scenarios.audience_scores(a, a.items.index(p), 500, exclude_seen=False)}
    assert now[c] == pytest.approx(was[c] * scenarios.RETURNED_AUDIENCE, rel=1e-6)


@pytest.mark.parametrize("name", ["fmcg", "industrial"])
def test_a_lost_quote_keeps_the_product_from_the_customer(loaded, name):
    case = loaded[name]
    cust, prod, date, outcome = (case.column("quotes", k) for k in ("customer", "product", "date", "outcome"))
    lost = [(q[cust], q[prod]) for q in case.tables["quotes"] if q[outcome].lower() == "lost" and q[date] >= QUOTE_FROM]
    assert lost
    b, _ = case.train("products")
    plain, _ = case.train("products", signals=False)
    a, _ = case.train("audience")
    changed = 0
    for c, p in lost:
        u = b.users.index(c)
        assert p not in ids(b, scenarios.product_scores(b, u, 20)), (c, p)
        changed += p in ids(plain, scenarios.product_scores(plain, u, 20))
        if p in a.items:
            assert c not in [a.users[v] for v, _, _ in scenarios.audience_scores(a, a.items.index(p), 500)], (c, p)
    assert changed >= len(lost) // 2


@pytest.mark.parametrize("name", ["fmcg", "industrial"])
def test_an_open_quote_is_offered_with_its_date(loaded, name):
    case = loaded[name]
    cust, prod, date, outcome = (case.column("quotes", k) for k in ("customer", "product", "date", "outcome"))
    open_ = [(q[cust], q[prod], q[date]) for q in case.tables["quotes"] if q[outcome].lower() == "open" and q[date] >= QUOTE_FROM]
    b, _ = case.train("products")
    offered = 0
    for c, p, day in open_:
        picks = scenarios.product_scores(b, b.users.index(c), 20)
        mine = [rs for i, _, rs in picks if b.items[i] == p]
        if mine:
            offered += 1
            assert {"code": "quoted", "text": f"Quoted on {day}, not ordered yet"} in mine[0], (c, p)
    assert open_ and offered >= len(open_) // 2


# --------------------------------------------------------------------------
# One case each
# --------------------------------------------------------------------------


def test_fmcg_outlets_learn_from_nearby_outlets_of_their_kind(loaded):
    case = loaded["fmcg"]
    b, meta = case.train("products")
    assert meta["evaluation"]["nearby_customers"] > 0
    kind = {o["outlet_id"]: o["outlet_type"] for o in case.tables["outlets"]}
    near = 0
    for u in range(0, len(b.users), 10):
        texts = reasons(scenarios.product_scores(b, u, 10), "peer_group")
        near += any(t.endswith(" customers within 5 km") for t in texts)
        # "Bought by 4 of 8 Minimarket · small · General trade · Jakarta customers within 5 km"
        assert all(f" of {t.split(' of ')[1].split()[0]} {kind[b.users[u]]} · " in t for t in texts), texts
    assert near >= 10


def test_fmcg_reorder_reads_each_outlets_own_rhythm(loaded):
    b, meta = loaded["fmcg"].train("reorder")
    # Within two days of the real next order, on average, against seven
    # for everyone's usual gap.
    assert meta["metrics"]["mae_days"] < 2.5 and meta["baseline"]["mae_days"] > 6


def test_industrial_peers_are_of_the_same_industry_and_price_tier(loaded):
    case = loaded["industrial"]
    b, meta = case.train("products")
    accounts = {a["account_no"]: a for a in case.tables["accounts"]}
    assert meta["evaluation"]["peer_groups"] == 9
    for u, account in enumerate(b.users):
        peers = scenarios.peer_group(b, u)
        assert peers is not None and peers.label == f"{accounts[account]['industry']} · {accounts[account]['price_tier']}"


def test_industrial_contract_tiers_are_offered_their_own_brand_line(loaded):
    case = loaded["industrial"]
    b, _ = case.train("products")
    accounts = {a["account_no"]: a for a in case.tables["accounts"]}
    line = {p["item_code"]: p["product_line"].split()[-1] for p in case.tables["items"]}
    for u, account in enumerate(b.users):
        want = {"Contract A": "premium", "List": "economy"}.get(accounts[account]["price_tier"])
        if want:
            assert {line[p] for p in ids(b, scenarios.product_scores(b, u, 5))} == {want}, account


def test_pharmacy_offers_the_generic_of_a_brand_out_of_stock(loaded):
    case = loaded["pharmacy"]
    products = {p["product_code"]: p for p in case.tables["products"]}
    s, _ = case.train("similar")
    empty = [p for p in sorted(products) if products[p]["on_hand"] == 0 and p in s.items]
    assert empty
    for p in empty:
        first, _, why = scenarios.similar_scores(s, s.items.index(p), 5)[0]
        assert s.items[first] == products[p]["substitute"], p
        assert {"code": "same_kind", "text": f"Same subcategory ({products[p]['subcategory']})"} in why
    # A branch's products put the generic in the brand's place.
    b, _ = case.train("products")
    texts = [t for u in range(len(b.users)) for t in reasons(scenarios.product_scores(b, u, 20, exclude_seen=False), "substitute")]
    assert any(t.endswith("(out of stock)") for t in texts) and any(t.endswith("(discontinued)") for t in texts)


def test_pharmacy_branches_learn_from_nearby_branches(loaded):
    b, meta = loaded["pharmacy"].train("products")
    assert meta["evaluation"]["nearby_customers"] > 0
    texts = [t for u in range(len(b.users)) for t in reasons(scenarios.product_scores(b, u, 10), "peer_group")]
    assert any(t.endswith(" customers within 5 km") for t in texts)
