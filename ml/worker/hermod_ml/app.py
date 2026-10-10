"""HTTP layer: routes, auth and error rendering.

The routes stay thin. They validate the request shape, hand off to the
dataset store, trainer or model server, and render the result. Anything
CPU- or disk-heavy runs in the threadpool so the event loop stays free for
health checks.
"""

from __future__ import annotations

import hmac
import json
import logging
from typing import Any

from fastapi import APIRouter, Depends, FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse, Response
from starlette.concurrency import run_in_threadpool
from starlette.exceptions import HTTPException as StarletteHTTPException

from .datasets import DatasetStore, parse_csv, parse_xlsx, rows_to_table
from .errors import ApiError, bad_request
from .names import validate_name
from .serving import ModelServer
from .settings import Settings
from .training import TASKS, ModelStore, Trainer, algorithm_capabilities

log = logging.getLogger("hermod_ml")


def _reject_constant(token: str) -> Any:
    raise ValueError(f"{token} is not allowed in JSON")


async def read_body(request: Request, limit: int) -> bytes:
    """Read the request body, refusing with 413 once it passes `limit` bytes."""
    declared = request.headers.get("content-length", "")
    if declared.isdigit() and int(declared) > limit:
        raise ApiError(413, f"The request body is larger than the {limit // (1024 * 1024)} MB limit.")
    chunks, size = [], 0
    async for chunk in request.stream():
        size += len(chunk)
        if size > limit:
            raise ApiError(413, f"The request body is larger than the {limit // (1024 * 1024)} MB limit.")
        chunks.append(chunk)
    return b"".join(chunks)


async def read_json_object(request: Request, limit: int) -> dict[str, Any]:
    """Read and parse a JSON object body. NaN/Infinity are rejected."""
    body = await read_body(request, limit)
    try:
        data = json.loads(body, parse_constant=_reject_constant)
    except (ValueError, UnicodeDecodeError) as exc:
        raise bad_request(f"The request body is not valid JSON: {exc}") from None
    if not isinstance(data, dict):
        raise bad_request("The request body must be a JSON object.")
    return data


def create_app(settings: Settings) -> FastAPI:
    """Build the FastAPI app around one data directory."""
    app = FastAPI(title="hermod-ml", docs_url=None, redoc_url=None, openapi_url=None)
    datasets = DatasetStore(settings.data_dir)
    models = ModelStore(settings.data_dir)
    server = ModelServer(models, capacity=settings.model_cache)
    trainer = Trainer(datasets, models, max_concurrent=settings.max_trainings, on_saved=server.evict)
    app.state.settings = settings
    app.state.datasets = datasets
    app.state.models = models
    app.state.server = server
    app.state.trainer = trainer
    limit = settings.max_upload_bytes

    # -- errors --------------------------------------------------------------

    @app.exception_handler(ApiError)
    async def _api_error(_: Request, exc: ApiError) -> JSONResponse:
        return JSONResponse({"error": exc.message}, status_code=exc.status)

    @app.exception_handler(StarletteHTTPException)
    async def _http_error(_: Request, exc: StarletteHTTPException) -> JSONResponse:
        message = {404: "Not found.", 405: "Method not allowed."}.get(exc.status_code, str(exc.detail))
        return JSONResponse({"error": message}, status_code=exc.status_code)

    @app.exception_handler(RequestValidationError)
    async def _validation_error(_: Request, exc: RequestValidationError) -> JSONResponse:
        return JSONResponse({"error": f"Invalid request: {exc.errors()}"}, status_code=400)

    @app.exception_handler(Exception)
    async def _unexpected(_: Request, exc: Exception) -> JSONResponse:
        log.exception("unhandled error")
        return JSONResponse({"error": "Internal error."}, status_code=500)

    # -- auth ----------------------------------------------------------------

    def require_token(request: Request) -> None:
        if not settings.token:
            return
        scheme, _, credential = request.headers.get("authorization", "").partition(" ")
        # compare_digest keeps the comparison constant-time.
        if scheme.lower() != "bearer" or not hmac.compare_digest(
            credential.strip().encode(), settings.token.encode()
        ):
            raise ApiError(401, "Missing or invalid bearer token.")

    # -- health (no auth) ----------------------------------------------------

    @app.get("/v2/health/live")
    async def live() -> dict[str, bool]:
        return {"live": True}

    @app.get("/v2/health/ready")
    async def ready() -> dict[str, bool]:
        return {"ready": True}

    api = APIRouter(dependencies=[Depends(require_token)])

    # -- capabilities --------------------------------------------------------

    @api.get("/v1/capabilities")
    async def capabilities() -> dict[str, Any]:
        """What this image can train: the slim image has no deep-learning
        libraries, so Hermod offers only the algorithms listed here."""
        available, unavailable = await run_in_threadpool(algorithm_capabilities)
        return {"tasks": list(TASKS), "algorithms": available, "unavailable": unavailable}

    # -- datasets ------------------------------------------------------------

    @api.post("/v1/datasets/{vhost}/{name}/rows")
    async def append_rows(vhost: str, name: str, request: Request) -> dict[str, Any]:
        validate_name(vhost, "vhost")
        validate_name(name, "dataset")
        body = await read_json_object(request, limit)
        if "rows" not in body:
            raise bad_request("'rows' is required.")
        replace = body.get("replace", False)
        if not isinstance(replace, bool):
            raise bad_request("'replace' must be true or false.")
        table = rows_to_table(body["rows"])
        total = await run_in_threadpool(datasets.append, vhost, name, table, replace)
        return {"name": name, "rows": total}

    @api.put("/v1/datasets/{vhost}/{name}/file")
    async def upload_file(vhost: str, name: str, request: Request, format: str = "") -> dict[str, Any]:
        validate_name(vhost, "vhost")
        validate_name(name, "dataset")
        parsers = {"csv": parse_csv, "xlsx": parse_xlsx}
        if format not in parsers:
            raise bad_request("Query parameter 'format' must be 'csv' or 'xlsx'.")
        body = await read_body(request, limit)
        table = await run_in_threadpool(parsers[format], body)
        await run_in_threadpool(datasets.replace, vhost, name, table)
        return await run_in_threadpool(datasets.info, vhost, name)

    @api.get("/v1/datasets/{vhost}")
    async def list_datasets(vhost: str) -> dict[str, Any]:
        validate_name(vhost, "vhost")
        return {"datasets": await run_in_threadpool(datasets.list, vhost)}

    @api.get("/v1/datasets/{vhost}/{name}")
    async def get_dataset(vhost: str, name: str) -> dict[str, Any]:
        validate_name(vhost, "vhost")
        validate_name(name, "dataset")
        return await run_in_threadpool(datasets.info_with_sample, vhost, name)

    @api.delete("/v1/datasets/{vhost}/{name}", status_code=204)
    async def delete_dataset(vhost: str, name: str) -> Response:
        validate_name(vhost, "vhost")
        validate_name(name, "dataset")
        await run_in_threadpool(datasets.delete, vhost, name)
        return Response(status_code=204)

    # -- training ------------------------------------------------------------

    @api.post("/v1/models/{vhost}/{name}/train")
    async def train(vhost: str, name: str, request: Request) -> dict[str, Any]:
        validate_name(vhost, "vhost")
        validate_name(name, "model")
        body = await read_json_object(request, limit)
        return await run_in_threadpool(trainer.train, vhost, name, body)

    @api.get("/v1/models/{vhost}/{name}/versions")
    async def list_versions(vhost: str, name: str) -> dict[str, Any]:
        validate_name(vhost, "vhost")
        validate_name(name, "model")
        return {"versions": await run_in_threadpool(models.versions, vhost, name)}

    @api.delete("/v1/models/{vhost}/{name}", status_code=204)
    async def delete_model(vhost: str, name: str) -> Response:
        validate_name(vhost, "vhost")
        validate_name(name, "model")
        await run_in_threadpool(models.delete, vhost, name)
        server.evict(vhost, name)
        return Response(status_code=204)

    # -- serving (OIP v2) ----------------------------------------------------

    @api.get("/vhosts/{vhost}/v2/models/{name}")
    async def model_metadata(vhost: str, name: str) -> dict[str, Any]:
        return await run_in_threadpool(server.metadata, vhost, name, None)

    @api.get("/vhosts/{vhost}/v2/models/{name}/versions/{version}")
    async def model_version_metadata(vhost: str, name: str, version: str) -> dict[str, Any]:
        return await run_in_threadpool(server.metadata, vhost, name, version)

    @api.post("/vhosts/{vhost}/v2/models/{name}/infer")
    async def infer(vhost: str, name: str, request: Request) -> dict[str, Any]:
        body = await read_json_object(request, limit)
        return await run_in_threadpool(server.infer, vhost, name, None, body)

    @api.post("/vhosts/{vhost}/v2/models/{name}/versions/{version}/infer")
    async def infer_version(vhost: str, name: str, version: str, request: Request) -> dict[str, Any]:
        body = await read_json_object(request, limit)
        return await run_in_threadpool(server.infer, vhost, name, version, body)

    app.include_router(api)
    return app
