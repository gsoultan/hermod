"""Entry point: `python -m hermod_ml` serves the worker with uvicorn."""

from __future__ import annotations

import logging
import sys

import uvicorn

from .app import create_app
from .settings import Settings


def main() -> int:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")
    try:
        settings = Settings.from_env()
    except ValueError as exc:
        print(f"hermod-ml: invalid configuration: {exc}", file=sys.stderr)
        return 2
    settings.data_dir.mkdir(parents=True, exist_ok=True)
    host, port = settings.host_port()
    uvicorn.run(create_app(settings), host=host, port=port, log_level="info")
    return 0


if __name__ == "__main__":
    sys.exit(main())
