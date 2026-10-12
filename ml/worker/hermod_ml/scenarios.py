"""Recommendation scenarios: several tables, each with a role, turned into a
recommender for one business question.

A scenario model reads up to five datasets, joined by the key columns the
request names (no cross-database SQL):

    transactions   customer*, product, date, quantity, amount   (always)
    customers      id*, salesman, territory, segment, lat, lon, blocked, name
    products       id*, category, price, margin, active, name
    visits         customer*, date*, salesman, outcome
    salesmen       id*, visits_per_day, working_days

and answers one of five questions (`scenario`):

    visits    which customers a salesman should visit in a period
    products  which products to offer a customer
    reorder   when each customer will order again
    audience  which customers are likely to want a product
    similar   which products are like a product

Everything here is plain arithmetic a person can follow. Per customer it
computes recency, frequency and value, the trend (last 90 days against the 90
before), the reorder cadence (median days between orders, the share of gaps
that came on time), contact (last visit, median days between visits) and
whitespace: how many products the customer's most similar customers buy that
it never has. A visit plan scores each customer as a weighted sum of five
signals, each between 0 and 1 (visit_signals); products re-rank the
collaborative-filtering candidates of recommend.py with whitespace and margin.
Hermod serves the model in Go (pkg/ml/recommend) with the same formulas, so
the functions here that score are its reference.

Evaluation splits on time only: the last period of history is held back
(7 days for a visit plan unless the request says otherwise), the features
are computed from what came before, and the scenario is measured on what
happened in the period, beside named baselines (most overdue first, biggest
first, most popular first). The saved model is then built from all of it.

The artifact is recommend.py's format 2.
"""

from __future__ import annotations

import datetime as dt
import math
from dataclasses import dataclass
from typing import Any

import numpy as np
import pandas as pd
import scipy.sparse as sp

from . import recommend
from .datasets import BOOL, NUMBER, is_null, to_text
from .errors import ApiError, bad_request
from .names import validate_name

TASK = recommend.TASK
SCENARIOS = ("visits", "products", "reorder", "audience", "similar")
# The scenarios that need transactions.product.
PRODUCT_SCENARIOS = ("products", "audience", "similar")

# The columns each role may name, besides "dataset"; the first ones are
# required whenever the role is given.
ROLE_KEYS: dict[str, tuple[str, ...]] = {
    "transactions": ("customer", "product", "date", "quantity", "amount"),
    "customers": ("id", "salesman", "territory", "segment", "lat", "lon", "blocked", "name"),
    "products": ("id", "category", "price", "margin", "active", "name"),
    "visits": ("customer", "date", "salesman", "outcome"),
    "salesmen": ("id", "visits_per_day", "working_days", "name"),
}
REQUIRED_KEYS: dict[str, tuple[str, ...]] = {
    "transactions": ("customer",),
    "customers": ("id",),
    "products": ("id",),
    "visits": ("customer", "date"),
    "salesmen": ("id",),
}
NUMBER_KEYS = {("transactions", "quantity"), ("transactions", "amount"), ("customers", "lat"), ("customers", "lon"),
               ("products", "price"), ("products", "margin"), ("salesmen", "visits_per_day")}
DATE_KEYS = {("transactions", "date"), ("visits", "date")}
ID_KEYS = {("transactions", "customer"), ("transactions", "product"), ("customers", "id"), ("products", "id"),
           ("visits", "customer"), ("salesmen", "id")}

DEFAULT_WEIGHTS = {"reorder_due": 0.35, "churn_risk": 0.2, "value": 0.2, "opportunity": 0.1, "neglect": 0.15}
REASON_CODES = ("reorder_due", "churn_risk", "high_value", "whitespace", "not_visited", "similar_customers",
                "bought_together", "popular", "boosted", "pinned")

# The features the artifact holds per customer and per item, in this order.
CUSTOMER_FEATURES = ("first_order", "last_order", "orders", "monetary", "value_pct", "median_gap", "on_time_share",
                     "trend", "last_visit", "visits", "visit_gap", "whitespace", "whitespace_pct", "margin", "lat", "lon")
ITEM_FEATURES = ("price", "margin", "margin_pct", "buyers", "popularity")
CUSTOMER_ATTRS = ("salesman", "territory", "segment", "blocked", "name")
ITEM_ATTRS = ("category", "active", "name")

WEEKDAYS = ("mon", "tue", "wed", "thu", "fri", "sat", "sun")
DEFAULT_WORKING_DAYS = WEEKDAYS[:5]
DEFAULT_VISITS_PER_DAY = 8
# A visit plan's period when the request names none.
VISIT_PERIOD_DAYS = 7
# The evaluation period of the product scenarios, and the bounds of reorder's.
PRODUCT_PERIOD_DAYS = 30
REORDER_PERIOD_BOUNDS = (14, 180)
# Days of the trend's two windows.
TREND_DAYS = 90
# A gap counts as on time when it is at most this many times the median.
ON_TIME_FACTOR = 1.5
# The gap assumed when no customer has two orders, and the visit rhythm when
# no customer has two visits.
FALLBACK_GAP = 30.0
CUSTOMER_NEIGHBORS = 20
# Customer-to-customer similarity costs customers squared; above this many,
# whitespace is left out rather than taking the training's whole budget.
MAX_NEIGHBOR_CUSTOMERS = 20_000
# A product counts as whitespace for a customer when at least this share of
# its similar customers buy it, and at least two of them.
WHITESPACE_SHARE = 0.3
# Products' collaborative-filtering candidates re-ranked per customer.
CANDIDATES = 50
# The re-ranking weights of the product scenarios.
PRODUCT_WEIGHTS = {"cf": 0.6, "whitespace": 0.3, "margin": 0.1}
AUDIENCE_WEIGHTS = {"cf": 0.6, "whitespace": 0.4}
MAX_RULES = 50
SAMPLES = 3
SAMPLE_ITEMS = 5

# Vendor guidance the health check measures against (research §1.2, §1.3).
MIN_CUSTOMERS_PRODUCTS = 1000
MIN_CUSTOMERS_TIMING = 500
MIN_TX_PER_CUSTOMER_PRODUCTS = 3
MIN_TX_PER_CUSTOMER = 2
MAX_PRODUCTS = 50_000
# History a scenario wants: at least twice its planning or prediction window.
WANTED_HISTORY_DAYS = {"visits": 2 * VISIT_PERIOD_DAYS, "reorder": 2 * 90, "products": 365, "audience": 365, "similar": 365}

_EPOCH = pd.Timestamp(0, tz="UTC")


# --------------------------------------------------------------------------
# Request
# --------------------------------------------------------------------------


@dataclass(frozen=True)
class ScenarioRequest:
    scenario: str
    roles: dict[str, dict[str, str]]
    weights: dict[str, float]
    rules: list[dict[str, Any]]
    algorithm: str
    test_size: float
    seed: int
    period_days: int | None


def is_scenario(body: dict[str, Any]) -> bool:
    """Whether a recommendation body uses roles rather than #268's
    `recommend` shape, which is scenario "products" on one table."""
    return body.get("task") == TASK and body.get("recommend") is None and ("scenario" in body or "data" in body)


def parse_request(body: dict[str, Any], test_size: float, seed: int) -> ScenarioRequest:
    """Validate a scenario body's shape. What its datasets hold is the health
    check's to judge; this refuses only a body that cannot be read."""
    scenario = body.get("scenario")
    if scenario not in SCENARIOS:
        raise bad_request(f"'scenario' must be one of {', '.join(SCENARIOS)}.")
    for key in ("recommend", "target", "features", "params", "preset"):
        if body.get(key) not in (None, "", []):
            raise bad_request(f"'{key}' does not apply to a recommendation scenario; name each table's columns in 'data'.")
    data = body.get("data")
    if not isinstance(data, dict) or not data:
        raise bad_request("'data' is required: the datasets of each role and their columns, e.g. {\"transactions\": {\"dataset\": ..., \"customer\": ...}}.")
    roles: dict[str, dict[str, str]] = {}
    for role, mapping in data.items():
        if role not in ROLE_KEYS:
            raise bad_request(f"'data' has unknown role {role!r}; the roles are {', '.join(ROLE_KEYS)}.")
        if not isinstance(mapping, dict):
            raise bad_request(f"'data.{role}' must be an object naming its dataset and columns.")
        unknown = set(mapping) - {"dataset", *ROLE_KEYS[role]}
        if unknown:
            raise bad_request(f"'data.{role}' has unknown keys {sorted(unknown)}; it takes dataset, {', '.join(ROLE_KEYS[role])}.")
        dataset = mapping.get("dataset")
        if not isinstance(dataset, str) or not dataset:
            raise bad_request(f"'data.{role}.dataset' is required and must be a dataset name.")
        validate_name(dataset, "dataset")
        for key in REQUIRED_KEYS[role]:
            if not isinstance(mapping.get(key), str) or not mapping.get(key):
                raise bad_request(f"'data.{role}.{key}' is required and must be a column name.")
        for key, column in mapping.items():
            if not isinstance(column, str) or not column:
                raise bad_request(f"'data.{role}.{key}' must be a column name.")
        roles[role] = dict(mapping)
    dataset = body.get("dataset")
    tx = roles.get("transactions", {}).get("dataset")
    if dataset not in (None, "") and dataset != tx:
        raise bad_request("'dataset' must be the transactions dataset when 'data' is given, or left out.")

    weights = body.get("weights")
    if weights is None:
        weights = dict(DEFAULT_WEIGHTS)
    else:
        if not isinstance(weights, dict):
            raise bad_request("'weights' must be an object of signal: weight.")
        unknown = set(weights) - set(DEFAULT_WEIGHTS)
        if unknown:
            raise bad_request(f"'weights' has unknown signals {sorted(unknown)}; they are {', '.join(DEFAULT_WEIGHTS)}.")
        for key, value in weights.items():
            if isinstance(value, bool) or not isinstance(value, (int, float)) or not 0 <= value <= 1:
                raise bad_request(f"The weight of {key!r} must be a number between 0 and 1.")
        weights = {**DEFAULT_WEIGHTS, **{k: float(v) for k, v in weights.items()}}
        if sum(weights.values()) <= 0:
            raise bad_request("At least one weight must be above 0.")

    rules = body.get("rules", [])
    if rules is None:
        rules = []
    if not isinstance(rules, list) or len(rules) > MAX_RULES or not all(isinstance(r, dict) and isinstance(r.get("kind"), str) for r in rules):
        raise bad_request(f"'rules' must be a list of at most {MAX_RULES} rule objects, each with a 'kind'.")

    period = body.get("period_days")
    if period is not None and (isinstance(period, bool) or not isinstance(period, int) or not 1 <= period <= 366):
        raise bad_request("'period_days' must be a whole number of days between 1 and 366.")

    algorithm = body.get("algorithm", "auto")
    if algorithm not in recommend.ALGORITHMS:
        raise bad_request(f"A recommendation model's 'algorithm' must be one of {', '.join(recommend.ALGORITHMS)}.")
    return ScenarioRequest(scenario, roles, weights, list(rules), algorithm, test_size, seed, period)


# --------------------------------------------------------------------------
# Tables
# --------------------------------------------------------------------------


@dataclass
class Tables:
    """The roles' rows, cleaned: ids as text, dates as day numbers (days since
    1970-01-01), numbers as floats. A role not given is None."""

    tx: pd.DataFrame  # customer, product (or None), day (float, NaN unknown), quantity, amount
    customers: pd.DataFrame | None = None  # id + the attributes named
    products: pd.DataFrame | None = None
    visits: pd.DataFrame | None = None  # customer, day, salesman, outcome
    salesmen: pd.DataFrame | None = None  # id, visits_per_day, working_days
    has_product: bool = False
    has_date: bool = False
    has_amount: bool = False


class _Checks:
    """The health check's findings, one entry per code, the worst status
    winning."""

    ORDER = ("missing_role", "bad_column_type", "few_customers", "few_transactions_per_customer", "short_history",
             "too_many_products", "duplicates", "future_dates", "negative_quantities", "unmatched_ids")
    RANK = {"green": 0, "amber": 1, "red": 2}

    def __init__(self) -> None:
        self.found: dict[str, dict[str, Any]] = {}

    def add(self, code: str, status: str, message: str, fix: str = "") -> None:
        have = self.found.get(code)
        if have is None or have["status"] == "green":
            self.found[code] = {"code": code, "status": status, "message": message, "fix": fix}
            return
        if status == "green":
            return
        have["message"] += " " + message
        if fix and fix not in have["fix"]:
            have["fix"] = (have["fix"] + " " + fix).strip()
        if self.RANK[status] > self.RANK[have["status"]]:
            have["status"] = status

    def ok(self, code: str, message: str) -> None:
        self.add(code, "green", message, "")

    def listed(self) -> list[dict[str, Any]]:
        return [self.found[c] for c in self.ORDER if c in self.found]

    def red(self) -> list[str]:
        return [c["message"] for c in self.listed() if c["status"] == "red"]


def _text_ids(series: pd.Series) -> pd.Series:
    return series.map(lambda v: None if is_null(v) or to_text(v).strip() == "" else to_text(v).strip())


def _days(series: pd.Series, kind: str) -> pd.Series | None:
    """Day numbers, NaN where a value is missing or unreadable; None when the
    column holds no dates at all."""
    if kind == BOOL:
        return None
    if kind == NUMBER:
        values = pd.to_numeric(series, errors="coerce").astype(np.float64)
        finite = values[np.isfinite(values)]
        if len(finite) == 0 or (finite.abs() < 1e8).any():
            return None
        # Epoch seconds, or milliseconds for values that large.
        seconds = np.where(values.abs() >= 1e11, values / 1000.0, values)
        return pd.Series(np.floor(seconds / 86400.0), index=series.index)
    parsed = pd.to_datetime(series.map(lambda v: None if is_null(v) else to_text(v)), errors="coerce", utc=True, format="mixed")
    if parsed.notna().sum() == 0:
        return None
    return pd.Series(np.floor((parsed - _EPOCH) / pd.Timedelta(1, "D")), index=series.index).astype(np.float64)


def _truthy(v: Any) -> bool | None:
    if is_null(v):
        return None
    if isinstance(v, bool):
        return v
    if isinstance(v, (int, float)):
        return v != 0
    text = str(v).strip().lower()
    if text in ("true", "yes", "y", "1", "t", "x", "blocked", "hold", "on"):
        return True
    if text in ("false", "no", "n", "0", "f", "", "off"):
        return False
    return None


def parse_working_days(v: Any) -> list[str] | None:
    """Working days from "mon,tue,wed", "Mon-Fri", or a number of days from
    Monday (5 is Monday to Friday); None when it cannot be read."""
    if is_null(v):
        return None
    if isinstance(v, (int, float)) and not isinstance(v, bool):
        n = int(v)
        return list(WEEKDAYS[:n]) if 1 <= n <= 7 else None
    text = str(v).strip().lower().replace(";", ",").replace(" ", ",")
    days: list[str] = []
    for part in filter(None, text.split(",")):
        if "-" in part:
            lo, _, hi = part.partition("-")
            a, b = lo[:3], hi[:3]
            if a not in WEEKDAYS or b not in WEEKDAYS:
                return None
            i, j = WEEKDAYS.index(a), WEEKDAYS.index(b)
            days += list(WEEKDAYS[i : j + 1]) if i <= j else []
        elif part[:3] in WEEKDAYS:
            days.append(part[:3])
        else:
            return None
    ordered = [d for d in WEEKDAYS if d in days]
    return ordered or None


def _today() -> int:
    return int((pd.Timestamp(dt.datetime.now(dt.timezone.utc)) - _EPOCH) / pd.Timedelta(1, "D"))


def day_text(day: float) -> str:
    return (dt.date(1970, 1, 1) + dt.timedelta(days=int(day))).isoformat()


def inspect(load, req: ScenarioRequest, today: int | None = None) -> tuple[dict[str, Any], Tables | None]:
    """The health check: what the roles' datasets hold, judged against what
    the scenario needs and what vendors advise. `load(dataset)` returns
    (frame, types) or raises ApiError. Returns (health, tables); tables is
    None when the transactions cannot be read at all."""
    today = _today() if today is None else today
    checks = _Checks()
    scenario = req.scenario
    frames: dict[str, tuple[pd.DataFrame, dict[str, str]] | None] = {}
    cache: dict[str, Any] = {}

    def required_role(role: str) -> bool:
        return role == "transactions" or (role == "customers" and scenario == "visits")

    def required_column(role: str, key: str) -> bool:
        if key in REQUIRED_KEYS[role]:
            return True
        return (role, key) in {("transactions", "date") if scenario in ("visits", "reorder") else ("", ""),
                               ("transactions", "product") if scenario in PRODUCT_SCENARIOS else ("", ""),
                               ("customers", "salesman") if scenario == "visits" else ("", "")}

    for role in ROLE_KEYS:
        mapping = req.roles.get(role)
        if mapping is None:
            frames[role] = None
            if required_role(role):
                need = "every scenario" if role == "transactions" else "a visit plan, which needs each customer's salesman"
                checks.add("missing_role", "red", f"There is no {role} table; {need} needs one.",
                           f"Point the {role} role at a dataset.")
            elif scenario == "visits" and role == "visits":
                checks.add("missing_role", "amber", "There is no visits table, so the plan cannot tell who has not been visited for a while.",
                           "Add your visit or call log as the visits role.")
            continue
        name = mapping["dataset"]
        try:
            if name not in cache:
                cache[name] = load(name)
            frame, types = cache[name]
        except ApiError as exc:
            if exc.status != 404:
                raise
            frames[role] = None
            status = "red" if required_role(role) else "amber"
            checks.add("missing_role", status, f"The {role} dataset {name!r} does not exist.", f"Upload or fill dataset {name!r}, or pick another.")
            continue
        usable = True
        for key, column in mapping.items():
            if key == "dataset":
                continue
            if column not in types:
                status = "red" if required_column(role, key) else "amber"
                checks.add("missing_role", status, f"The {role} dataset {name!r} has no column {column!r} (the {key}).",
                           f"Pick the column that holds the {key}.")
                if key in REQUIRED_KEYS[role]:
                    usable = False
        frames[role] = (frame, types) if usable else None

    if scenario == "visits" and "customers" in req.roles and "salesman" not in req.roles["customers"]:
        checks.add("missing_role", "red", "The customers table names no salesman column; a visit plan is made per salesman.",
                   "Map the customers role's salesman column.")
    if scenario in PRODUCT_SCENARIOS and req.roles.get("transactions") and "product" not in req.roles["transactions"]:
        checks.add("missing_role", "red", f"The transactions table names no product column; scenario {scenario!r} recommends products.",
                   "Map the transactions role's product column.")
    if scenario in ("visits", "reorder") and req.roles.get("transactions") and "date" not in req.roles["transactions"]:
        checks.add("missing_role", "red", f"The transactions table names no date column; scenario {scenario!r} works from when customers order.",
                   "Map the transactions role's date column.")
    if "missing_role" not in checks.found:
        checks.ok("missing_role", "Every table the scenario needs is there.")

    tables = _clean(frames, req, checks, today)
    stats = {"customers": 0, "products": 0, "transactions": 0, "months_of_history": 0.0, "tx_per_customer": 0.0}
    if tables is not None:
        stats = _measure(tables, req, checks)
    if "bad_column_type" not in checks.found:
        checks.ok("bad_column_type", "Every column holds what its role needs.")
    listed = checks.listed()
    health = {"ok": not any(c["status"] == "red" for c in listed), "checks": listed, "stats": stats}
    return health, tables


def _column(frame: pd.DataFrame, mapping: dict[str, str], key: str) -> pd.Series | None:
    column = mapping.get(key)
    return None if column is None or column not in frame.columns else frame[column]


def _number(frames, req, checks, role: str, key: str) -> pd.Series | None:
    loaded = frames.get(role)
    if loaded is None:
        return None
    frame, types = loaded
    column = req.roles[role].get(key)
    if column is None or column not in types:
        return None
    if types[column] != NUMBER:
        checks.add("bad_column_type", "red", f"The {role} column {column!r} (the {key}) must hold numbers; it holds {types[column]}.",
                   f"Pick a numeric column for the {key}, or clean the values.")
        return None
    return pd.to_numeric(frame[column], errors="coerce").astype(np.float64)


def _date(frames, req, checks, role: str, key: str) -> pd.Series | None:
    loaded = frames.get(role)
    if loaded is None:
        return None
    frame, types = loaded
    column = req.roles[role].get(key)
    if column is None or column not in types:
        return None
    days = _days(frame[column], types[column])
    if days is None:
        checks.add("bad_column_type", "red", f"The {role} column {column!r} (the {key}) holds no dates Hermod can read.",
                   "Use ISO 8601 dates such as 2026-03-31, or epoch seconds.")
    return days


def _clean(frames, req: ScenarioRequest, checks: _Checks, today: int) -> Tables | None:
    loaded = frames.get("transactions")
    if loaded is None:
        return None
    frame, _ = loaded
    m = req.roles["transactions"]
    tx = pd.DataFrame({"customer": _text_ids(frame[m["customer"]])}, index=frame.index)
    product = _column(frame, m, "product")
    tx["product"] = _text_ids(product) if product is not None else None
    day = _date(frames, req, checks, "transactions", "date")
    tx["day"] = day if day is not None else np.nan
    qty = _number(frames, req, checks, "transactions", "quantity")
    amount = _number(frames, req, checks, "transactions", "amount")
    tx["quantity"] = qty if qty is not None else 1.0
    tx["amount"] = amount if amount is not None else np.nan

    mapped = [c for k, c in m.items() if k != "dataset" and c in frame.columns]
    dupes = int(frame.duplicated(subset=mapped).sum()) if mapped else 0
    if dupes:
        checks.add("duplicates", "amber", f"{dupes} transaction rows are exact copies of another row.",
                   "If they are not real repeat lines, remove them at the source; they count as more buying than happened.")
    else:
        checks.ok("duplicates", "No transaction row is an exact copy of another.")

    keep = tx["customer"].notna()
    if req.scenario in PRODUCT_SCENARIOS:
        keep &= tx["product"].notna()
    has_date = day is not None
    if has_date:
        future = tx["day"] > today + 1
        if int(future.sum()):
            checks.add("future_dates", "amber", f"{int(future.sum())} transactions are dated after today; they are left out.",
                       "Check the date column's time zone and format, or remove planned orders.")
        else:
            checks.ok("future_dates", "No transaction is dated in the future.")
        keep &= tx["day"].notna() & ~future
    negative = (tx["quantity"] < 0) | (tx["amount"] < 0)
    if int(negative.sum()):
        checks.add("negative_quantities", "amber",
                   f"{int(negative.sum())} transactions have a negative quantity or amount (returns or credit notes); they are left out.",
                   "Filter returns out at the source, or flag them, so they do not cancel real orders.")
    else:
        checks.ok("negative_quantities", "No transaction has a negative quantity or amount.")
    keep &= ~negative & tx["quantity"].notna()
    tx = tx[keep].reset_index(drop=True)

    tables = Tables(tx=tx, has_product=product is not None, has_date=has_date, has_amount=amount is not None)
    tables.customers = _customers(frames, req, checks)
    tables.products = _products(frames, req, checks)
    tables.visits = _visits(frames, req, checks, today)
    tables.salesmen = _salesmen(frames, req, checks)
    return tables


def _customers(frames, req, checks) -> pd.DataFrame | None:
    loaded = frames.get("customers")
    if loaded is None:
        return None
    frame, _ = loaded
    m = req.roles["customers"]
    out = pd.DataFrame({"id": _text_ids(frame[m["id"]])}, index=frame.index)
    for key in ("salesman", "territory", "segment", "name"):
        col = _column(frame, m, key)
        out[key] = _text_ids(col) if col is not None else None
    col = _column(frame, m, "blocked")
    out["blocked"] = col.map(_truthy) if col is not None else None
    for key in ("lat", "lon"):
        values = _number(frames, req, checks, "customers", key)
        out[key] = values if values is not None else np.nan
    out = out[out["id"].notna()].drop_duplicates("id", keep="last")
    return out.reset_index(drop=True)


def _products(frames, req, checks) -> pd.DataFrame | None:
    loaded = frames.get("products")
    if loaded is None:
        return None
    frame, _ = loaded
    m = req.roles["products"]
    out = pd.DataFrame({"id": _text_ids(frame[m["id"]])}, index=frame.index)
    for key in ("category", "name"):
        col = _column(frame, m, key)
        out[key] = _text_ids(col) if col is not None else None
    col = _column(frame, m, "active")
    out["active"] = col.map(_truthy) if col is not None else None
    for key in ("price", "margin"):
        values = _number(frames, req, checks, "products", key)
        out[key] = values if values is not None else np.nan
    out = out[out["id"].notna()].drop_duplicates("id", keep="last")
    return out.reset_index(drop=True)


def _visits(frames, req, checks, today: int) -> pd.DataFrame | None:
    loaded = frames.get("visits")
    if loaded is None:
        return None
    frame, _ = loaded
    m = req.roles["visits"]
    day = _date(frames, req, checks, "visits", "date")
    if day is None:
        return None
    out = pd.DataFrame({"customer": _text_ids(frame[m["customer"]]), "day": day}, index=frame.index)
    for key in ("salesman", "outcome"):
        col = _column(frame, m, key)
        out[key] = _text_ids(col) if col is not None else None
    out = out[out["customer"].notna() & out["day"].notna() & (out["day"] <= today + 1)]
    return out.reset_index(drop=True)


def _salesmen(frames, req, checks) -> pd.DataFrame | None:
    loaded = frames.get("salesmen")
    if loaded is None:
        return None
    frame, _ = loaded
    m = req.roles["salesmen"]
    out = pd.DataFrame({"id": _text_ids(frame[m["id"]])}, index=frame.index)
    cap = _number(frames, req, checks, "salesmen", "visits_per_day")
    out["visits_per_day"] = cap if cap is not None else np.nan
    col = _column(frame, m, "working_days")
    out["working_days"] = col.map(parse_working_days) if col is not None else None
    if col is not None:
        unread = int((col.map(lambda v: not is_null(v)) & out["working_days"].isna()).sum())
        if unread:
            checks.add("bad_column_type", "amber", f"{unread} salesmen's working days cannot be read; they work Monday to Friday.",
                       'Write working days as "mon,tue,wed" or "mon-fri", or as a number of days from Monday.')
    out = out[out["id"].notna()].drop_duplicates("id", keep="last")
    return out.reset_index(drop=True)


def _measure(tables: Tables, req: ScenarioRequest, checks: _Checks) -> dict[str, Any]:
    tx = tables.tx
    scenario = req.scenario
    n_customers = int(tx["customer"].nunique())
    n_products = int(tx["product"].nunique()) if tables.has_product else 0
    if tables.has_date:
        orders = int(tx[["customer", "day"]].drop_duplicates().shape[0])
        history = float(tx["day"].max() - tx["day"].min() + 1) if len(tx) else 0.0
    else:
        orders, history = len(tx), 0.0
    per_customer = orders / n_customers if n_customers else 0.0
    months = round(history / 30.44, 1)

    product_like = scenario in PRODUCT_SCENARIOS
    want = MIN_CUSTOMERS_PRODUCTS if product_like else MIN_CUSTOMERS_TIMING
    if n_customers < 2:
        checks.add("few_customers", "red", f"The transactions hold {n_customers} customers; a recommender needs at least 2.",
                   "Check the customer column and that the dataset is filled.")
    elif n_customers < want:
        what = "product recommendations" if product_like else "churn and reorder timing"
        checks.add("few_customers", "amber", f"You have {n_customers:,} customers; vendors advise at least {want:,} for {what}.",
                   "Results will be rougher; add more customers' history if you have it.")
    else:
        checks.ok("few_customers", f"You have {n_customers:,} customers.")

    want_tx = MIN_TX_PER_CUSTOMER_PRODUCTS if product_like else MIN_TX_PER_CUSTOMER
    if len(tx) < recommend.MIN_INTERACTIONS:
        checks.add("few_transactions_per_customer", "red",
                   f"Only {len(tx)} usable transactions; training needs at least {recommend.MIN_INTERACTIONS}.",
                   "Fill the transactions dataset with more history.")
    elif per_customer < want_tx:
        checks.add("few_transactions_per_customer", "amber",
                   f"Customers have {per_customer:.1f} orders each on average; about {want_tx} or more makes good recommendations.",
                   "Use a longer history, or invoice lines rather than invoice totals.")
    else:
        checks.ok("few_transactions_per_customer", f"Customers have {per_customer:.1f} orders each on average.")

    wanted = WANTED_HISTORY_DAYS[scenario]
    if not tables.has_date:
        status = "red" if scenario in ("visits", "reorder") else "amber"
        checks.add("short_history", status, "The transactions have no usable dates, so history cannot be measured or split in time.",
                   "Map the transactions role's date column.")
    elif tx["day"].nunique() < 2:
        checks.add("short_history", "red", "Every transaction is on the same day; there is no history to learn timing from.",
                   "Load more than one day of transactions.")
    elif history < wanted:
        checks.add("short_history", "amber",
                   f"You have {months} months of history; this scenario wants at least {round(wanted / 30.44, 1)} ({wanted} days).",
                   "Load a longer history if you have it.")
    else:
        checks.ok("short_history", f"You have {months} months of history.")

    if n_products >= MAX_PRODUCTS:
        checks.add("too_many_products", "amber", f"There are {n_products:,} products; above {MAX_PRODUCTS:,} recommendations get thin.",
                   "Recommend at a higher level of the product hierarchy (a product group), or leave out inactive products.")
    else:
        checks.ok("too_many_products", f"There are {n_products:,} products.")

    _unmatched(tables, checks)
    return {"customers": n_customers, "products": n_products, "transactions": int(len(tx)),
            "months_of_history": months, "tx_per_customer": round(per_customer, 2)}


def _unmatched(tables: Tables, checks: _Checks) -> None:
    found = False

    def report(count: int, total: int, what: str, role: str) -> None:
        nonlocal found
        if count:
            found = True
            checks.add("unmatched_ids", "amber", f"{count} of {total} {what} are not in the {role} table.",
                       "Use the same ids in every table (watch for leading zeros, spaces and upper/lower case).")

    tx = tables.tx
    if tables.customers is not None:
        known = set(tables.customers["id"])
        ids = set(tx["customer"])
        report(len(ids - known), len(ids), "customers in the transactions", "customers")
        if tables.visits is not None:
            ids = set(tables.visits["customer"])
            report(len(ids - known), len(ids), "customers in the visits", "customers")
        if tables.salesmen is not None and tables.customers["salesman"].notna().any():
            reps = set(tables.customers["salesman"].dropna())
            report(len(reps - set(tables.salesmen["id"])), len(reps), "salesmen in the customers table", "salesmen")
    if tables.products is not None and tables.has_product:
        ids = set(tx["product"].dropna())
        report(len(ids - set(tables.products["id"])), len(ids), "products in the transactions", "products")
    if not found:
        checks.ok("unmatched_ids", "Every id matches across the tables.")


# --------------------------------------------------------------------------
# Features
# --------------------------------------------------------------------------


def cadence(days: np.ndarray, amounts: np.ndarray, today: int) -> dict[str, float]:
    """One customer's order rhythm and value from its order days and what
    each order was worth, measured on `today` (the first day not seen)."""
    order_days, inverse = np.unique(days, return_inverse=True)
    worth = np.bincount(inverse, weights=amounts)
    gaps = np.diff(order_days).astype(np.float64)
    median_gap = float(np.median(gaps)) if len(gaps) else math.nan
    on_time = float(np.mean(gaps <= ON_TIME_FACTOR * median_gap)) if len(gaps) >= 2 else math.nan
    recent = float(worth[order_days >= today - TREND_DAYS].sum())
    before = float(worth[(order_days >= today - 2 * TREND_DAYS) & (order_days < today - TREND_DAYS)].sum())
    if before > 0:
        trend = min(3.0, (recent - before) / before)
    else:
        trend = math.nan if recent == 0 else 3.0
    return {
        "first_order": float(order_days[0]),
        "last_order": float(order_days[-1]),
        "orders": float(len(order_days)),
        "monetary": float(worth.sum()),
        "median_gap": median_gap,
        "on_time_share": on_time,
        "trend": trend,
    }


def _percentile(values: np.ndarray) -> np.ndarray:
    """Each finite value's share of finite values at or below it; NaN stays."""
    out = np.full(len(values), np.nan)
    finite = np.isfinite(values)
    if finite.any():
        ranks = pd.Series(values[finite]).rank(method="max", pct=True).to_numpy()
        out[finite] = ranks
    return out


@dataclass
class Built:
    """Everything one point in time knows: the features of every customer and
    item, and the model fitted on the interactions before it."""

    today: int
    users: list[str]
    items: list[str]
    user_index: dict[str, int]
    item_index: dict[str, int]
    customer_features: np.ndarray  # float64 [users, CUSTOMER_FEATURES]
    item_features: np.ndarray  # float64 [items, ITEM_FEATURES]
    model: recommend.Model
    global_gap: float
    global_visit_gap: float
    has_visits: bool
    has_opportunity: bool
    has_margin: bool

    def feature(self, u: int) -> dict[str, float]:
        return dict(zip(CUSTOMER_FEATURES, self.customer_features[u].tolist()))


def _interactions(tx: pd.DataFrame, users: list[str], items: list[str], user_index, item_index) -> recommend.Interactions:
    pairs = tx.groupby(["customer", "product"], sort=False, as_index=False).agg(strength=("quantity", "sum"), time=("day", "max"))
    pairs = pairs[pairs["strength"] > 0]
    return recommend.Interactions(
        users=users, items=items,
        user=pairs["customer"].map(user_index).to_numpy(dtype=np.int64),
        item=pairs["product"].map(item_index).to_numpy(dtype=np.int64),
        strength=pairs["strength"].to_numpy(dtype=np.float64),
        time=None,
    )


def build(tables: Tables, users: list[str], items: list[str], today: int, algorithm: str, seed: int) -> Built:
    """Features and model from the transactions and visits before `today`."""
    user_index = {u: i for i, u in enumerate(users)}
    item_index = {p: i for i, p in enumerate(items)}
    tx = tables.tx
    if tables.has_date:
        tx = tx[tx["day"] < today]
    feats = np.full((len(users), len(CUSTOMER_FEATURES)), np.nan)
    col = {name: i for i, name in enumerate(CUSTOMER_FEATURES)}
    worth = tx["amount"] if tables.has_amount else tx["quantity"]
    if tables.has_date and len(tx):
        frame = pd.DataFrame({"u": tx["customer"].map(user_index), "day": tx["day"], "worth": worth.fillna(0.0)})
        for u, group in frame.groupby("u", sort=False):
            for name, value in cadence(group["day"].to_numpy(np.int64), group["worth"].to_numpy(np.float64), today).items():
                feats[int(u), col[name]] = value
    elif len(tx):
        counts = tx.groupby("customer")["quantity"].size()
        money = pd.Series(worth.fillna(0.0).to_numpy(), index=tx["customer"]).groupby(level=0).sum()
        for c, n in counts.items():
            feats[user_index[c], col["orders"]] = float(n)
            feats[user_index[c], col["monetary"]] = float(money[c])
    monetary = feats[:, col["monetary"]]
    feats[:, col["value_pct"]] = _percentile(monetary)

    has_visits = tables.visits is not None and len(tables.visits) > 0
    if has_visits:
        visits = tables.visits[(tables.visits["day"] < today) & tables.visits["customer"].isin(user_index)]
        for c, group in visits.groupby("customer", sort=False):
            vd = np.unique(group["day"].to_numpy(np.int64))
            u = user_index[c]
            feats[u, col["last_visit"]] = float(vd[-1])
            feats[u, col["visits"]] = float(len(vd))
            if len(vd) >= 2:
                feats[u, col["visit_gap"]] = float(np.median(np.diff(vd)))
    if tables.customers is not None:
        located = tables.customers[tables.customers["id"].isin(user_index)]
        rows = located["id"].map(user_index).to_numpy(dtype=np.int64)
        feats[rows, col["lat"]] = located["lat"].to_numpy(np.float64)
        feats[rows, col["lon"]] = located["lon"].to_numpy(np.float64)

    item_feats = np.full((len(items), len(ITEM_FEATURES)), np.nan)
    icol = {name: i for i, name in enumerate(ITEM_FEATURES)}
    has_margin = False
    if tables.products is not None and len(items):
        known = tables.products[tables.products["id"].isin(item_index)]
        rows = known["id"].map(item_index).to_numpy(dtype=np.int64)
        item_feats[rows, icol["price"]] = known["price"].to_numpy(np.float64)
        item_feats[rows, icol["margin"]] = known["margin"].to_numpy(np.float64)
        item_feats[:, icol["margin_pct"]] = _percentile(item_feats[:, icol["margin"]])
        has_margin = bool(np.isfinite(item_feats[:, icol["margin"]]).any())

    if items and len(tx):
        data = _interactions(tx, users, items, user_index, item_index)
    else:
        data = recommend.Interactions(users, items, np.empty(0, np.int64), np.empty(0, np.int64), np.empty(0), None)
    matrix = data.matrix()
    if items:
        buyers = np.asarray((matrix > 0).sum(axis=0)).ravel().astype(np.float64)
        item_feats[:, icol["buyers"]] = buyers
        item_feats[:, icol["popularity"]] = buyers / buyers.max() if buyers.max() > 0 else 0.0
        if has_margin and tables.has_amount:
            ratio = item_feats[:, icol["margin"]]
            share = np.all(ratio[np.isfinite(ratio)] <= 1)
            per_line = tx["product"].map(item_index).to_numpy(dtype=np.int64)
            m = ratio[per_line]
            earned = np.where(np.isfinite(m), (tx["amount"].to_numpy() * m) if share else (tx["quantity"].to_numpy() * m), 0.0)
            margin = pd.Series(earned).groupby(tx["customer"].map(user_index).to_numpy()).sum()
            feats[margin.index.to_numpy(dtype=np.int64), col["margin"]] = margin.to_numpy()

    if items and len(data.user):
        model = recommend.fit(algorithm, data, None, seed)
    else:
        popular, scores = np.empty(0, np.uint32), np.empty(0, np.float32)
        model = recommend.Model("none", users, items, recommend._seen(matrix), popular, scores)

    has_opportunity = False
    if items and len(users) <= MAX_NEIGHBOR_CUSTOMERS and len(data.user):
        nbrs, scores = recommend.fit_neighbors(matrix.T.tocsr(), CUSTOMER_NEIGHBORS)
        model.customer_neighbors, model.customer_neighbor_scores = nbrs, scores
        has_opportunity = True
        seen = model.seen
        ws = np.zeros(len(users))
        for u in range(len(users)):
            nb = nbrs[u][nbrs[u] != recommend.EMPTY].astype(np.int64)
            if len(nb) == 0:
                ws[u] = np.nan
                continue
            counts = np.asarray(seen[nb].sum(axis=0)).ravel()
            want = max(2.0, WHITESPACE_SHARE * len(nb))
            mine = seen.indices[seen.indptr[u] : seen.indptr[u + 1]]
            counts[mine] = 0
            ws[u] = float((counts >= want).sum())
        feats[:, col["whitespace"]] = ws
        feats[:, col["whitespace_pct"]] = _percentile(ws)
    else:
        model.customer_neighbors = np.empty((len(users), 0), np.uint32)
        model.customer_neighbor_scores = np.empty((len(users), 0), np.float32)

    gaps = feats[:, col["median_gap"]]
    vgaps = feats[:, col["visit_gap"]]
    global_gap = float(np.nanmedian(gaps)) if np.isfinite(gaps).any() else FALLBACK_GAP
    global_visit_gap = float(np.nanmedian(vgaps)) if np.isfinite(vgaps).any() else FALLBACK_GAP
    # Stored in float32 and scored from there, as Hermod reads them.
    return Built(today, users, items, user_index, item_index, feats.astype(np.float32).astype(np.float64),
                 item_feats.astype(np.float32).astype(np.float64), model, global_gap, global_visit_gap,
                 has_visits, has_opportunity, has_margin)


# --------------------------------------------------------------------------
# Scoring: the formulas Hermod serves with
# --------------------------------------------------------------------------


def _clip01(x: float) -> float:
    return 0.0 if not math.isfinite(x) else min(1.0, max(0.0, x))


def normalised_weights(weights: dict[str, float] | None, has_visits: bool, has_opportunity: bool) -> dict[str, float]:
    """The visit weights that apply, summing to 1. Neglect needs visits and
    opportunity needs whitespace; a signal the data cannot give is dropped."""
    w = dict(DEFAULT_WEIGHTS) if weights is None else {k: float(weights.get(k, 0.0)) for k in DEFAULT_WEIGHTS}
    if not has_visits:
        w.pop("neglect")
    if not has_opportunity:
        w.pop("opportunity")
    total = sum(w.values())
    if total <= 0:
        return {k: 1.0 / len(w) for k in w}
    return {k: v / total for k, v in w.items()}


def _gap(feat: dict[str, float], global_gap: float) -> float:
    gap = feat.get("median_gap", math.nan)
    return gap if math.isfinite(gap) and gap > 0 else global_gap


def overdue(feat: dict[str, float], today: int, global_gap: float) -> float:
    """Days since the last order over the usual gap; NaN for no order."""
    last = feat.get("last_order", math.nan)
    if not math.isfinite(last):
        return math.nan
    return (today - last) / _gap(feat, global_gap)


def visit_signals(feat: dict[str, float], today: int, global_gap: float, global_visit_gap: float, has_visits: bool,
                  period: int = VISIT_PERIOD_DAYS) -> dict[str, float]:
    """The five visit signals of a plan for the `period` days from `today`,
    each in [0, 1]. With r the days since the last order over the usual gap,
    today (r_now) and at the period's end (r_end):

    reorder_due  how due the customer is by the period's end: 0 while
                 r_end <= 0.5, 1 from r_end = 1; it fades out between
                 r_now = 1.5 and 3, where an order is no longer due but lost
    churn_risk   0 until r_now = 1.5, 1 from r_now = 3
    value        the customer's revenue percentile
    opportunity  the customer's whitespace percentile
    neglect      days since the last visit over the customer's visit rhythm:
                 0 until half of it, 1 from twice it, and 1 for a customer
                 never visited
    """
    ratio = overdue(feat, today, global_gap)
    by_end = overdue(feat, today + period, global_gap)
    churn = _clip01((ratio - 1.5) / 1.5)
    out = {
        "reorder_due": _clip01((by_end - 0.5) / 0.5) * (1.0 - churn),
        "churn_risk": churn,
        "value": _clip01(feat.get("value_pct", math.nan)),
        "opportunity": _clip01(feat.get("whitespace_pct", math.nan)),
        "neglect": 0.0,
    }
    if has_visits:
        last = feat.get("last_visit", math.nan)
        if not math.isfinite(last):
            out["neglect"] = 1.0
        else:
            target = feat.get("visit_gap", math.nan)
            target = target if math.isfinite(target) and target > 0 else global_visit_gap
            out["neglect"] = _clip01(((today - last) / target - 0.5) / 1.5)
    return out


def visit_score(signals: dict[str, float], weights: dict[str, float]) -> float:
    return float(sum(w * signals[k] for k, w in weights.items()))


def _top_share(pct: float) -> int:
    return max(1, int(round((1 - pct) * 100)))


def visit_reasons(feat: dict[str, float], signals: dict[str, float], today: int, global_gap: float, global_visit_gap: float,
                  weights: dict[str, float] | None = None) -> list[dict[str, str]]:
    """Why a customer is on the list, strongest first, in words with numbers."""
    w = weights or DEFAULT_WEIGHTS
    out: list[tuple[float, int, dict[str, str]]] = []
    since = today - feat.get("last_order", math.nan)
    own = feat.get("median_gap", math.nan)
    gap = _gap(feat, global_gap)
    if signals["reorder_due"] > 0:
        text = (f"Usually orders every {round(gap)} days; last order {round(since)} days ago" if math.isfinite(own) and own > 0
                else f"Customers usually order every {round(gap)} days; last order {round(since)} days ago")
        out.append((w.get("reorder_due", 0) * signals["reorder_due"], 0, {"code": "reorder_due", "text": text}))
    if signals["churn_risk"] > 0:
        out.append((w.get("churn_risk", 0) * signals["churn_risk"], 1, {
            "code": "churn_risk",
            "text": f"No order for {round(since)} days, {since / gap:.1f} times the usual {round(gap)}-day gap"}))
    pct = feat.get("value_pct", math.nan)
    if math.isfinite(pct) and pct >= 0.75:
        out.append((w.get("value", 0) * signals["value"], 2, {"code": "high_value", "text": f"Top {_top_share(pct)}% of customers by revenue"}))
    ws = feat.get("whitespace", math.nan)
    if math.isfinite(ws) and ws >= 1 and signals["opportunity"] >= 0.5:
        out.append((w.get("opportunity", 0) * signals["opportunity"], 3, {
            "code": "whitespace", "text": f"Similar customers buy {int(ws)} products this customer has never bought"}))
    if signals["neglect"] > 0:
        last = feat.get("last_visit", math.nan)
        if math.isfinite(last):
            target = feat.get("visit_gap", math.nan)
            target = target if math.isfinite(target) and target > 0 else global_visit_gap
            text = f"Not visited for {round(today - last)} days (target every {round(target)})"
        else:
            text = "No visit on record"
        out.append((w.get("neglect", 0) * signals["neglect"], 4, {"code": "not_visited", "text": text}))
    out.sort(key=lambda r: (-r[0], r[1]))
    return [r[2] for r in out]


def _neighbors_of(model: recommend.Model, u: int) -> np.ndarray:
    if model.customer_neighbors is None or model.customer_neighbors.shape[1] == 0:
        return np.empty(0, np.int64)
    nb = model.customer_neighbors[u]
    return nb[nb != recommend.EMPTY].astype(np.int64)


def whitespace_share(model: recommend.Model, u: int, item: int) -> tuple[float, int, int]:
    """(share, count, of): how many of a customer's similar customers have
    the item."""
    nb = _neighbors_of(model, u)
    if len(nb) == 0:
        return 0.0, 0, 0
    seen = model.seen
    have = sum(1 for n in nb.tolist() if item in seen.indices[seen.indptr[n] : seen.indptr[n + 1]])
    return have / len(nb), have, len(nb)


def product_scores(b: Built, u: int, k: int, exclude_seen: bool = True) -> list[tuple[int, float, list[dict[str, str]]]]:
    """A customer's top k products: CANDIDATES collaborative-filtering
    candidates (topped up with popular ones), re-ranked by
    PRODUCT_WEIGHTS of the candidate's score (as a share of the best),
    whitespace share and margin percentile."""
    model = b.model
    picks = recommend.recommend_user(model, u, CANDIDATES, exclude_seen)
    best = max([s for _, s, r in picks if r is None and s > 0], default=0.0)
    weights = dict(PRODUCT_WEIGHTS)
    if not b.has_opportunity:
        weights.pop("whitespace")
    if not b.has_margin:
        weights.pop("margin")
    total = sum(weights.values())
    margin_col = ITEM_FEATURES.index("margin_pct")
    scored = []
    for i, s, reason in picks:
        cf = s / best if reason is None and best > 0 and s > 0 else 0.0
        share, have, of = whitespace_share(model, u, i)
        mp = b.item_features[i, margin_col]
        mp = mp if math.isfinite(mp) else 0.0
        score = (weights["cf"] * cf + weights.get("whitespace", 0) * share + weights.get("margin", 0) * mp) / total
        reasons = []
        if have and share >= 0.2:
            reasons.append({"code": "similar_customers", "text": f"Bought by {have} of {of} similar customers"})
        if reason == "popular":
            reasons.append({"code": "popular", "text": "One of the most popular products"})
        elif not reasons:
            reasons.append({"code": "similar_customers", "text": "Customers who buy what this customer buys also buy it"})
        scored.append((i, float(np.float32(score)), reasons))
    # Stable: a tie keeps the candidates' order, so popular fill stays most popular first.
    scored.sort(key=lambda t: -t[1])
    return scored[:k]


def reorder_due(b: Built, u: int) -> tuple[float, float] | None:
    """(due day, overdue ratio) of a customer with at least one order."""
    feat = b.feature(u)
    if not math.isfinite(feat["last_order"]):
        return None
    gap = _gap(feat, b.global_gap)
    return feat["last_order"] + gap, (b.today - feat["last_order"]) / gap


def audience_scores(b: Built, item: int, k: int, exclude_seen: bool = True) -> list[tuple[int, float, list[dict[str, str]]]]:
    """The k customers most likely to want an item: AUDIENCE_WEIGHTS of the
    customer's collaborative-filtering score for it (as a share of the best)
    and the share of its similar customers who have it."""
    model = b.model
    n = len(b.users)
    cf = np.zeros(n)
    if model.user_factors is not None:
        cf = model.user_factors.astype(np.float64) @ model.item_factors[item].astype(np.float64)
    elif model.neighbor_items is not None:
        # Each item j that lists `item` as a neighbour adds its similarity to
        # every customer who has j.
        sims = np.zeros(len(b.items))
        rows, slots = np.nonzero(model.neighbor_items == item)
        sims[rows] = model.neighbor_scores[rows, slots]
        cf = np.asarray(model.seen @ sims).ravel()
    best = cf.max() if n and cf.max() > 0 else 0.0
    weights = dict(AUDIENCE_WEIGHTS)
    if not b.has_opportunity:
        weights.pop("whitespace")
    total = sum(weights.values())
    out = []
    seen = model.seen
    for u in range(n):
        has = item in seen.indices[seen.indptr[u] : seen.indptr[u + 1]]
        if exclude_seen and has:
            continue
        share, have, of = whitespace_share(model, u, item)
        c = cf[u] / best if best > 0 and cf[u] > 0 else 0.0
        score = (weights["cf"] * c + weights.get("whitespace", 0) * share) / total
        if score <= 0:
            continue
        reasons = []
        if have and share >= 0.2:
            reasons.append({"code": "similar_customers", "text": f"{have} of {of} similar customers buy it"})
        if not reasons:
            reasons.append({"code": "bought_together", "text": "Buys products often bought with it"})
        out.append((u, float(np.float32(score)), reasons))
    out.sort(key=lambda t: (-t[1], t[0]))
    return out[:k]


# --------------------------------------------------------------------------
# Evaluation
# --------------------------------------------------------------------------


def _visit_metrics(planned: list[list[int]], ordered: set[int], revenue: dict[int, float], pools: list[list[int]]) -> dict[str, float]:
    k = sum(len(p) for p in planned)
    hits = sum(1 for plan in planned for u in plan if u in ordered)
    got = sum(revenue.get(u, 0.0) for plan in planned for u in plan)
    total = sum(revenue.get(u, 0.0) for pool in pools for u in pool)
    return {"ordered_in_period@k": hits / k if k else 0.0, "revenue_share@k": got / total if total > 0 else 0.0}


def _working(days: list[str] | None, start: int, period: int) -> int:
    allowed = set(days or DEFAULT_WORKING_DAYS)
    # 1970-01-01 was a Thursday.
    return sum(1 for d in range(start, start + period) if WEEKDAYS[(d + 3) % 7] in allowed)


def _salesman_capacity(tables: Tables) -> dict[str, tuple[float, list[str] | None]]:
    out: dict[str, tuple[float, list[str] | None]] = {}
    if tables.salesmen is not None:
        for row in tables.salesmen.itertuples(index=False):
            cap = row.visits_per_day if math.isfinite(row.visits_per_day) and row.visits_per_day > 0 else DEFAULT_VISITS_PER_DAY
            out[row.id] = (float(cap), row.working_days if isinstance(row.working_days, list) else None)
    return out


def _salesman_pools(tables: Tables, b: Built) -> dict[str, list[int]]:
    pools: dict[str, list[int]] = {}
    for row in tables.customers.itertuples(index=False):
        if row.salesman is None or row.blocked is True or row.id not in b.user_index:
            continue
        pools.setdefault(row.salesman, []).append(b.user_index[row.id])
    return pools


def evaluate_visits(tables: Tables, req: ScenarioRequest, users: list[str], items: list[str], as_of: int, algorithm: str) -> dict[str, Any]:
    period = req.period_days or VISIT_PERIOD_DAYS
    cutoff = as_of - period + 1
    b = build(tables, users, items, cutoff, algorithm, req.seed)
    weights = normalised_weights(req.weights, b.has_visits, b.has_opportunity)
    window = tables.tx[(tables.tx["day"] >= cutoff) & (tables.tx["day"] <= as_of)]
    worth = window["amount"] if tables.has_amount else window["quantity"]
    revenue = pd.Series(worth.fillna(0.0).to_numpy(), index=window["customer"].map(b.user_index).to_numpy()).groupby(level=0).sum().to_dict()
    ordered = set(revenue)
    caps = _salesman_capacity(tables)
    rankers = {
        "model": lambda u: visit_score(visit_signals(b.feature(u), cutoff, b.global_gap, b.global_visit_gap, b.has_visits, period), weights),
        "most_overdue": lambda u: overdue(b.feature(u), cutoff, b.global_gap),
        "biggest": lambda u: b.feature(u)["monetary"],
        "popular": lambda u: b.feature(u)["orders"],
    }
    plans: dict[str, list[list[int]]] = {name: [] for name in rankers}
    pools = _salesman_pools(tables, b)
    for salesman in sorted(pools):
        pool = pools[salesman]
        per_day, days = caps.get(salesman, (DEFAULT_VISITS_PER_DAY, None))
        k = min(len(pool), int(per_day * _working(days, cutoff, period)))
        for name, rank in rankers.items():
            keyed = [(-(v if math.isfinite(v := float(rank(u))) else -math.inf), u) for u in pool]
            plans[name].append([u for _, u in sorted(keyed)[:k]])
    pool_lists = [pools[s] for s in sorted(pools)]
    named = [{"name": n, "metrics": _visit_metrics(plans[n], ordered, revenue, pool_lists)} for n in ("most_overdue", "biggest", "popular")]
    return {
        "metrics": _visit_metrics(plans["model"], ordered, revenue, pool_lists),
        "baseline": named[0]["metrics"],
        "baselines_named": named,
        "evaluation": {"period_days": period, "cutoff": day_text(cutoff), "until": day_text(as_of),
                       "planned": sum(len(p) for p in plans["model"]), "salesmen": len(pools)},
    }


def evaluate_products(tables: Tables, req: ScenarioRequest, users: list[str], items: list[str], as_of: int) -> tuple[dict[str, Any], str, dict[str, float]]:
    """Products, audience and similar are measured by the products a customer
    goes on to buy for the first time after the cut-off. Without dates (or
    without such purchases) each customer's latest picks are held back, as
    for a model trained with the `recommend` shape."""
    user_index = {u: i for i, u in enumerate(users)}
    item_index = {p: i for i, p in enumerate(items)}
    data = _interactions(tables.tx, users, items, user_index, item_index)
    rows = np.arange(len(data.user), dtype=np.int64)
    pseudo = recommend.RecommendRequest("", "customer", "product", None, None, req.algorithm, req.test_size, req.seed)
    period = req.period_days or PRODUCT_PERIOD_DAYS
    cutoff = as_of - period + 1
    truths: dict[int, set[int]] = {}
    if tables.has_date:
        before = tables.tx[tables.tx["day"] < cutoff]
        after = tables.tx[tables.tx["day"] >= cutoff]
        had = set(zip(before["customer"], before["product"]))
        known = set(before["customer"])
        for c, p in zip(after["customer"], after["product"]):
            if c in known and (c, p) not in had:
                truths.setdefault(user_index[c], set()).add(item_index[p])
    if truths:
        before_keys = np.asarray([user_index[c] * len(items) + item_index[p] for c, p in had], dtype=np.int64)
        train_rows = rows[np.isin(data.user * len(items) + data.item, before_keys)]
        algorithm, candidates = (req.algorithm, {}) if req.algorithm != "auto" else recommend._choose(data, train_rows, pseudo)
        b = build(tables, users, items, cutoff, algorithm, req.seed)
        if len(truths) > recommend.MAX_EVAL_USERS:
            rng = np.random.default_rng(req.seed)
            chosen = rng.choice(sorted(truths), size=recommend.MAX_EVAL_USERS, replace=False)
            truths = {u: truths[u] for u in sorted(chosen.tolist())}
        evaluation = {"period_days": period, "cutoff": day_text(cutoff), "until": day_text(as_of), "customers": len(truths), "split": "time"}
    else:
        train_rows, test_rows = recommend.holdout(data, rows, req.test_size, req.seed)
        if len(test_rows) == 0:
            raise bad_request("Nobody has two or more products, so there is nothing to hold back and test on.")
        algorithm, candidates = (req.algorithm, {}) if req.algorithm != "auto" else recommend._choose(data, train_rows, pseudo)
        truths = recommend._truths(data, test_rows, req.seed)
        held = recommend.fit(algorithm, data, train_rows, req.seed)
        b = _built_from(held, tables, users, items, as_of + 1)
        evaluation = {"split": "holdout", "customers": len(truths)}
    users_eval = sorted(truths)
    lists = [[i for i, _, _ in product_scores(b, u, recommend.K)] for u in users_eval]
    baseline = []
    for u in users_eval:
        seen = set(b.model.seen.indices[b.model.seen.indptr[u] : b.model.seen.indptr[u + 1]].tolist())
        baseline.append([i for i in b.model.popular.tolist() if i not in seen][: recommend.K])
    truth_list = [truths[u] for u in users_eval]
    metrics = recommend._metrics(lists, truth_list, len(items))
    base = recommend._metrics(baseline, truth_list, len(items))
    result = {"metrics": metrics, "baseline": base, "baselines_named": [{"name": "popular", "metrics": base}], "evaluation": evaluation}
    return result, algorithm, candidates


def _built_from(model: recommend.Model, tables: Tables, users, items, today: int) -> Built:
    b = build(tables, users, items, today, "item_similarity", 0)
    nb, sc = b.model.customer_neighbors, b.model.customer_neighbor_scores
    model.customer_neighbors, model.customer_neighbor_scores = nb, sc
    b.model = model
    return b


def evaluate_reorder(tables: Tables, req: ScenarioRequest, users: list[str], as_of: int) -> dict[str, Any]:
    probe = build(tables, users, [], as_of + 1, "none", req.seed)
    period = req.period_days or int(min(REORDER_PERIOD_BOUNDS[1], max(REORDER_PERIOD_BOUNDS[0], 2 * probe.global_gap)))
    cutoff = as_of - period + 1
    b = build(tables, users, [], cutoff, "none", req.seed)
    after = tables.tx[tables.tx["day"] >= cutoff]
    first = after.groupby("customer")["day"].min()
    errors, base_errors, on_time, base_on_time = [], [], [], []
    for c, day in first.items():
        u = b.user_index[c]
        feat = b.feature(u)
        if not math.isfinite(feat["last_order"]):
            continue
        gap = _gap(feat, b.global_gap)
        tolerance = max(7.0, 0.25 * gap)
        err = abs(day - (feat["last_order"] + gap))
        base = abs(day - (feat["last_order"] + b.global_gap))
        errors.append(err)
        base_errors.append(base)
        on_time.append(err <= tolerance)
        base_on_time.append(base <= max(7.0, 0.25 * b.global_gap))
    metrics = {"mae_days": float(np.mean(errors)) if errors else 0.0, "on_time_share": float(np.mean(on_time)) if on_time else 0.0}
    base = {"mae_days": float(np.mean(base_errors)) if base_errors else 0.0, "on_time_share": float(np.mean(base_on_time)) if base_on_time else 0.0}
    return {"metrics": metrics, "baseline": base, "baselines_named": [{"name": "popular", "metrics": base}],
            "evaluation": {"period_days": period, "cutoff": day_text(cutoff), "until": day_text(as_of), "customers": len(errors)}}


# --------------------------------------------------------------------------
# Samples
# --------------------------------------------------------------------------


def _customer_fields(b: Built, attrs: dict[str, list[Any]], u: int) -> dict[str, Any]:
    out: dict[str, Any] = {}
    for key in ("name", "territory", "segment", "salesman"):
        value = attrs.get(key, [None] * len(b.users))[u]
        if value is not None:
            out[key] = value
    feat = b.feature(u)
    if math.isfinite(feat["lat"]) and math.isfinite(feat["lon"]):
        out["lat"], out["lon"] = feat["lat"], feat["lon"]
    return out


def _item_fields(attrs: dict[str, list[Any]], i: int) -> dict[str, Any]:
    out: dict[str, Any] = {}
    for key in ("name", "category"):
        value = attrs.get(key, [])[i] if attrs.get(key) else None
        if value is not None:
            out[key] = value
    return out


def _item(id_: str, score: float, rank: int, reasons, fields, due: str | None = None) -> dict[str, Any]:
    out = {"id": id_, "score": score, "rank": rank, "reasons": reasons, "fields": fields}
    if due is not None:
        out["due_date"] = due
    return out


def samples(req: ScenarioRequest, b: Built, cattrs, iattrs) -> list[dict[str, Any]]:
    """Up to SAMPLES example results, as Hermod serves them."""
    out: list[dict[str, Any]] = []
    salesman = cattrs.get("salesman")
    blocked = cattrs.get("blocked", [None] * len(b.users))
    if req.scenario in ("visits", "reorder") and salesman:
        reps = sorted({s for s in salesman if s is not None})[:SAMPLES]
        weights = normalised_weights(req.weights, b.has_visits, b.has_opportunity)
        for rep in reps:
            pool = [u for u in range(len(b.users)) if salesman[u] == rep and blocked[u] != "true"]
            ranked = []
            for u in pool:
                feat = b.feature(u)
                if req.scenario == "visits":
                    signals = visit_signals(feat, b.today, b.global_gap, b.global_visit_gap, b.has_visits)
                    ranked.append((float(np.float32(visit_score(signals, weights))), u,
                                   visit_reasons(feat, signals, b.today, b.global_gap, b.global_visit_gap, weights), None))
                else:
                    due = reorder_due(b, u)
                    if due is not None:
                        ranked.append((float(np.float32(due[1])), u, _reorder_reasons(b, u), day_text(math.ceil(due[0]))))
            ranked.sort(key=lambda t: (-t[0], t[1]))
            items = [_item(b.users[u], s, r + 1, reasons, _customer_fields(b, cattrs, u), due)
                     for r, (s, u, reasons, due) in enumerate(ranked[:SAMPLE_ITEMS])]
            if items:
                out.append({"for": rep, "items": items})
    elif req.scenario == "reorder":
        for u in range(min(SAMPLES, len(b.users))):
            due = reorder_due(b, u)
            if due is not None:
                out.append({"for": b.users[u], "items": [_item(b.users[u], float(np.float32(due[1])), 1, _reorder_reasons(b, u),
                                                               _customer_fields(b, cattrs, u), day_text(math.ceil(due[0])))]})
    elif req.scenario == "products":
        busiest = np.argsort(-np.diff(b.model.seen.indptr), kind="stable")[:SAMPLES]
        for u in sorted(busiest.tolist()):
            picks = product_scores(b, u, SAMPLE_ITEMS)
            out.append({"for": b.users[u], "items": [_item(b.items[i], s, r + 1, reasons, _item_fields(iattrs, i)) for r, (i, s, reasons) in enumerate(picks)]})
    elif req.scenario == "audience":
        for i in b.model.popular[:SAMPLES].tolist():
            picks = audience_scores(b, i, SAMPLE_ITEMS)
            out.append({"for": b.items[i], "items": [_item(b.users[u], s, r + 1, reasons, _customer_fields(b, cattrs, u)) for r, (u, s, reasons) in enumerate(picks)]})
    elif req.scenario == "similar":
        for i in b.model.popular[:SAMPLES].tolist():
            picks = recommend.similar_items(b.model, i, SAMPLE_ITEMS)
            items = []
            for r, (j, s, reason) in enumerate(picks):
                reasons = ([{"code": "popular", "text": "One of the most popular products"}] if reason == "popular"
                           else [{"code": "bought_together", "text": f"Often bought by the same customers as {b.items[i]}"}])
                items.append(_item(b.items[j], s, r + 1, reasons, _item_fields(iattrs, j)))
            out.append({"for": b.items[i], "items": items})
    return out


def _reorder_reasons(b: Built, u: int) -> list[dict[str, str]]:
    feat = b.feature(u)
    signals = visit_signals(feat, b.today, b.global_gap, b.global_visit_gap, False)
    reasons = [r for r in visit_reasons(feat, signals, b.today, b.global_gap, b.global_visit_gap) if r["code"] in ("reorder_due", "churn_risk")]
    if not reasons:
        gap = _gap(feat, b.global_gap)
        reasons = [{"code": "reorder_due", "text": f"Usually orders every {round(gap)} days; last order {round(b.today - feat['last_order'])} days ago"}]
    return reasons


# --------------------------------------------------------------------------
# Training
# --------------------------------------------------------------------------


def _attrs(tables: Tables, users: list[str], items: list[str]) -> tuple[dict[str, list[Any]], dict[str, list[Any]]]:
    def column(frame: pd.DataFrame | None, ids: list[str], key: str) -> list[Any] | None:
        if frame is None or key not in frame.columns or frame[key].isna().all():
            return None
        lookup = dict(zip(frame["id"], frame[key]))
        out = []
        for i in ids:
            v = lookup.get(i)
            if isinstance(v, bool):
                out.append("true" if v else "false")
            elif v is None or is_null(v):
                out.append(None)
            else:
                out.append(str(v))
        return out

    cattrs = {k: v for k in CUSTOMER_ATTRS if (v := column(tables.customers, users, k)) is not None}
    iattrs = {k: v for k in ITEM_ATTRS if (v := column(tables.products, items, k)) is not None}
    return cattrs, iattrs


def _salesmen_header(tables: Tables) -> dict[str, dict[str, Any]]:
    out: dict[str, dict[str, Any]] = {}
    if tables.salesmen is None:
        return out
    for row in tables.salesmen.itertuples(index=False):
        cap = row.visits_per_day
        out[row.id] = {
            "visits_per_day": float(cap) if math.isfinite(cap) and cap > 0 else None,
            "working_days": row.working_days if isinstance(row.working_days, list) else None,
        }
    return out


def _pair_cadence(tables: Tables, model: recommend.Model, user_index, item_index) -> tuple[np.ndarray, np.ndarray]:
    """Each seen pair's latest day and median gap, in the seen matrix's order."""
    nnz = model.seen.nnz
    last = np.full(nnz, recommend.EMPTY, dtype=np.uint32)
    gaps = np.full(nnz, np.nan, dtype=np.float32)
    if nnz == 0 or not tables.has_date:
        return last, gaps
    tx = tables.tx[["customer", "product", "day"]].dropna()
    tx = tx.assign(u=tx["customer"].map(user_index), i=tx["product"].map(item_index)).drop_duplicates(["u", "i", "day"])
    tx = tx.sort_values(["u", "i", "day"])
    seen = model.seen
    position = {}
    rows = np.repeat(np.arange(seen.shape[0]), np.diff(seen.indptr))
    for k, (u, i) in enumerate(zip(rows.tolist(), seen.indices.tolist())):
        position[(u, i)] = k
    for (u, i), group in tx.groupby(["u", "i"], sort=False):
        k = position.get((int(u), int(i)))
        if k is None:
            continue
        days = group["day"].to_numpy(np.int64)
        last[k] = int(days[-1])
        if len(days) >= 2:
            gaps[k] = float(np.median(np.diff(days)))
    return last, gaps


def train(load, req: ScenarioRequest, name: str) -> tuple[bytes, dict[str, Any]]:
    """Check, evaluate, build and encode a scenario. Returns (artifact, meta)."""
    health, tables = inspect(load, req)
    if not health["ok"] or tables is None:
        reds = [c["message"] for c in health["checks"] if c["status"] == "red"]
        raise bad_request("The data cannot train this scenario: " + " ".join(reds))
    tx = tables.tx
    customer_ids = set(tx["customer"])
    if tables.customers is not None:
        customer_ids |= set(tables.customers["id"])
    users = sorted(customer_ids)
    items = sorted(set(tx["product"].dropna())) if tables.has_product and req.scenario != "reorder" else []
    if req.scenario == "visits" and tables.has_product:
        items = sorted(set(tx["product"].dropna()))
    as_of = int(tx["day"].max()) if tables.has_date else _today() - 1

    algorithm, candidates = "none", {}
    if req.scenario == "visits":
        algorithm = "item_similarity" if items else "none"
        result = evaluate_visits(tables, req, users, items, as_of, algorithm)
    elif req.scenario == "reorder":
        result = evaluate_reorder(tables, req, users, as_of)
    else:
        result, algorithm, candidates = evaluate_products(tables, req, users, items, as_of)

    final = build(tables, users, items, as_of + 1, algorithm, req.seed)
    cattrs, iattrs = _attrs(tables, users, items)
    model = final.model
    model.algorithm = algorithm if items else "none"
    model.seen_last_day, model.seen_gap = _pair_cadence(tables, model, final.user_index, final.item_index)
    model.customer_features = final.customer_features.astype(np.float32)
    model.item_features = final.item_features.astype(np.float32)
    weights = normalised_weights(req.weights, final.has_visits, final.has_opportunity) if req.scenario == "visits" else {}
    model.extra = {
        "scenario": req.scenario,
        "as_of": day_text(as_of),
        "period_days": req.period_days or (VISIT_PERIOD_DAYS if req.scenario == "visits" else result["evaluation"].get("period_days", PRODUCT_PERIOD_DAYS)),
        "customer_features": list(CUSTOMER_FEATURES),
        "item_features": list(ITEM_FEATURES),
        "customer_attrs": cattrs,
        "item_attrs": iattrs,
        "salesmen": _salesmen_header(tables),
        "weights": dict(req.weights),
        "rules": req.rules,
        "global_gap": final.global_gap,
        "global_visit_gap": final.global_visit_gap,
        "has_visits": final.has_visits,
        "has_margin": final.has_margin,
    }
    artifact = recommend.encode(model)
    if len(artifact) > recommend.MAX_ARTIFACT_BYTES:
        raise bad_request(
            f"The trained recommender is {len(artifact) >> 20} MB, over the {recommend.MAX_ARTIFACT_BYTES >> 20} MB limit: "
            f"it has {len(users)} customers and {len(items)} products. Train on fewer customers or products."
        )

    metrics = dict(result["metrics"])
    score_key = {"visits": "ordered_in_period@k", "reorder": "on_time_share"}.get(req.scenario, f"ndcg@{recommend.K}")
    metrics["score"] = metrics[score_key]
    meta: dict[str, Any] = {
        "model": name,
        "task": TASK,
        "scenario": req.scenario,
        "algorithm": model.algorithm,
        "dataset": req.roles["transactions"]["dataset"],
        "target": "",
        "features": [],
        "feature_types": {},
        "roles": req.roles,
        "weights": dict(req.weights),
        "applied_weights": weights,
        "rules": req.rules,
        "metrics": {k: float(v) for k, v in metrics.items() if math.isfinite(v)},
        "baseline": {k: float(v) for k, v in result["baseline"].items() if math.isfinite(v)},
        "baselines_named": result["baselines_named"],
        "evaluation": result["evaluation"],
        "health": health,
        "rows": {"train": int(len(tx)), "test": 0},
        "catalog": {"users": len(users), "items": len(items)},
        "samples": samples(req, final, cattrs, iattrs),
    }
    if candidates:
        meta["candidates"] = {k: float(v) for k, v in candidates.items()}
    return artifact, meta
