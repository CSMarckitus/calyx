import asyncio
import socket
import sys
import threading
import time
import traceback
import uuid
from collections.abc import Callable
from contextlib import suppress
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

import grpc
import pytest
from calyx_client_sdk import CalyxError, ClientCode, CoreCode, RetryAdvice, Sent, Source
from certificates import Authority
from fake_core import (
    KEY,
    MAX_REQUEST,
    SEND,
    TOKEN,
    Call,
    FakeCall,
    FakeCore,
    RawConnection,
    check_client_error,
    check_config_error,
    raw_server,
    wait_for_end,
)
from google.protobuf.message import Message
from google.protobuf.timestamp_pb2 import Timestamp
from google.rpc.status_pb2 import Status

from calyx_client_sdk_core import AsyncCoreClient, CoreClient, __version__
from calyx_client_sdk_core._base import read_answer
from calyx_client_sdk_core._channel import Result, finish
from calyx_client_sdk_core._generated import convert, methods
from calyx_client_sdk_core._generated.methods import AsyncCoreMethods, SyncCoreMethods
from calyx_client_sdk_core._proto.calyx.core.v1 import instance_pb2, message_pb2
from calyx_client_sdk_core.models import (
    BulkJob,
    BulkPacing,
    GetBulkJobsResponse,
    GetInstanceStatusResponse,
    InstanceInfo,
    IntRange,
    ListInstancesResponse,
    PlainTextPayload,
    RemoveInstanceResponse,
    SendPlainTextResponse,
    UpdateInstanceConfigResponse,
    WebhookConfig,
    WebhookSettings,
    WhatsAppWebSettings,
)

PATH = "/calyx.core.v1."

JOB = message_pb2.BulkJob(
    id="job-1", instance_name="i_1", status=1, total_messages=2, created_at=Timestamp(seconds=1)
)
JOB_READ = BulkJob(
    id="job-1",
    instance_name="i_1",
    status="QUEUED",
    total_messages=2,
    sent_messages=0,
    failed_messages=0,
    progress_percent=0,
    estimated_minutes=0,
    display_time="",
    created_at=datetime(1970, 1, 1, 0, 0, 1, tzinfo=UTC),
    replayed=False,
)

# name: the path, the arguments, the request, the answer of the core, the result.
CASES: dict[str, tuple[str, dict[str, Any], Message, Message, Any]] = {
    "remove_instance": (
        "InstanceService/RemoveInstance",
        {"instance_name": "i_1"},
        instance_pb2.RemoveInstanceRequest(instance_name="i_1"),
        instance_pb2.RemoveInstanceResponse(),
        RemoveInstanceResponse(),
    ),
    "update_instance_config": (
        "InstanceService/UpdateInstanceConfig",
        {
            "instance_name": "i_1",
            "is_enabled": False,
            "webhooks": [WebhookConfig(url="https://example.com", event="bulk.finished")],
        },
        instance_pb2.UpdateInstanceConfigRequest(
            instance_name="i_1",
            is_enabled=False,
            webhooks=instance_pb2.WebhookList(
                webhooks=[
                    instance_pb2.WebhookConfig(url="https://example.com", event="bulk.finished")
                ]
            ),
        ),
        instance_pb2.UpdateInstanceConfigResponse(),
        UpdateInstanceConfigResponse(),
    ),
    "list_instances": (
        "InstanceService/ListInstances",
        {},
        instance_pb2.ListInstancesRequest(),
        instance_pb2.ListInstancesResponse(
            instances=[
                instance_pb2.InstanceInfo(
                    instance_name="i_1",
                    provider=99,
                    connection_status="INSTANCE_STATUS_CONNECTED",
                    is_enabled=True,
                    webhooks=[
                        instance_pb2.WebhookSettings(url="https://example.com", has_secret=True)
                    ],
                )
            ]
        ),
        ListInstancesResponse(
            instances=(
                InstanceInfo(
                    instance_name="i_1",
                    # The SDK does not know the number 99.
                    provider="99",
                    connection_status="CONNECTED",
                    is_enabled=True,
                    is_used_only_for_send_messages=False,
                    webhooks=(
                        WebhookSettings(
                            url="https://example.com", event="", is_active=False, has_secret=True
                        ),
                    ),
                    device_identity="UNSPECIFIED",
                ),
            )
        ),
    ),
    "get_instance_status": (
        "InstanceService/GetInstanceStatus",
        {"instance_name": "i_1"},
        instance_pb2.GetInstanceStatusRequest(instance_name="i_1"),
        instance_pb2.GetInstanceStatusResponse(status="INSTANCE_STATUS_NEEDS_AUTH"),
        GetInstanceStatusResponse(status="NEEDS_AUTH"),
    ),
    "send_plain_text": (
        "MessageService/SendPlainText",
        {**SEND, "idempotency_key": KEY},
        message_pb2.SendPlainTextRequest(**SEND, idempotency_key=KEY),
        message_pb2.SendPlainTextResponse(message_id="m-1", replayed=True),
        Sent(data=SendPlainTextResponse(message_id="m-1", replayed=True), idempotency_key=KEY),
    ),
    "send_bulk_same_message": (
        "MessageService/SendBulkSameMessage",
        {
            "instance_name": "i_1",
            "recipients": ["34600000000", "34600000001"],
            "text": "Hi",
            "pacing": BulkPacing(delay=IntRange(min=1, max=2)),
            "idempotency_key": KEY,
        },
        message_pb2.SendBulkSameMessageRequest(
            instance_name="i_1",
            recipients=["34600000000", "34600000001"],
            text="Hi",
            pacing=message_pb2.BulkPacing(delay=message_pb2.IntRange(min=1, max=2)),
            idempotency_key=KEY,
        ),
        JOB,
        Sent(data=JOB_READ, idempotency_key=KEY),
    ),
    "send_bulk_custom_messages": (
        "MessageService/SendBulkCustomMessages",
        {
            "instance_name": "i_1",
            "messages": [PlainTextPayload(to="34600000000", text="Hi")],
            "idempotency_key": KEY,
        },
        message_pb2.SendBulkCustomMessagesRequest(
            instance_name="i_1",
            messages=[message_pb2.PlainTextPayload(to="34600000000", text="Hi")],
            idempotency_key=KEY,
        ),
        JOB,
        Sent(data=JOB_READ, idempotency_key=KEY),
    ),
    "get_bulk_job": (
        "MessageService/GetBulkJob",
        {"job_id": "job-1"},
        message_pb2.GetBulkJobRequest(job_id="job-1"),
        JOB,
        JOB_READ,
    ),
    "get_bulk_jobs": (
        "MessageService/GetBulkJobs",
        {"job_ids": ("job-1", "job-2")},
        message_pb2.GetBulkJobsRequest(job_ids=["job-1", "job-2"]),
        message_pb2.GetBulkJobsResponse(jobs=[JOB]),
        GetBulkJobsResponse(jobs=(JOB_READ,)),
    ),
    "cancel_bulk_job": (
        "MessageService/CancelBulkJob",
        {"job_id": "job-1"},
        message_pb2.CancelBulkJobRequest(job_id="job-1"),
        JOB,
        JOB_READ,
    ),
}
ANSWERS = {PATH + path: answer for path, _, _, answer, _ in CASES.values()}
SENDS = [name for name in CASES if name.startswith("send_")]
each_call = pytest.mark.parametrize("name", CASES)
each_send = pytest.mark.parametrize("name", SENDS)


def answer_ok(seen: FakeCall) -> Message:
    return ANSWERS[seen.path]


def test_the_calls_are_the_unary_methods() -> None:
    for kind in (SyncCoreMethods, AsyncCoreMethods):
        assert {name for name in vars(kind) if not name.startswith("_")} == set(CASES)


# One call.


@each_call
def test_a_call(call: Call, name: str) -> None:
    path, arguments, request, _, result = CASES[name]
    with FakeCore(answer_ok) as core:
        assert call(core.address, name, **arguments) == result
        (seen,) = core.calls
    assert seen.path == PATH + path
    assert type(request).FromString(seen.body) == request
    assert seen.metadata["authorization"] == f"Bearer {TOKEN}"
    assert seen.metadata["user-agent"].startswith(f"calyx-python-sdk/{__version__} ")


@each_call
def test_the_default_time_limits_are_the_ones_of_the_design(call: Call, name: str) -> None:
    with FakeCore(answer_ok) as core:
        call(core.address, name, **CASES[name][1])
        (seen,) = core.calls
    limit = 360 if name == "send_plain_text" else 120
    assert limit - 5 < (seen.time_limit or 0) < limit + 1


@each_call
def test_the_error_of_a_call(call: Call, name: str) -> None:
    with FakeCore(lambda seen: seen.fail(Status(code=5, message="No."))) as core:
        with pytest.raises(CalyxError) as caught:
            call(core.address, name, **CASES[name][1])
        assert len(core.calls) == 1
    assert (caught.value.source, caught.value.code) == (Source.CORE, CoreCode.NOT_FOUND)
    assert caught.value.idempotency_key == (KEY if name in SENDS else None)


def test_a_large_request(call: Call) -> None:
    text = "x" * (MAX_REQUEST - 1000)
    with FakeCore(answer_ok) as core:
        call(core.address, "send_plain_text", **{**SEND, "text": text})
        (seen,) = core.calls
    assert message_pb2.SendPlainTextRequest.FromString(seen.body).text == text


def test_an_answer_above_4_mib_has_the_error_of_grpc(call: Call) -> None:
    # As the Go client: gRPC refuses it.
    answer = message_pb2.SendPlainTextResponse(message_id="x" * (4 << 20))
    with FakeCore(lambda _: answer) as core, pytest.raises(CalyxError) as caught:
        call(core.address, "send_plain_text", **SEND)
    assert (caught.value.source, caught.value.code) == (Source.CORE, CoreCode.RESOURCE_EXHAUSTED)


# The idempotency key.


@each_send
def test_a_send_with_no_key_gets_a_new_one(call: Call, name: str) -> None:
    arguments = {k: v for k, v in CASES[name][1].items() if k != "idempotency_key"}
    with FakeCore(answer_ok) as core:
        sent = [call(core.address, name, **arguments) for _ in range(2)]
        bodies = [seen.body for seen in core.calls]
    keys = [s.idempotency_key for s in sent]
    assert keys[0] != keys[1]
    for key, body in zip(keys, bodies, strict=True):
        assert uuid.UUID(key).version == 4
        assert type(CASES[name][2]).FromString(body).idempotency_key == key


@each_send
@pytest.mark.parametrize("key", ["", "a b", "ñ", "x" * 256, 5, uuid.uuid4(), "line\n"])
def test_a_bad_key(call: Call, name: str, key: Any) -> None:
    with FakeCore(answer_ok) as core:
        with pytest.raises(CalyxError) as caught:
            call(core.address, name, **{**CASES[name][1], "idempotency_key": key})
        assert core.calls == []
    check_config_error(caught.value)


@each_send
def test_each_error_of_a_send_has_the_key(
    call: Call, name: str, tmp_path: Path, authority: Authority
) -> None:
    arguments = CASES[name][1]

    def get_error(core: FakeCore, options: Any = None, **more: Any) -> CalyxError:
        with pytest.raises(CalyxError) as caught:
            call(core.address, name, options, **{**arguments, **more})
        return caught.value

    with FakeCore(answer_ok) as core:
        check_config_error(get_error(core, instance_name=5), KEY)
        check_config_error(get_error(core, time_limit=0), KEY)
        check_config_error(get_error(core, {"ca_file": str(tmp_path)}), KEY)
        assert core.calls == []
        error = get_error(core, {"ca_file": authority.ca_file}, time_limit=2)
        assert (error.code, error.idempotency_key) == (CoreCode.UNAVAILABLE, KEY)
    with FakeCore(wait_for_end) as core:
        check_client_error(get_error(core, time_limit=0.2), ClientCode.TIMEOUT, key=KEY)
    with FakeCore(lambda _: b"\xff") as core:
        check_client_error(get_error(core), ClientCode.INVALID_RESPONSE, key=KEY)
    with FakeCore(lambda _: message_pb2.BulkJob()) as core:
        if name != "send_plain_text":
            error = get_error(core)
            check_client_error(error, ClientCode.INVALID_RESPONSE, RetryAdvice.UNKNOWN, KEY)


# The arguments of a call.


@pytest.mark.parametrize(
    ("name", "arguments", "detail"),
    [
        ("get_bulk_job", {"job_id": 5}, "job_id must be a str of valid Unicode."),
        ("get_bulk_job", {"job_id": "\ud800"}, "job_id must be a str of valid Unicode."),
        ("get_bulk_jobs", {"job_ids": "job-1"}, "job_ids must be a list or a tuple."),
        (
            "update_instance_config",
            {"instance_name": "i_1", "is_enabled": "no"},
            "is_enabled must be a bool.",
        ),
    ],
)
def test_a_wrong_argument(call: Call, name: str, arguments: dict[str, Any], detail: str) -> None:
    # The ca_file is wrong too. The argument is the first check, so nothing of gRPC exists yet.
    with pytest.raises(CalyxError) as caught:
        call("127.0.0.1:1", name, {"ca_file": "/nope"}, **arguments)
    check_config_error(caught.value)
    assert caught.value.detail == detail


REQUIRED_LISTS = [
    ("send_bulk_same_message", {"instance_name": "i_1", "text": "Hi"}, "recipients"),
    ("send_bulk_custom_messages", {"instance_name": "i_1"}, "messages"),
    ("get_bulk_jobs", {}, "job_ids"),
]


@pytest.mark.parametrize(("name", "arguments", "missing"), REQUIRED_LISTS)
def test_a_list_with_no_default(
    call: Call, name: str, arguments: dict[str, Any], missing: str
) -> None:
    with pytest.raises(TypeError, match=f"required keyword-only argument: '{missing}'"):
        call("127.0.0.1:1", name, **arguments)


@pytest.mark.parametrize(("name", "arguments", "empty"), REQUIRED_LISTS)
def test_the_core_refuses_an_empty_list(
    call: Call, name: str, arguments: dict[str, Any], empty: str
) -> None:
    def refuse(seen: FakeCall) -> None:
        seen.fail(Status(code=3, message="The list is empty."))

    with FakeCore(refuse) as core, pytest.raises(CalyxError) as caught:
        call(core.address, name, **arguments, **{empty: []})
    assert len(core.calls) == 1
    assert (caught.value.source, caught.value.code) == (Source.CORE, CoreCode.INVALID_ARGUMENT)


def make_request_with_unknown_name() -> Message:
    return convert.create_instance_request(
        instance_name="i_1",
        is_used_only_for_send_messages=None,
        webhooks=(),
        provider=WhatsAppWebSettings(device_identity="NOPE"),
    )


def test_an_unknown_enum_name() -> None:
    # Only the stream create_instance takes an enum.
    arguments = (
        methods.CREATE_INSTANCE,
        make_request_with_unknown_name,
        convert.read_create_instance_response,
    )

    async def call_async(address: str) -> None:
        async with AsyncCoreClient(address, TOKEN, insecure=True) as client:
            await client._call(*arguments, time_limit=None)

    def call_sync(address: str) -> None:
        with CoreClient(address, TOKEN, insecure=True) as client:
            client._call(*arguments, time_limit=None)

    with FakeCore(answer_ok) as core:
        for run in (call_sync, lambda address: asyncio.run(call_async(address))):
            with pytest.raises(CalyxError) as caught:
                run(core.address)
            check_config_error(caught.value)
            detail = caught.value.detail
            assert detail.startswith("provider.device_identity must be one of UNSPECIFIED, ")
            assert "NOPE" not in detail
        assert core.calls == []


# The time limit.


def test_a_slow_core_ends_at_the_limit(call: Call) -> None:
    with FakeCore(wait_for_end) as core:
        start = time.monotonic()
        with pytest.raises(CalyxError) as caught:
            call(core.address, "list_instances", time_limit=0.5)
        assert 0.5 <= time.monotonic() - start < 1.5
        (seen,) = core.calls
        assert seen.ended.wait(1)
    check_client_error(caught.value, ClientCode.TIMEOUT)
    assert caught.value.detail == "The core did not answer in time."
    assert isinstance(caught.value.__cause__, TimeoutError)


def test_the_cause_of_a_timeout_has_no_text_of_the_core() -> None:
    # A DEADLINE_EXCEEDED of the core can arrive when the limit passed.
    result = Result(
        status=grpc.StatusCode.DEADLINE_EXCEEDED,
        data=None,
        detail="private",
        trailers=(),
        timed_out=True,
    )
    with pytest.raises(CalyxError) as caught:
        finish(result, KEY, closed=False)
    check_client_error(caught.value, ClientCode.TIMEOUT, key=KEY)
    cause = caught.value.__cause__
    assert isinstance(cause, TimeoutError)
    assert "private" not in str(cause)


def test_a_silent_peer_ends_at_the_limit(call: Call) -> None:
    with raw_server(RawConnection.read_to_end) as address:
        start = time.monotonic()
        with pytest.raises(CalyxError) as caught:
            call(address, "list_instances", time_limit=0.5)
        assert 0.5 <= time.monotonic() - start < 1.5
    check_client_error(caught.value, ClientCode.TIMEOUT)


def test_a_deadline_of_the_core_is_an_error_of_the_core(call: Call) -> None:
    with FakeCore(lambda seen: seen.fail(Status(code=4, message="Slow."))) as core:
        with pytest.raises(CalyxError) as caught:
            call(core.address, "list_instances", time_limit=5)
    error = caught.value
    assert (error.source, error.code) == (Source.CORE, CoreCode.DEADLINE_EXCEEDED)
    assert (error.status, error.detail) == (4, "Slow.")


@pytest.mark.parametrize(
    ("client_limit", "call_limit", "expected"),
    [(50, None, 50), (500, None, 500), (50, 60, 60), (50, 40, 40), (None, 30.5, 30.5)],
)
def test_the_limit_of_the_call_wins(
    call: Call, client_limit: float | None, call_limit: float | None, expected: float
) -> None:
    with FakeCore(answer_ok) as core:
        options = {"insecure": True, "time_limit": client_limit}
        call(core.address, "send_plain_text", options, **SEND, time_limit=call_limit)
        (seen,) = core.calls
    assert expected - 5 < (seen.time_limit or 0) < expected + 1


@pytest.mark.parametrize("limit", [10**9, 1e30, sys.float_info.max])
def test_a_very_large_time_limit(call: Call, limit: float) -> None:
    with FakeCore(answer_ok) as core:
        call(core.address, "list_instances", time_limit=limit)
        (seen,) = core.calls
    assert (seen.time_limit or 0) > 365 * 24 * 3600


@pytest.mark.parametrize("limit", [0, -1, float("nan"), float("inf"), 10**400, True, "5"])
def test_a_bad_time_limit(call: Call, limit: Any) -> None:
    for options, arguments in [({"time_limit": limit}, {}), ({}, {"time_limit": limit})]:
        with pytest.raises(CalyxError) as caught:
            call("127.0.0.1:1", "list_instances", {"insecure": True, **options}, **arguments)
        check_config_error(caught.value)
        assert "time limit" in caught.value.detail


# The answer.


@pytest.mark.parametrize("answer", [b"\xff", b"\x0a\x05ab", b"<html>", b"\x0a\x01\xff", None])
def test_an_answer_that_is_not_protobuf(call: Call, answer: bytes | None) -> None:
    with FakeCore(lambda _: answer) as core, pytest.raises(CalyxError) as caught:
        call(core.address, "get_bulk_job", job_id="job-1")
    check_client_error(caught.value, ClientCode.INVALID_RESPONSE)
    assert caught.value.detail == "The core answered with bytes that are not the expected message."


def test_an_answer_of_the_wrong_shape(call: Call) -> None:
    with FakeCore(lambda _: message_pb2.BulkJob(id="job-1")) as core:
        with pytest.raises(CalyxError) as caught:
            call(core.address, "get_bulk_job", job_id="job-1")
    # A repeated call gets the same shape.
    check_client_error(caught.value, ClientCode.INVALID_RESPONSE, RetryAdvice.UNKNOWN)
    assert caught.value.detail == (
        "The core answered with a message of the wrong shape. BulkJob.created_at is missing."
    )
    assert caught.value.__cause__ is not None


def test_a_text_that_is_not_utf_8_shows_no_value() -> None:
    # The pure Python version of protobuf gives this error, and it holds the text.
    def parse(data: bytes) -> Any:
        return data.decode()

    answer = b"private\xff"
    rpc = methods.Rpc(path="/x", parse=parse, time_limit=1)
    with pytest.raises(CalyxError) as caught:
        read_answer(rpc, lambda message: message, answer, KEY)
    check_client_error(caught.value, ClientCode.INVALID_RESPONSE, key=KEY)
    assert caught.value.__context__ is None
    assert "private" not in "".join(traceback.format_exception(caught.value))
    assert "private" not in repr(caught.value.__cause__)


# The connection.


def test_a_refused_connection(call: Call) -> None:
    with socket.create_server(("127.0.0.1", 0)) as listener:
        address = f"127.0.0.1:{listener.getsockname()[1]}"
    start = time.monotonic()
    with pytest.raises(CalyxError) as caught:
        call(address, "list_instances")
    assert time.monotonic() - start < 2
    error = caught.value
    assert (error.source, error.code, error.status) == (Source.CORE, CoreCode.UNAVAILABLE, 14)
    assert error.retry is RetryAdvice.RETRY_NOW


def test_a_host_that_does_not_exist(call: Call) -> None:
    with pytest.raises(CalyxError) as caught:
        call("nope.invalid:50051", "list_instances", time_limit=3)
    assert (caught.value.source, caught.value.code) == (Source.CORE, CoreCode.UNAVAILABLE)


def test_a_host_with_the_name_of_a_grpc_scheme(
    call: Call, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    # For gRPC alone, unix:50051 is the socket file 50051 of the current folder.
    monkeypatch.chdir(tmp_path)
    with FakeCore(answer_ok, unix="50051") as core:
        with pytest.raises(CalyxError) as caught:
            call("unix:50051", "list_instances", time_limit=3)
        assert core.calls == []
    assert (caught.value.source, caught.value.code) == (Source.CORE, CoreCode.UNAVAILABLE)


def send_goaway(connection: RawConnection, stream: bytes) -> None:
    # The last stream that the server read is 0.
    connection.sock.sendall(b"\0\0\x08\x07\0" + b"\0" * 12)


def send_refused_stream(connection: RawConnection, stream: bytes) -> None:
    connection.sock.sendall(b"\0\0\4\3\0" + stream + b"\0\0\0\7")


@pytest.mark.parametrize("refuse", [send_goaway, send_refused_stream])
def test_the_sdk_never_sends_a_call_again(
    call: Call, refuse: Callable[[RawConnection, bytes], None]
) -> None:
    # The server says that it did not read the call, so gRPC can send it again.
    connections: list[RawConnection] = []
    streams: list[bytes] = []

    def handle(connection: RawConnection) -> None:
        connections.append(connection)
        connection.send_settings()
        while True:
            stream = connection.read_call()
            streams.append(stream)
            refuse(connection, stream)

    with raw_server(handle) as address, pytest.raises(CalyxError) as caught:
        call(address, "send_plain_text", **SEND, time_limit=2)
    assert (caught.value.source, caught.value.code) == (Source.CORE, CoreCode.UNAVAILABLE)
    assert (len(connections), len(streams)) == (1, 1)


def test_a_connection_that_breaks(call: Call) -> None:
    def stop(connection: RawConnection) -> None:
        connection.send_settings()
        connection.read_call()

    with raw_server(stop) as address, pytest.raises(CalyxError) as caught:
        call(address, "list_instances")
    assert (caught.value.source, caught.value.code) == (Source.CORE, CoreCode.UNAVAILABLE)


def test_the_end_of_a_client_closes_its_connection(call: Call) -> None:
    closed = threading.Event()

    def handle(connection: RawConnection) -> None:
        connection.send_settings()
        connection.read_call()
        with suppress(ConnectionError):
            connection.read_to_end()
        closed.set()

    with raw_server(handle) as address:
        with pytest.raises(CalyxError) as caught:
            call(address, "list_instances", time_limit=0.2)
        # The error is still here, and it must not keep the connection.
        assert closed.wait(2)
    check_client_error(caught.value, ClientCode.TIMEOUT)


@pytest.mark.parametrize("name", ["http_proxy", "https_proxy", "grpc_proxy"])
def test_a_proxy_of_the_environment_has_no_effect(
    call: Call, monkeypatch: pytest.MonkeyPatch, name: str
) -> None:
    # A proxy would see the token.
    monkeypatch.setenv(name, "http://127.0.0.1:1")
    with FakeCore(answer_ok) as core:
        call(core.address, "list_instances")


def test_ipv6_and_a_host_name(call: Call) -> None:
    with FakeCore(answer_ok, host="[::1]") as core:
        call(core.address, "list_instances")
    with FakeCore(answer_ok) as core:
        call(f"localhost:{core.port}", "list_instances")
