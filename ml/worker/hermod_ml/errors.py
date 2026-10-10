"""The single error type the HTTP layer turns into `{"error": "..."}`."""

from __future__ import annotations


class ApiError(Exception):
    """An error with an HTTP status and a plain-sentence message for the caller.

    Raise it from anywhere below the routes; the app's exception handler
    renders it as JSON with the given status.
    """

    def __init__(self, status: int, message: str) -> None:
        super().__init__(message)
        self.status = status
        self.message = message


def bad_request(message: str) -> ApiError:
    return ApiError(400, message)


def not_found(message: str) -> ApiError:
    return ApiError(404, message)
