"""Validation for every name that ends up in a filesystem path.

vhost, dataset, model and version names all go through `validate_name`
before any path is built from them. The pattern forbids path separators,
leading dots and anything outside a small ASCII set, so a validated name can
never escape its parent directory.
"""

from __future__ import annotations

import re

from .errors import bad_request

# Anchored with \Z rather than $ so a trailing newline cannot slip through.
NAME_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,127}\Z")


def validate_name(value: str, kind: str) -> str:
    """Return `value` unchanged if it is a legal name, else raise a 400."""
    if not isinstance(value, str) or value in (".", "..") or not NAME_RE.match(value):
        raise bad_request(
            f"Invalid {kind} name {value!r}: use 1-128 letters, digits, '_', '.' or '-', "
            "starting with a letter or digit."
        )
    return value
