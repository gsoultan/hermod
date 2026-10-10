import pytest


@pytest.mark.parametrize("path", ["/v2/health/live", "/v2/health/ready"])
def test_health_is_open_without_token(make_client, path):
    client = make_client(token="s3cret")
    assert client.get(path).status_code == 200


@pytest.mark.parametrize(
    "method,path",
    [
        ("GET", "/v1/datasets/acme"),
        ("GET", "/v1/datasets/acme/orders"),
        ("POST", "/v1/datasets/acme/orders/rows"),
        ("PUT", "/v1/datasets/acme/orders/file?format=csv"),
        ("DELETE", "/v1/datasets/acme/orders"),
        ("POST", "/v1/models/acme/m/train"),
        ("GET", "/v1/models/acme/m/versions"),
        ("DELETE", "/v1/models/acme/m"),
        ("GET", "/vhosts/acme/v2/models/m"),
        ("GET", "/vhosts/acme/v2/models/m/versions/1"),
        ("POST", "/vhosts/acme/v2/models/m/infer"),
        ("POST", "/vhosts/acme/v2/models/m/versions/1/infer"),
    ],
)
@pytest.mark.parametrize("header", [None, "Bearer wrong", "s3cret", "Basic s3cret", "Bearer "])
def test_protected_routes_need_the_bearer_token(make_client, method, path, header):
    client = make_client(token="s3cret")
    headers = {"Authorization": header} if header is not None else {}
    resp = client.request(method, path, headers=headers)
    assert resp.status_code == 401
    assert isinstance(resp.json()["error"], str)


def test_correct_token_is_accepted(make_client):
    client = make_client(token="s3cret")
    resp = client.get("/v1/datasets/acme", headers={"Authorization": "Bearer s3cret"})
    assert resp.status_code == 200


def test_no_token_configured_means_open(client):
    assert client.get("/v1/datasets/acme").status_code == 200


def test_unknown_route_is_json_404(client):
    resp = client.get("/nope")
    assert resp.status_code == 404
    assert "error" in resp.json()
