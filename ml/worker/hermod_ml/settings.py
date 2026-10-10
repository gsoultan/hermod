"""Runtime configuration, read once from the environment at startup."""

from __future__ import annotations

import os
from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path


def _positive_int(env: Mapping[str, str], key: str, default: int) -> int:
    raw = env.get(key)
    if raw is None or raw == "":
        return default
    try:
        value = int(raw)
    except ValueError:
        raise ValueError(f"{key} must be an integer, got {raw!r}") from None
    if value < 1:
        raise ValueError(f"{key} must be at least 1, got {value}")
    return value


@dataclass(frozen=True)
class Settings:
    data_dir: Path = Path("/var/lib/hermod-ml")
    token: str = ""  # empty = no auth
    max_upload_mb: int = 200
    max_trainings: int = 1
    model_cache: int = 32
    addr: str = "0.0.0.0:8090"

    @classmethod
    def from_env(cls, env: Mapping[str, str] | None = None) -> "Settings":
        """Build settings from `env` (defaults to os.environ). Bad values raise ValueError."""
        env = os.environ if env is None else env
        settings = cls(
            data_dir=Path(env.get("HERMOD_ML_DATA_DIR") or "/var/lib/hermod-ml"),
            token=env.get("HERMOD_ML_TOKEN", ""),
            max_upload_mb=_positive_int(env, "HERMOD_ML_MAX_UPLOAD_MB", 200),
            max_trainings=_positive_int(env, "HERMOD_ML_MAX_TRAININGS", 1),
            model_cache=_positive_int(env, "HERMOD_ML_MODEL_CACHE", 32),
            addr=env.get("HERMOD_ML_ADDR") or "0.0.0.0:8090",
        )
        settings.host_port()  # fail fast on a malformed address
        return settings

    @property
    def max_upload_bytes(self) -> int:
        return self.max_upload_mb * 1024 * 1024

    def host_port(self) -> tuple[str, int]:
        """Split `addr` ("host:port") into its parts."""
        host, sep, port = self.addr.rpartition(":")
        if not sep or not host:
            raise ValueError(f"HERMOD_ML_ADDR must look like host:port, got {self.addr!r}")
        try:
            port_num = int(port)
        except ValueError:
            raise ValueError(f"HERMOD_ML_ADDR port must be a number, got {port!r}") from None
        if not 0 < port_num < 65536:
            raise ValueError(f"HERMOD_ML_ADDR port out of range: {port_num}")
        return host.strip("[]"), port_num
