from __future__ import annotations

from dataclasses import dataclass, field
from datetime import UTC, datetime
from typing import Any

import pytest

from calyx_client_sdk._fields import WrongShapeError, read, to_json
from calyx_client_sdk.models import BulkPacingInput, MetaCredentialsInput, RangeInput, WebhookInput


@dataclass(frozen=True, slots=True, kw_only=True)
class Child:
    name: str


@dataclass(frozen=True, slots=True, kw_only=True)
class Sample:
    text: str
    count: int
    share: float
    ready: bool
    at: datetime
    child: Child
    children: tuple[Child, ...]
    words: tuple[str, ...]
    nullable: str | None
    nullable_at: datetime | None
    nullable_child: Child | None
    optional: int | None = None
    optional_child: Child | None = None
    optional_words: tuple[str, ...] | None = None


NOON = datetime(2026, 11, 22, 12, 0, tzinfo=UTC)


def answer(**changes: object) -> dict[str, object]:
    return {
        "text": "a",
        "count": 1,
        "share": 1.5,
        "ready": True,
        "at": "2026-11-22T12:00:00Z",
        "child": {"name": "b"},
        "children": [{"name": "c"}, {"name": "d"}],
        "words": ["e", "f"],
        "nullable": None,
        "nullable_at": None,
        "nullable_child": None,
    } | changes


def test_an_answer_becomes_an_object() -> None:
    assert read(Sample, answer()) == Sample(
        text="a",
        count=1,
        share=1.5,
        ready=True,
        at=NOON,
        child=Child(name="b"),
        children=(Child(name="c"), Child(name="d")),
        words=("e", "f"),
        nullable=None,
        nullable_at=None,
        nullable_child=None,
    )


def test_a_value_in_each_nullable_and_optional_field() -> None:
    sample = read(
        Sample,
        answer(
            nullable="g",
            nullable_at="2026-11-22T12:00:00Z",
            nullable_child={"name": "h"},
            optional=2,
            optional_child={"name": "i"},
            optional_words=["j"],
        ),
    )
    assert sample.nullable == "g"
    assert sample.nullable_at == NOON
    assert sample.nullable_child == Child(name="h")
    assert sample.optional == 2
    assert sample.optional_child == Child(name="i")
    assert sample.optional_words == ("j",)


def test_an_unknown_field_is_ignored_at_each_depth() -> None:
    data = answer(new={"x": 1}, child={"name": "b", "new": [1]})
    assert read(Sample, data) == read(Sample, answer())


def test_an_optional_field_is_none_when_it_is_null() -> None:
    sample = read(Sample, answer(optional=None, optional_child=None, optional_words=None))
    assert sample == read(Sample, answer())


@pytest.mark.parametrize("name", answer())
def test_a_required_field_is_not_absent(name: str) -> None:
    data = answer()
    del data[name]
    with pytest.raises(WrongShapeError) as caught:
        read(Sample, data)
    assert str(caught.value) == f"Sample.{name} is missing."


@pytest.mark.parametrize(
    ("changes", "text"),
    [
        ({"text": None}, "Sample.text is not a string."),
        ({"text": 5}, "Sample.text is not a string."),
        ({"count": "1"}, "Sample.count is not an integer."),
        ({"count": 1.0}, "Sample.count is not an integer."),
        ({"count": True}, "Sample.count is not an integer."),
        ({"share": "1.5"}, "Sample.share is not a number."),
        ({"share": False}, "Sample.share is not a number."),
        ({"share": 10**400}, "Sample.share is not a number."),
        ({"ready": 1}, "Sample.ready is not a boolean."),
        ({"ready": "true"}, "Sample.ready is not a boolean."),
        ({"at": None}, "Sample.at is not a date with a time zone."),
        ({"at": 5}, "Sample.at is not a date with a time zone."),
        ({"child": None}, "Sample.child is not an object."),
        ({"child": []}, "Sample.child is not an object."),
        ({"child": {}}, "Sample.child.name is missing."),
        ({"child": {"name": 1}}, "Sample.child.name is not a string."),
        ({"children": None}, "Sample.children is not a list."),
        ({"children": {}}, "Sample.children is not a list."),
        ({"children": "cd"}, "Sample.children is not a list."),
        ({"children": [{"name": "c"}, None]}, "Sample.children[1] is not an object."),
        ({"children": [{"name": "c"}, {}]}, "Sample.children[1].name is missing."),
        ({"words": ["e", 5]}, "Sample.words[1] is not a string."),
        ({"nullable": 5}, "Sample.nullable is not a string."),
        ({"nullable_child": "x"}, "Sample.nullable_child is not an object."),
        ({"optional": "2"}, "Sample.optional is not an integer."),
        ({"optional_words": "j"}, "Sample.optional_words is not a list."),
    ],
)
def test_a_wrong_json_type(changes: dict[str, object], text: str) -> None:
    with pytest.raises(WrongShapeError) as caught:
        read(Sample, answer(**changes))
    assert str(caught.value) == text


@pytest.mark.parametrize("value", [None, [], "text", 5, True])
def test_the_whole_answer_is_an_object(value: object) -> None:
    with pytest.raises(WrongShapeError) as caught:
        read(Sample, value)
    assert str(caught.value) == "Sample is not an object."


def test_a_number_field_accepts_an_integer() -> None:
    share = read(Sample, answer(share=50)).share
    assert share == 50
    assert type(share) is float


@pytest.mark.parametrize(
    ("text", "expected"),
    [
        ("2026-11-22T12:00:00Z", NOON),
        ("2026-11-22T12:00:00.5Z", NOON.replace(microsecond=500000)),
        ("2026-11-22T12:00:00.123456Z", NOON.replace(microsecond=123456)),
        # Go writes up to 9 digits. A datetime has 6.
        ("2026-11-22T12:00:00.123456789Z", NOON.replace(microsecond=123456)),
        ("2026-11-22T14:30:00+02:30", NOON),
        ("2026-11-22T07:00:00.000-05:00", NOON),
        ("0001-01-01T00:00:00Z", datetime.min.replace(tzinfo=UTC)),
    ],
)
def test_a_date_is_in_utc(text: str, expected: datetime) -> None:
    date = read(Sample, answer(at=text)).at
    assert date == expected
    assert date.tzinfo is UTC


@pytest.mark.parametrize(
    "text",
    [
        "",
        "soon",
        "2026-11-22",
        "2026-11-22T12:00:00",
        "2026-13-22T12:00:00Z",
        "0001-01-01T00:00:00+14:00",
        "9999-12-31T23:59:59-14:00",
        "\ud800",
    ],
)
def test_a_bad_date(text: str) -> None:
    with pytest.raises(WrongShapeError) as caught:
        read(Sample, answer(at=text))
    assert str(caught.value) == "Sample.at is not a date with a time zone."


@dataclass(frozen=True, slots=True, kw_only=True)
class Renamed:
    from_: str = field(metadata={"json": "from"})
    chat_id: str | None = field(default=None, metadata={"json": "chatId"})


def test_a_field_with_another_name_in_the_json() -> None:
    assert read(Renamed, {"from": "a", "chatId": "b"}) == Renamed(from_="a", chat_id="b")
    assert read(Renamed, {"from": "a", "chat_id": "b"}) == Renamed(from_="a")
    for data, text in [
        ({"from_": "a"}, "Renamed.from is missing."),
        ({"from": "a", "chatId": 5}, "Renamed.chatId is not a string."),
    ]:
        with pytest.raises(WrongShapeError) as caught:
            read(Renamed, data)
        assert str(caught.value) == text


def test_the_error_does_not_have_the_value() -> None:
    with pytest.raises(WrongShapeError) as caught:
        read(Sample, answer(at="private text"))
    assert "private text" not in str(caught.value)


@pytest.mark.parametrize(
    ("value", "expected"),
    [
        ("text", "text"),
        (5, 5),
        (1.5, 1.5),
        (False, False),
        (None, None),
        (["a", "b"], ["a", "b"]),
        (("a", "b"), ["a", "b"]),
        ([], []),
        (RangeInput(min=0, max=2), {"min": 0, "max": 2}),
        (BulkPacingInput(), {}),
        (
            BulkPacingInput(delay=RangeInput(min=1, max=2)),
            {"delay": {"min": 1, "max": 2}},
        ),
        (
            (WebhookInput(url="u", event="e"), WebhookInput(url="u", event="e", secret="")),
            [{"url": "u", "event": "e"}, {"url": "u", "event": "e", "secret": ""}],
        ),
        (
            MetaCredentialsInput(phone_number_id="1", access_token="t", app_secret="s"),
            {"phone_number_id": "1", "access_token": "t", "app_secret": "s"},
        ),
    ],
)
def test_a_request_value_becomes_json(value: object, expected: Any) -> None:
    result = to_json(value)
    assert result == expected
    assert type(result) is type(expected)
