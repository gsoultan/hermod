from pathlib import Path

import pytest

from hermod_ml.settings import Settings


def test_defaults_match_the_contract():
    s = Settings.from_env({})
    assert s.data_dir == Path("/var/lib/hermod-ml")
    assert s.token == ""
    assert s.max_upload_mb == 200
    assert s.max_trainings == 1
    assert s.model_cache == 32
    assert s.addr == "0.0.0.0:8090"
    assert s.host_port() == ("0.0.0.0", 8090)


def test_env_overrides():
    s = Settings.from_env(
        {
            "HERMOD_ML_DATA_DIR": "/srv/ml",
            "HERMOD_ML_TOKEN": "secret",
            "HERMOD_ML_MAX_UPLOAD_MB": "5",
            "HERMOD_ML_MAX_TRAININGS": "3",
            "HERMOD_ML_MODEL_CACHE": "4",
            "HERMOD_ML_ADDR": "127.0.0.1:9000",
        }
    )
    assert s.data_dir == Path("/srv/ml")
    assert s.token == "secret"
    assert (s.max_upload_mb, s.max_trainings, s.model_cache) == (5, 3, 4)
    assert s.host_port() == ("127.0.0.1", 9000)


@pytest.mark.parametrize(
    "env",
    [
        {"HERMOD_ML_MAX_UPLOAD_MB": "lots"},
        {"HERMOD_ML_MAX_TRAININGS": "0"},
        {"HERMOD_ML_MODEL_CACHE": "-1"},
        {"HERMOD_ML_ADDR": "nohostport"},
        {"HERMOD_ML_ADDR": "host:notaport"},
    ],
)
def test_bad_env_values_fail_fast(env):
    with pytest.raises(ValueError):
        Settings.from_env(env).host_port()
