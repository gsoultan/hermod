"""Trains with a custom script against a running worker, over HTTP.

CI runs it against the image started the way the chart starts the custom-script
pool (read-only root, an arbitrary uid, every capability dropped), so the
sandbox is proven to work there and not only in the test process:

    python -m tests.smoke_custom http://127.0.0.1:8090 <token>
"""

from __future__ import annotations

import json
import sys
import urllib.error
import urllib.request

from tests.helpers import classification_rows
from tests.scripts import GOOD, script


def call(base: str, token: str, method: str, path: str, body: dict) -> tuple[int, dict]:
    req = urllib.request.Request(
        base + path,
        data=json.dumps(body).encode(),
        method=method,
        headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=600) as resp:
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as exc:
        return exc.code, json.loads(exc.read() or b"{}")


def main(base: str, token: str) -> int:
    status, body = call(base, token, "POST", "/v1/datasets/ci/orders/rows", {"rows": classification_rows(), "replace": True})
    if status != 200:
        print("loading the dataset failed:", status, body)
        return 1
    status, body = call(base, token, "POST", "/v1/models/ci/churn/train-custom", {
        "dataset": "orders", "target": "churned", "algorithm": "custom:extra_trees",
        "script": script("extra_trees", GOOD),
    })
    if status != 200 or body.get("algorithm") != "custom:extra_trees" or "accuracy" not in body.get("metrics", {}):
        print("training with the script failed:", status, body)
        return 1
    print("trained version", body.get("version"), "accuracy", body["metrics"]["accuracy"])
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1], sys.argv[2]))
