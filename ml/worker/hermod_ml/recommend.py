"""Recommendations: learn who picked what, and say what each person picks next.

A recommendation model reads interactions, one row per person and item, with
an optional strength (a rating, quantity or count) and an optional time. It
answers two questions: which items a person should see next, and which items
are like a given one.

Two algorithms, both plain numpy/scipy:

- `als`: implicit-feedback alternating least squares (Hu, Koren and Volinsky,
  2008). Every interaction is a "liked" with a confidence of
  1 + ALS_ALPHA * log1p(strength); people and items get factor vectors whose
  dot product scores a pair.
- `item_similarity`: item-item cosine similarity over who picked what. Each
  item keeps its NEIGHBORS most similar items; a person's score for an item is
  the sum of its similarities to the items they already have.

`auto` trains both on a validation split cut from the training rows and keeps
the one with the better NDCG@10. The held-out test rows are never used to
choose.

The split holds back each person's latest interactions when there is a time
column, or a random share of them when there is not. People with a single
interaction stay in training. Every metric is also measured for "recommend
the most popular items", the baseline a model has to beat. The saved model is
fitted again on every interaction, so it knows each person's latest picks.

The artifact is not ONNX. It is a small binary file Hermod parses and serves
in-process (pkg/ml/recommend), little-endian throughout:

    "HREC"                   4 bytes, magic
    format version           uint32, FORMAT_VERSION
    header length H          uint32
    header                   H bytes of UTF-8 JSON: algorithm, users, items,
                             factors, neighbors, seen, popular
    user factors             float32 [users x factors]      when factors > 0
    item factors             float32 [items x factors]      when factors > 0
    neighbor items           uint32  [items x neighbors]    when neighbors > 0
                             (EMPTY for an unused slot)
    neighbor scores          float32 [items x neighbors]    when neighbors > 0
    seen offsets             uint32  [users + 1]
    seen items               uint32  [seen], ascending per user
    popular items            uint32  [popular], most popular first
    popular scores           float32 [popular]

Nothing follows the last section. Exactly one of factors and neighbors is
non-zero. Nothing is pickled.

Format 2 (scenarios.py) keeps every format 1 section, in the same order, and
adds what serving a scenario needs. Its header also holds
customer_neighbors, and everything in `Model.extra`: the scenario, as_of,
the feature and attribute names and values, the salesmen, weights and rules.
Its algorithm may be "none" (factors and neighbors both 0, items possibly
empty) for a scenario that needs no products. After popular scores come:

    customer neighbors       uint32  [users x customer_neighbors]  when > 0
                             (EMPTY for an unused slot)
    customer neighbor scores float32 [users x customer_neighbors]  when > 0
    seen last day            uint32  [seen], days since 1970-01-01 of the
                             latest interaction of each seen pair, or EMPTY
    seen gap                 float32 [seen], median days between a pair's
                             orders, NaN when it has fewer than two
    customer features        float32 [users x customer_features], NaN unknown
    item features            float32 [items x item_features], NaN unknown
    seen quantity            float32 [seen], a pair's median quantity per
                             order, NaN unknown          when pair_details
    seen spend               float32 [seen], what the person spent on the
                             pair                        when pair_details
    signal users             uint32  [signals]           when signals
    signal items             uint32  [signals]           when signals
    signal days              uint32  [signals], days since 1970-01-01
                                                         when signals
    signal kinds             uint32  [signals], 0 returned, 1 lost quote,
                             2 open quote                when signals

Seen quantity and spend are a model's with product details (a brand, pack
size or launch date), whose header says "pair_details": true; the signals,
what returns and quotes say of each person and item (scenarios._signals),
ascending, are a model's trained with either, whose header says how many in
"signals". An artifact without them is as it was before them.

Hermod reads both formats; the worker writes format 1 for a model trained with
the `recommend` shape and format 2 for a scenario.
"""

from __future__ import annotations

import json
import math
import struct
from dataclasses import dataclass
from typing import Any

import numpy as np
import pandas as pd
import scipy.sparse as sp

from .datasets import BOOL, NUMBER, is_null, to_text
from .errors import bad_request
from .names import validate_name

TASK = "recommendation"
ALGORITHMS = ("auto", "als", "item_similarity")
# The file a recommendation version keeps in place of model.onnx.
ARTIFACT = "model.rec"

MAGIC = b"HREC"
FORMAT_VERSION = 1
# The format a scenario's artifact is written in (see the module docstring).
FORMAT_VERSION_SCENARIO = 2
EMPTY = 0xFFFFFFFF
# The largest artifact saved; Hermod refuses a larger one too. A scenario's
# artifact carries per-customer features and neighbours besides the model.
MAX_ARTIFACT_BYTES = 128 << 20
# The header keys of format 1; anything else in a format 2 header is extra.
_V1_KEYS = ("algorithm", "users", "items", "factors", "neighbors", "seen", "popular")

K = 10
MIN_INTERACTIONS = 10
ALS_FACTORS = 32
ALS_REGULARIZATION = 0.1
ALS_ALPHA = 20.0
ALS_ITERATIONS = 10
NEIGHBORS = 50
POPULAR = 100
# Evaluation scores at most this many people, sampled with the seed.
MAX_EVAL_USERS = 5000
SAMPLES = 3
# Items per chunk of the item-item similarity product, bounded so a chunk
# holds at most this many similarities.
SIMILARITY_CHUNK_CELLS = 1 << 24

_U32 = np.dtype("<u4")
_F32 = np.dtype("<f4")


# --------------------------------------------------------------------------
# Request
# --------------------------------------------------------------------------


@dataclass(frozen=True)
class RecommendRequest:
    dataset: str
    user: str
    item: str
    strength: str | None
    time: str | None
    algorithm: str
    test_size: float
    seed: int


def parse_request(body: dict[str, Any], test_size: float, seed: int) -> RecommendRequest:
    """Validate a training body whose task is "recommendation".

    test_size and seed were checked by the caller, as for every task. What
    does not apply to a recommender is refused rather than ignored.
    """
    dataset = body.get("dataset")
    if not isinstance(dataset, str):
        raise bad_request("'dataset' is required and must be a string.")
    validate_name(dataset, "dataset")

    if body.get("target") not in (None, ""):
        raise bad_request("'target' does not apply to a recommendation model; name the columns in 'recommend'.")
    if body.get("features") not in (None, []):
        raise bad_request("'features' does not apply to a recommendation model; it learns from 'recommend' columns only.")
    if body.get("params") is not None:
        raise bad_request("'params' does not apply to a recommendation model.")
    if body.get("preset") not in (None, ""):
        raise bad_request("'preset' does not apply to a recommendation model; auto always tries als and item_similarity.")

    spec = body.get("recommend")
    if not isinstance(spec, dict):
        raise bad_request("'recommend' is required for task recommendation: {\"user\": column, \"item\": column}.")
    unknown = set(spec) - {"user", "item", "strength", "time"}
    if unknown:
        raise bad_request(f"'recommend' has unknown keys {sorted(unknown)}; it takes user, item, strength and time.")
    columns: dict[str, str | None] = {}
    for key in ("user", "item", "strength", "time"):
        value = spec.get(key)
        required = key in ("user", "item")
        if value is None and not required:
            columns[key] = None
            continue
        if not isinstance(value, str) or not value:
            what = "is required and must be" if required else "must be"
            raise bad_request(f"'recommend.{key}' {what} a column name.")
        columns[key] = value
    named = [c for c in columns.values() if c is not None]
    if len(set(named)) != len(named):
        raise bad_request("'recommend' names a column more than once; user, item, strength and time are different columns.")

    algorithm = body.get("algorithm", "auto")
    if algorithm not in ALGORITHMS:
        raise bad_request(f"A recommendation model's 'algorithm' must be one of {', '.join(ALGORITHMS)}.")
    return RecommendRequest(
        dataset, columns["user"], columns["item"], columns["strength"], columns["time"], algorithm, test_size, seed
    )


# --------------------------------------------------------------------------
# Interactions
# --------------------------------------------------------------------------


@dataclass
class Interactions:
    """The interactions as indices: one entry per distinct (user, item)."""

    users: list[str]
    items: list[str]
    user: np.ndarray  # int64 index into users
    item: np.ndarray  # int64 index into items
    strength: np.ndarray  # float64, > 0
    time: np.ndarray | None  # float64 ordering key, or None

    def matrix(self, rows: np.ndarray | None = None) -> sp.csr_matrix:
        """users x items, summing strengths; `rows` selects interactions."""
        sel = slice(None) if rows is None else rows
        return sp.csr_matrix(
            (self.strength[sel], (self.user[sel], self.item[sel])),
            shape=(len(self.users), len(self.items)),
        )


def _time_key(series: pd.Series, column: str, kind: str) -> np.ndarray:
    """An ordering key per row: the number itself, or the parsed date."""
    if kind == NUMBER:
        return series.to_numpy(dtype=np.float64)
    if kind == BOOL:
        raise bad_request(f"Time column {column!r} holds true/false; it must hold dates or numbers.")
    parsed = pd.to_datetime(series.map(lambda v: None if is_null(v) else to_text(v)), errors="coerce", utc=True, format="mixed")
    if parsed.isna().all():
        raise bad_request(f"Time column {column!r} holds no dates Hermod can read; use ISO 8601 dates or numbers.")
    # Seconds since the epoch, NaN where a value did not parse.
    seconds = (parsed - pd.Timestamp(0, tz="UTC")) / pd.Timedelta(1, "s")
    return seconds.to_numpy(dtype=np.float64, na_value=np.nan)


def interactions(frame: pd.DataFrame, types: dict[str, str], req: RecommendRequest) -> Interactions:
    """Read the request's columns into Interactions, refusing data that cannot train.

    Rows with no user or item are dropped, as are rows whose strength is
    missing or not above zero and rows whose time is missing. The same person
    and item seen several times is one interaction: strengths add up and the
    latest time counts.
    """
    for key in ("user", "item", "strength", "time"):
        column = getattr(req, key)
        if column is not None and column not in types:
            raise bad_request(f"The {key} column {column!r} is not in dataset {req.dataset!r}.")
    if req.strength is not None and types[req.strength] != NUMBER:
        raise bad_request(f"Strength column {req.strength!r} must hold numbers (a rating, quantity or count); it is {types[req.strength]}.")

    users = frame[req.user].map(lambda v: None if is_null(v) else to_text(v))
    items = frame[req.item].map(lambda v: None if is_null(v) else to_text(v))
    keep = users.notna() & items.notna() & (users != "") & (items != "")
    if req.strength is not None:
        strength = pd.Series([math.nan if is_null(v) else float(v) for v in frame[req.strength]], index=frame.index)
        keep &= strength.notna() & (strength > 0) & np.isfinite(strength)
    else:
        strength = pd.Series(1.0, index=frame.index)
    if req.time is not None:
        time = pd.Series(_time_key(frame[req.time], req.time, types[req.time]), index=frame.index)
        keep &= time.notna()
    else:
        time = None

    data = pd.DataFrame({"user": users[keep], "item": items[keep], "strength": strength[keep].astype(np.float64)})
    if time is not None:
        data["time"] = time[keep]
    if len(data) < MIN_INTERACTIONS:
        raise bad_request(f"Training needs at least {MIN_INTERACTIONS} usable interactions; found {len(data)}.")

    agg: dict[str, Any] = {"strength": "sum"}
    if time is not None:
        agg["time"] = "max"
    data = data.groupby(["user", "item"], sort=False, as_index=False).agg(agg)

    user_codes, user_ids = pd.factorize(data["user"], sort=True)
    item_codes, item_ids = pd.factorize(data["item"], sort=True)
    if len(user_ids) < 2 or len(item_ids) < 2:
        raise bad_request("Training needs at least two people and two items.")
    return Interactions(
        users=[str(u) for u in user_ids],
        items=[str(i) for i in item_ids],
        user=user_codes.astype(np.int64),
        item=item_codes.astype(np.int64),
        strength=data["strength"].to_numpy(dtype=np.float64),
        time=None if time is None else data["time"].to_numpy(dtype=np.float64),
    )


def holdout(data: Interactions, rows: np.ndarray, fraction: float, seed: int) -> tuple[np.ndarray, np.ndarray]:
    """Split interaction indices `rows` into (train, test).

    Each person with at least two interactions holds back ceil(fraction * n)
    of them, at most n - 1: their latest when there is a time, otherwise a
    random choice. Everyone else's interactions all train.
    """
    rng = np.random.default_rng(seed)
    order = rows[np.argsort(data.user[rows], kind="stable")]
    users = data.user[order]
    starts = np.flatnonzero(np.r_[True, users[1:] != users[:-1]])
    ends = np.r_[starts[1:], len(order)]
    test: list[np.ndarray] = []
    for start, end in zip(starts, ends):
        n = end - start
        if n < 2:
            continue
        hold = min(n - 1, max(1, math.ceil(fraction * n)))
        mine = order[start:end]
        if data.time is not None:
            # Latest last; ties broken by row so the split is deterministic.
            mine = mine[np.lexsort((mine, data.time[mine]))]
            test.append(mine[-hold:])
        else:
            test.append(rng.choice(mine, size=hold, replace=False))
    test_rows = np.sort(np.concatenate(test)) if test else np.empty(0, dtype=np.int64)
    train_rows = np.setdiff1d(rows, test_rows, assume_unique=True)
    return train_rows, test_rows


# --------------------------------------------------------------------------
# Models
# --------------------------------------------------------------------------


@dataclass
class Model:
    """A fitted recommender, as the artifact holds it."""

    algorithm: str
    users: list[str]
    items: list[str]
    seen: sp.csr_matrix  # users x items, 1 where a person has the item; sorted indices
    popular: np.ndarray  # uint32 item indices, most popular first
    popular_scores: np.ndarray  # float32
    user_factors: np.ndarray | None = None  # float32 [users, factors]
    item_factors: np.ndarray | None = None  # float32 [items, factors]
    neighbor_items: np.ndarray | None = None  # uint32 [items, neighbors]
    neighbor_scores: np.ndarray | None = None  # float32 [items, neighbors]
    # Format 2 only (scenarios.py); extra is None for a format 1 model.
    extra: dict[str, Any] | None = None
    customer_neighbors: np.ndarray | None = None  # uint32 [users, n]
    customer_neighbor_scores: np.ndarray | None = None  # float32 [users, n]
    seen_last_day: np.ndarray | None = None  # uint32 [seen]
    seen_gap: np.ndarray | None = None  # float32 [seen]
    customer_features: np.ndarray | None = None  # float32 [users, features]
    item_features: np.ndarray | None = None  # float32 [items, features]
    # With product details (extra["pair_details"]): each seen pair's median
    # quantity per order (NaN unknown) and what the person spent on it.
    seen_qty: np.ndarray | None = None  # float32 [seen]
    seen_spend: np.ndarray | None = None  # float32 [seen]
    # With returns or quotes (extra["signals"], their count): each signal's
    # person, item, day and kind.
    signals: np.ndarray | None = None  # uint32 [signals, 4]


def _popular(matrix: sp.csr_matrix) -> tuple[np.ndarray, np.ndarray]:
    """The POPULAR items most people have, with a score of share of the top one."""
    counts = np.asarray((matrix > 0).sum(axis=0)).ravel().astype(np.float64)
    n = min(POPULAR, int((counts > 0).sum()))
    order = np.lexsort((np.arange(len(counts)), -counts))[:n]
    top = counts[order[0]] if n else 1.0
    return order.astype(np.uint32), (counts[order] / top).astype(np.float32)


def _seen(matrix: sp.csr_matrix) -> sp.csr_matrix:
    seen = (matrix > 0).astype(np.float32).tocsr()
    seen.sort_indices()
    return seen


def fit_als(matrix: sp.csr_matrix, seed: int, factors: int = ALS_FACTORS, iterations: int = ALS_ITERATIONS) -> tuple[np.ndarray, np.ndarray]:
    """Implicit ALS: user and item factors, float32."""
    rng = np.random.default_rng(seed)
    n_users, n_items = matrix.shape
    factors = max(1, min(factors, n_users, n_items))
    confidence = matrix.copy().astype(np.float64)
    confidence.data = 1.0 + ALS_ALPHA * np.log1p(confidence.data)
    by_user = confidence.tocsr()
    by_item = confidence.T.tocsr()
    users = rng.normal(0.0, 0.01, (n_users, factors))
    items = rng.normal(0.0, 0.01, (n_items, factors))
    reg = ALS_REGULARIZATION * np.eye(factors)
    for _ in range(iterations):
        users = _als_step(by_user, items, reg)
        items = _als_step(by_item, users, reg)
    return users.astype(np.float32), items.astype(np.float32)


def _als_step(conf: sp.csr_matrix, fixed: np.ndarray, reg: np.ndarray) -> np.ndarray:
    """Solve every row of `conf` against the fixed side's factors."""
    gram = fixed.T @ fixed + reg
    out = np.zeros((conf.shape[0], fixed.shape[1]))
    indptr, indices, data = conf.indptr, conf.indices, conf.data
    for row in range(conf.shape[0]):
        lo, hi = indptr[row], indptr[row + 1]
        if lo == hi:
            continue
        y = fixed[indices[lo:hi]]
        c = data[lo:hi]
        a = gram + (y.T * (c - 1.0)) @ y
        b = y.T @ c
        out[row] = np.linalg.solve(a, b)
    return out


def fit_neighbors(matrix: sp.csr_matrix, neighbors: int = NEIGHBORS) -> tuple[np.ndarray, np.ndarray]:
    """Each item's most similar items by cosine over who has them."""
    binary = (matrix > 0).astype(np.float64).tocsc()
    norms = np.sqrt(np.asarray(binary.multiply(binary).sum(axis=0)).ravel())
    norms[norms == 0] = 1.0
    normed = (binary @ sp.diags(1.0 / norms)).tocsc()
    n_items = normed.shape[1]
    keep = max(1, min(neighbors, n_items - 1))
    nbr_items = np.full((n_items, keep), EMPTY, dtype=np.uint32)
    nbr_scores = np.zeros((n_items, keep), dtype=np.float32)
    chunk = max(1, SIMILARITY_CHUNK_CELLS // n_items)
    normed_t = normed.T.tocsr()
    for start in range(0, n_items, chunk):
        stop = min(n_items, start + chunk)
        sims = np.asarray((normed_t[start:stop] @ normed).todense(), dtype=np.float64)
        for row, item in enumerate(range(start, stop)):
            # Ranked as stored, in float32, so ties break the same way
            # wherever the lists are read.
            sim = sims[row].astype(np.float32)
            sim[item] = 0.0
            candidates = np.flatnonzero(sim > 0)
            if len(candidates) == 0:
                continue
            order = candidates[np.lexsort((candidates, -sim[candidates]))][:keep]
            nbr_items[item, : len(order)] = order
            nbr_scores[item, : len(order)] = sim[order]
    return nbr_items, nbr_scores


def fit(algorithm: str, data: Interactions, rows: np.ndarray | None, seed: int) -> Model:
    matrix = data.matrix(rows)
    popular, popular_scores = _popular(matrix)
    model = Model(algorithm, data.users, data.items, _seen(matrix), popular, popular_scores)
    if algorithm == "als":
        model.user_factors, model.item_factors = fit_als(matrix, seed)
    else:
        model.neighbor_items, model.neighbor_scores = fit_neighbors(matrix)
    return model


# --------------------------------------------------------------------------
# Scoring: the same arithmetic Hermod serves with
# --------------------------------------------------------------------------


def _top(scores: np.ndarray, k: int, exclude: np.ndarray | None = None) -> list[int]:
    """Indices of the k best positive-or-any scores, best first; ties by index."""
    scores = scores.copy()
    if exclude is not None and len(exclude):
        scores[exclude] = -np.inf
    candidates = np.flatnonzero(np.isfinite(scores))
    if len(candidates) == 0:
        return []
    order = candidates[np.lexsort((candidates, -scores[candidates]))]
    return order[:k].tolist()


def user_scores(model: Model, user: int) -> tuple[np.ndarray, np.ndarray]:
    """(scores over every item, mask of items with a personal score)."""
    seen = model.seen.indices[model.seen.indptr[user] : model.seen.indptr[user + 1]]
    if model.user_factors is not None:
        scores = model.item_factors.astype(np.float64) @ model.user_factors[user].astype(np.float64)
        return scores, np.ones(len(model.items), dtype=bool)
    scores = np.zeros(len(model.items))
    for j in seen:
        nbrs = model.neighbor_items[j]
        used = nbrs != EMPTY
        np.add.at(scores, nbrs[used].astype(np.int64), model.neighbor_scores[j][used].astype(np.float64))
    return scores, scores > 0


def recommend_user(model: Model, user: int, k: int, exclude_seen: bool = True) -> list[tuple[int, float, str | None]]:
    """A known person's top k: personal scores, topped up from popular items."""
    seen = model.seen.indices[model.seen.indptr[user] : model.seen.indptr[user + 1]]
    scores, personal = user_scores(model, user)
    scores = np.where(personal, scores, -np.inf)
    picked = _top(scores, k, seen if exclude_seen else None)
    out: list[tuple[int, float, str | None]] = [(i, float(np.float32(scores[i])), None) for i in picked]
    return _top_up(model, out, k, set(seen.tolist()) if exclude_seen else set())


def popular_items(model: Model, k: int) -> list[tuple[int, float, str | None]]:
    return [(i, float(s), "popular") for i, s in zip(model.popular[:k].tolist(), model.popular_scores[:k].tolist())]


def _top_up(model: Model, out: list[tuple[int, float, str | None]], k: int, taken: set[int]) -> list[tuple[int, float, str | None]]:
    """Fill `out` to k with popular items not in `taken` or already listed."""
    taken = taken | {i for i, _, _ in out}
    for i, s in zip(model.popular.tolist(), model.popular_scores.tolist()):
        if len(out) == k:
            break
        if i not in taken:
            out.append((i, float(s), "popular"))
    return out


def similar_items(model: Model, item: int, k: int) -> list[tuple[int, float, str | None]]:
    """The k items most like `item`, topped up from popular items."""
    out: list[tuple[int, float, str | None]] = []
    if model.neighbor_items is not None:
        for j, s in zip(model.neighbor_items[item].tolist(), model.neighbor_scores[item].tolist()):
            if j == EMPTY or len(out) == k:
                break
            out.append((j, float(s), None))
    else:
        vectors = model.item_factors.astype(np.float64)
        norms = np.linalg.norm(vectors, axis=1)
        norms[norms == 0] = 1.0
        scores = (vectors @ vectors[item]) / (norms * norms[item])
        picked = _top(scores, k, np.asarray([item]))
        out = [(i, float(np.float32(scores[i])), None) for i in picked]
    return _top_up(model, out, k, {item})


# --------------------------------------------------------------------------
# Evaluation
# --------------------------------------------------------------------------


def _metrics(lists: list[list[int]], truths: list[set[int]], n_items: int) -> dict[str, float]:
    hits = precision = recall = ndcg = 0.0
    shown: set[int] = set()
    for recs, truth in zip(lists, truths):
        shown.update(recs)
        gains = [1.0 if r in truth else 0.0 for r in recs[:K]]
        found = sum(gains)
        hits += 1.0 if found else 0.0
        precision += found / K
        recall += found / len(truth)
        dcg = sum(g / math.log2(rank + 2) for rank, g in enumerate(gains))
        ideal = sum(1.0 / math.log2(rank + 2) for rank in range(min(len(truth), K)))
        ndcg += dcg / ideal
    n = max(1, len(lists))
    return {
        f"hit_rate@{K}": hits / n,
        f"precision@{K}": precision / n,
        f"recall@{K}": recall / n,
        f"ndcg@{K}": ndcg / n,
        "coverage": len(shown) / n_items,
    }


def _truths(data: Interactions, test_rows: np.ndarray, seed: int) -> dict[int, set[int]]:
    truth: dict[int, set[int]] = {}
    for u, i in zip(data.user[test_rows].tolist(), data.item[test_rows].tolist()):
        truth.setdefault(u, set()).add(i)
    if len(truth) > MAX_EVAL_USERS:
        rng = np.random.default_rng(seed)
        chosen = rng.choice(sorted(truth), size=MAX_EVAL_USERS, replace=False)
        truth = {u: truth[u] for u in sorted(chosen.tolist())}
    return truth


def evaluate(model: Model, truth: dict[int, set[int]]) -> tuple[dict[str, float], dict[str, float]]:
    """(the model's metrics, the popular-items baseline's) on held-out picks."""
    users = sorted(truth)
    truths = [truth[u] for u in users]
    lists = [[i for i, _, _ in recommend_user(model, u, K)] for u in users]
    baseline = []
    for u in users:
        seen = set(model.seen.indices[model.seen.indptr[u] : model.seen.indptr[u + 1]].tolist())
        baseline.append([i for i in model.popular.tolist() if i not in seen][:K])
    return _metrics(lists, truths, len(model.items)), _metrics(baseline, truths, len(model.items))


# --------------------------------------------------------------------------
# Artifact
# --------------------------------------------------------------------------


def encode(model: Model) -> bytes:
    """The model as the artifact Hermod serves (see the module docstring):
    format 2 when it carries `extra`, format 1 otherwise."""
    factors = 0 if model.user_factors is None else int(model.user_factors.shape[1])
    neighbors = 0 if model.neighbor_items is None else int(model.neighbor_items.shape[1])
    header: dict[str, Any] = {
        "algorithm": model.algorithm,
        "users": model.users,
        "items": model.items,
        "factors": factors,
        "neighbors": neighbors,
        "seen": int(model.seen.nnz),
        "popular": int(len(model.popular)),
    }
    v2 = model.extra is not None
    customer_neighbors = 0
    if v2:
        customer_neighbors = 0 if model.customer_neighbors is None else int(model.customer_neighbors.shape[1])
        header["customer_neighbors"] = customer_neighbors
        header.update(model.extra)
    head = json.dumps(header, separators=(",", ":"), ensure_ascii=False, allow_nan=False).encode("utf-8")
    version = FORMAT_VERSION_SCENARIO if v2 else FORMAT_VERSION
    parts = [MAGIC, struct.pack("<II", version, len(head)), head]
    if factors:
        parts += [np.ascontiguousarray(model.user_factors, _F32).tobytes(), np.ascontiguousarray(model.item_factors, _F32).tobytes()]
    if neighbors:
        parts += [np.ascontiguousarray(model.neighbor_items, _U32).tobytes(), np.ascontiguousarray(model.neighbor_scores, _F32).tobytes()]
    parts += [
        np.asarray(model.seen.indptr, _U32).tobytes(),
        np.asarray(model.seen.indices, _U32).tobytes(),
        np.asarray(model.popular, _U32).tobytes(),
        np.asarray(model.popular_scores, _F32).tobytes(),
    ]
    if v2:
        if customer_neighbors:
            parts += [np.ascontiguousarray(model.customer_neighbors, _U32).tobytes(),
                      np.ascontiguousarray(model.customer_neighbor_scores, _F32).tobytes()]
        parts += [
            np.ascontiguousarray(model.seen_last_day, _U32).tobytes(),
            np.ascontiguousarray(model.seen_gap, _F32).tobytes(),
            np.ascontiguousarray(model.customer_features, _F32).tobytes(),
            np.ascontiguousarray(model.item_features, _F32).tobytes(),
        ]
        if model.extra.get("pair_details"):
            parts += [np.ascontiguousarray(model.seen_qty, _F32).tobytes(), np.ascontiguousarray(model.seen_spend, _F32).tobytes()]
        if "signals" in model.extra:
            parts.append(np.ascontiguousarray(np.asarray(model.signals, _U32).reshape(-1, 4).T).tobytes())
    return b"".join(parts)


def decode(raw: bytes) -> Model:
    """Read an artifact of either format back; raise ValueError when it is
    not one."""
    if len(raw) < 12 or raw[:4] != MAGIC:
        raise ValueError("not a recommendation artifact")
    version, size = struct.unpack_from("<II", raw, 4)
    if version not in (FORMAT_VERSION, FORMAT_VERSION_SCENARIO):
        raise ValueError(f"artifact format {version} is not {FORMAT_VERSION} or {FORMAT_VERSION_SCENARIO}")
    header = json.loads(raw[12 : 12 + size].decode("utf-8"))
    n_users, n_items = len(header["users"]), len(header["items"])
    f, nb = header["factors"], header["neighbors"]
    offset = 12 + size

    def take(dtype: np.dtype, count: int) -> np.ndarray:
        nonlocal offset
        end = offset + count * dtype.itemsize
        if end > len(raw):
            raise ValueError("artifact is truncated")
        out = np.frombuffer(raw, dtype=dtype, count=count, offset=offset)
        offset = end
        return out

    model = Model(header["algorithm"], header["users"], header["items"], sp.csr_matrix((n_users, n_items)), np.empty(0), np.empty(0))
    if f:
        model.user_factors = take(_F32, n_users * f).reshape(n_users, f)
        model.item_factors = take(_F32, n_items * f).reshape(n_items, f)
    if nb:
        model.neighbor_items = take(_U32, n_items * nb).reshape(n_items, nb)
        model.neighbor_scores = take(_F32, n_items * nb).reshape(n_items, nb)
    indptr = take(_U32, n_users + 1).astype(np.int64)
    indices = take(_U32, header["seen"]).astype(np.int64)
    model.seen = sp.csr_matrix((np.ones(len(indices), dtype=np.float32), indices, indptr), shape=(n_users, n_items))
    model.popular = take(_U32, header["popular"])
    model.popular_scores = take(_F32, header["popular"])
    if version == FORMAT_VERSION_SCENARIO:
        cn = header["customer_neighbors"]
        model.extra = {k: v for k, v in header.items() if k not in _V1_KEYS and k != "customer_neighbors"}
        if cn:
            model.customer_neighbors = take(_U32, n_users * cn).reshape(n_users, cn)
            model.customer_neighbor_scores = take(_F32, n_users * cn).reshape(n_users, cn)
        model.seen_last_day = take(_U32, header["seen"])
        model.seen_gap = take(_F32, header["seen"])
        cf, itf = len(header["customer_features"]), len(header["item_features"])
        model.customer_features = take(_F32, n_users * cf).reshape(n_users, cf)
        model.item_features = take(_F32, n_items * itf).reshape(n_items, itf)
        if header.get("pair_details"):
            model.seen_qty = take(_F32, header["seen"])
            model.seen_spend = take(_F32, header["seen"])
        if "signals" in header:
            n = header["signals"]
            model.signals = take(_U32, 4 * n).reshape(4, n).T
    if offset != len(raw):
        raise ValueError("artifact has bytes after its last section")
    return model


# --------------------------------------------------------------------------
# Training
# --------------------------------------------------------------------------


def _choose(data: Interactions, train_rows: np.ndarray, req: RecommendRequest) -> tuple[str, dict[str, float]]:
    """Auto: fit both on part of the training rows and score them on the rest."""
    fit_rows, valid_rows = holdout(data, train_rows, req.test_size, req.seed + 1)
    if len(valid_rows) == 0:
        return "item_similarity", {}
    truth = _truths(data, valid_rows, req.seed)
    scores = {}
    for algorithm in ("als", "item_similarity"):
        metrics, _ = evaluate(fit(algorithm, data, fit_rows, req.seed), truth)
        scores[algorithm] = metrics[f"ndcg@{K}"]
    # The simpler model wins a tie: it explains itself.
    best = "als" if scores["als"] > scores["item_similarity"] else "item_similarity"
    return best, scores


def train(frame: pd.DataFrame, types: dict[str, str], req: RecommendRequest, name: str) -> tuple[bytes, dict[str, Any]]:
    """Fit, evaluate and encode a recommender. Returns (artifact, meta)."""
    data = interactions(frame, types, req)
    rows = np.arange(len(data.user), dtype=np.int64)
    train_rows, test_rows = holdout(data, rows, req.test_size, req.seed)
    if len(test_rows) == 0:
        raise bad_request("Nobody has two or more interactions, so there is nothing to hold back and test on.")

    algorithm, candidates = (req.algorithm, {}) if req.algorithm != "auto" else _choose(data, train_rows, req)
    truth = _truths(data, test_rows, req.seed)
    held = fit(algorithm, data, train_rows, req.seed)
    metrics, baseline = evaluate(held, truth)

    samples = []
    for u in sorted(truth)[:SAMPLES]:
        samples.append({
            "user": data.users[u],
            "recommended": [data.items[i] for i, _, _ in recommend_user(held, u, K)],
            "picked": sorted(data.items[i] for i in truth[u]),
        })

    final = fit(algorithm, data, None, req.seed)
    artifact = encode(final)
    if len(artifact) > MAX_ARTIFACT_BYTES:
        raise bad_request(
            f"The trained recommender is {len(artifact) >> 20} MB, over the {MAX_ARTIFACT_BYTES >> 20} MB limit: "
            f"it has {len(data.users)} people and {len(data.items)} items. Train on fewer people or items."
        )

    all_metrics = dict(metrics)
    all_metrics.update({f"baseline_{k}": v for k, v in baseline.items()})
    all_metrics["score"] = metrics[f"ndcg@{K}"]
    meta: dict[str, Any] = {
        "model": name,
        "task": TASK,
        "algorithm": algorithm,
        "dataset": req.dataset,
        "target": "",
        "features": [],
        "feature_types": {},
        "recommend": {"user": req.user, "item": req.item, "strength": req.strength, "time": req.time},
        "metrics": {k: float(v) for k, v in all_metrics.items() if math.isfinite(v)},
        "rows": {"train": int(len(train_rows)), "test": int(len(test_rows))},
        "catalog": {"users": len(data.users), "items": len(data.items)},
        "samples": samples,
    }
    if candidates:
        meta["candidates"] = {k: float(v) for k, v in candidates.items()}
    return artifact, meta

