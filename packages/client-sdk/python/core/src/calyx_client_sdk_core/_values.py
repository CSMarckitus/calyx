"""The functions that the converters use for one value. A request checks each value itself: protobuf
accepts some wrong types, and its errors hold the value."""

from collections.abc import Callable, Mapping, Sequence
from datetime import UTC, datetime
from typing import cast

from google.protobuf.timestamp_pb2 import Timestamp


class WrongArgumentError(Exception):
    """An argument of a call has a value that the core cannot get. The text has the path of the
    argument, and no value, because a value can be private."""


class WrongShapeError(Exception):
    """An answer of the core does not fit its class. The text has no value."""


def check_text(path: str, value: object) -> str:
    if isinstance(value, str):
        try:
            value.encode()
            return value
        except UnicodeEncodeError:
            pass
    # Outside the "except": the error of Python holds the text, and it would be the context.
    raise WrongArgumentError(f"{path} must be a str of valid Unicode.")


def check_bool(path: str, value: object) -> bool:
    if isinstance(value, bool):
        return value
    raise WrongArgumentError(f"{path} must be a bool.")


def check_int32(path: str, value: object) -> int:
    # bool is a kind of int.
    if isinstance(value, int) and not isinstance(value, bool) and -(2**31) <= value < 2**31:
        return value
    raise WrongArgumentError(f"{path} must be an int of 32 bits.")


def check_constant(path: str, value: object, names: Mapping[int, str], prefix: str) -> str:
    if isinstance(value, str) and value in names.values():
        return prefix + value
    raise WrongArgumentError(f"{path} must be one of {', '.join(names.values())}.")


def check_optional[T](check: Callable[[str, object], T], path: str, value: object) -> T | None:
    return None if value is None else check(path, value)


def check_optional_bool(path: str, value: object) -> bool:
    # None leaves a field out, but the types of protobuf do not accept it for a bool.
    return cast(bool, check_optional(check_bool, path, value))


def check_list[T](check: Callable[[str, object], T], path: str, value: object) -> list[T]:
    # A str is a Sequence too: the core would get one item for each character.
    if isinstance(value, Sequence) and not isinstance(value, str | bytes):
        items = enumerate(cast(Sequence[object], value))
        return [check(f"{path}[{i}]", item) for i, item in items]
    raise WrongArgumentError(f"{path} must be a list or a tuple.")


def check_member[T](
    kind: type, check: Callable[[str, object], T], path: str, value: object
) -> T | None:
    return check(path, value) if isinstance(value, kind) else None


def require(present: bool, path: str) -> None:
    if not present:
        raise WrongShapeError(f"{path} is missing.")


def if_present[A, T](present: bool, read: Callable[[A], T], value: A) -> T | None:
    return read(value) if present else None


def utc_date(value: Timestamp) -> datetime:
    try:
        return value.ToDatetime(tzinfo=UTC)
    except ValueError:
        pass
    # Outside the "except", as in check_text.
    raise WrongShapeError("A date of the answer is out of range.")


def utc_text(value: Timestamp) -> str | None:
    """Gives the text of a webhook: RFC 3339 with no zeros at the end of the fraction. None: the
    time is out of range."""
    try:
        text = value.ToJsonString()
    except ValueError:
        return None
    return text.removesuffix("Z").rstrip("0") + "Z" if "." in text else text
