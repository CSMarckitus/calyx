import asyncio
import gc
import json
import signal
import socket
import subprocess
import sys
import threading
import time
import weakref
from collections.abc import Awaitable, Callable, Iterable, Iterator
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Self, get_type_hints

import grpc
import pytest
from calyx_client_sdk import (
    BulkFinishedEvent,
    CalyxError,
    ClientCode,
    ConnectionUpdateEvent,
    CoreCode,
    InvalidPayloadEvent,
    MessageReceivedEvent,
    MessageStatusEvent,
    OtherEvent,
    RetryAdvice,
    Source,
    decode_event,
)
from fake_core import (
    EVENT,
    EVENT_READ,
    MAX_WAIT,
    TOKEN,
    Answer,
    FakeCall,
    FakeCore,
    RawConnection,
    answer_empty,
    check_client_error,
    check_config_error,
    raw_server,
    sending,
    set_short_keepalive,
    start_proxy,
    wait_for_end,
)
from google.protobuf.any_pb2 import Any as AnyMessage
from google.protobuf.timestamp_pb2 import Timestamp
from google.rpc.error_details_pb2 import ErrorInfo
from google.rpc.status_pb2 import Status

import calyx_client_sdk_core
from calyx_client_sdk_core import (
    AsyncCoreClient,
    AsyncEventStream,
    AsyncInstanceStream,
    CoreClient,
    EventStream,
    InstanceStream,
)
from calyx_client_sdk_core._proto.calyx.core.v1 import event_pb2, instance_pb2
from calyx_client_sdk_core.models import (
    CreateInstanceResponse,
    DeviceIdentity,
    InstanceStatus,
    ListInstancesResponse,
    MetaCloudSettings,
    PairingCodeSettings,
    WebhookConfig,
    WhatsAppWebSettings,
)

CREATE_PATH = "/calyx.core.v1.InstanceService/CreateInstance"
SUBSCRIBE_PATH = "/calyx.core.v1.EventService/Subscribe"
WEB = WhatsAppWebSettings(locale="es-ES")
META = MetaCloudSettings(phone_number_id="1", access_token="a", app_secret="s")
EVENTS = json.loads((Path(__file__).parents[3] / "testdata" / "events.json").read_text())["cases"]

UPDATE = instance_pb2.CreateInstanceResponse(status=3, pairing_code="QR-1", message="Pair.")
UPDATE_READ = CreateInstanceResponse(
    status=InstanceStatus.NEEDS_AUTH,
    pairing_code="QR-1",
    message="Pair.",
    device_identity=DeviceIdentity.UNSPECIFIED,
)
LAST = instance_pb2.CreateInstanceResponse(status=1, device_identity=1, locale="es-ES")
LAST_READ = CreateInstanceResponse(
    status=InstanceStatus.CONNECTED,
    message="",
    device_identity=DeviceIdentity.CHROME_WINDOWS,
    locale="es-ES",
)


# The same tests run on both clients: these classes read an async stream like a sync one.


class BlockingStream:
    """Runs each step of an async stream in a task of its own."""

    def __init__(self, stream: Any, run: Callable[[Awaitable[Any]], Any]) -> None:
        self.stream = stream
        self._run = run

    def __enter__(self) -> Self:
        assert self._run(self.stream.__aenter__()) is self.stream
        return self

    def __exit__(self, *details: Any) -> None:
        # A value that is true would stop the exception.
        assert not self._run(self.stream.__aexit__(*details))

    def __iter__(self) -> Self:
        assert self.stream.__aiter__() is self.stream
        return self

    def __next__(self) -> Any:
        try:
            return self._run(self.stream.__anext__())
        except StopAsyncIteration:
            pass
        raise StopIteration


class BlockingClient:
    def __init__(self, client: AsyncCoreClient, runner: asyncio.Runner) -> None:
        self._client = client
        self._runner = runner

    def _run(self, step: Awaitable[Any]) -> Any:
        async def main() -> Any:
            return await step

        return self._runner.run(main())

    def __enter__(self) -> Self:
        return self

    def __exit__(self, *details: Any) -> None:
        self.close()

    def close(self) -> None:
        self._run(self._client.close())

    def subscribe(self, **arguments: Any) -> BlockingStream:
        return BlockingStream(self._client.subscribe(**arguments), self._run)

    def create_instance(self, *names: str, **arguments: Any) -> BlockingStream:
        return BlockingStream(self._client.create_instance(*names, **arguments), self._run)

    def list_instances(self) -> Any:
        return self._run(self._client.list_instances())


type Stream = EventStream | InstanceStream | BlockingStream


@dataclass(frozen=True, slots=True)
class Connect:
    runner: asyncio.Runner | None
    """None for the sync client."""

    def __call__(self, address: str, **options: Any) -> Any:
        options = {"insecure": True, **options}
        if self.runner is None:
            return CoreClient(address, TOKEN, **options)
        return BlockingClient(AsyncCoreClient(address, TOKEN, **options), self.runner)


@pytest.fixture(params=["sync", "async"])
def connect(request: pytest.FixtureRequest) -> Iterator[Connect]:
    if request.param == "sync":
        yield Connect(None)
    else:
        with asyncio.Runner(debug=True) as runner:
            yield Connect(runner)


def open_create(client: Any, **arguments: Any) -> Any:
    return client.create_instance("sales", **{"provider": WEB, **arguments})


def open_subscribe(client: Any, **arguments: Any) -> Any:
    return client.subscribe(**arguments)


type Open = Callable[..., Any]

# How to open the stream, an item of the core, and the item of the program.
each_stream = pytest.mark.parametrize(
    ("open_stream", "sent", "read"),
    [(open_create, UPDATE, UPDATE_READ), (open_subscribe, EVENT, EVENT_READ)],
    ids=["create", "subscribe"],
)


def failing(status: Status, *items: Answer) -> Any:
    def handle(seen: FakeCall) -> Iterable[Answer]:
        yield from items
        seen.fail(status)

    return handle


def read_all(stream: Any, core: FakeCore | None = None) -> tuple[list[Any], CalyxError | None]:
    """Reads a stream to its end. Then the call is closed, also inside the block."""
    items: list[Any] = []
    failure = None
    with stream:
        try:
            for item in stream:
                items.append(item)
        except CalyxError as error:
            failure = error
        if core is not None:
            assert all(seen.ended.wait(1) for seen in core.calls)
        for _ in range(2):
            assert next(stream, None) is None
    if core is not None:
        # The SDK never opens a stream again.
        assert len(core.calls) == 1
    return items, failure


def check_timeout(error: CalyxError | None) -> None:
    assert error is not None
    check_client_error(error, ClientCode.TIMEOUT)
    assert error.detail == "The core did not answer in time."
    assert isinstance(error.__cause__, TimeoutError)


def check_aborted(error: CalyxError) -> None:
    check_client_error(error, ClientCode.ABORTED)
    assert error.detail == "The client is closed."


# create_instance.


@pytest.mark.parametrize(
    ("provider", "settings"),
    [
        (
            WhatsAppWebSettings(
                pairing_code=PairingCodeSettings(phone_number="34600000000"),
                device_identity=DeviceIdentity.FIREFOX_LINUX,
                locale="es-ES",
            ),
            {
                "whatsapp_web": instance_pb2.WhatsAppWebSettings(
                    pairing_code=instance_pb2.PairingCodeSettings(phone_number="34600000000"),
                    device_identity="DEVICE_IDENTITY_FIREFOX_LINUX",
                    locale="es-ES",
                )
            },
        ),
        (WhatsAppWebSettings(), {"whatsapp_web": instance_pb2.WhatsAppWebSettings()}),
        (
            META,
            {
                "meta_cloud": instance_pb2.MetaCloudSettings(
                    phone_number_id="1", access_token="a", app_secret="s"
                )
            },
        ),
    ],
    ids=["code", "qr", "meta"],
)
def test_the_request_of_create_instance(
    connect: Connect, provider: Any, settings: dict[str, Any]
) -> None:
    webhook = WebhookConfig(url="https://example.com", event="message.received", secret="s")
    with FakeCore(sending(LAST)) as core, connect(core.address) as client:
        stream = client.create_instance(
            "sales", provider=provider, webhooks=[webhook], is_used_only_for_send_messages=False
        )
        assert read_all(stream, core) == ([LAST_READ], None)
        (seen,) = core.calls
    assert seen.path == CREATE_PATH
    assert seen.metadata["authorization"] == f"Bearer {TOKEN}"
    assert seen.time_limit is None
    assert instance_pb2.CreateInstanceRequest.FromString(
        seen.body
    ) == instance_pb2.CreateInstanceRequest(
        instance_name="sales",
        is_used_only_for_send_messages=False,
        webhooks=[
            instance_pb2.WebhookConfig(
                url="https://example.com", event="message.received", secret="s"
            )
        ],
        **settings,
    )


def test_the_smallest_request_of_create_instance(connect: Connect) -> None:
    with FakeCore(sending(LAST)) as core, connect(core.address) as client:
        read_all(client.create_instance(instance_name="sales", provider=META), core)
        request = instance_pb2.CreateInstanceRequest.FromString(core.calls[0].body)
    assert not request.HasField("is_used_only_for_send_messages")
    assert list(request.webhooks) == []


def test_the_updates_of_a_pairing(connect: Connect) -> None:
    with FakeCore(sending(UPDATE, 0.1, UPDATE, LAST)) as core, connect(core.address) as client:
        stream = open_create(client)
        # The end of the stream is no error.
        assert read_all(stream, core) == ([UPDATE_READ, UPDATE_READ, LAST_READ], None)


class OtherWeb:
    """Not a class of the SDK, with the same attributes."""

    pairing_code = None
    device_identity = None
    locale = None


@pytest.mark.parametrize(
    "provider",
    [
        None,
        "WHATSAPP_WEB",
        {"locale": "es-ES"},
        (WEB, META),
        PairingCodeSettings(phone_number="1"),
        WhatsAppWebSettings,
        OtherWeb(),
        instance_pb2.WhatsAppWebSettings(),
    ],
    ids=lambda value: repr(value)[:40],
)
def test_a_provider_of_a_wrong_class(connect: Connect, provider: Any) -> None:
    with FakeCore(answer_empty) as core, connect(core.address) as client:
        # At the call of the method, not at the first read.
        with pytest.raises(CalyxError) as caught:
            client.create_instance("sales", provider=provider)
        assert core.calls == []
    check_config_error(caught.value)
    assert caught.value.detail == ("provider must be a WhatsAppWebSettings or a MetaCloudSettings.")


# The arguments.


@pytest.mark.parametrize(
    ("open_stream", "arguments", "detail"),
    [
        (
            open_create,
            {"provider": WhatsAppWebSettings(device_identity="private")},
            "provider.device_identity must be one of UNSPECIFIED, ",
        ),
        (open_create, {"webhooks": "private"}, "webhooks must be a list or a tuple."),
        (open_create, {"webhooks": None}, "webhooks must be a list or a tuple."),
        (
            open_create,
            {"is_used_only_for_send_messages": "private"},
            "is_used_only_for_send_messages must be a bool.",
        ),
        (
            open_subscribe,
            {"instance_names": "private"},
            "instance_names must be a list or a tuple.",
        ),
        (open_subscribe, {"event_types": [5]}, "event_types[0] must be a str of valid Unicode."),
        *(
            (open_stream, {"time_limit": limit}, "The time limit must be a number of seconds")
            for open_stream in (open_create, open_subscribe)
            for limit in (0, -1, float("nan"), float("inf"), True, "private")
        ),
    ],
)
def test_a_wrong_argument_fails_before_the_first_read(
    connect: Connect, open_stream: Open, arguments: dict[str, Any], detail: str
) -> None:
    with connect("127.0.0.1:1") as client, pytest.raises(CalyxError) as caught:
        open_stream(client, **arguments)
    check_config_error(caught.value)
    assert caught.value.detail.startswith(detail)
    assert "private" not in caught.value.detail


# subscribe.


@pytest.mark.parametrize(
    ("arguments", "request_"),
    [
        ({}, event_pb2.SubscribeRequest()),
        (
            {"instance_names": ["sales", "help"], "event_types": ("message.received",)},
            event_pb2.SubscribeRequest(
                instance_names=["sales", "help"], event_types=["message.received"]
            ),
        ),
    ],
)
def test_the_request_of_subscribe(
    connect: Connect, arguments: dict[str, Any], request_: event_pb2.SubscribeRequest
) -> None:
    with FakeCore(sending(EVENT)) as core, connect(core.address) as client:
        items, _ = read_all(client.subscribe(**arguments), core)
        (seen,) = core.calls
    assert items == [EVENT_READ]
    assert (seen.path, seen.time_limit) == (SUBSCRIBE_PATH, None)
    assert seen.metadata["authorization"] == f"Bearer {TOKEN}"
    assert event_pb2.SubscribeRequest.FromString(seen.body) == request_


def test_a_subscribe_that_ends_with_ok_gives_stream_failed(connect: Connect) -> None:
    with FakeCore(sending(EVENT)) as core, connect(core.address) as client:
        items, error = read_all(client.subscribe(), core)
    assert items == [EVENT_READ]
    assert error is not None
    check_client_error(error, ClientCode.STREAM_FAILED)
    assert error.detail == "The core ended the event stream."
    assert (error.__cause__, error.__context__) == (None, None)


def to_message(body: bytes) -> event_pb2.Event:
    """Makes the event of the stream that has the same envelope as a webhook."""
    envelope = json.loads(body)
    time = Timestamp()
    time.FromJsonString(envelope["timestamp"])
    return event_pb2.Event(
        id=envelope.get("id", ""),
        instance_name=envelope["instance"],
        type=envelope["event"],
        payload_json=json.dumps(envelope["data"]),
        occurred_at=time,
    )


@pytest.mark.parametrize("case", EVENTS, ids=lambda case: case["name"])
def test_an_event_is_the_one_of_the_webhook(connect: Connect, case: dict[str, Any]) -> None:
    body = case["body"].encode()
    with FakeCore(sending(to_message(body))) as core, connect(core.address) as client:
        items, error = read_all(client.subscribe(), core)
    assert error is not None
    if "error" in case["expected"]:
        assert items == []
        check_client_error(error, case["expected"]["error"], RetryAdvice.DO_NOT_RETRY)
    else:
        assert items == [decode_event(body)]
        check_client_error(error, ClientCode.STREAM_FAILED)


@pytest.mark.parametrize(
    ("seconds", "nanos", "text"),
    [
        (0, 0, "1970-01-01T00:00:00Z"),
        (10, 0, "1970-01-01T00:00:10Z"),
        (0, 500_000_000, "1970-01-01T00:00:00.5Z"),
        (0, 120_000, "1970-01-01T00:00:00.00012Z"),
        (0, 1, "1970-01-01T00:00:00.000000001Z"),
        (-1, 0, "1969-12-31T23:59:59Z"),
        (-62135596800, 0, "0001-01-01T00:00:00Z"),
        (253402300799, 999_999_999, "9999-12-31T23:59:59.999999999Z"),
    ],
)
def test_the_time_has_the_text_of_the_webhook(
    connect: Connect, seconds: int, nanos: int, text: str
) -> None:
    event = event_pb2.Event()
    event.CopyFrom(EVENT)
    event.occurred_at.CopyFrom(Timestamp(seconds=seconds, nanos=nanos))
    with FakeCore(sending(event)) as core, connect(core.address) as client:
        ((item,), _) = read_all(client.subscribe(), core)
    assert item.timestamp == text


def changed(**fields: Any) -> event_pb2.Event:
    event = event_pb2.Event()
    event.CopyFrom(EVENT)
    for name, value in fields.items():
        event.ClearField(name)  # type: ignore[arg-type]
        if value is not None:
            event.MergeFrom(event_pb2.Event(**{name: value}))
    return event


@pytest.mark.parametrize(
    "fields",
    [
        {"id": None},
        {"type": None},
        {"instance_name": None},
        {"occurred_at": None},
        {"occurred_at": Timestamp(seconds=253402300800)},
        {"occurred_at": Timestamp(seconds=-62135596801)},
        {"occurred_at": Timestamp(nanos=1_000_000_000)},
        {"occurred_at": Timestamp(nanos=-1)},
        {"payload_json": None},
        {"payload_json": '{"private":'},
        {"payload_json": "private"},
        {"payload_json": "NaN"},
        {"payload_json": "[" * 100_000},
    ],
    ids=repr,
)
def test_an_invalid_event_ends_the_stream(connect: Connect, fields: dict[str, Any]) -> None:
    core_stream = sending(EVENT, changed(**fields), EVENT, wait=True)
    with FakeCore(core_stream) as core, connect(core.address) as client:
        items, error = read_all(client.subscribe(), core)
    assert items == [EVENT_READ]
    assert error is not None
    check_client_error(error, ClientCode.INVALID_EVENT, RetryAdvice.DO_NOT_RETRY)
    current: BaseException | None = error
    while current is not None:
        assert "private" not in f"{current!r} {current} {current.__dict__}"
        current = current.__cause__ or current.__context__


@pytest.mark.parametrize("payload", ["{}", "null", "5", "[]", '{"status":5}'])
def test_a_wrong_payload_does_not_end_the_stream(connect: Connect, payload: str) -> None:
    core_stream = sending(changed(payload_json=payload), EVENT)
    with FakeCore(core_stream) as core, connect(core.address) as client:
        items, error = read_all(client.subscribe(), core)
    assert isinstance(items[0], InvalidPayloadEvent)
    assert (items[0].event, items[0].data) == ("connection.update", json.loads(payload))
    assert items[0].reason
    assert items[1:] == [EVENT_READ]
    assert error is not None
    assert error.code == ClientCode.STREAM_FAILED


def test_a_raw_event_has_any_json(connect: Connect) -> None:
    event = changed(type="whatsmeow.Receipt", payload_json="[1, null]")
    with FakeCore(sending(event)) as core, connect(core.address) as client:
        ((item,), _) = read_all(client.subscribe(), core)
    assert isinstance(item, OtherEvent)
    assert (item.event, item.data) == ("whatsmeow.Receipt", [1, None])


# The errors of both streams.


@each_stream
def test_a_connection_that_fails(
    connect: Connect, open_stream: Open, sent: Answer, read: Any
) -> None:
    with socket.create_server(("127.0.0.1", 0)) as listener:
        address = f"127.0.0.1:{listener.getsockname()[1]}"
    with connect(address) as client:
        items, error = read_all(open_stream(client))
    assert items == []
    assert error is not None
    assert (error.source, error.code, error.status) == (Source.CORE, CoreCode.UNAVAILABLE, 14)
    assert error.retry is RetryAdvice.RETRY_NOW
    assert (error.__cause__, error.__context__) == (None, None)


@each_stream
def test_a_connection_that_breaks(
    connect: Connect, open_stream: Open, sent: Answer, read: Any
) -> None:
    def stop(connection: RawConnection) -> None:
        connection.send_settings()
        connection.read_call()

    with raw_server(stop) as address, connect(address) as client:
        items, error = read_all(open_stream(client))
    assert items == []
    assert error is not None
    assert (error.source, error.code) == (Source.CORE, CoreCode.UNAVAILABLE)


@each_stream
def test_a_core_host_that_disappears(
    connect: Connect,
    open_stream: Open,
    sent: Answer,
    read: Any,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    def handle(seen: FakeCall) -> Any:
        return b"" if seen.path.endswith("ListInstances") else sending(sent, wait=True)(seen)

    set_short_keepalive(monkeypatch)
    with (
        FakeCore(handle) as core,
        start_proxy(core) as (proxy, address),
        connect(address) as client,
    ):
        # Without the keepalive, the stream ends at this limit.
        with open_stream(client, time_limit=6) as stream:
            assert next(stream) == read
            # gRPC sends two pings here. Alone, it would limit the next ones.
            time.sleep(2.5)
            proxy.cut()
            start = time.monotonic()
            with pytest.raises(CalyxError) as raised:
                next(stream)
            assert time.monotonic() - start < 3
        error = raised.value
        assert (error.source, error.code, error.status) == (Source.CORE, CoreCode.UNAVAILABLE, 14)
        assert error.retry is RetryAdvice.RETRY_NOW
        # The SDK does not call again by itself. The next call opens a new connection.
        assert (len(core.calls), proxy.connections) == (1, 1)
        assert client.list_instances() == ListInstancesResponse(instances=())
        assert (len(core.calls), proxy.connections) == (2, 2)


@each_stream
@pytest.mark.parametrize("count", [0, 3])
def test_an_error_of_the_core(
    connect: Connect, open_stream: Open, sent: Answer, read: Any, count: int
) -> None:
    info = AnyMessage()
    info.Pack(
        ErrorInfo(
            reason=CoreCode.INSTANCE_QUEUE_FULL,
            domain="calyx.core.v1",
            metadata={"instance": "sales"},
        )
    )
    status = Status(code=8, message="The text of the core.", details=[info])
    with FakeCore(failing(status, *[sent] * count)) as core, connect(core.address) as client:
        items, error = read_all(open_stream(client), core)
    assert items == [read] * count
    assert error is not None
    assert (error.source, error.code) == (Source.CORE, CoreCode.INSTANCE_QUEUE_FULL)
    assert (error.status, error.retry) == (8, RetryAdvice.RETRY_LATER)
    assert error.detail == "The text of the core."
    assert (error.metadata, error.idempotency_key) == ({"instance": "sales"}, None)
    assert (error.__cause__, error.__context__) == (None, None)


@each_stream
@pytest.mark.parametrize("end", [None, Status(code=13, message="Broken.")], ids=["ok", "error"])
def test_the_items_before_the_end_stay(
    connect: Connect, open_stream: Open, sent: Answer, read: Any, end: Status | None
) -> None:
    handle = sending(*[sent] * 20) if end is None else failing(end, *[sent] * 20)
    with FakeCore(handle) as core, connect(core.address) as client:
        with open_stream(client) as stream:
            items = [next(stream)]
            # The other items and the end arrive here.
            time.sleep(0.3)
            items += (next(stream) for _ in range(19))
            assert items == [read] * 20
            if end is None and open_stream is open_create:
                assert next(stream, None) is None
            else:
                with pytest.raises(CalyxError):
                    next(stream)


@each_stream
@pytest.mark.parametrize("data", [b"\xff", b"\x0a\x05ab", b"\x1a\x01\xff"])
def test_an_item_that_is_not_protobuf(
    connect: Connect, open_stream: Open, sent: Answer, read: Any, data: bytes
) -> None:
    with FakeCore(sending(sent, data, wait=True)) as core, connect(core.address) as client:
        items, error = read_all(open_stream(client), core)
    assert items == [read]
    assert error is not None
    check_client_error(error, ClientCode.INVALID_RESPONSE)


@each_stream
def test_a_cancelled_status_of_the_core_is_no_cancellation(
    connect: Connect, open_stream: Open, sent: Answer, read: Any
) -> None:
    status = Status(code=1, message="Stop.")
    with FakeCore(failing(status, sent)) as core, connect(core.address) as client:
        items, error = read_all(open_stream(client), core)
    assert items == [read]
    assert error is not None
    assert (error.source, error.code, error.detail) == (Source.CORE, CoreCode.CANCELLED, "Stop.")


# The life of a stream.


@each_stream
def test_the_call_starts_at_the_first_read(
    connect: Connect, open_stream: Open, sent: Answer, read: Any
) -> None:
    with FakeCore(sending(sent, wait=True)) as core, connect(core.address) as client:
        stream = open_stream(client)
        with stream as entered:
            assert entered is stream
            assert iter(stream) is stream
            time.sleep(0.2)
            assert core.calls == []
            assert next(stream) == read
            assert len(core.calls) == 1


@each_stream
def test_a_block_with_no_read(connect: Connect, open_stream: Open, sent: Answer, read: Any) -> None:
    with FakeCore(sending(sent)) as core, connect(core.address) as client:
        with open_stream(client) as stream:
            pass
        assert next(stream, None) is None
        assert core.calls == []


@each_stream
def test_a_break_ends_the_call_at_the_end_of_the_block(
    connect: Connect, open_stream: Open, sent: Answer, read: Any
) -> None:
    with FakeCore(sending(sent, wait=True)) as core, connect(core.address) as client:
        with open_stream(client) as stream:
            for item in stream:
                assert item == read
                break
            (seen,) = core.calls
            assert not seen.ended.wait(0.1)
        assert seen.ended.wait(1)
        assert next(stream, None) is None
        # The client still works.
        assert client.subscribe() is not None


@each_stream
def test_the_end_of_the_block_wins_over_an_item_that_waits(
    connect: Connect, open_stream: Open, sent: Answer, read: Any
) -> None:
    with FakeCore(sending(sent, sent, wait=True)) as core, connect(core.address) as client:
        with open_stream(client) as stream:
            assert next(stream) == read
            time.sleep(0.2)
        assert next(stream, None) is None


@pytest.mark.parametrize("kind", [ValueError, KeyboardInterrupt, CalyxError, StopIteration])
@each_stream
def test_an_exception_of_the_program_passes_unchanged(
    connect: Connect,
    open_stream: Open,
    sent: Answer,
    read: Any,
    kind: type[BaseException],
) -> None:
    if kind is CalyxError:
        mine: BaseException = CalyxError(source=Source.CORE, code="x", status=1, detail="")
    else:
        mine = kind("mine")
    with FakeCore(sending(sent, wait=True)) as core, connect(core.address) as client:
        with pytest.raises(kind) as caught, open_stream(client) as stream:
            next(stream)
            raise mine
        assert caught.value is mine
        assert (mine.__cause__, mine.__context__) == (None, None)
        assert core.calls[0].ended.wait(1)


@each_stream
def test_ctrl_c_in_a_read(connect: Connect, open_stream: Open, sent: Answer, read: Any) -> None:
    main = threading.main_thread().ident or 0

    def handle(seen: FakeCall) -> Iterable[Answer]:
        yield sent
        seen.ended.wait(0.3)
        signal.pthread_kill(main, signal.SIGINT)
        seen.wait_for_end()

    with FakeCore(handle) as core, connect(core.address) as client:
        with open_stream(client) as stream:
            assert next(stream) == read
            with pytest.raises(KeyboardInterrupt) as caught:
                next(stream)
            # The exception is still here, and it must not keep the call.
            assert core.calls[0].ended.wait(1)
            assert caught.type is KeyboardInterrupt
            assert next(stream, None) is None


@each_stream
def test_a_stream_with_no_block_closes_at_its_end(
    connect: Connect, open_stream: Open, sent: Answer, read: Any
) -> None:
    with FakeCore(failing(Status(code=14), sent)) as core, connect(core.address) as client:
        stream = open_stream(client)
        with pytest.raises(CalyxError):
            for item in stream:
                assert item == read
        assert core.calls[0].ended.wait(1)


# The program must use a block. Without one, the garbage collection closes the stream, with no
# warning.
@each_stream
def test_a_stream_that_nobody_closes(
    connect: Connect,
    open_stream: Open,
    sent: Answer,
    read: Any,
    caplog: pytest.LogCaptureFixture,
) -> None:
    with FakeCore(sending(sent, wait=True)) as core, connect(core.address) as client:
        for item in open_stream(client):
            assert item == read
            break
        gc.collect()
        assert core.calls[0].ended.wait(1)
    assert caplog.records == []


def test_an_async_stream_that_another_thread_drops(caplog: pytest.LogCaptureFixture) -> None:
    async def main() -> None:
        async with AsyncCoreClient(core.address, TOKEN, insecure=True) as client:
            streams = [client.subscribe()]
            assert await anext(streams[0]) == EVENT_READ
            # The garbage collection can run in any thread.
            await asyncio.to_thread(streams.clear)
            assert await asyncio.to_thread(core.calls[0].ended.wait, 1)

    with FakeCore(sending(EVENT, wait=True)) as core:
        asyncio.run(main(), debug=True)
    assert caplog.records == []


OPEN_AT_EXIT = """
import asyncio, sys
from calyx_client_sdk_core import AsyncCoreClient, CoreClient

async def main():
    await anext(async_stream)

client = CoreClient(*sys.argv[1:], insecure=True)
stream = client.subscribe()
next(stream)
async_client = AsyncCoreClient(*sys.argv[1:], insecure=True)
async_stream = async_client.subscribe()
asyncio.run(main())
"""


def test_a_program_that_ends_with_open_streams() -> None:
    with FakeCore(sending(EVENT, wait=True)) as core:
        command = [sys.executable, "-W", "error", "-c", OPEN_AT_EXIT, core.address, TOKEN]
        done = subprocess.run(command, timeout=8, capture_output=True, text=True, check=False)
        assert (done.returncode, done.stdout, done.stderr) == (0, "", "")
        assert all(seen.ended.wait(1) for seen in core.calls)


def test_an_async_stream_that_stays_after_its_event_loop(
    caplog: pytest.LogCaptureFixture,
) -> None:
    async def main(address: str) -> AsyncEventStream:
        stream = AsyncCoreClient(address, TOKEN, insecure=True).subscribe()
        assert await anext(stream) == EVENT_READ
        return stream

    with FakeCore(sending(EVENT, wait=True)) as core:
        stream = asyncio.run(main(core.address), debug=True)
        del stream
        gc.collect()
        assert core.calls[0].ended.wait(1)
    assert caplog.records == []


@each_stream
def test_many_streams_and_calls_use_one_connection(
    connect: Connect, open_stream: Open, sent: Answer, read: Any
) -> None:
    def handle(seen: FakeCall) -> Any:
        unary = seen.path.endswith("ListInstances")
        return b"" if unary else sending(sent, 0.1, sent, wait=True)(seen)

    with FakeCore(handle) as core, connect(core.address) as client:
        with open_stream(client) as first, open_stream(client) as second:
            assert [next(first), next(second), next(first)] == [read] * 3
            assert client.list_instances() == ListInstancesResponse(instances=())
            with first:
                pass
            # The end of one stream does not change the other one.
            assert next(second) == read
        assert len(core.calls) == 3
        assert len({seen.peer for seen in core.calls}) == 1


@each_stream
@pytest.mark.parametrize("limit", [None, 0.3])
def test_an_error_does_not_keep_the_connection(
    connect: Connect, open_stream: Open, sent: Answer, read: Any, limit: float | None
) -> None:
    closed = threading.Event()

    def handle(connection: RawConnection) -> None:
        connection.send_settings()
        stream = connection.read_call()
        if limit is None:
            # RST_STREAM, with the code CANCEL.
            connection.sock.sendall(b"\0\0\4\3\0" + stream + b"\0\0\0\x08")
        try:
            connection.read_to_end()
        finally:
            closed.set()

    with raw_server(handle) as address:
        with connect(address) as client:
            stream = open_stream(client, time_limit=limit)
            with pytest.raises(CalyxError) as caught:
                next(stream)
        # The error and the stream are still here.
        assert closed.wait(2)
    assert caught.value.code == (CoreCode.CANCELLED if limit is None else ClientCode.TIMEOUT)


def find_grpc_call(stream: Any) -> Any:
    call = getattr(stream, "stream", stream)._call
    assert isinstance(call, grpc.RpcContext | grpc.aio.Call)
    return call


@each_stream
@pytest.mark.parametrize("handle", ["error", "break"])
def test_nothing_keeps_the_call_after_the_end(
    connect: Connect, open_stream: Open, sent: Answer, read: Any, handle: str
) -> None:
    core_stream = failing(Status(code=14), sent) if handle == "error" else sending(sent, wait=True)
    with FakeCore(core_stream) as core, connect(core.address) as client:
        gc.disable()
        try:
            with open_stream(client) as stream:
                next(stream)
                call = weakref.ref(find_grpc_call(stream))
                if handle == "error":
                    with pytest.raises(CalyxError) as caught:
                        next(stream)
                    assert caught.value.code == CoreCode.UNAVAILABLE
            assert next(stream, None) is None
            # grpc needs some time to end the call in its own threads.
            for _ in range(20):
                if call() is None:
                    break
                time.sleep(0.05)
            assert call() is None
        finally:
            gc.enable()


# The time limit.


@each_stream
@pytest.mark.parametrize("count", [0, 1])
def test_the_time_limit_of_a_call(
    connect: Connect, open_stream: Open, sent: Answer, read: Any, count: int
) -> None:
    with FakeCore(sending(*[sent] * count, wait=True)) as core, connect(core.address) as client:
        stream = open_stream(client, time_limit=0.6)
        # The time starts at the first read.
        time.sleep(0.3)
        start = time.monotonic()
        items, error = read_all(stream, core)
        assert 0.6 <= time.monotonic() - start < 1.5
        assert 0.3 < (core.calls[0].time_limit or 0) < 0.7
    assert items == [read] * count
    check_timeout(error)


@each_stream
def test_the_time_limit_covers_the_whole_stream(
    connect: Connect, open_stream: Open, sent: Answer, read: Any
) -> None:
    steps = [0.1, sent] * 30
    with FakeCore(sending(*steps, wait=True)) as core, connect(core.address) as client:
        start = time.monotonic()
        items, error = read_all(open_stream(client, time_limit=0.6), core)
        assert 0.6 <= time.monotonic() - start < 1.5
    assert 1 <= len(items) <= 6
    check_timeout(error)


@each_stream
def test_a_silent_peer_ends_at_the_limit(
    connect: Connect, open_stream: Open, sent: Answer, read: Any
) -> None:
    with raw_server(RawConnection.read_to_end) as address, connect(address) as client:
        start = time.monotonic()
        items, error = read_all(open_stream(client, time_limit=0.5))
        assert 0.5 <= time.monotonic() - start < 1.5
    assert items == []
    check_timeout(error)


@each_stream
@pytest.mark.parametrize("more", [0, 1, 3], ids=["end", "item", "items"])
def test_the_time_limit_counts_the_code_of_the_program(
    connect: Connect, open_stream: Open, sent: Answer, read: Any, more: int
) -> None:
    with FakeCore(sending(sent, *[sent] * more)) as core, connect(core.address) as client:
        with open_stream(client, time_limit=0.4) as stream:
            assert next(stream) == read
            time.sleep(0.5)
            # The data waits in the client, but the limit passed.
            with pytest.raises(CalyxError) as caught:
                next(stream)
            assert core.calls[0].ended.wait(1)
            assert next(stream, None) is None
    check_timeout(caught.value)


@each_stream
def test_a_read_after_the_end_and_the_time_limit(
    connect: Connect, open_stream: Open, sent: Answer, read: Any
) -> None:
    with FakeCore(sending(sent)) as core, connect(core.address) as client:
        with open_stream(client, time_limit=0.4) as stream:
            assert next(stream) == read
            try:
                assert next(stream, None) is None
            except CalyxError as error:
                assert error.code == ClientCode.STREAM_FAILED
            time.sleep(0.5)
            assert next(stream, None) is None


@each_stream
def test_the_limit_of_the_client_does_not_stop_the_stream(
    connect: Connect, open_stream: Open, sent: Answer, read: Any
) -> None:
    with FakeCore(sending(0.4, sent, 0.4, sent)) as core:
        with connect(core.address, time_limit=0.2) as client:
            items, error = read_all(open_stream(client), core)
            assert core.calls[0].time_limit is None
    assert items == [read, read]
    assert error is None or error.code == ClientCode.STREAM_FAILED


@each_stream
def test_the_limit_of_the_call_can_be_longer_than_the_one_of_the_client(
    connect: Connect, open_stream: Open, sent: Answer, read: Any
) -> None:
    with FakeCore(sending(0.4, sent)) as core:
        with connect(core.address, time_limit=0.2) as client:
            items, _ = read_all(open_stream(client, time_limit=2), core)
    assert items == [read]


@each_stream
@pytest.mark.parametrize("limit", [5, 1e30, sys.float_info.max])
def test_a_long_time_limit(
    connect: Connect, open_stream: Open, sent: Answer, read: Any, limit: float
) -> None:
    with FakeCore(sending(sent)) as core, connect(core.address) as client:
        items, _ = read_all(open_stream(client, time_limit=limit), core)
    assert items == [read]


@each_stream
def test_a_deadline_of_the_core_is_an_error_of_the_core(
    connect: Connect, open_stream: Open, sent: Answer, read: Any
) -> None:
    status = Status(code=4, message="Slow.")
    with FakeCore(failing(status, sent)) as core, connect(core.address) as client:
        items, error = read_all(open_stream(client, time_limit=5), core)
    assert items == [read]
    assert error is not None
    assert (error.source, error.code) == (Source.CORE, CoreCode.DEADLINE_EXCEEDED)


# The end of the client.


@each_stream
def test_a_stream_of_a_closed_client(
    connect: Connect, open_stream: Open, sent: Answer, read: Any
) -> None:
    with FakeCore(sending(sent)) as core, connect(core.address) as client:
        before = open_stream(client)
        client.close()
        for stream in (before, open_stream(client)):
            with pytest.raises(CalyxError) as caught:
                next(stream)
            check_aborted(caught.value)
            assert next(stream, None) is None
        assert core.calls == []


@each_stream
def test_the_end_of_the_client_wins_over_an_item_that_waits(
    connect: Connect, open_stream: Open, sent: Answer, read: Any
) -> None:
    with FakeCore(sending(sent, sent, sent, wait=True)) as core, connect(core.address) as client:
        with open_stream(client) as stream:
            assert next(stream) == read
            time.sleep(0.2)
            client.close()
            with pytest.raises(CalyxError) as caught:
                next(stream)
            assert core.calls[0].ended.wait(1)
            assert next(stream, None) is None
    check_aborted(caught.value)


@each_stream
def test_close_stops_a_read(open_stream: Open, sent: Answer, read: Any) -> None:
    with FakeCore(sending(sent, wait=True)) as core:
        client = CoreClient(core.address, TOKEN, insecure=True)
        stream = open_stream(client)
        assert next(stream) == read
        timer = threading.Timer(0.2, client.close)
        timer.start()
        with pytest.raises(CalyxError) as caught:
            next(stream)
        check_aborted(caught.value)
        assert core.calls[0].ended.wait(1)
        timer.join()


@each_stream
def test_close_stops_an_async_read(open_stream: Open, sent: Answer, read: Any) -> None:
    async def main() -> None:
        client = AsyncCoreClient(core.address, TOKEN, insecure=True)
        stream = open_stream(client)
        assert await anext(stream) == read
        task = asyncio.create_task(read_one(stream))
        await asyncio.sleep(0.2)
        await client.close()
        with pytest.raises(CalyxError) as caught:
            await task
        check_aborted(caught.value)
        assert await asyncio.to_thread(core.calls[0].ended.wait, 1)
        assert await anext(stream, None) is None

    with FakeCore(sending(sent, wait=True)) as core:
        asyncio.run(main(), debug=True)


@each_stream
def test_close_in_a_signal_handler_stops_a_read(open_stream: Open, sent: Answer, read: Any) -> None:
    main = threading.main_thread().ident or 0

    def handle(seen: FakeCall) -> Iterable[Answer]:
        yield sent
        seen.ended.wait(0.3)
        signal.pthread_kill(main, signal.SIGUSR1)
        seen.wait_for_end()

    with FakeCore(handle) as core, CoreClient(core.address, TOKEN, insecure=True) as client:
        old = signal.signal(signal.SIGUSR1, lambda *_: client.close())
        try:
            with open_stream(client) as stream:
                assert next(stream) == read
                with pytest.raises(CalyxError) as caught:
                    next(stream)
        finally:
            signal.signal(signal.SIGUSR1, old)
    check_aborted(caught.value)


# The program ends a stream while a read waits. The docs do not promise it, but it must not hang.


@each_stream
def test_the_end_of_a_stream_in_another_thread(open_stream: Open, sent: Answer, read: Any) -> None:
    with FakeCore(sending(sent, wait=True)) as core:
        with CoreClient(core.address, TOKEN, insecure=True) as client:
            stream = open_stream(client)
            assert next(stream) == read
            timer = threading.Timer(0.2, lambda: stream.__exit__(None, None, None))
            timer.start()
            assert next(stream, None) is None
            assert core.calls[0].ended.wait(1)
            timer.join()


@each_stream
def test_the_end_of_a_stream_in_another_task(open_stream: Open, sent: Answer, read: Any) -> None:
    async def main() -> None:
        async with AsyncCoreClient(core.address, TOKEN, insecure=True) as client:
            stream = open_stream(client)
            assert await anext(stream) == read
            task = asyncio.create_task(read_one(stream))
            await asyncio.sleep(0.2)
            await stream.__aexit__(None, None, None)
            assert await task is None
            assert await asyncio.to_thread(core.calls[0].ended.wait, 1)

    with FakeCore(sending(sent, wait=True)) as core:
        asyncio.run(main(), debug=True)


@each_stream
def test_two_threads_read_one_stream(open_stream: Open, sent: Answer, read: Any) -> None:
    more = threading.Event()

    def handle(seen: FakeCall) -> Iterable[Answer]:
        if seen.path.endswith("ListInstances"):
            yield b""
            return
        yield sent
        more.wait(MAX_WAIT)
        yield sent
        seen.wait_for_end()

    with FakeCore(handle) as core, CoreClient(core.address, TOKEN, insecure=True) as client:
        with open_stream(client) as stream, ThreadPoolExecutor() as pool:
            assert next(stream) == read
            first = pool.submit(next, stream)
            time.sleep(0.2)
            with pytest.raises(CalyxError) as caught:
                next(stream)
            check_config_error(caught.value)
            assert caught.value.detail == "Read a stream in one thread at a time."
            client.list_instances(time_limit=1)
            # The first one goes on, and the stream too.
            more.set()
            assert first.result(2) == read
            last = pool.submit(next, stream, None)
            time.sleep(0.1)
            assert not last.done()
            stream.__exit__(None, None, None)
            assert last.result(2) is None


@each_stream
def test_two_tasks_read_one_stream(open_stream: Open, sent: Answer, read: Any) -> None:
    more = threading.Event()

    def handle(seen: FakeCall) -> Iterable[Answer]:
        if seen.path.endswith("ListInstances"):
            yield b""
            return
        yield sent
        more.wait(MAX_WAIT)
        yield sent
        seen.wait_for_end()

    async def main() -> None:
        async with AsyncCoreClient(core.address, TOKEN, insecure=True) as client:
            async with open_stream(client) as stream:
                assert await anext(stream) == read
                first = asyncio.create_task(read_one(stream))
                await asyncio.sleep(0.2)
                with pytest.raises(CalyxError) as caught:
                    await anext(stream)
                check_config_error(caught.value)
                assert caught.value.detail == "Read a stream in one task at a time."
                await client.list_instances(time_limit=1)
                more.set()
                assert await first == read
                # A cancelled read lets the next one in.
                last = asyncio.create_task(read_one(stream))
                await asyncio.sleep(0.1)
                last.cancel()
                with pytest.raises(asyncio.CancelledError):
                    await last
                assert await anext(stream, None) is None

    with FakeCore(handle) as core:
        asyncio.run(main(), debug=True)


@each_stream
def test_a_second_read_of_a_stream_of_a_closed_client(
    open_stream: Open, sent: Answer, read: Any, monkeypatch: pytest.MonkeyPatch
) -> None:
    with FakeCore(sending(sent, wait=True)) as core:
        with CoreClient(core.address, TOKEN, insecure=True) as client:
            with open_stream(client) as stream:
                assert next(stream) == read
                # As when another thread reads.
                with stream._reading:
                    monkeypatch.setattr(client._channel, "_closed", True)
                    with pytest.raises(CalyxError) as caught:
                        next(stream)
                    check_aborted(caught.value)
                    monkeypatch.undo()
                    stream.__exit__(None, None, None)
                    assert next(stream, None) is None


def test_a_stream_that_ends_while_its_call_starts(monkeypatch: pytest.MonkeyPatch) -> None:
    # A signal handler or another thread ends it between two steps of the first read.
    with FakeCore(wait_for_end) as core:
        with CoreClient(core.address, TOKEN, insecure=True) as client:
            stream = client.subscribe()
            open_channel = client._channel._open

            def open_and_end(key: str | None) -> grpc.Channel:
                channel = open_channel(key)
                stream.__exit__(None, None, None)
                return channel

            monkeypatch.setattr(client._channel, "_open", open_and_end)
            start = time.monotonic()
            assert next(stream, None) is None
            assert next(stream, None) is None
            assert time.monotonic() - start < 1
            assert all(seen.ended.wait(1) for seen in core.calls)
            assert len(core.calls) <= 1


# asyncio.


async def read_one(stream: Any) -> Any:
    return await anext(stream, None)


@each_stream
@pytest.mark.parametrize("limit", [None, 5])
def test_a_cancellation_passes_unchanged(
    open_stream: Open,
    sent: Answer,
    read: Any,
    limit: float | None,
    caplog: pytest.LogCaptureFixture,
) -> None:
    async def main() -> None:
        async with AsyncCoreClient(core.address, TOKEN, insecure=True) as client:
            stream = open_stream(client, time_limit=limit)
            assert await anext(stream) == read
            task = asyncio.create_task(read_one(stream))
            await asyncio.sleep(0.1)
            assert task.cancel("stop")
            with pytest.raises(asyncio.CancelledError) as caught:
                await task
            assert caught.value.args == ("stop",)
            assert task.cancelled()
            assert await asyncio.to_thread(core.calls[0].ended.wait, 1)
            assert await anext(stream, None) is None
            # No task of the stream stays. gRPC needs some time for its own one.
            async with asyncio.timeout(1):
                while asyncio.all_tasks() != {asyncio.current_task()}:  # noqa: ASYNC110
                    await asyncio.sleep(0.01)

    with FakeCore(sending(sent, wait=True)) as core:
        asyncio.run(main(), debug=True)
        assert len(core.calls) == 1
    assert caplog.records == []


@each_stream
def test_a_cancellation_of_the_first_read(
    open_stream: Open, sent: Answer, read: Any, caplog: pytest.LogCaptureFixture
) -> None:
    async def main(address: str, delay: float) -> None:
        async with AsyncCoreClient(address, TOKEN, insecure=True) as client:
            stream = open_stream(client)

            async def read_block() -> None:
                async with stream:
                    async for _ in stream:
                        pass

            task = asyncio.create_task(read_block())
            await asyncio.sleep(delay)
            assert task.cancel("stop")
            with pytest.raises(asyncio.CancelledError) as caught:
                await task
            assert caught.value.args == ("stop",)
            assert await anext(stream, None) is None

    with raw_server(RawConnection.read_to_end) as address:
        for delay in (0, 0.001, 0.2):
            asyncio.run(main(address, delay), debug=True)
    gc.collect()
    assert caplog.records == []


@each_stream
def test_a_cancellation_in_the_block(open_stream: Open, sent: Answer, read: Any) -> None:
    async def main() -> None:
        async with AsyncCoreClient(core.address, TOKEN, insecure=True) as client:

            async def read_block() -> None:
                async with open_stream(client) as stream:
                    await anext(stream)
                    await asyncio.sleep(5)

            task = asyncio.create_task(read_block())
            await asyncio.sleep(0.2)
            assert task.cancel("stop")
            with pytest.raises(asyncio.CancelledError) as caught:
                await task
            assert caught.value.args == ("stop",)
            assert await asyncio.to_thread(core.calls[0].ended.wait, 1)

    with FakeCore(sending(sent, wait=True)) as core:
        asyncio.run(main(), debug=True)


@each_stream
def test_a_time_limit_of_the_program_is_not_an_sdk_timeout(
    open_stream: Open, sent: Answer, read: Any
) -> None:
    async def main() -> None:
        async with AsyncCoreClient(core.address, TOKEN, insecure=True) as client:
            with pytest.raises(TimeoutError):
                async with asyncio.timeout(0.3), open_stream(client) as stream:
                    async for item in stream:
                        assert item == read
            assert await asyncio.to_thread(core.calls[0].ended.wait, 1)

    with FakeCore(sending(sent, wait=True)) as core:
        asyncio.run(main(), debug=True)


@each_stream
def test_a_cancellation_at_the_time_limit_wins(open_stream: Open, sent: Answer, read: Any) -> None:
    async def main() -> None:
        async with AsyncCoreClient(core.address, TOKEN, insecure=True) as client:
            stream = open_stream(client, time_limit=0.4)
            assert await anext(stream) == read
            task = asyncio.create_task(read_one(stream))
            asyncio.get_running_loop().call_later(0.35, task.cancel)
            await asyncio.sleep(0.1)
            # The loop stops until both times are in the past, so it handles them in one step.
            time.sleep(0.6)  # noqa: ASYNC251
            with pytest.raises(asyncio.CancelledError):
                await task

    with FakeCore(sending(sent, wait=True)) as core:
        asyncio.run(main())


@each_stream
def test_a_read_lets_other_tasks_run(open_stream: Open, sent: Answer, read: Any) -> None:
    async def main() -> None:
        ticks = 0

        async def tick() -> None:
            nonlocal ticks
            while True:
                ticks += 1
                await asyncio.sleep(0.02)

        task = asyncio.create_task(tick())
        async with AsyncCoreClient(core.address, TOKEN, insecure=True) as client:
            async with open_stream(client) as stream:
                assert [await anext(stream), await anext(stream)] == [read, read]
        assert ticks > 10
        task.cancel()

    with FakeCore(sending(sent, 0.5, sent, wait=True)) as core:
        asyncio.run(main())


def test_two_tasks_read_two_streams() -> None:
    async def main() -> None:
        async with AsyncCoreClient(core.address, TOKEN, insecure=True) as client:

            async def collect() -> list[Any]:
                async with client.create_instance("sales", provider=WEB) as stream:
                    return [update async for update in stream]

            assert (
                await asyncio.gather(*(collect() for _ in range(5)))
                == [[UPDATE_READ, LAST_READ]] * 5
            )

    with FakeCore(sending(UPDATE, 0.1, LAST)) as core:
        asyncio.run(main(), debug=True)
        assert len({seen.peer for seen in core.calls}) == 1


# The public classes.

STREAMS = [InstanceStream, AsyncInstanceStream, EventStream, AsyncEventStream]


def test_the_public_names() -> None:
    assert {kind.__name__ for kind in STREAMS} <= set(calyx_client_sdk_core.__all__)
    assert len(calyx_client_sdk_core.__all__) == 7


@pytest.mark.parametrize("kind", STREAMS)
def test_the_text_of_a_stream(kind: type[Any]) -> None:
    text = " ".join((kind.__doc__ or "").split())
    for word in ("Example:", "block", "first read", "read after the end gives no item"):
        assert word in text
    assert ("async with" in text) is kind.__name__.startswith("Async")
    for name in vars(kind):
        assert name.startswith("_")


@pytest.mark.parametrize("kind", STREAMS)
def test_no_public_attribute_shows_grpc_or_protobuf(kind: type[Any]) -> None:
    prefix = "a" if kind.__name__.startswith("Async") else ""
    for name in ("enter", "exit", "iter", "next"):
        for hint in get_type_hints(getattr(kind, f"__{prefix}{name}__")).values():
            for word in ("grpc", "protobuf", "_proto"):
                assert word not in repr(hint) + getattr(hint, "__module__", "")


def describe(event: Any) -> str:
    match event:
        case ConnectionUpdateEvent(data=data):
            return data.status
        case MessageReceivedEvent(data=data):
            return data.from_
        case MessageStatusEvent(data=data) | BulkFinishedEvent(data=data):
            return data.status
        case OtherEvent(event=name) | InvalidPayloadEvent(event=name):
            return name
        case _:
            raise AssertionError


def test_match() -> None:
    assert describe(EVENT_READ) == "CONNECTED"
