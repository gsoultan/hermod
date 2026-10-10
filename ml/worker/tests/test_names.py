import pytest

from hermod_ml.errors import ApiError
from hermod_ml.names import validate_name


@pytest.mark.parametrize("name", ["a", "A1", "orders", "my_data.v2-final", "9" * 128, "x.."])
def test_valid_names_pass(name):
    assert validate_name(name, "dataset") == name


@pytest.mark.parametrize(
    "name",
    ["", ".", "..", ".hidden", "_x", "-x", "a/b", "a\\b", "a b", "a$b", "é", "a" * 129, "a\x00b", "../etc"],
)
def test_invalid_names_raise_400(name):
    with pytest.raises(ApiError) as exc:
        validate_name(name, "dataset")
    assert exc.value.status == 400
    assert "dataset" in exc.value.message


@pytest.mark.parametrize(
    "path",
    [
        "/v1/datasets/bad%20vhost",
        "/v1/datasets/ok/bad$name",
        "/v1/datasets/ok/%2E%2E",
        "/v1/models/ok/bad%20name/versions",
        "/vhosts/bad%24/v2/models/m",
        "/vhosts/ok/v2/models/m/versions/1%20",
    ],
)
def test_routes_reject_invalid_names(client, path):
    resp = client.get(path)
    assert resp.status_code == 400
    assert "error" in resp.json()
