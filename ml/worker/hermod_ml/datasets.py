"""Dataset storage: tabular data kept as parquet part files.

Layout on disk:

    DATA_DIR/datasets/{vhost}/{name}/part-000001.parquet
                                     part-000002.parquet ...

Each append writes one new part. Every column in a part has one of four
arrow types: bool, int64, double or string (or the null type when every
value in that part was null). The column type a caller sees is the union of
the part types:

    bool everywhere            -> "bool"
    int64/double everywhere    -> "number"
    anything else              -> "string" (other values rendered as text)

Parts whose value is null for a column do not vote. A dataset with no parts
at all is a valid, empty dataset; it exists as long as its directory does.
"""

from __future__ import annotations

import io
import json
import math
import os
import re
import shutil
import threading
import uuid
from datetime import date, datetime, time, timezone
from pathlib import Path
from typing import Any

import pandas as pd
import pyarrow as pa
import pyarrow.parquet as pq

from .errors import bad_request, not_found
from .names import NAME_RE, validate_name

PART_RE = re.compile(r"part-(\d+)\.parquet\Z")
SAMPLE_ROWS = 20

# Column types exposed in the API.
NUMBER, STRING, BOOL = "number", "string", "bool"

_INT64_MIN, _INT64_MAX = -(2**63), 2**63 - 1


# --------------------------------------------------------------------------
# Value helpers (also used by training and serving so text is rendered the
# same way everywhere).
# --------------------------------------------------------------------------


def is_null(value: Any) -> bool:
    """True for None, NaN, pandas NA/NaT."""
    if value is None:
        return True
    if isinstance(value, float):
        return math.isnan(value)
    try:
        return bool(pd.isna(value))
    except (TypeError, ValueError):
        return False


def to_text(value: Any) -> str:
    """Render a non-null scalar as text in one canonical way.

    bool -> "true"/"false"; integral floats lose their ".0" so 3 and 3.0
    both become "3"; dates use ISO 8601; objects and arrays become compact
    JSON. This is the rule used when a column ends up typed "string" and
    when a string feature is fed a non-string value at inference.
    """
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, int):
        return str(value)
    if isinstance(value, float):
        if value.is_integer() and abs(value) < 2**53:
            return str(int(value))
        return repr(value)
    if isinstance(value, (datetime, date, time)):
        return value.isoformat()
    if isinstance(value, (dict, list)):
        return json.dumps(value, separators=(",", ":"), ensure_ascii=False)
    # numpy scalars and anything else
    if hasattr(value, "item"):
        return to_text(value.item())
    return str(value)


def json_number(value: float) -> int | float | None:
    """Render a stored number for JSON: integral values as int, NaN as null."""
    if value is None or (isinstance(value, float) and math.isnan(value)):
        return None
    value = float(value)
    if value.is_integer() and abs(value) < 2**53:
        return int(value)
    return value


def _column_array(values: list[Any]) -> pa.Array:
    """Turn one column of Python values into a typed arrow array.

    Nulls are ignored when choosing the type. All bools -> bool; all ints
    (fitting int64) -> int64; all numbers -> double; anything else -> string,
    with non-string values rendered by `to_text`.
    """
    present = [v for v in values if not is_null(v)]
    cleaned = [None if is_null(v) else v for v in values]
    if not present:
        return pa.nulls(len(values))
    if all(isinstance(v, bool) for v in present):
        return pa.array(cleaned, type=pa.bool_())
    numeric = all(isinstance(v, (int, float)) and not isinstance(v, bool) for v in present)
    if numeric:
        if all(isinstance(v, int) and _INT64_MIN <= v <= _INT64_MAX for v in present):
            return pa.array(cleaned, type=pa.int64())
        return pa.array([None if v is None else float(v) for v in cleaned], type=pa.float64())
    return pa.array([None if v is None else (v if isinstance(v, str) else to_text(v)) for v in cleaned], type=pa.string())


def rows_to_table(rows: Any) -> pa.Table:
    """Validate a JSON `rows` array and convert it into an arrow table.

    Columns appear in first-seen order; a key missing from a row is null.
    Objects and arrays are stored as JSON text.
    """
    if not isinstance(rows, list):
        raise bad_request("'rows' must be an array of objects.")
    order: dict[str, None] = {}
    for i, row in enumerate(rows):
        if not isinstance(row, dict):
            raise bad_request(f"Row {i} is not a JSON object.")
        for key in row:
            order.setdefault(key, None)
    if rows and not order:
        raise bad_request("Every row is empty; rows need at least one column.")
    columns = {}
    for key in order:
        values = []
        for row in rows:
            v = row.get(key)
            if isinstance(v, (dict, list)):
                v = to_text(v)
            values.append(v)
        columns[key] = _column_array(values)
    return pa.table(columns) if columns else pa.table({})


def frame_to_table(df: pd.DataFrame) -> pa.Table:
    """Convert a parsed CSV/XLSX frame into an arrow table with our four types."""
    columns = {}
    for raw_name in df.columns:
        name = str(raw_name)
        if name in columns:
            raise bad_request(f"Column {name!r} appears more than once.")
        series = df[raw_name]
        if pd.api.types.is_bool_dtype(series.dtype):
            values = [None if is_null(v) else bool(v) for v in series]
        elif pd.api.types.is_integer_dtype(series.dtype):
            values = [None if is_null(v) else int(v) for v in series]
        elif pd.api.types.is_float_dtype(series.dtype):
            values = [None if is_null(v) else float(v) for v in series]
        elif pd.api.types.is_datetime64_any_dtype(series.dtype):
            values = [None if is_null(v) else v.isoformat() for v in series]
        else:
            values = [None if is_null(v) else (v.item() if hasattr(v, "item") else v) for v in series]
        columns[name] = _column_array(values)
    return pa.table(columns)


def parse_csv(body: bytes) -> pa.Table:
    if not body.strip():
        raise bad_request("The CSV file is empty.")
    try:
        df = pd.read_csv(io.BytesIO(body), encoding="utf-8-sig")
    except UnicodeDecodeError:
        raise bad_request("The CSV file is not valid UTF-8.") from None
    except (pd.errors.ParserError, pd.errors.EmptyDataError, ValueError) as exc:
        raise bad_request(f"Could not parse the CSV file: {exc}") from None
    return frame_to_table(df)


def parse_xlsx(body: bytes) -> pa.Table:
    """Read the first sheet of a workbook; its first row is the header."""
    if not body:
        raise bad_request("The XLSX file is empty.")
    try:
        df = pd.read_excel(io.BytesIO(body), sheet_name=0, header=0, engine="openpyxl")
    except Exception as exc:  # openpyxl raises a zoo of types for bad files
        raise bad_request(f"Could not read the XLSX file: {exc}") from None
    return frame_to_table(df)


# --------------------------------------------------------------------------
# Storage
# --------------------------------------------------------------------------


def _kind_of(arrow_type: pa.DataType) -> str | None:
    """Map a part's arrow type to an API type; None for the all-null type."""
    if pa.types.is_null(arrow_type):
        return None
    if pa.types.is_boolean(arrow_type):
        return BOOL
    if pa.types.is_integer(arrow_type) or pa.types.is_floating(arrow_type):
        return NUMBER
    return STRING


def _rfc3339(ts: float) -> str:
    return datetime.fromtimestamp(ts, tz=timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


class DatasetStore:
    """All dataset reads and writes. Safe to share across request threads."""

    def __init__(self, data_dir: Path) -> None:
        self.root = Path(data_dir) / "datasets"
        # One lock for the whole store keeps appends, replaces and deletes
        # from interleaving; reads take it too so they never see a dataset
        # halfway through a swap.
        self._lock = threading.RLock()

    # -- paths -------------------------------------------------------------

    def _vhost_dir(self, vhost: str) -> Path:
        return self.root / validate_name(vhost, "vhost")

    def _dir(self, vhost: str, name: str) -> Path:
        return self._vhost_dir(vhost) / validate_name(name, "dataset")

    def _existing_dir(self, vhost: str, name: str) -> Path:
        path = self._dir(vhost, name)
        if not path.is_dir():
            raise not_found(f"Dataset {name!r} does not exist in vhost {vhost!r}.")
        return path

    @staticmethod
    def _parts(path: Path) -> list[tuple[int, Path]]:
        found = []
        for entry in path.iterdir():
            match = PART_RE.match(entry.name)
            if match and entry.is_file():
                found.append((int(match.group(1)), entry))
        return sorted(found)

    @staticmethod
    def _write_part(directory: Path, seq: int, table: pa.Table) -> None:
        """Write a part atomically: temp file in the same dir, then rename."""
        tmp = directory / f".tmp-{uuid.uuid4().hex}.parquet"
        try:
            pq.write_table(table, tmp)
            os.replace(tmp, directory / f"part-{seq:06d}.parquet")
        finally:
            tmp.unlink(missing_ok=True)

    # -- writes ------------------------------------------------------------

    def append(self, vhost: str, name: str, table: pa.Table, replace: bool) -> int:
        """Add `table` as a new part (or replace everything) and return the row total."""
        if replace:
            self.replace(vhost, name, table)
            return table.num_rows
        path = self._dir(vhost, name)
        with self._lock:
            path.mkdir(parents=True, exist_ok=True)
            if table.num_rows > 0:
                parts = self._parts(path)
                seq = parts[-1][0] + 1 if parts else 1
                self._write_part(path, seq, table)
            return self._row_count(path)

    def replace(self, vhost: str, name: str, table: pa.Table) -> None:
        """Swap the dataset for one holding only `table` (possibly empty).

        The new data is staged in a sibling directory and renamed into place,
        so readers see either the old dataset or the new one, never a mix.
        """
        target = self._dir(vhost, name)
        with self._lock:
            target.parent.mkdir(parents=True, exist_ok=True)
            staging = target.parent / f".staging-{uuid.uuid4().hex}"
            trash = target.parent / f".trash-{uuid.uuid4().hex}"
            staging.mkdir()
            try:
                if table.num_rows > 0 or table.num_columns > 0:
                    self._write_part(staging, 1, table)
                if target.exists():
                    os.rename(target, trash)
                os.rename(staging, target)
            finally:
                shutil.rmtree(staging, ignore_errors=True)
                shutil.rmtree(trash, ignore_errors=True)

    def delete(self, vhost: str, name: str) -> None:
        with self._lock:
            path = self._existing_dir(vhost, name)
            trash = path.parent / f".trash-{uuid.uuid4().hex}"
            os.rename(path, trash)
            shutil.rmtree(trash, ignore_errors=True)

    # -- reads -------------------------------------------------------------

    def _row_count(self, path: Path) -> int:
        return sum(pq.read_metadata(p).num_rows for _, p in self._parts(path))

    def _schema(self, path: Path) -> list[tuple[str, str]]:
        """Unified column list [(name, type)] across all parts, first-seen order."""
        votes: dict[str, set[str]] = {}
        for _, part in self._parts(path):
            for field in pq.read_schema(part):
                kinds = votes.setdefault(field.name, set())
                kind = _kind_of(field.type)
                if kind is not None:
                    kinds.add(kind)
        return [(col, kinds.pop() if len(kinds) == 1 else STRING) for col, kinds in votes.items()]

    def _info(self, name: str, path: Path) -> dict[str, Any]:
        mtimes = [path.stat().st_mtime] + [p.stat().st_mtime for _, p in self._parts(path)]
        return {
            "name": name,
            "rows": self._row_count(path),
            "columns": [{"name": c, "type": t} for c, t in self._schema(path)],
            "updated_at": _rfc3339(max(mtimes)),
        }

    def info(self, vhost: str, name: str) -> dict[str, Any]:
        with self._lock:
            return self._info(name, self._existing_dir(vhost, name))

    def info_with_sample(self, vhost: str, name: str) -> dict[str, Any]:
        with self._lock:
            path = self._existing_dir(vhost, name)
            info = self._info(name, path)
            frame, types = self._load(path, limit=SAMPLE_ROWS)
        info["sample"] = frame_records(frame, types)
        return info

    def list(self, vhost: str) -> list[dict[str, Any]]:
        with self._lock:
            vdir = self._vhost_dir(vhost)
            if not vdir.is_dir():
                return []
            # Skip staging/trash dirs and anything else that is not a legal name.
            names = sorted(e.name for e in vdir.iterdir() if e.is_dir() and NAME_RE.match(e.name))
            return [self._info(n, vdir / n) for n in names]

    def load(self, vhost: str, name: str) -> tuple[pd.DataFrame, dict[str, str]]:
        """Load the whole dataset for training. See `_load` for the frame's dtypes."""
        with self._lock:
            return self._load(self._existing_dir(vhost, name))

    def _load(self, path: Path, limit: int | None = None) -> tuple[pd.DataFrame, dict[str, str]]:
        """Read parts into one frame with the unified column types applied.

        number -> float64 (NaN for null); bool -> object (True/False/None);
        string -> object (str/None). Returns (frame, {column: type}).
        """
        schema = self._schema(path)
        types = dict(schema)
        frames = []
        remaining = limit
        for _, part in self._parts(path):
            if remaining is not None and remaining <= 0:
                break
            table = pq.read_table(part)
            if remaining is not None:
                table = table.slice(0, remaining)
                remaining -= table.num_rows
            data = {}
            for col, kind in schema:
                if col in table.column_names:
                    values = table.column(col).to_pylist()
                else:
                    values = [None] * table.num_rows
                if kind == NUMBER:
                    data[col] = pd.Series([math.nan if v is None else float(v) for v in values], dtype="float64")
                elif kind == BOOL:
                    data[col] = pd.Series(values, dtype="object")
                else:
                    data[col] = pd.Series([None if v is None else (v if isinstance(v, str) else to_text(v)) for v in values], dtype="object")
            frames.append(pd.DataFrame(data, columns=[c for c, _ in schema]))
        if not frames:
            empty = {c: pd.Series([], dtype="float64" if k == NUMBER else "object") for c, k in schema}
            return pd.DataFrame(empty, columns=[c for c, _ in schema]), types
        return pd.concat(frames, ignore_index=True), types


def frame_records(frame: pd.DataFrame, types: dict[str, str]) -> list[dict[str, Any]]:
    """Render a loaded frame as JSON-ready row objects."""
    records = []
    for row in frame.itertuples(index=False, name=None):
        record = {}
        for col, value in zip(frame.columns, row):
            if types[col] == NUMBER:
                record[col] = json_number(value)
            elif is_null(value):
                record[col] = None
            else:
                record[col] = bool(value) if types[col] == BOOL else str(value)
        records.append(record)
    return records
