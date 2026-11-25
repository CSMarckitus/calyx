"""The checks of both clients, the backend client and the core client. They take `object`, because a
program without a type checker can pass any value."""

import re
import sys

from ._generated.codes import ClientCode
from .errors import CalyxError, Source


def config_error(detail: str, key: str | None = None) -> CalyxError:
    return CalyxError(
        source=Source.CLIENT,
        code=ClientCode.INVALID_CONFIG,
        status=0,
        detail=detail,
        idempotency_key=key,
    )


def is_visible_ascii(value: object) -> bool:
    return isinstance(value, str) and re.fullmatch("[!-~]*", value) is not None


def check_time_limit(value: object, key: str | None = None) -> float:
    # bool is a kind of int. A comparison with NaN is false.
    if (
        isinstance(value, int | float)
        and not isinstance(value, bool)
        and 0 < value <= sys.float_info.max
    ):
        return float(value)
    raise config_error("The time limit must be a number of seconds above 0, and not infinity.", key)
