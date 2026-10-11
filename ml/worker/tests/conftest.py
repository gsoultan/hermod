"""Shared fixtures: every test gets its own data dir under tmp_path."""

from __future__ import annotations

import pytest
from fastapi.testclient import TestClient

from hermod_ml.app import create_app
from hermod_ml.settings import Settings


@pytest.fixture
def make_client(tmp_path):
    """Build a TestClient for an app with the given Settings overrides."""

    def build(sandbox=None, **overrides) -> TestClient:
        settings = Settings(data_dir=tmp_path / "data", **overrides)
        return TestClient(create_app(settings, sandbox=sandbox))

    return build


@pytest.fixture
def client(make_client) -> TestClient:
    return make_client()
