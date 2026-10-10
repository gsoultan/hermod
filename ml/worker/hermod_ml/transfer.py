"""Moving datasets to a training pool and trained versions back.

A training on a separate worker pool (custom scripts, GPU) goes:

    serving worker  GET  /v1/datasets/{vhost}/{name}/export          parquet
    pool            PUT  /v1/datasets/{vhost}/{job}/import           parquet
    pool            POST /v1/models/{vhost}/{job}/train[-custom]
    pool            GET  /v1/models/{vhost}/{job}/versions/{v}/export {"meta", "onnx"}
    serving worker  POST /v1/models/{vhost}/{name}/import            {"meta", "onnx"}

Hermod pipes each body from one worker to the next without reading it. The
pool keeps nothing: Hermod deletes the job's dataset and model afterwards.
The serving worker trusts nothing the pool sends: an imported version is
checked as a custom script's output is (artifacts.py) before it is saved.

The routes are added by `add_routes`, so the app module only wires them up.
"""

from __future__ import annotations

import base64
import binascii
import io
from typing import Any

import pyarrow as pa
import pyarrow.parquet as pq
from fastapi import APIRouter, Request
from fastapi.responses import Response
from starlette.concurrency import run_in_threadpool

from .artifacts import check_meta, check_onnx
from .datasets import BOOL, NUMBER, DatasetStore, _kind_of
from .errors import bad_request
from .names import validate_name
from .training import ModelStore

PARQUET = "application/vnd.apache.parquet"


def export_dataset(datasets: DatasetStore, vhost: str, name: str) -> bytes:
    """The whole dataset as one parquet file, with its column types kept."""
    frame, types = datasets.load(vhost, name)
    columns = {}
    for col in frame.columns:
        values = frame[col]
        if types[col] == NUMBER:
            columns[col] = pa.array(values.to_numpy(dtype="float64"), type=pa.float64(), from_pandas=True)
        elif types[col] == BOOL:
            columns[col] = pa.array([None if v is None else bool(v) for v in values], type=pa.bool_())
        else:
            columns[col] = pa.array(list(values), type=pa.string())
    buf = io.BytesIO()
    pq.write_table(pa.table(columns) if columns else pa.table({}), buf)
    return buf.getvalue()


def import_dataset(datasets: DatasetStore, vhost: str, name: str, body: bytes) -> dict[str, Any]:
    """Replace the dataset with a parquet file of bool, number and string columns."""
    try:
        table = pq.read_table(pa.BufferReader(body))
    except (pa.ArrowException, OSError, ValueError):
        raise bad_request("The body is not a parquet file.") from None
    for field in table.schema:
        if _kind_of(field.type) is not None and not (
            pa.types.is_boolean(field.type)
            or pa.types.is_integer(field.type)
            or pa.types.is_floating(field.type)
            or pa.types.is_string(field.type)
            or pa.types.is_large_string(field.type)
        ):
            raise bad_request(f"Column {field.name!r} has type {field.type}; a dataset holds bool, number and string columns.")
    datasets.replace(vhost, name, table)
    return datasets.info(vhost, name)


def export_version(models: ModelStore, vhost: str, name: str, version: str) -> dict[str, Any]:
    resolved = models.resolve(vhost, name, version)
    meta = models.meta(vhost, name, resolved)
    onnx_bytes = models.onnx_path(vhost, name, resolved).read_bytes()
    return {"meta": meta, "onnx": base64.b64encode(onnx_bytes).decode("ascii")}


def import_version(models: ModelStore, vhost: str, name: str, dataset: str | None, body: dict[str, Any]) -> dict[str, Any]:
    """Check and save a version trained elsewhere; it gets this worker's next number."""
    if dataset is not None:
        validate_name(dataset, "dataset")
    encoded = body.get("onnx")
    if not isinstance(encoded, str) or not encoded:
        raise bad_request("'onnx' is required: the model file, base64-encoded.")
    try:
        model_bytes = base64.b64decode(encoded, validate=True)
    except (binascii.Error, ValueError):
        raise bad_request("'onnx' is not valid base64.") from None
    meta = check_meta(body.get("meta"))
    check_onnx(model_bytes, meta)
    meta = {k: v for k, v in meta.items() if k != "version"}
    meta["model"] = name
    if dataset is not None:
        meta["dataset"] = dataset
    return models.save(vhost, name, model_bytes, meta)


def add_routes(api: APIRouter, datasets: DatasetStore, models: ModelStore, on_saved, read_body, read_json_object, limit: int) -> None:
    """Mount the transfer routes on the authenticated router."""

    @api.get("/v1/datasets/{vhost}/{name}/export")
    async def export_dataset_route(vhost: str, name: str) -> Response:
        validate_name(vhost, "vhost")
        validate_name(name, "dataset")
        data = await run_in_threadpool(export_dataset, datasets, vhost, name)
        return Response(content=data, media_type=PARQUET)

    @api.put("/v1/datasets/{vhost}/{name}/import")
    async def import_dataset_route(vhost: str, name: str, request: Request) -> dict[str, Any]:
        validate_name(vhost, "vhost")
        validate_name(name, "dataset")
        body = await read_body(request, limit)
        return await run_in_threadpool(import_dataset, datasets, vhost, name, body)

    @api.get("/v1/models/{vhost}/{name}/versions/{version}/export")
    async def export_version_route(vhost: str, name: str, version: str) -> dict[str, Any]:
        validate_name(vhost, "vhost")
        validate_name(name, "model")
        return await run_in_threadpool(export_version, models, vhost, name, version)

    @api.post("/v1/models/{vhost}/{name}/import")
    async def import_version_route(vhost: str, name: str, request: Request, dataset: str | None = None) -> dict[str, Any]:
        validate_name(vhost, "vhost")
        validate_name(name, "model")
        body = await read_json_object(request, limit)
        meta = await run_in_threadpool(import_version, models, vhost, name, dataset, body)
        on_saved(vhost, name)
        return meta
