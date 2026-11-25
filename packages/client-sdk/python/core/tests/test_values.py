"""Checks each function that the converters use for one value."""

import re
from collections import deque
from collections.abc import Callable, Sequence
from datetime import UTC, datetime
from enum import StrEnum

import pytest
from google.protobuf.timestamp_pb2 import Timestamp

from calyx_client_sdk_core._values import (
    WrongArgumentError,
    WrongShapeError,
    check_bool,
    check_constant,
    check_int32,
    check_list,
    check_member,
    check_optional,
    check_optional_bool,
    check_text,
    if_present,
    require,
    utc_date,
)

NAMES = {0: "UNSPECIFIED", 1: "FIRST"}


class Name(StrEnum):
    FIRST = "FIRST"


def check_name(path: str, value: object) -> str:
    return check_constant(path, value, NAMES, "PREFIX_")


def refused(check: Callable[[str, object], object], value: object, text: str) -> None:
    """The whole text is equal, so it has no value. No other error holds the value either."""
    with pytest.raises(WrongArgumentError, match=f"^{re.escape(text)}$") as info:
        check("a.b", value)
    assert info.value.__cause__ is None
    assert info.value.__context__ is None


@pytest.mark.parametrize("value", ["", "a", "ñ\U0001f600", "\x00"])
def test_a_text(value: str) -> None:
    assert check_text("a.b", value) == value


@pytest.mark.parametrize("value", [None, b"a", 5, True, ["a"], "a\ud800", "\udfff"])
def test_a_wrong_text(value: object) -> None:
    refused(check_text, value, "a.b must be a str of valid Unicode.")


def test_a_bool() -> None:
    assert check_bool("a.b", True) is True
    assert check_bool("a.b", False) is False


@pytest.mark.parametrize("value", [None, 0, 1, "true", b"", []])
def test_a_wrong_bool(value: object) -> None:
    refused(check_bool, value, "a.b must be a bool.")


@pytest.mark.parametrize("value", [0, 5, -5, 2**31 - 1, -(2**31)])
def test_an_int32(value: int) -> None:
    assert check_int32("a.b", value) == value


@pytest.mark.parametrize("value", [None, True, False, 1.0, "1", b"x", 2**31, -(2**31) - 1, 10**40])
def test_a_wrong_int32(value: object) -> None:
    refused(check_int32, value, "a.b must be an int of 32 bits.")


@pytest.mark.parametrize("value", ["FIRST", Name.FIRST], ids=["str", "StrEnum"])
def test_a_constant(value: str) -> None:
    result = check_name("a.b", value)
    assert result == "PREFIX_FIRST"
    assert type(result) is str


@pytest.mark.parametrize("value", [None, "", "first", "PREFIX_FIRST", "SECOND", "1", 5, b"x"])
def test_a_wrong_constant(value: object) -> None:
    refused(check_name, value, "a.b must be one of UNSPECIFIED, FIRST.")


def test_an_optional_value() -> None:
    assert check_optional(check_text, "a.b", None) is None
    assert check_optional(check_text, "a.b", "") == ""
    with pytest.raises(WrongArgumentError, match=r"^a\.b must be a str"):
        check_optional(check_text, "a.b", 0)


def test_an_optional_bool() -> None:
    assert check_optional_bool("a.b", None) is None
    assert check_optional_bool("a.b", False) is False
    assert check_optional_bool("a.b", True) is True
    refused(check_optional_bool, 0, "a.b must be a bool.")


@pytest.mark.parametrize("value", [["x", "y"], ("x", "y"), deque(["x", "y"])])
def test_a_list(value: Sequence[str]) -> None:
    result = check_list(check_text, "a.b", value)
    assert result == ["x", "y"]
    assert result is not value


def test_an_empty_list() -> None:
    assert check_list(check_text, "a.b", ()) == []


@pytest.mark.parametrize(
    "value",
    [None, pytest.param("", id="empty"), "text", b"", 5, {"x"}, {"x": "y"}, iter(["x"]), {}.keys()],
    ids=lambda value: type(value).__name__,
)
def test_a_wrong_list(value: object) -> None:
    refused(
        lambda path, v: check_list(check_text, path, v), value, "a.b must be a list or a tuple."
    )


def test_the_path_of_a_wrong_item() -> None:
    refused(
        lambda path, v: check_list(check_text, path, v),
        ["x", "y", 5],
        "a.b[2] must be a str of valid Unicode.",
    )


def test_a_member() -> None:
    assert check_member(str, check_text, "a.b", "x") == "x"
    assert check_member(int, check_text, "a.b", "x") is None


def test_require() -> None:
    require(True, "A.b")
    with pytest.raises(WrongShapeError, match=r"^A\.b is missing\.$"):
        require(False, "A.b")


def test_if_present() -> None:
    assert if_present(True, str, 5) == "5"
    assert if_present(False, str, 5) is None


@pytest.mark.parametrize(
    ("seconds", "nanos", "want"),
    [
        (0, 0, datetime(1970, 1, 1, tzinfo=UTC)),
        (1795343400, 250000000, datetime(2026, 11, 22, 10, 30, 0, 250000, tzinfo=UTC)),
        (0, 1999, datetime(1970, 1, 1, 0, 0, 0, 1, tzinfo=UTC)),
        (-1, 0, datetime(1969, 12, 31, 23, 59, 59, tzinfo=UTC)),
        (-62135596800, 0, datetime(1, 1, 1, tzinfo=UTC)),
        (253402300799, 0, datetime(9999, 12, 31, 23, 59, 59, tzinfo=UTC)),
    ],
)
def test_a_date(seconds: int, nanos: int, want: datetime) -> None:
    date = utc_date(Timestamp(seconds=seconds, nanos=nanos))
    assert date == want
    assert date.tzinfo is UTC


@pytest.mark.parametrize(
    ("seconds", "nanos"),
    [(253402300800, 0), (-62135596801, 0), (2**63 - 1, 0), (0, -1), (0, 10**9)],
)
def test_a_date_out_of_range(seconds: int, nanos: int) -> None:
    with pytest.raises(WrongShapeError, match=r"^A date of the answer is out of range\.$") as info:
        utc_date(Timestamp(seconds=seconds, nanos=nanos))
    assert info.value.__cause__ is None
    assert info.value.__context__ is None
