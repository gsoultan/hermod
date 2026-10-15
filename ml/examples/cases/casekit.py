"""What the case generators share: the command line, and writing a table as
the CSV file a dataset is uploaded from.

Every generator is deterministic: the same rows and seed give the same file,
byte for byte, so the numbers in docs/ml-cases.md can be reproduced and the
samples in samples/ are checked against the generators by the worker's tests
(ml/worker/tests/test_cases.py).
"""

from __future__ import annotations

import argparse
import gzip
import io
import sys
from collections.abc import Callable
from pathlib import Path

import numpy as np
import pandas as pd

# The sample of each case the built-in engine trains, kept in the repository:
# small enough to commit, big enough to train on. The Go tests read them
# (pkg/ml/builtin/cases_test.go).
SAMPLE_DIR = Path(__file__).resolve().parent / "samples"


def to_csv(frame: pd.DataFrame) -> bytes:
    """The table as CSV: missing values as empty cells, booleans as true/false."""
    out = frame.copy()
    for name in out.columns:
        if pd.api.types.is_bool_dtype(out[name].dtype):
            out[name] = out[name].map(lambda v: "" if v is pd.NA else ("true" if v else "false"))
    buf = io.StringIO()
    out.to_csv(buf, index=False, lineterminator="\n")
    return buf.getvalue().encode()


def gzip_bytes(raw: bytes) -> bytes:
    """raw gzipped with no file name and a zero timestamp, so the same rows give the same file."""
    buf = io.BytesIO()
    with gzip.GzipFile(filename="", mode="wb", fileobj=buf, mtime=0, compresslevel=9) as gz:
        gz.write(raw)
    return buf.getvalue()


def write(frame: pd.DataFrame, path: Path) -> None:
    raw = to_csv(frame)
    path.write_bytes(gzip_bytes(raw) if path.suffix == ".gz" else raw)


def missing(rng: np.random.Generator, values: np.ndarray, share: float) -> np.ndarray:
    """values as floats, with about share of them made missing (NaN)."""
    out = values.astype(float)
    out[rng.random(len(out)) < share] = np.nan
    return out


def sigmoid(x: np.ndarray) -> np.ndarray:
    return 1 / (1 + np.exp(-x))


def main(generate: Callable[[int, int], pd.DataFrame], default_rows: int, sample_rows: int, name: str) -> None:
    """The command line of a generator:

        python3 <case>.py [--rows N] [--seed S] [--out FILE]   # FILE.csv or FILE.csv.gz
        python3 <case>.py --sample                           # rewrites samples/<case>.csv.gz
    """
    parser = argparse.ArgumentParser(description=sys.modules["__main__"].__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--rows", type=int, default=default_rows, help=f"rows to write (default {default_rows})")
    parser.add_argument("--seed", type=int, default=7, help="random seed (default 7)")
    parser.add_argument("--out", type=Path, help="file to write; .gz is gzipped (default: stdout)")
    parser.add_argument("--sample", action="store_true", help=f"write the committed sample ({sample_rows} rows)")
    args = parser.parse_args()
    if args.sample and not sample_rows:
        parser.error(f"{name} keeps no sample: it trains on the worker, whose tests generate it in full")
    if args.sample:
        write(generate(sample_rows, 7), SAMPLE_DIR / f"{name}.csv.gz")
        return
    frame = generate(args.rows, args.seed)
    if args.out is None:
        sys.stdout.buffer.write(to_csv(frame))
    else:
        write(frame, args.out)
