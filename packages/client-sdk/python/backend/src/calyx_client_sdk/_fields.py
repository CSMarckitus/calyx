import json
from collections.abc import Sequence
from dataclasses import MISSING, fields, is_dataclass
from datetime import UTC, datetime
from functools import cache
from types import NoneType
from typing import Any, cast, get_args, get_origin, get_type_hints


class NotJsonError(Exception):
    """The body of an answer or of a webhook, or the data of an event, is not JSON."""


def parse_json(body: bytes) -> object:
    try:
        # json.loads accepts more encodings when it gets bytes.
        value: object = json.loads(body.decode(), parse_constant=_refuse_constant)
        return value
    # RecursionError: a deep nesting. ValueError: also a number with too many digits for Python.
    except (ValueError, RecursionError) as error:
        reason = str(error)
    # Outside the "except": the error of Python holds the body, and it would be the context.
    raise NotJsonError(reason)


def _refuse_constant(name: str) -> None:
    raise ValueError(f"{name} is not JSON.")


class WrongShapeError(Exception):
    """The JSON of an answer does not fit its class. The text has the path of the field, and no
    value, because a value can be private."""


def read[T](kind: type[T], value: object) -> T:
    """Makes an object of a dataclass from the JSON of an answer. The annotations of the class give
    the types. A field with a default is not required. The "json" of the metadata of a field is its
    name in the JSON, when it differs."""
    result: T = _read(kind, value, kind.__name__)
    return result


def to_json(value: object) -> object:
    """Makes the JSON of an argument of a request. A field that is None is left out. The names
    are the ones of the attributes: a class with a "json" in its metadata is for answers only."""
    if is_dataclass(value):
        items = ((f.name, getattr(value, f.name)) for f in fields(value))
        return {name: to_json(item) for name, item in items if item is not None}
    if isinstance(value, Sequence) and not isinstance(value, str | bytes):
        return [to_json(item) for item in cast(Sequence[object], value)]
    return value


def _read(kind: Any, value: object, path: str) -> Any:
    arguments = get_args(kind)
    if NoneType in arguments:
        if value is None:
            return None
        return _read(next(a for a in arguments if a is not NoneType), value, path)
    if get_origin(kind) is tuple:
        if not isinstance(value, list):
            raise WrongShapeError(f"{path} is not a list.")
        items = enumerate(cast(list[object], value))
        return tuple(_read(arguments[0], item, f"{path}[{i}]") for i, item in items)
    if is_dataclass(kind):
        return _read_object(kind, value, path)
    if kind is datetime:
        return _read_date(value, path)
    if kind is float and type(value) is int:
        try:
            return float(value)
        except OverflowError:
            raise WrongShapeError(f"{path} is not a number.") from None
    # Not isinstance: a bool is also an int in Python.
    if type(value) is not kind:
        raise WrongShapeError(f"{path} is not {_NAMES[kind]}.")
    return value


_NAMES = {str: "a string", int: "an integer", float: "a number", bool: "a boolean"}


def _read_object(kind: Any, value: object, path: str) -> Any:
    if not isinstance(value, dict):
        raise WrongShapeError(f"{path} is not an object.")
    data = cast(dict[str, object], value)
    arguments: dict[str, Any] = {}
    for name, wire, annotation, required in _shape(kind):
        if wire in data and (required or data[wire] is not None):
            arguments[name] = _read(annotation, data[wire], f"{path}.{wire}")
        elif required:
            raise WrongShapeError(f"{path}.{wire} is missing.")
    return kind(**arguments)


@cache
def _shape(kind: Any) -> list[tuple[str, str, Any, bool]]:
    """Gives the name, the name in the JSON, the type and the "required" of each field."""
    hints = get_type_hints(kind)
    return [
        (f.name, f.metadata.get("json", f.name), hints[f.name], f.default is MISSING)
        for f in fields(kind)
    ]


def _read_date(value: object, path: str) -> datetime:
    if isinstance(value, str):
        try:
            date = datetime.fromisoformat(value)
            # A date with no time zone has no clear meaning, so the SDK refuses it.
            if date.tzinfo is not None:
                return date.astimezone(UTC)
        except (ValueError, OverflowError):
            pass
    raise WrongShapeError(f"{path} is not a date with a time zone.")
