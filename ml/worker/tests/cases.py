"""Three synthetic businesses for the recommendation cases in
docs/ml-recommendation-cases.md, and the tests that train every scenario on
them (test_cases.py).

- "fmcg": a fast-moving consumer goods distributor. About 300 outlets in
  three cities (minimarkets, restaurants and pharmacies, in neighbourhoods
  with tastes of their own), four salesmen, 150 products over 12 months,
  with a Ramadan spike of syrups, dates and cooking oil, products launched,
  discontinued and out of stock, returns and lost quotes.
- "industrial": a B2B industrial supplier. 60 larger accounts (factories,
  workshops, contractors) on contract price tiers that buy different brand
  lines, three key account managers, 80 products over 18 months, long
  reorder cycles, and quotes for most new business: won, lost and open.
- "pharmacy": a chain of 40 pharmacy branches ordering from its warehouse,
  four area managers, 120 products in brand and generic pairs over 12
  months, a rainy-season rise in cough and cold, brand products out of stock
  or discontinued with their generic as the substitute, and near-expiry
  returns.

Each case's tables follow from its seed; CASES names them, and roles(case)
is the training body's "data" for them.

    python -I ml/worker/tests/cases.py OUT_DIR [CASE ...]   # every table as CSV
    python -I ml/worker/tests/cases.py --sample 12 OUT_DIR  # the first 12 rows of each
"""

from __future__ import annotations

import csv
import datetime as dt
import random
import sys
from pathlib import Path

CASES = ("fmcg", "industrial", "pharmacy")

END = dt.date(2026, 9, 30)
# The day a model trained on these tables answers for: the day after the
# last order.
TODAY = END + dt.timedelta(days=1)


def _day(d: dt.date) -> str:
    return d.isoformat()


def _visits(rng: random.Random, cust: str, rep: str, start: dt.date, every: int, ordered: set[str]) -> list[dict]:
    """A salesman's visits to a customer about every `every` days, on
    weekdays; the outcome is "ordered" when the customer ordered that day."""
    out = []
    day = start + dt.timedelta(days=rng.randrange(every))
    while day <= END:
        if day.weekday() < 5:
            out.append({"customer_id": cust, "visit_date": _day(day), "salesman_id": rep,
                        "outcome": "ordered" if _day(day) in ordered else "no order"})
        day += dt.timedelta(days=max(1, round(every * rng.uniform(0.8, 1.2))))
    return out


# --------------------------------------------------------------------------
# FMCG distributor
# --------------------------------------------------------------------------

CITIES = {"Jakarta": (-6.2000, 106.8167), "Bandung": (-6.9175, 107.6191), "Surabaya": (-7.2575, 112.7521)}
# Three neighbourhoods a city's outlets cluster in, about 6 km apart.
HOODS = ((0.0, 0.0), (0.05, 0.03), (-0.03, 0.05))
FMCG_CATEGORIES = {
    "Beverages": ("Tea", "Syrup"),
    "Snacks": ("Chips", "Biscuits"),
    "Instant noodles": ("Fried noodles", "Soup noodles"),
    "Household": ("Detergent", "Dishwash"),
    "Personal care": ("Soap", "Shampoo"),
    "Cooking": ("Cooking oil", "Dates"),
    "Frozen": ("Nuggets", "Sausages"),
    "OTC medicine": ("Pain relief", "Cold relief"),
    "Vitamins": ("Vitamin C", "Multivitamin"),
    "Baby care": ("Diapers", "Baby soap"),
}
FMCG_BRANDS = ("Sedap Rasa", "Kapal Api Jaya", "Mawar", "Nusantara", "Pelita", "Samudra")
OUTLET_KINDS = {
    "Minimarket": ("Beverages", "Snacks", "Instant noodles", "Household", "Personal care", "Cooking"),
    "Restaurant": ("Cooking", "Beverages", "Frozen"),
    "Pharmacy": ("OTC medicine", "Vitamins", "Personal care", "Baby care"),
}
# Ramadan 2026 and the two weeks before it, when syrups, dates and cooking oil
# sell several times as much.
RAMADAN = (dt.date(2026, 2, 4), dt.date(2026, 3, 19))
SEASONAL_SUBS = ("Syrup", "Dates", "Cooking oil")


def _fmcg_products() -> list[dict]:
    """150 products: 15 in each of 10 categories, two subcategories each.
    The 15th of a category was launched in the last 45 days; the 13th was
    discontinued at the end of August and the 14th is out of stock, each
    with the product two places before it (of the same subcategory) as
    the substitute."""
    out = []
    for c, (cat, subs) in enumerate(FMCG_CATEGORIES.items()):
        for k in range(15):
            n = len(out)
            sku = f"FM{n + 1:03d}"
            sub = subs[k % 2]
            price = round(4000 + 1500 * ((n * 37) % 23), -2)
            out.append({
                "sku": sku, "name": f"{FMCG_BRANDS[(c + k) % len(FMCG_BRANDS)]} {sub} {k + 1}", "category": cat, "subcategory": sub,
                "brand": FMCG_BRANDS[(c + k) % len(FMCG_BRANDS)], "price": price, "margin": round(0.08 + 0.02 * (n % 9), 2),
                "pack_size": (12, 24, 40)[k % 3], "unit": ("carton", "box", "carton")[k % 3],
                "launch_date": _day(END - dt.timedelta(days=20 + 5 * c)) if k == 14 else "2024-01-01",
                "end_date": "2026-08-31" if k == 12 else "",
                "stock": 0 if k == 13 else 200 + 7 * n,
                "substitute": out[-2]["sku"] if k in (12, 13) else "",
                "active": "true",
            })
    return out


def fmcg(seed: int = 11) -> dict[str, list[dict]]:
    rng = random.Random(seed)
    start = END - dt.timedelta(days=364)
    products = _fmcg_products()
    by_sku = {p["sku"]: p for p in products}
    by_cat: dict[str, list[dict]] = {}
    for p in products:
        by_cat.setdefault(p["category"], []).append(p)
    # A neighbourhood's favourite products of each kind of outlet: two of its
    # kind's categories' regular products, the same for every outlet there.
    favourites: dict[tuple[str, int, str], list[str]] = {}
    for city in CITIES:
        for h in range(len(HOODS)):
            for kind, cats in OUTLET_KINDS.items():
                pool = [p["sku"] for cat in cats for p in by_cat[cat][:12]]
                favourites[(city, h, kind)] = rng.sample(pool, 3)
    salesmen = [{"salesman_id": "SM1", "visits_per_day": 14, "working_days": "mon,tue,wed,thu,fri,sat"},
                {"salesman_id": "SM2", "visits_per_day": 14, "working_days": "mon,tue,wed,thu,fri,sat"},
                {"salesman_id": "SM3", "visits_per_day": 12, "working_days": "mon,tue,wed,thu,fri"},
                {"salesman_id": "SM4", "visits_per_day": 12, "working_days": "mon,tue,wed,thu,fri"}]
    rep_of = {"Jakarta": ("SM1", "SM2"), "Bandung": ("SM3",), "Surabaya": ("SM4",)}
    outlets, orders, visits, returns, quotes = [], [], [], [], []
    n = 0
    for city, count in (("Jakarta", 120), ("Bandung", 90), ("Surabaya", 90)):
        lat0, lon0 = CITIES[city]
        for j in range(count):
            n += 1
            oid = f"OUT{n:03d}"
            kind = rng.choices(list(OUTLET_KINDS), weights=(5, 3, 2))[0]
            size = rng.choices(("small", "medium", "large"), weights=(5, 3.5, 1.5))[0]
            hood = j % len(HOODS)
            dlat, dlon = HOODS[hood]
            rep = rep_of[city][j % len(rep_of[city])]
            channel = {"Minimarket": "Modern trade" if size == "large" else "General trade", "Restaurant": "HoReCa", "Pharmacy": "Pharmacy"}[kind]
            outlets.append({
                "outlet_id": oid, "name": f"{kind} {city} {j + 1}", "salesman_id": rep, "city": city, "outlet_type": kind,
                "outlet_size": size, "channel": channel,
                "latitude": round(lat0 + dlat + rng.gauss(0, 0.008), 5), "longitude": round(lon0 + dlon + rng.gauss(0, 0.008), 5),
                "credit_hold": "true" if rng.random() < 0.02 else "false",
            })
            cats = OUTLET_KINDS[kind]
            regular = [p for cat in cats for p in by_cat[cat][:12]]
            core = list(dict.fromkeys(favourites[(city, hood, kind)][:2] + [p["sku"] for p in rng.sample(regular, rng.randint(3, 6))]))
            scale = {"small": 1, "medium": 2, "large": 4}[size]
            cycle = rng.choice((7, 14, 14, 21, 30))
            ordered_days: set[str] = set()
            bought: dict[str, int] = {}
            day = start + dt.timedelta(days=rng.randrange(cycle))
            while day <= END:
                basket = [s for s in core if rng.random() < 0.75] or [core[0]]
                if rng.random() < 0.2:
                    basket.append(rng.choice(regular)["sku"])
                ramadan = RAMADAN[0] <= day <= RAMADAN[1]
                if ramadan and kind != "Pharmacy":
                    basket += [p["sku"] for cat in ("Beverages", "Cooking") for p in by_cat[cat][:12] if p["subcategory"] in SEASONAL_SUBS
                               and rng.random() < 0.3]
                # A third of the outlets bought the discontinued product of one
                # of their categories until its end, and a fifth the one now
                # out of stock; a new product sells from its launch, to a few.
                for c, cat in enumerate(cats):
                    old, empty, new = by_cat[cat][12], by_cat[cat][13], by_cat[cat][14]
                    mine = c == n % len(cats)
                    if mine and n % 3 == 0 and day <= dt.date(2026, 8, 31) and rng.random() < 0.6:
                        basket.append(old["sku"])
                    if mine and n % 5 == 1 and rng.random() < 0.6:
                        basket.append(empty["sku"])
                    if _day(day) >= new["launch_date"] and rng.random() < 0.05:
                        basket.append(new["sku"])
                for sku in dict.fromkeys(basket):
                    p = by_sku[sku]
                    boost = 3 if ramadan and p["subcategory"] in SEASONAL_SUBS else 1
                    qty = max(1, round(scale * boost * rng.uniform(1, 4)))
                    orders.append({"outlet_id": oid, "sku": sku, "order_date": _day(day), "qty": qty, "amount": qty * p["price"]})
                    bought[sku] = bought.get(sku, 0) + qty
                ordered_days.add(_day(day))
                day += dt.timedelta(days=max(1, round(cycle * rng.uniform(0.8, 1.25))))
            visits += [{"outlet_id": v["customer_id"], "visit_date": v["visit_date"], "salesman_id": v["salesman_id"], "outcome": v["outcome"]}
                       for v in _visits(rng, oid, rep, start, 7 if size == "large" else 14, ordered_days)]
            # One outlet in twelve sent back most of a product it bought; one
            # in twelve a little of one.
            if bought and n % 12 == 5:
                sku = max(sorted(bought), key=lambda s: bought[s])
                returns.append({"outlet_id": oid, "sku": sku, "return_date": "2026-07-15", "qty": round(0.6 * bought[sku]), "reason": "slow seller"})
            elif bought and n % 12 == 9:
                sku = sorted(bought)[0]
                returns.append({"outlet_id": oid, "sku": sku, "return_date": "2026-08-20", "qty": max(1, round(0.1 * bought[sku])), "reason": "damaged"})
            # One outlet in ten lost a quote for a regular product of its kind it
            # never bought, a month ago; one in ten was quoted a new product
            # it has not ordered yet.
            never = [p["sku"] for p in regular if p["sku"] not in bought]
            if never and n % 10 == 3:
                quotes.append({"outlet_id": oid, "sku": never[0], "quote_date": "2026-09-01", "status": "lost", "lost_reason": "price"})
            if n % 10 == 7:
                new = by_cat[cats[0]][14]["sku"]
                if new not in bought:
                    quotes.append({"outlet_id": oid, "sku": new, "quote_date": "2026-09-22", "status": "open", "lost_reason": ""})
    return {"orders": orders, "outlets": outlets, "products": products, "visits": visits, "salesmen": salesmen,
            "returns": returns, "quotes": quotes}


# --------------------------------------------------------------------------
# Industrial supplier
# --------------------------------------------------------------------------

IND_CATEGORIES = ("Bearings", "Lubricants", "Filters", "Safety", "Fasteners", "Hydraulics", "Tools", "Electrical")
# A premium and an economy brand line in every category; contract A accounts
# buy the premium line, list-price ones the economy line.
IND_LINES = {"premium": ("Ironbark", "Kestrel"), "economy": ("Volta", "Saguaro")}
REGIONS = ("West Java", "Central Java", "East Java", "Banten")


def _industrial_products() -> list[dict]:
    """80 products: 10 in each of 8 categories, the first five of a
    category the premium line and the rest the economy line. The 10th of a
    category was launched in the last 40 days."""
    out = []
    for c, cat in enumerate(IND_CATEGORIES):
        for k in range(10):
            n = len(out)
            line = "premium" if k < 5 else "economy"
            brand = IND_LINES[line][c % 2]
            out.append({
                "item_code": f"IN{n + 1:03d}", "description": f"{brand} {cat.lower()} {k + 1}", "product_group": cat,
                "product_line": f"{cat} {line}", "brand": brand, "list_price": round((180 if line == "premium" else 95) * (1 + 0.3 * (k % 5)), 2),
                "pack_size": (1, 10, 50)[k % 3], "unit": ("piece", "box", "case")[k % 3],
                "launched_on": _day(END - dt.timedelta(days=25 + 2 * c)) if k == 9 else "2023-06-01",
            })
    return out


def industrial(seed: int = 23) -> dict[str, list[dict]]:
    rng = random.Random(seed)
    start = END - dt.timedelta(days=547)
    products = _industrial_products()
    by_cat: dict[str, list[dict]] = {}
    for p in products:
        by_cat.setdefault(p["product_group"], []).append(p)
    managers = [{"kam": f"KAM{r + 1}", "visits_per_day": 4, "working_days": "mon,tue,wed,thu,fri"} for r in range(3)]
    accounts, orders, visits, returns, quotes = [], [], [], [], []
    kinds = {"Factory": ("Bearings", "Lubricants", "Filters", "Hydraulics", "Electrical"),
             "Workshop": ("Tools", "Lubricants", "Fasteners", "Bearings"),
             "Contractor": ("Safety", "Fasteners", "Tools", "Electrical")}
    for a in range(60):
        aid = f"ACC{a + 1:02d}"
        kind = ("Factory", "Workshop", "Contractor")[a % 3]
        tier = ("Contract A", "Contract B", "List")[(a // 3) % 3]
        staff = round({"Contract A": 900, "Contract B": 300, "List": 60}[tier] * rng.uniform(0.6, 1.6))
        region = REGIONS[(a // 9) % len(REGIONS)]
        kam = f"KAM{a % 3 + 1}"
        accounts.append({"account_no": aid, "account_name": f"{kind} {a + 1}", "kam": kam, "industry": kind, "headcount": staff,
                         "price_tier": tier, "channel": "Direct" if tier != "List" else "Distributor", "region": region})
        line = "premium" if tier == "Contract A" else "economy" if tier == "List" else rng.choice(("premium", "economy"))
        pool = [p for cat in kinds[kind] for p in by_cat[cat][:9] if (p["product_line"].endswith(line))]
        core = rng.sample(pool, min(len(pool), rng.randint(3, 5)))
        cycle = rng.choice((45, 60, 90, 120))
        scale = max(1, staff // 100)
        bought: dict[str, int] = {}
        ordered: set[str] = set()
        day = start + dt.timedelta(days=rng.randrange(cycle))
        while day <= END:
            for p in [p for p in core if rng.random() < 0.8] or core[:1]:
                qty = max(1, round(scale * rng.uniform(2, 8)))
                orders.append({"account_no": aid, "item_code": p["item_code"], "invoice_date": _day(day), "qty": qty,
                               "net_amount": round(qty * p["list_price"] * {"Contract A": 0.8, "Contract B": 0.9, "List": 1.0}[tier], 2)})
                bought[p["item_code"]] = bought.get(p["item_code"], 0) + qty
            ordered.add(_day(day))
            day += dt.timedelta(days=max(1, round(cycle * rng.uniform(0.85, 1.2))))
        # New business comes through quotes: each account is quoted two
        # products of its line it does not buy, one won (and ordered two weeks
        # later) and one lost or still open.
        others = [p for p in pool if p["item_code"] not in bought]
        if len(others) >= 2:
            won, other = others[0], others[1]
            qday = END - dt.timedelta(days=200 + 3 * a)
            quotes.append({"account_no": aid, "item_code": won["item_code"], "quote_date": _day(qday), "result": "Won", "loss_reason": ""})
            oday = qday + dt.timedelta(days=14)
            qty = max(1, scale * 3)
            orders.append({"account_no": aid, "item_code": won["item_code"], "invoice_date": _day(oday), "qty": qty,
                           "net_amount": round(qty * won["list_price"], 2)})
            ordered.add(_day(oday))
            if a % 4 == 1:
                quotes.append({"account_no": aid, "item_code": other["item_code"], "quote_date": _day(END - dt.timedelta(days=20 + a % 30)),
                               "result": "Lost", "loss_reason": ("price", "lead time", "spec")[a % 3]})
            elif a % 4 == 3:
                quotes.append({"account_no": aid, "item_code": other["item_code"], "quote_date": _day(END - dt.timedelta(days=10 + a % 20)),
                               "result": "Open", "loss_reason": ""})
        # One account in ten sent back every one of a product once: the
        # wrong specification.
        if a % 10 == 4 and bought:
            sku = sorted(bought)[0]
            returns.append({"account_no": aid, "item_code": sku, "return_date": "2026-06-10", "qty": bought[sku], "reason": "wrong spec"})
        visits += [{"account_no": v["customer_id"], "visit_date": v["visit_date"], "kam": v["salesman_id"], "outcome": v["outcome"]}
                   for v in _visits(rng, aid, kam, start, 30, ordered)]
    orders.sort(key=lambda o: (o["invoice_date"], o["account_no"], o["item_code"]))
    return {"invoices": orders, "accounts": accounts, "items": products, "visits": visits, "managers": managers,
            "returns": returns, "quotes": quotes}


# --------------------------------------------------------------------------
# Pharmacy chain
# --------------------------------------------------------------------------

PH_CATEGORIES = ("Analgesics", "Cough and cold", "Vitamins", "Skin care", "Baby", "Diabetes care")
BRANCH_AREAS = {"North": (-6.1380, 106.8650), "South": (-6.2900, 106.8000)}


def _pharmacy_products() -> list[dict]:
    """120 products: 20 in each of 6 categories, in brand and generic pairs
    (the brand at an even place, its generic next). Brands at places 0, 2
    and 4 of a category are out of stock, and the one at 6 was discontinued
    last month; each has its generic as the substitute."""
    out = []
    for cat in PH_CATEGORIES:
        for k in range(20):
            n = len(out)
            generic = k % 2 == 1
            pair = k // 2 + 1
            brand = "Generik" if generic else ("Medika", "Sehat", "Prima")[pair % 3]
            out.append({
                "product_code": f"PH{n + 1:03d}", "product_name": f"{brand} {cat.lower()} {pair}", "category": cat,
                "subcategory": f"{cat} {pair}", "brand": brand, "price": round((3.5 if generic else 9.0) * (1 + 0.2 * pair), 2),
                "pack_size": (10, 20, 30)[pair % 3], "unit": "box",
                "discontinued_on": "2026-09-01" if k == 6 else "",
                "on_hand": 0 if k in (0, 2, 4) else 40 + n,
                "substitute": f"PH{n + 2:03d}" if k in (0, 2, 4, 6) else "",
            })
    return out


def pharmacy(seed: int = 31) -> dict[str, list[dict]]:
    rng = random.Random(seed)
    start = END - dt.timedelta(days=364)
    products = _pharmacy_products()
    by_cat: dict[str, list[dict]] = {}
    for p in products:
        by_cat.setdefault(p["category"], []).append(p)
    managers = [{"manager": f"AM{r + 1}", "visits_per_day": 5, "working_days": "mon,tue,wed,thu,fri"} for r in range(4)]
    branches, orders, visits, returns = [], [], [], []
    for b in range(40):
        bid = f"BR{b + 1:02d}"
        area = ("North", "South")[b % 2]
        kind = ("Hospital", "Mall", "Neighbourhood", "Neighbourhood")[b % 4]
        size = ("small", "medium", "large")[(b // 4) % 3]
        lat0, lon0 = BRANCH_AREAS[area]
        manager = f"AM{(b % 4) + 1}"
        branches.append({"branch_code": bid, "branch_name": f"{kind} branch {b + 1}", "area_manager": manager, "area": area,
                         "branch_type": kind, "branch_size": size,
                         "lat": round(lat0 + rng.gauss(0, 0.012), 5), "lng": round(lon0 + rng.gauss(0, 0.012), 5)})
        # Hospital branches stock more analgesics and diabetes care, malls
        # more skin care; a branch carries a product's brand and its generic
        # together.
        wanted = {"Hospital": ("Analgesics", "Diabetes care", "Cough and cold", "Vitamins"),
                  "Mall": ("Skin care", "Vitamins", "Baby", "Analgesics"),
                  "Neighbourhood": ("Analgesics", "Cough and cold", "Baby", "Vitamins")}[kind]
        core = [p for cat in wanted for pair in rng.sample(range(10), 3) for p in by_cat[cat][2 * pair : 2 * pair + 2]]
        scale = {"small": 1, "medium": 2, "large": 3}[size]
        cycle = (7, 10, 14)[b % 3]
        bought: dict[str, int] = {}
        ordered: set[str] = set()
        day = start + dt.timedelta(days=rng.randrange(cycle))
        while day <= END:
            rainy = day.month in (11, 12, 1, 2)
            for p in core:
                if p["discontinued_on"] and _day(day) >= p["discontinued_on"]:
                    continue
                if rng.random() < (0.85 if rainy and p["category"] == "Cough and cold" else 0.45):
                    qty = max(1, round(scale * (2.5 if rainy and p["category"] == "Cough and cold" else 1) * rng.uniform(2, 6)))
                    orders.append({"branch_code": bid, "product_code": p["product_code"], "order_date": _day(day), "qty": qty,
                                   "amount": round(qty * p["price"], 2)})
                    bought[p["product_code"]] = bought.get(p["product_code"], 0) + qty
            ordered.add(_day(day))
            day += dt.timedelta(days=max(1, round(cycle * rng.uniform(0.8, 1.3))))
        # One branch in five sent the slowest of its products back before it
        # expired: most of what it had bought.
        if b % 5 == 2 and bought:
            sku = min(sorted(bought), key=lambda s: bought[s])
            returns.append({"branch_code": bid, "product_code": sku, "return_date": "2026-08-05", "qty": round(0.7 * bought[sku]),
                            "reason": "near expiry"})
        visits += [{"branch_code": v["customer_id"], "visit_date": v["visit_date"], "area_manager": v["salesman_id"], "outcome": v["outcome"]}
                   for v in _visits(rng, bid, manager, start, 21, ordered)]
    return {"orders": orders, "branches": branches, "products": products, "visits": visits, "managers": managers, "returns": returns}


# --------------------------------------------------------------------------


GENERATORS = {"fmcg": fmcg, "industrial": industrial, "pharmacy": pharmacy}


def generate(case: str) -> dict[str, list[dict]]:
    """A case's tables, by dataset name."""
    return GENERATORS[case]()


def roles(case: str) -> dict[str, dict[str, str]]:
    """The training body's "data" for a case's tables, each dataset named as
    generate names its table."""
    if case == "fmcg":
        return {
            "transactions": {"dataset": "orders", "customer": "outlet_id", "product": "sku", "date": "order_date", "quantity": "qty", "amount": "amount"},
            "customers": {"dataset": "outlets", "id": "outlet_id", "salesman": "salesman_id", "territory": "city", "segment": "outlet_type",
                          "size": "outlet_size", "channel": "channel", "region": "city", "lat": "latitude", "lon": "longitude",
                          "blocked": "credit_hold", "name": "name"},
            "products": {"dataset": "products", "id": "sku", "category": "category", "subcategory": "subcategory", "brand": "brand",
                         "price": "price", "margin": "margin", "pack_size": "pack_size", "unit": "unit", "launch_date": "launch_date",
                         "end_date": "end_date", "stock": "stock", "substitute": "substitute", "active": "active", "name": "name"},
            "visits": {"dataset": "visits", "customer": "outlet_id", "date": "visit_date", "salesman": "salesman_id", "outcome": "outcome"},
            "salesmen": {"dataset": "salesmen", "id": "salesman_id", "visits_per_day": "visits_per_day", "working_days": "working_days"},
            "returns": {"dataset": "returns", "customer": "outlet_id", "product": "sku", "date": "return_date", "quantity": "qty"},
            "quotes": {"dataset": "quotes", "customer": "outlet_id", "product": "sku", "date": "quote_date", "outcome": "status", "reason": "lost_reason"},
        }
    if case == "industrial":
        return {
            "transactions": {"dataset": "invoices", "customer": "account_no", "product": "item_code", "date": "invoice_date", "quantity": "qty",
                             "amount": "net_amount"},
            # Sixty accounts split by industry, size, tier, channel and
            # region make groups too small to learn from: the industry and
            # the price tier are what decide what an account buys.
            "customers": {"dataset": "accounts", "id": "account_no", "salesman": "kam", "territory": "region", "segment": "industry",
                          "price_tier": "price_tier", "name": "account_name"},
            "products": {"dataset": "items", "id": "item_code", "category": "product_group", "subcategory": "product_line", "brand": "brand",
                         "price": "list_price", "pack_size": "pack_size", "unit": "unit", "launch_date": "launched_on", "name": "description"},
            "visits": {"dataset": "visits", "customer": "account_no", "date": "visit_date", "salesman": "kam", "outcome": "outcome"},
            "salesmen": {"dataset": "managers", "id": "kam", "visits_per_day": "visits_per_day", "working_days": "working_days"},
            "returns": {"dataset": "returns", "customer": "account_no", "product": "item_code", "date": "return_date", "quantity": "qty"},
            "quotes": {"dataset": "quotes", "customer": "account_no", "product": "item_code", "date": "quote_date", "outcome": "result",
                       "reason": "loss_reason"},
        }
    if case == "pharmacy":
        return {
            "transactions": {"dataset": "orders", "customer": "branch_code", "product": "product_code", "date": "order_date", "quantity": "qty",
                             "amount": "amount"},
            "customers": {"dataset": "branches", "id": "branch_code", "salesman": "area_manager", "territory": "area", "segment": "branch_type",
                          "size": "branch_size", "lat": "lat", "lon": "lng", "name": "branch_name"},
            "products": {"dataset": "products", "id": "product_code", "category": "category", "subcategory": "subcategory", "brand": "brand",
                         "price": "price", "pack_size": "pack_size", "unit": "unit", "end_date": "discontinued_on", "stock": "on_hand",
                         "substitute": "substitute", "name": "product_name"},
            "visits": {"dataset": "visits", "customer": "branch_code", "date": "visit_date", "salesman": "area_manager", "outcome": "outcome"},
            "salesmen": {"dataset": "managers", "id": "manager", "visits_per_day": "visits_per_day", "working_days": "working_days"},
            "returns": {"dataset": "returns", "customer": "branch_code", "product": "product_code", "date": "return_date", "quantity": "qty"},
        }
    raise KeyError(case)


def write_csv(out: Path, case: str, sample: int = 0) -> None:
    """A case's tables as CSV files under out/case; with sample, only the
    first that many rows of each."""
    folder = out / case
    folder.mkdir(parents=True, exist_ok=True)
    for name, rows in generate(case).items():
        with (folder / f"{name}.csv").open("w", newline="") as f:
            writer = csv.DictWriter(f, fieldnames=list(rows[0]), lineterminator="\n")
            writer.writeheader()
            writer.writerows(rows[:sample] if sample else rows)


if __name__ == "__main__":
    args = sys.argv[1:]
    sample = 0
    if args[:1] == ["--sample"] and len(args) >= 2:
        sample, args = int(args[1]), args[2:]
    if not args or any(a not in CASES for a in args[1:]):
        sys.exit(f"usage: cases.py [--sample N] OUT_DIR [{' '.join(CASES)}]")
    for case in args[1:] or CASES:
        write_csv(Path(args[0]), case, sample)
