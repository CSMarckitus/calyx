"""The errors that the core sends (CONTRACT.md parts 4 to 6)."""

import base64
import copy
import json
import pickle
import traceback
from pathlib import Path
from typing import Any

import grpc
import pytest
from calyx_client_sdk import CalyxError, CoreCode, RetryAdvice, Source
from fake_core import Call, FakeCore
from google.protobuf.any_pb2 import Any as AnyMessage
from google.protobuf.duration_pb2 import Duration
from google.protobuf.message import Message
from google.rpc.error_details_pb2 import BadRequest, ErrorInfo, RetryInfo
from google.rpc.status_pb2 import Status

TESTDATA = Path(__file__).parents[3] / "testdata"
CASES = json.loads((TESTDATA / "core-errors.json").read_text())["cases"]
SEND = {"instance_name": "i_1", "to": "34600000000", "text": "Hi", "idempotency_key": "key-1"}


def status(*details: Message, code: int = 14, message: str = "Text of the core.") -> Status:
    result = Status(code=code, message=message)
    for detail in details:
        result.details.add().Pack(detail)
    return result


def reason(name: str, domain: str = "calyx.core.v1", **metadata: str) -> ErrorInfo:
    return ErrorInfo(reason=name, domain=domain, metadata=metadata)


def get_error(call: Call, sent: Status, trailer: bytes | None = None) -> CalyxError:
    with FakeCore(lambda seen: seen.fail(sent, trailer=trailer)) as core:
        with pytest.raises(CalyxError) as caught:
            call(core.address, "send_plain_text", **SEND)
        assert len(core.calls) == 1
    return caught.value


@pytest.mark.parametrize("case", CASES, ids=lambda case: case["name"])
def test_an_error_of_core_errors_json(call: Call, case: dict[str, Any]) -> None:
    sent = Status.FromString(base64.b64decode(case["statusBase64"]))
    error = get_error(call, sent)
    expected = case["expected"]
    assert error.source is Source(expected["source"])
    assert error.code == expected["code"]
    assert error.status == expected["status"] == case["grpcCode"]
    assert error.detail == sent.message
    assert error.retry is RetryAdvice(expected["retry"])
    assert error.retry_after == expected["retryAfterSeconds"]
    assert error.metadata == expected.get("metadata", {})
    assert error.idempotency_key == "key-1"
    assert (error.errors, error.request_id) == ((), None)


@pytest.mark.parametrize("code", [c for c in grpc.StatusCode if c.value[0] != 0])
def test_each_status_has_its_canonical_name(call: Call, code: grpc.StatusCode) -> None:
    error = get_error(call, status(code=code.value[0]))
    assert (error.source, error.status) == (Source.CORE, code.value[0])
    assert error.code == getattr(CoreCode, code.name)
    assert error.detail == "Text of the core."


def test_a_reason_that_the_sdk_does_not_know(call: Call) -> None:
    error = get_error(call, status(reason("ERROR_REASON_NEW", at="now")))
    assert (error.code, error.retry) == ("ERROR_REASON_NEW", RetryAdvice.UNKNOWN)
    assert error.metadata == {"at": "now"}


def test_the_first_reason_of_the_core_wins(call: Call) -> None:
    sent = status(
        reason("OTHER", domain="example.com", a="1"),
        reason("FIRST", b="2"),
        reason("SECOND", c="3"),
    )
    error = get_error(call, sent)
    assert (error.code, error.metadata) == ("FIRST", {"b": "2"})


def test_the_metadata_of_an_empty_reason_stays(call: Call) -> None:
    error = get_error(call, status(reason("", a="1")))
    assert (error.code, error.metadata) == (CoreCode.UNAVAILABLE, {"a": "1"})


@pytest.mark.parametrize(
    ("seconds", "nanos", "expected"),
    [
        (12, 0, 12),
        (0, 1, 1),
        (1, 999_999_999, 2),
        (-5, 0, 0),
        (-1, -1, 0),
        (0, -1, 0),
        (0, 0, 0),
        (10**11, 0, 10**11),
    ],
)
def test_the_delay_of_the_retry_info_is_rounded_up(
    call: Call, seconds: int, nanos: int, expected: int
) -> None:
    delay = Duration(seconds=seconds, nanos=nanos)
    error = get_error(call, status(RetryInfo(retry_delay=delay)))
    assert (error.retry_after, error.code) == (expected, CoreCode.UNAVAILABLE)


def test_a_retry_info_with_no_delay(call: Call) -> None:
    assert get_error(call, status(RetryInfo())).retry_after == 0


def test_other_details_are_ignored(call: Call) -> None:
    sent = status(BadRequest(field_violations=[BadRequest.FieldViolation(field="to")]))
    sent.details.append(AnyMessage(type_url="type.googleapis.com/x.Unknown", value=b"\xff"))
    sent.details.append(AnyMessage(type_url="type.googleapis.com/google.rpc.ErrorInfo", value=b"x"))
    sent.details.append(AnyMessage(type_url="type.googleapis.com/google.rpc.RetryInfo", value=b"x"))
    sent.details.append(AnyMessage())
    sent.details.add().Pack(reason("ERROR_REASON_SEND_IN_PROGRESS"))
    error = get_error(call, sent)
    assert error.code == CoreCode.SEND_IN_PROGRESS
    assert (error.errors, error.retry_after) == ((), 0)


@pytest.mark.parametrize("trailer", [b"", b"not protobuf", b"\x12\x01\xff"])
def test_bytes_that_are_no_status_give_the_canonical_name(call: Call, trailer: bytes) -> None:
    error = get_error(call, status(reason("LOST")), trailer)
    assert (error.source, error.code, error.status) == (Source.CORE, CoreCode.UNAVAILABLE, 14)
    assert (error.detail, error.metadata) == ("Text of the core.", {})


def test_a_detail_that_is_not_ascii(call: Call) -> None:
    assert get_error(call, status(message="Año: 100 %\n")).detail == "Año: 100 %\n"


def test_the_text_of_the_core_is_only_in_detail(call: Call) -> None:
    # It can have private data, for example a phone number.
    error = get_error(call, status(reason("ERROR_REASON_NEW", at="34600111222"), message="private"))
    assert (error.__cause__, error.__context__) == (None, None)
    text = "".join(traceback.format_exception(error)) + repr(error)
    assert "private" not in text
    assert "34600111222" not in text
    assert "key-1" in text


def test_pickle_and_copy_keep_the_error(call: Call) -> None:
    error = get_error(call, status(reason("ERROR_REASON_NEW", at="now"), RetryInfo()))
    for other in (pickle.loads(pickle.dumps(error)), copy.copy(error)):
        assert vars(other) == vars(error)
        assert str(other) == str(error)
