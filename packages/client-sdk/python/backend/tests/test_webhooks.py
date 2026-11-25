"""The check of a webhook: the steps of CONTRACT part 10, in their order."""

import hashlib
import hmac
import json
import traceback
from collections.abc import Iterable, Iterator, Mapping
from datetime import UTC, datetime, timedelta, timezone
from pathlib import Path
from types import MappingProxyType
from typing import Any, get_type_hints

import pytest

import calyx_client_sdk
from calyx_client_sdk import (
    CalyxError,
    ClientCode,
    Event,
    MessageReceivedEvent,
    RetryAdvice,
    Source,
    decode_event,
    verify_webhook,
    webhooks,
)

PACKAGES = Path(__file__).parents[4]
CASE = json.loads((PACKAGES / "shared-types" / "testdata" / "webhook-signature.json").read_text())
BODY: bytes = CASE["body"].encode()
HEADERS: dict[str, str] = CASE["headers"]
SECRET: str = CASE["secret"]
SECONDS: int = CASE["now"]
NOW = datetime.fromtimestamp(SECONDS, UTC)
TIMESTAMP = "X-Calyx-Timestamp"
SIGNATURE = "X-Calyx-Signature"


def sign(body: bytes = BODY, timestamp: str = str(SECONDS), secret: str = SECRET) -> dict[str, str]:
    mac = hmac.new(secret.encode(), f"{timestamp}.".encode() + body, hashlib.sha256)
    return {TIMESTAMP: timestamp, SIGNATURE: f"sha256={mac.hexdigest()}"}


def refused(
    code: str,
    *,
    body: Any = BODY,
    headers: Any = None,
    secret: Any = SECRET,
    now: Any = NOW,
    **more: Any,
) -> CalyxError:
    """Checks the error of a webhook, and that no text of it is private."""
    headers = sign() if headers is None else headers
    with pytest.raises(CalyxError) as caught:
        verify_webhook(body, headers, secret, now=now, **more)
    error = caught.value
    assert (error.source, error.status) == (Source.CLIENT, 0)
    assert error.code == code
    assert error.retry is RetryAdvice.DO_NOT_RETRY
    assert error.detail[0].isupper() and error.detail.endswith(".")
    text = "".join(traceback.format_exception(error, chain=True)) + error.detail
    assert "Hello, this is a test" not in text
    assert CASE["secret"] not in text
    assert not isinstance(secret, str) or secret == "" or secret not in text
    assert HEADERS[SIGNATURE] not in text
    return error


# A good webhook.


def test_the_shared_case() -> None:
    event = verify_webhook(BODY, HEADERS, SECRET, now=NOW)
    assert isinstance(event, MessageReceivedEvent)
    assert event.id == CASE["event"]["id"]
    assert event.instance == CASE["event"]["instanceName"]
    assert event.event == CASE["event"]["type"]
    assert event.timestamp == CASE["event"]["occurredAt"]
    assert event.data.text == "Hello, this is a test"
    assert event == decode_event(BODY)


class FlaskHeaders:
    """Headers of Flask: not a Mapping, and the iterator gives pairs."""

    def __init__(self, pairs: dict[str, str]) -> None:
        self._pairs = pairs

    def items(self) -> Iterable[tuple[str, str]]:
        return iter(self._pairs.items())

    def __iter__(self) -> Iterator[tuple[str, str]]:
        return iter(self._pairs.items())


class DjangoHeaders(Mapping[str, str]):
    """Headers of Django or Starlette: a Mapping that is not a dict."""

    def __init__(self, pairs: dict[str, str]) -> None:
        self._pairs = pairs

    def __getitem__(self, name: str) -> str:
        return self._pairs[name]

    def __iter__(self) -> Iterator[str]:
        return iter(self._pairs)

    def __len__(self) -> int:
        return len(self._pairs)


FORMS: dict[str, Any] = {
    "dict": dict,
    "mappingproxy": MappingProxyType,
    "django": DjangoHeaders,
    "flask": FlaskHeaders,
    "list of tuples": lambda pairs: list(pairs.items()),
    "tuple of tuples": lambda pairs: tuple(pairs.items()),
    "list of lists": lambda pairs: [list(pair) for pair in pairs.items()],
    "items": lambda pairs: pairs.items(),
}


@pytest.mark.parametrize("change", [str, str.lower, str.upper, str.title])
@pytest.mark.parametrize("form", FORMS)
def test_the_forms_of_the_headers(form: str, change: Any) -> None:
    headers = FORMS[form]({change(name): value for name, value in HEADERS.items()})
    event = verify_webhook(BODY, headers, SECRET, now=NOW)
    assert event.id == "evt_0001"


def test_the_first_of_two_headers_counts() -> None:
    good, bad = list(sign().items()), [(TIMESTAMP, "1"), (SIGNATURE, "sha256=00")]
    assert verify_webhook(BODY, good + bad, SECRET, now=NOW).id == "evt_0001"
    refused(ClientCode.STALE_TIMESTAMP, headers=bad + good)


def test_the_body_is_signed_as_bytes() -> None:
    body = json.dumps(json.loads(BODY), indent=2, ensure_ascii=False).encode()
    assert verify_webhook(body, sign(body), SECRET, now=NOW) == decode_event(BODY)


def test_the_signature_can_have_upper_case_letters() -> None:
    headers = sign()
    headers[SIGNATURE] = headers[SIGNATURE].upper()
    assert verify_webhook(BODY, headers, SECRET, now=NOW).id == "evt_0001"


def test_a_secret_with_other_letters() -> None:
    headers = sign(secret="contraseña")
    assert verify_webhook(BODY, headers, "contraseña", now=NOW).id == "evt_0001"


# The time.


@pytest.mark.parametrize("seconds", [-300, -1, 0, 1, 300])
def test_a_time_inside_the_tolerance(seconds: int) -> None:
    now = NOW + timedelta(seconds=seconds)
    assert verify_webhook(BODY, sign(), SECRET, now=now).id == "evt_0001"


@pytest.mark.parametrize("seconds", [-301, -300.5, 300.5, 301, 10**9])
def test_a_time_outside_the_tolerance(seconds: float) -> None:
    refused(ClientCode.STALE_TIMESTAMP, now=NOW + timedelta(seconds=seconds))


def test_the_tolerance_of_the_program() -> None:
    later = NOW + timedelta(seconds=11)
    refused(ClientCode.STALE_TIMESTAMP, now=later, tolerance=10)
    assert verify_webhook(BODY, sign(), SECRET, tolerance=11, now=later).id == "evt_0001"


def test_now_can_have_another_time_zone() -> None:
    now = NOW.astimezone(timezone(timedelta(hours=5)))
    assert verify_webhook(BODY, sign(), SECRET, now=now).id == "evt_0001"


def test_the_default_of_now_is_the_clock() -> None:
    clock = str(int(datetime.now(UTC).timestamp()))
    assert verify_webhook(BODY, sign(timestamp=clock), SECRET).id == "evt_0001"
    refused(ClientCode.STALE_TIMESTAMP, now=None)


@pytest.mark.parametrize(
    "timestamp",
    ["soon", "1.5", "-1790000005", "+1790000005", " 1790000005", "1_0", "١٢", "9" * 5000],
)
def test_a_timestamp_that_is_no_number(timestamp: str) -> None:
    refused(ClientCode.INVALID_SIGNATURE, headers=sign(timestamp=timestamp))


def test_a_very_large_timestamp_is_stale() -> None:
    refused(ClientCode.STALE_TIMESTAMP, headers=sign(timestamp="9" * 18))


# The signature.


def test_an_empty_secret() -> None:
    # A signature with an empty secret is easy to make, so the SDK never accepts one.
    refused(ClientCode.INVALID_SIGNATURE, headers=sign(secret=""), secret="")


@pytest.mark.parametrize("name", [TIMESTAMP, SIGNATURE])
def test_a_missing_header(name: str) -> None:
    headers = sign()
    del headers[name]
    for form in (headers, list(headers.items()), headers | {name: ""}):
        assert "missing" in refused(ClientCode.INVALID_SIGNATURE, headers=form).detail


@pytest.mark.parametrize("headers", [{}, []])
def test_no_header(headers: Any) -> None:
    refused(ClientCode.INVALID_SIGNATURE, headers=headers)


@pytest.mark.parametrize(
    "signature",
    [
        "sha256=",
        "sha256=00",
        sign()[SIGNATURE].removeprefix("sha256="),
        sign()[SIGNATURE] + "0",
        f"{sign()[SIGNATURE]},{sign()[SIGNATURE]}",
        "sha256=" + "é" * 64,
        "\udce9",
    ],
)
def test_a_wrong_signature(signature: str) -> None:
    refused(ClientCode.INVALID_SIGNATURE, headers=sign() | {SIGNATURE: signature})


def test_another_secret() -> None:
    refused(ClientCode.INVALID_SIGNATURE, headers=sign(secret="other"))


def test_a_changed_body_or_time() -> None:
    refused(ClientCode.INVALID_SIGNATURE, body=BODY + b" ")
    refused(ClientCode.INVALID_SIGNATURE, headers=sign() | {TIMESTAMP: str(SECONDS + 1)})


def test_the_comparison_takes_constant_time(monkeypatch: pytest.MonkeyPatch) -> None:
    compared: list[int] = []
    real = hmac.compare_digest

    def compare(a: bytes, b: bytes) -> bool:
        compared.append(len(a))
        return real(a, b)

    monkeypatch.setattr(hmac, "compare_digest", compare)
    refused(ClientCode.INVALID_SIGNATURE, headers=sign(secret="other"))
    assert compared == [len(HEADERS[SIGNATURE])]


# The body.


@pytest.mark.parametrize(
    "body", [b"not json", b"[]", b'{"hello":"world"}', b'{"data":NaN}', b"\xff"]
)
def test_a_signed_body_that_is_no_event(body: bytes) -> None:
    refused(ClientCode.INVALID_EVENT, body=body, headers=sign(body))


def test_the_signature_is_checked_before_the_body() -> None:
    refused(ClientCode.INVALID_SIGNATURE, body=b"not json")


# A wrong setup of the program.


@pytest.mark.parametrize("body", [json.loads(BODY), CASE["body"], None, bytearray(BODY)])
def test_a_body_that_is_not_bytes(body: object) -> None:
    error = refused(ClientCode.INVALID_CONFIG, body=body)
    assert "raw body" in error.detail


@pytest.mark.parametrize("secret", [None, b"test-secret", 5])
def test_a_secret_of_a_wrong_type(secret: object) -> None:
    refused(ClientCode.INVALID_CONFIG, secret=secret)


def test_a_secret_that_is_not_valid_unicode() -> None:
    error = refused(ClientCode.INVALID_CONFIG, secret="private-secret-\udce9")
    assert error.__context__ is None


@pytest.mark.parametrize(
    "headers",
    [
        5,
        "X-Calyx-Timestamp: 1",
        [(TIMESTAMP,)],
        [("a", "b", "c")],
        ["ab"],
        # The headers of ASGI.
        [(name.encode(), value.encode()) for name, value in sign().items()],
        {name: [value] for name, value in sign().items()},
        # The META of Django.
        {"HTTP_X_CALYX_TIMESTAMP": "1", "wsgi.input": object()},
    ],
)
def test_headers_of_a_wrong_type(headers: object) -> None:
    error = refused(ClientCode.INVALID_CONFIG, headers=headers)
    assert "headers" in error.detail


def test_headers_that_are_none() -> None:
    nothing: Any = None
    with pytest.raises(CalyxError) as caught:
        verify_webhook(BODY, nothing, SECRET, now=NOW)
    assert caught.value.code == ClientCode.INVALID_CONFIG


@pytest.mark.parametrize("tolerance", [0, -1, True, 300.0, float("nan"), "300", None])
def test_a_wrong_tolerance(tolerance: object) -> None:
    refused(ClientCode.INVALID_CONFIG, tolerance=tolerance)


@pytest.mark.parametrize("now", [datetime(2026, 11, 22), SECONDS, "now"])
def test_a_wrong_now(now: object) -> None:
    refused(ClientCode.INVALID_CONFIG, now=now)


# The public function.


def test_the_public_names() -> None:
    assert set(calyx_client_sdk.__all__) >= {"decode_event", "verify_webhook"}


def test_the_result_is_an_event() -> None:
    assert get_type_hints(webhooks.verify_webhook)["return"] == Event
