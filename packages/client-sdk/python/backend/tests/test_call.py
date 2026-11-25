import json
import math
from collections.abc import Iterator
from dataclasses import dataclass
from decimal import Decimal
from pathlib import Path
from typing import Any, cast
from uuid import UUID

import pytest

from calyx_client_sdk import CalyxError, ClientCode, FieldError, RetryAdvice, Source
from calyx_client_sdk import __version__ as version
from calyx_client_sdk._call import (
    CREATE_CODES,
    CREATE_OPERATIONS,
    CREATE_RETRY,
    Config,
    Operation,
    Prepared,
    RawAnswer,
    client_error,
    finish,
    make_config,
    prepare,
    read_model,
    read_nothing,
    read_status,
)
from calyx_client_sdk._fields import WrongShapeError
from calyx_client_sdk.models import RangeInput, WebhookInput

TESTDATA = Path(__file__).parents[3] / "testdata"
ERROR_CASES = json.loads((TESTDATA / "backend-errors.json").read_text())["cases"]
CREATE_CALLS = json.loads((TESTDATA / "create-calls.json").read_text())
API_KEY = "cx_secret"


@dataclass(frozen=True, slots=True, kw_only=True)
class Thing:
    id: str


GET = Operation(
    id="getThing", method="GET", path="/api/v1/things/{id}", time_limit=120, read=read_model(Thing)
)
LIST = Operation(
    id="listThings", method="GET", path="/api/v1/things", time_limit=120, read=read_model(Thing)
)
UPDATE = Operation(
    id="updateThing",
    method="PATCH",
    path="/api/v1/things/{id}",
    time_limit=120,
    read=read_model(Thing),
)
CANCEL = Operation(
    id="cancelThing",
    method="POST",
    path="/api/v1/things/{id}/cancel",
    time_limit=120,
    read=read_model(Thing),
)
DELETE = Operation(
    id="deleteThing", method="DELETE", path="/api/v1/things/{id}", time_limit=120, read=read_nothing
)
REMOVE = Operation(
    id="removeThing", method="DELETE", path="/api/v1/things/{id}", time_limit=120, read=read_status
)
SEND = Operation(
    id="sendThing",
    method="POST",
    path="/api/v1/things/{id}/messages",
    time_limit=360,
    read=read_model(Thing),
)


def create(operation_id: str = "createClient") -> Operation[Thing]:
    return Operation(
        id=operation_id,
        method="POST",
        path="/api/v1/things",
        time_limit=120,
        read=read_model(Thing),
    )


def config(
    base_url: Any = "https://calyx.example.com", api_key: Any = API_KEY, time_limit: Any = None
) -> Config:
    return make_config(base_url, api_key, time_limit)


def prepare_send(**changes: Any) -> Prepared[Thing]:
    arguments: dict[str, Any] = {
        "path_id": "t1",
        "arguments": {"text": "Hello"},
        "idempotency_key": "key-1",
    }
    return prepare(config(), SEND, **(arguments | changes))


def answer(
    status: int = 200, body: bytes = b'{"id":"t1"}', headers: dict[str, str] | None = None
) -> RawAnswer:
    items = (headers or {}).items()
    return RawAnswer(status=status, headers=[(n.encode(), v.encode()) for n, v in items], body=body)


def refused(caught: pytest.ExceptionInfo[CalyxError], code: str) -> CalyxError:
    error = caught.value
    assert error.source is Source.CLIENT
    assert error.code == code
    assert error.status == 0
    return error


def test_the_url_and_the_method() -> None:
    prepared = prepare(config(), GET, path_id="t1")
    assert prepared.url == "https://calyx.example.com/api/v1/things/t1"
    assert prepared.method == "GET"
    assert prepare(config(), LIST).url == "https://calyx.example.com/api/v1/things"


@pytest.mark.parametrize(
    ("base_url", "expected"),
    [
        ("https://calyx.example.com/", "https://calyx.example.com/api/v1/things"),
        ("http://localhost:8080", "http://localhost:8080/api/v1/things"),
        ("http://[::1]:8080/", "http://[::1]:8080/api/v1/things"),
        ("https://example.com/calyx/", "https://example.com/calyx/api/v1/things"),
        ("HTTPS://example.com", "HTTPS://example.com/api/v1/things"),
    ],
)
def test_the_base_url_loses_its_final_slash(base_url: str, expected: str) -> None:
    assert prepare(config(base_url), LIST).url == expected


@pytest.mark.parametrize(
    "base_url",
    [
        "",
        None,
        b"https://example.com",
        "example.com",
        "localhost:8080",
        "ftp://example.com",
        "https://",
        "https:///path",
        "https://:8080",
        "https://user:private@example.com",
        "https://private@example.com",
        "https://example.com/@private",
        "https://example.com?private=1",
        "https://example.com#private",
        "https://example.com?",
        "https://example.com\\private",
        "https://example.com:private",
        "https://example.com:99999",
        "https://[private",
        "https://private example.com",
        " https://private.example.com",
        "https://private.example.com\n",
        "https://private.exämple.com",
        # The name lookup of Python raises UnicodeError for these two.
        "https://private..example.com",
        f"https://{'private' * 10}.example.com",
    ],
)
def test_a_bad_base_url_gives_invalid_config(base_url: object) -> None:
    with pytest.raises(CalyxError) as caught:
        config(base_url)
    error = refused(caught, ClientCode.INVALID_CONFIG)
    assert error.retry is RetryAdvice.DO_NOT_RETRY
    assert "private" not in error.detail


@pytest.mark.parametrize(
    ("path_id", "expected"),
    [
        ("a b", "a%20b"),
        ("a/b", "a%2Fb"),
        ("../me", "..%2Fme"),
        ("a?b#c", "a%3Fb%23c"),
        ("100%", "100%25"),
        ("clé", "cl%C3%A9"),
        ("{id}", "%7Bid%7D"),
        ("\r\n", "%0D%0A"),
        ("a-b_c.d~e", "a-b_c.d~e"),
    ],
)
def test_the_path_parameter_is_encoded(path_id: str, expected: str) -> None:
    prepared = prepare(config(), CANCEL, path_id=path_id)
    assert prepared.url == f"https://calyx.example.com/api/v1/things/{expected}/cancel"


@pytest.mark.parametrize("path_id", [None, "", ".", "..", "\ud800", 5, UUID(int=0), b"t1"])
def test_a_bad_path_parameter_gives_invalid_config(path_id: Any) -> None:
    with pytest.raises(CalyxError) as caught:
        prepare_send(path_id=path_id)
    assert refused(caught, ClientCode.INVALID_CONFIG).idempotency_key == "key-1"


def test_the_headers_of_a_call_with_no_body() -> None:
    assert prepare(config(), LIST).headers == {
        "Accept-Encoding": "identity",
        "User-Agent": f"calyx-python-sdk/{version}",
        "Authorization": "Bearer cx_secret",
    }


def test_the_headers_of_a_send() -> None:
    assert prepare_send().headers == {
        "Accept-Encoding": "identity",
        "User-Agent": f"calyx-python-sdk/{version}",
        "Authorization": "Bearer cx_secret",
        "Content-Type": "application/json",
        "Idempotency-Key": "key-1",
    }


@pytest.mark.parametrize("api_key", [None, ""])
def test_without_an_api_key_there_is_no_authorization_header(api_key: str | None) -> None:
    assert "Authorization" not in prepare(config(api_key=api_key), LIST).headers


@pytest.mark.parametrize("api_key", ["!", "~", "".join(chr(code) for code in range(33, 127))])
def test_a_good_api_key_stays_as_it_is(api_key: str) -> None:
    prepared = prepare(config(api_key=api_key), LIST)
    assert prepared.headers["Authorization"] == f"Bearer {api_key}"


@pytest.mark.parametrize(
    "api_key",
    [
        "cx private",
        "private\n",
        " private",
        "private\r\nX-Other: 1",
        "private\x7f",
        "private\x00",
        "privaté",
        b"private",
        5,
    ],
)
def test_a_bad_api_key_gives_invalid_config(api_key: object) -> None:
    with pytest.raises(CalyxError) as caught:
        config(api_key=api_key)
    error = refused(caught, ClientCode.INVALID_CONFIG)
    assert error.retry is RetryAdvice.DO_NOT_RETRY
    assert "priva" not in error.detail


def test_repr_has_no_secret() -> None:
    prepared = prepare_send(arguments={"text": "private text"})
    raw = answer(body=b"private body", headers={"X-Private": "private header"})
    text = repr(config()) + repr(prepared) + repr(raw)
    assert API_KEY not in text
    assert "private" not in text


def chain(error: BaseException | None) -> Iterator[BaseException]:
    while error is not None:
        yield error
        if error.__cause__ is not None and error.__context__ not in (None, error.__cause__):
            yield from chain(error.__context__)
        error = error.__cause__ or error.__context__


def assert_no_private_text(error: BaseException) -> None:
    for item in chain(error):
        # JSONDecodeError and UnicodeError keep the whole text in an attribute.
        assert "private" not in f"{item!r} {item} {item.args} {vars(item)}"
        assert not hasattr(item, "doc")
        assert not hasattr(item, "object")


@pytest.mark.parametrize("value", ["\ud800", math.nan, {"a set"}, b"bytes"])
def test_an_error_of_the_body_has_no_secret(value: object) -> None:
    with pytest.raises(CalyxError) as caught:
        prepare_send(arguments={"text": "private text", "value": value})
    assert_no_private_text(caught.value)


@pytest.mark.parametrize("path_id", ["private\ud800", b"private"])
def test_an_error_of_the_path_has_no_secret(path_id: Any) -> None:
    with pytest.raises(CalyxError) as caught:
        prepare_send(path_id=path_id)
    assert_no_private_text(caught.value)


@pytest.mark.parametrize("status", [200, 503])
@pytest.mark.parametrize(
    "body",
    [
        b'{"private"',
        b'{"private":"\xff"}',
        b'{"private":NaN}',
        b'{"id":"private"',
        b'{"id":["private"]}',
        b'{"private":' + b"9" * 5000 + b"}",
    ],
    ids=lambda body: repr(body[:16]),
)
def test_an_error_of_the_answer_has_no_secret(status: int, body: bytes) -> None:
    with pytest.raises(CalyxError) as caught:
        finish(prepare_send(), answer(status, body))
    assert_no_private_text(caught.value)


def test_the_body_is_json_in_utf_8() -> None:
    prepared = prepare_send(arguments={"to": "34600", "text": "Olé 👍"})
    assert prepared.body == '{"to":"34600","text":"Olé 👍"}'.encode()


@pytest.mark.parametrize(
    ("arguments", "expected"),
    [
        ({}, {}),
        ({"name": None}, {}),
        ({"name": "n", "note": None}, {"name": "n"}),
        ({"name": ""}, {"name": ""}),
        ({"webhooks": []}, {"webhooks": []}),
        ({"webhooks": ()}, {"webhooks": []}),
        ({"pairing": False, "count": 0}, {"pairing": False, "count": 0}),
        ({"range": RangeInput(min=1, max=2)}, {"range": {"min": 1, "max": 2}}),
        (
            {"webhooks": (WebhookInput(url="u", event="e"),)},
            {"webhooks": [{"url": "u", "event": "e"}]},
        ),
    ],
)
def test_none_is_left_out_of_the_body(arguments: dict[str, object], expected: object) -> None:
    prepared = prepare(config(), UPDATE, path_id="t1", arguments=arguments)
    assert prepared.body is not None
    assert json.loads(prepared.body) == expected
    assert prepared.headers["Content-Type"] == "application/json"


@pytest.mark.parametrize("operation", [LIST, DELETE])
def test_a_call_with_no_arguments_has_no_body(operation: Operation[object]) -> None:
    assert prepare(config(), operation, path_id="t1").body is None


def test_a_post_with_no_arguments_has_an_empty_body() -> None:
    prepared = prepare(config(), CANCEL, path_id="t1")
    # The transport then sends "Content-Length: 0". Some proxies refuse a POST with no length.
    assert prepared.body == b""
    assert "Content-Type" not in prepared.headers


def a_generator() -> Iterator[str]:
    yield "34600"


def a_list_in_itself() -> list[object]:
    items: list[object] = []
    items.append(items)
    return items


def a_dict_in_itself() -> dict[str, object]:
    items: dict[str, object] = {}
    items["self"] = items
    return items


NOT_JSON_VALUES: dict[str, object] = {
    "generator": a_generator(),
    "set": {"34600"},
    "bytes": b"34600",
    "object": object(),
    "uuid": UUID(int=0),
    "nan": math.nan,
    "infinity": math.inf,
    "minus infinity": -math.inf,
    "nan in a dataclass": RangeInput(min=cast(Any, math.nan), max=1),
    "surrogate": "\ud800",
    "surrogate in a list": ["\ud800"],
    "dataclass in a dict": {"a": RangeInput(min=0, max=1)},
    "tuple as a key": {("a", "b"): 1},
    "long number": 10**5000,
    "list in itself": a_list_in_itself(),
    "dict in itself": a_dict_in_itself(),
}


@pytest.mark.parametrize("value", NOT_JSON_VALUES.values(), ids=NOT_JSON_VALUES)
def test_a_value_that_json_cannot_hold_gives_invalid_config(value: object) -> None:
    with pytest.raises(CalyxError) as caught:
        prepare_send(arguments={"value": value})
    error = refused(caught, ClientCode.INVALID_CONFIG)
    assert error.idempotency_key == "key-1"
    assert error.retry is RetryAdvice.DO_NOT_RETRY
    assert error.__cause__ is not None


def test_the_arguments_of_the_program_do_not_change() -> None:
    recipients = ["34600"]
    arguments: dict[str, object] = {"recipients": recipients, "text": None}
    prepare_send(arguments=arguments)
    assert arguments == {"recipients": recipients, "text": None}
    assert arguments["recipients"] is recipients


def test_another_call_has_no_key() -> None:
    prepared = prepare(config(), UPDATE, path_id="t1", arguments={})
    assert prepared.idempotency_key is None
    assert "Idempotency-Key" not in prepared.headers


@pytest.mark.parametrize("key", ["", "private key", "private\r\n", b"private", UUID(int=0), 5])
def test_a_bad_key_is_not_in_the_error(key: Any) -> None:
    with pytest.raises(CalyxError) as caught:
        # The time limit is bad too, but its error would carry the key.
        prepare_send(idempotency_key=key, time_limit=0)
    error = refused(caught, ClientCode.INVALID_CONFIG)
    assert error.detail.startswith("The idempotency key ")
    assert error.idempotency_key is None
    assert "private" not in error.detail


@pytest.mark.parametrize(
    ("client", "call", "expected"),
    [
        (None, None, 360),
        (10, None, 10),
        (None, 0.5, 0.5),
        (10, 20, 20),
        (10, 1000, 1000),
        (1000.0, None, 1000),
    ],
)
def test_the_limit_of_the_call_wins_over_the_client_and_the_default(
    client: float | None, call: float | None, expected: float
) -> None:
    prepared = prepare(config(time_limit=client), SEND, path_id="t1", time_limit=call)
    assert prepared.time_limit == expected
    assert type(prepared.time_limit) is float


BAD_LIMITS: list[object] = [
    0,
    0.0,
    -1,
    -0.5,
    math.nan,
    math.inf,
    -math.inf,
    10**400,
    True,
    False,
    "10",
    Decimal(10),
    [10],
]


@pytest.mark.parametrize("limit", BAD_LIMITS)
def test_a_bad_limit_of_the_client_gives_invalid_config(limit: object) -> None:
    with pytest.raises(CalyxError) as caught:
        config(time_limit=limit)
    assert refused(caught, ClientCode.INVALID_CONFIG).retry is RetryAdvice.DO_NOT_RETRY


@pytest.mark.parametrize("limit", BAD_LIMITS)
def test_a_bad_limit_of_a_call_gives_invalid_config(limit: object) -> None:
    with pytest.raises(CalyxError) as caught:
        prepare_send(time_limit=limit)
    error = refused(caught, ClientCode.INVALID_CONFIG)
    assert error.idempotency_key == "key-1"
    assert error.retry is RetryAdvice.DO_NOT_RETRY


def test_an_answer_becomes_an_object() -> None:
    thing: Thing = finish(prepare(config(), GET, path_id="t1"), answer())
    assert thing == Thing(id="t1")


def test_an_unknown_field_of_an_answer_is_ignored() -> None:
    prepared = prepare(config(), GET, path_id="t1")
    assert finish(prepared, answer(body=b'{"id":"t1","new":[1]}')) == Thing(id="t1")


def test_a_204_gives_none() -> None:
    result: object = finish(prepare(config(), DELETE, path_id="t1"), answer(204, b""))
    assert result is None


@pytest.mark.parametrize(("status", "body"), [(204, b""), (202, b""), (202, b"not json")])
def test_the_status_is_the_result_of_a_removal(status: int, body: bytes) -> None:
    result: int = finish(prepare(config(), REMOVE, path_id="t1"), answer(status, body))
    assert result == status


@pytest.mark.parametrize("case", ERROR_CASES, ids=lambda case: case["name"])
@pytest.mark.parametrize("lower_case", [False, True])
def test_the_shared_error_cases(case: dict[str, Any], lower_case: bool) -> None:
    headers = {n.lower() if lower_case else n: v for n, v in case["headers"].items()}
    with pytest.raises(CalyxError) as caught:
        finish(prepare_send(), answer(case["status"], case["body"].encode(), headers))
    error, expected = caught.value, case["expected"]
    assert error.source == expected["source"]
    assert error.code == expected["code"]
    assert error.status == expected["status"]
    assert error.detail == expected["detail"]
    assert error.retry == expected["retry"]
    assert error.request_id == (expected["requestId"] or None)
    assert error.retry_after == expected["retryAfterSeconds"]
    assert error.errors == tuple(FieldError(**item) for item in expected["errors"])
    assert error.idempotency_key == "key-1"


def test_an_error_of_another_call_has_no_key() -> None:
    with pytest.raises(CalyxError) as caught:
        finish(prepare(config(), GET, path_id="t1"), answer(404, b'{"code":"not_found"}'))
    assert caught.value.code == "not_found"
    assert caught.value.idempotency_key is None


@pytest.mark.parametrize(
    ("problem", "detail", "errors"),
    [
        ({}, "", []),
        ({"title": "Title"}, "Title", []),
        ({"detail": "", "title": "Title"}, "Title", []),
        ({"detail": None, "title": None, "errors": None}, "", []),
        ({"detail": 5, "title": "Title"}, "Title", []),
        ({"detail": ["private"], "title": {}}, "", []),
        ({"errors": "text"}, "", []),
        ({"errors": {"location": "a", "message": "b"}}, "", []),
        ({"errors": [None, 5, "text", []]}, "", []),
        ({"errors": [{}, {"location": "a"}, {"message": 5}]}, "", [("", ""), ("a", ""), ("", "")]),
    ],
)
def test_a_problem_with_a_wrong_member_keeps_its_code(
    problem: dict[str, object], detail: str, errors: list[tuple[str, str]]
) -> None:
    body = json.dumps({"code": "rate_limited", **problem}).encode()
    with pytest.raises(CalyxError) as caught:
        finish(prepare_send(), answer(429, body))
    error = caught.value
    assert error.code == "rate_limited"
    assert error.source is Source.BACKEND
    assert error.detail == detail
    assert [(item.location, item.message) for item in error.errors] == errors


@pytest.mark.parametrize(
    ("value", "expected"),
    [
        ("0", 0),
        ("007", 7),
        (" 12 ", 12),
        ("1.5", 0),
        ("1_0", 0),
        ("-0", 0),
        ("٣", 0),
        ("Wed, 21 Oct 2026 07:28:00 GMT", 0),
        pytest.param("9" * 5000, 0, id="long"),
        pytest.param("0" * 5000, 0, id="many zeros"),
    ],
)
def test_retry_after_is_a_number_of_seconds(value: str, expected: int) -> None:
    body = b'{"code":"rate_limited"}'
    with pytest.raises(CalyxError) as caught:
        finish(prepare_send(), answer(429, body, {"Retry-After": value}))
    assert caught.value.retry_after == expected


def test_an_empty_request_id_is_none() -> None:
    with pytest.raises(CalyxError) as caught:
        finish(prepare_send(), answer(429, b'{"code":"rate_limited"}', {"X-Request-Id": ""}))
    assert caught.value.request_id is None


NOT_JSON = [
    b"",
    b"not json",
    b'{"id":"t1"',
    b'{"id":"\xff"}',
    b'\xef\xbb\xbf{"id":"t1"}',
    '{"id":"t1"}'.encode("utf-16"),
    b'{"id":NaN}',
    b'{"id":Infinity}',
    b'{"id":-Infinity}',
    b'{"id":' + b"9" * 5000 + b"}",
    b"[" * 200_000,
]


@pytest.mark.parametrize("body", NOT_JSON, ids=lambda body: repr(body[:12]))
def test_a_body_that_is_not_json_keeps_retry_now(body: bytes) -> None:
    with pytest.raises(CalyxError) as caught:
        finish(prepare_send(), answer(200, body))
    error = refused(caught, ClientCode.INVALID_RESPONSE)
    assert error.detail == "The backend answered 200 with a body that is not JSON."
    assert error.retry is RetryAdvice.RETRY_NOW
    assert error.idempotency_key == "key-1"
    assert error.__cause__ is not None


NOT_A_PROBLEM = [*NOT_JSON, b"null", b"[]", b'"text"', b"{}", b'{"code":null}', b'{"code":5}']


@pytest.mark.parametrize("body", NOT_A_PROBLEM, ids=lambda body: repr(body[:12]))
def test_an_error_body_with_no_code_keeps_retry_now(body: bytes) -> None:
    with pytest.raises(CalyxError) as caught:
        finish(prepare_send(), answer(503, body, {"X-Request-Id": "r1"}))
    error = refused(caught, ClientCode.INVALID_RESPONSE)
    assert error.detail == "The backend answered 503 with a body that is not problem+json."
    assert error.retry is RetryAdvice.RETRY_NOW
    assert error.idempotency_key == "key-1"


@pytest.mark.parametrize("status", [300, 301, 302, 303, 304, 307, 308, 399])
def test_a_redirect_gives_invalid_response(status: int) -> None:
    headers = {"Location": "https://private.example.com"}
    with pytest.raises(CalyxError) as caught:
        # The body of a redirect is not read, because the redirect is not from the backend.
        finish(prepare_send(), answer(status, b'{"code":"rate_limited"}', headers))
    error = refused(caught, ClientCode.INVALID_RESPONSE)
    assert error.detail == f"The backend answered {status}. The SDK does not follow redirects."
    assert error.retry is RetryAdvice.RETRY_NOW
    assert error.idempotency_key == "key-1"


@pytest.mark.parametrize(
    ("body", "reason"),
    [
        (b"null", "Thing is not an object."),
        (b"[]", "Thing is not an object."),
        (b"{}", "Thing.id is missing."),
        (b'{"id":null}', "Thing.id is not a string."),
        (b'{"id":5}', "Thing.id is not a string."),
    ],
)
def test_a_wrong_shape_has_no_advice(body: bytes, reason: str) -> None:
    with pytest.raises(CalyxError) as caught:
        finish(prepare_send(), answer(200, body))
    error = refused(caught, ClientCode.INVALID_RESPONSE)
    assert error.detail == f"The backend answered 200 with JSON of the wrong shape. {reason}"
    assert error.retry is RetryAdvice.UNKNOWN
    assert error.idempotency_key == "key-1"
    assert isinstance(error.__cause__, WrongShapeError)


def test_an_error_of_the_sdk_has_the_key_and_the_advice_of_the_map() -> None:
    error = client_error(prepare_send(), ClientCode.TIMEOUT, "The backend did not answer in time.")
    assert error.source is Source.CLIENT
    assert error.code == ClientCode.TIMEOUT
    assert error.status == 0
    assert error.detail == "The backend did not answer in time."
    assert error.retry is RetryAdvice.RETRY_NOW
    assert error.idempotency_key == "key-1"


def test_the_create_call_lists_are_the_ones_of_create_calls_json() -> None:
    assert sorted(CREATE_OPERATIONS) == sorted(CREATE_CALLS["operations"])
    assert sorted(CREATE_CODES) == sorted(CREATE_CALLS["codes"])
    assert CREATE_RETRY == CREATE_CALLS["retry"]


@pytest.mark.parametrize("operation_id", CREATE_CALLS["operations"])
@pytest.mark.parametrize("code", CREATE_CALLS["codes"])
def test_a_client_error_of_a_create_call_has_no_advice(operation_id: str, code: str) -> None:
    prepared = prepare(config(), create(operation_id), arguments={"name": "n"})
    assert client_error(prepared, code, "").retry is RetryAdvice.UNKNOWN


@pytest.mark.parametrize("code", CREATE_CALLS["codes"])
def test_another_call_keeps_the_advice_of_the_map(code: str) -> None:
    prepared = prepare(config(), create("updateClient"), arguments={"name": "n"})
    expected = CalyxError(source=Source.CLIENT, code=code, status=0, detail="").retry
    assert client_error(prepared, code, "").retry is expected


@pytest.mark.parametrize("operation_id", CREATE_CALLS["operations"])
@pytest.mark.parametrize(
    ("status", "body"),
    [
        (201, b"not json"),
        (201, b"{}"),
        (502, b"<html></html>"),
        (307, b""),
    ],
)
def test_a_failed_read_of_a_create_call_has_no_advice(
    operation_id: str, status: int, body: bytes
) -> None:
    prepared = prepare(config(), create(operation_id), arguments={"name": "n"})
    with pytest.raises(CalyxError) as caught:
        finish(prepared, answer(status, body))
    assert refused(caught, ClientCode.INVALID_RESPONSE).retry is RetryAdvice.UNKNOWN


def test_other_errors_of_a_create_call_keep_their_advice() -> None:
    prepared = prepare(config(), create(), arguments={"name": "n"})
    with pytest.raises(CalyxError) as caught:
        finish(prepared, answer(429, b'{"code":"rate_limited"}'))
    assert caught.value.retry is RetryAdvice.RETRY_LATER
    with pytest.raises(CalyxError) as caught:
        prepare(config(), create(), arguments={"name": math.nan})
    assert caught.value.retry is RetryAdvice.DO_NOT_RETRY
    error = client_error(prepared, ClientCode.INVALID_CONFIG, "")
    assert error.retry is RetryAdvice.DO_NOT_RETRY
