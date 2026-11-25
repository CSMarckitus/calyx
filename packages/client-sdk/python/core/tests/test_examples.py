"""Runs each example of the README against a fake core."""

import os
import runpy
import time
from collections.abc import Callable, Iterable, Iterator
from contextlib import ExitStack
from pathlib import Path

import pytest
from fake_core import EVENT, TOKEN, Answer, FakeCall, FakeCore
from google.protobuf.message import Message
from google.rpc.status_pb2 import Status

from calyx_client_sdk_core._proto.calyx.core.v1 import event_pb2, instance_pb2, message_pb2

EXAMPLES = Path(__file__).parents[1] / "examples"
TO = "34600000000"
UNAVAILABLE = Status(code=14, message="A test.")
UNAUTHENTICATED = Status(code=16, message="A test.")
INTERNAL = Status(code=13, message="A test.")

type Step = list[Answer | Status]
"""The answer to one call. A Status is an error, after the messages before it."""


@pytest.fixture
def start(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> Iterator[Callable[..., FakeCore]]:
    """Starts a core that gives its answers in their order."""
    with ExitStack() as stack:

        def start(*steps: Step) -> FakeCore:
            left = list(steps)

            def handle(seen: FakeCall) -> Iterable[Answer]:
                for item in left.pop(0):
                    if isinstance(item, Status):
                        seen.fail(item)
                    yield item

            core = stack.enter_context(FakeCore(handle))
            monkeypatch.setenv("CALYX_CORE_ADDRESS", core.address)
            stack.callback(lambda: check(core, left))
            return core

        monkeypatch.setenv("CALYX_CORE_TOKEN", TOKEN)
        monkeypatch.setenv("CALYX_INSTANCE_NAME", "sales")
        monkeypatch.setenv("CALYX_TO", TO)
        monkeypatch.setenv("CALYX_PHONE", TO)
        # An example makes a file in the working folder.
        monkeypatch.chdir(tmp_path)
        yield start


def check(core: FakeCore, left: list[Step]) -> None:
    assert left == []
    for call in core.calls:
        assert call.metadata["authorization"] == f"Bearer {TOKEN}"


@pytest.fixture
def sleeps(monkeypatch: pytest.MonkeyPatch) -> list[float]:
    """The waits of an example. They end at once."""
    found: list[float] = []
    monkeypatch.setattr(time, "sleep", found.append)
    return found


def run(name: str) -> None:
    """Runs an example as a program."""
    runpy.run_path(str(EXAMPLES / name), run_name="__main__")


def run_to_exit(name: str) -> str:
    with pytest.raises(SystemExit) as stopped:
        run(name)
    return str(stopped.value)


def read[M: Message](call: FakeCall, kind: type[M]) -> M:
    return kind.FromString(call.body)


def test_every_example_has_a_test() -> None:
    tests = Path(__file__).read_text()
    for path in EXAMPLES.iterdir():
        assert f'"{path.name}"' in tests, f"No test runs {path.name}."


# The first message.


def sent(replayed: bool) -> Step:
    return [message_pb2.SendPlainTextResponse(message_id="m1", replayed=replayed)]


def test_send(
    start: Callable[..., FakeCore], capsys: pytest.CaptureFixture[str], tmp_path: Path
) -> None:
    core = start(sent(replayed=False), sent(replayed=True))

    run("send.py")
    assert "m1" in capsys.readouterr().out
    # A second run of the program uses the stored key.
    run("send.py")
    assert "True" in capsys.readouterr().out

    key = (tmp_path / "order-42.key").read_text()
    for call in core.calls:
        assert read(call, message_pb2.SendPlainTextRequest) == message_pb2.SendPlainTextRequest(
            instance_name="sales", to=TO, text="Your order 42 is ready.", idempotency_key=key
        )


def test_send_stores_the_key_before_the_call(
    start: Callable[..., FakeCore], tmp_path: Path
) -> None:
    core = start([UNAVAILABLE])
    assert run_to_exit("send.py") == "UNAVAILABLE: A test. (retry_now)"
    request = read(core.calls[0], message_pb2.SendPlainTextRequest)
    assert request.idempotency_key == (tmp_path / "order-42.key").read_text()


def test_send_does_not_use_an_old_key(start: Callable[..., FakeCore], tmp_path: Path) -> None:
    core = start()
    key_file = tmp_path / "order-42.key"
    key_file.write_text("key-1")
    old = time.time() - 24 * 60 * 60
    os.utime(key_file, (old, old))
    assert "too old" in run_to_exit("send.py")
    assert core.calls == []


# The events.


def event(number: int, name: str, data: str) -> event_pb2.Event:
    made = event_pb2.Event()
    made.CopyFrom(EVENT)
    made.id, made.type, made.payload_json = f"evt-{number}", name, data
    return made


EVENTS = [
    event(
        1,
        "message.received",
        '{"messageId":"m1","chatId":"c","from":"f","fromMe":false,"isGroup":false,"type":"text",'
        '"text":"private text","timestamp":1}',
    ),
    event(2, "message.status", '{"messageId":"m2","chatId":"c","status":"READ"}'),
    event(3, "connection.update", '{"status":"CONNECTED"}'),
    event(
        4,
        "bulk.finished",
        '{"jobId":"job","status":"COMPLETED","totalMessages":1,"sentMessages":1,"failedMessages":0}',
    ),
    event(5, "message.status", '{"messageId":1}'),
    event(6, "whatsmeow.Receipt", "{}"),
]


def test_subscribe_handles_each_event(
    start: Callable[..., FakeCore], sleeps: list[float], caplog: pytest.LogCaptureFixture
) -> None:
    caplog.set_level("INFO")
    start([*EVENTS, UNAUTHENTICATED])
    run_to_exit("subscribe.py")
    texts = [record.message for record in caplog.records]
    assert len(texts) == len(EVENTS)
    for text, part in zip(
        texts, ["m1", "m2", "CONNECTED", "job", "evt-5", "whatsmeow"], strict=True
    ):
        assert part in text
    assert [record.levelname == "ERROR" for record in caplog.records].index(True) == 4
    assert "private text" not in caplog.text
    assert sleeps == []


def test_subscribe_opens_a_broken_stream_again(
    start: Callable[..., FakeCore], sleeps: list[float], caplog: pytest.LogCaptureFixture
) -> None:
    caplog.set_level("INFO")
    start(
        [EVENTS[1], UNAVAILABLE],
        # An event that is not valid ends the stream.
        [event(7, "connection.update", "{")],
        [INTERNAL],
        # The core ends the stream.
        [],
        [EVENTS[2], UNAUTHENTICATED],
    )
    assert run_to_exit("subscribe.py") == "UNAUTHENTICATED: A test. (do_not_retry)"
    assert len(sleeps) == 4
    for part in ["m2", "UNAVAILABLE", "invalid_event", "INTERNAL", "stream_failed", "CONNECTED"]:
        assert part in caplog.text


# The pairing.


def test_create_instance(
    start: Callable[..., FakeCore], capsys: pytest.CaptureFixture[str]
) -> None:
    core = start(
        [
            instance_pb2.CreateInstanceResponse(
                status="INSTANCE_STATUS_NEEDS_AUTH", pairing_code="ABCD-2345"
            ),
            instance_pb2.CreateInstanceResponse(status="INSTANCE_STATUS_CONNECTED"),
        ]
    )
    run("create_instance.py")
    assert read(core.calls[0], instance_pb2.CreateInstanceRequest) == (
        instance_pb2.CreateInstanceRequest(
            instance_name="sales",
            whatsapp_web=instance_pb2.WhatsAppWebSettings(
                locale="es-ES",
                pairing_code=instance_pb2.PairingCodeSettings(phone_number=TO),
            ),
        )
    )
    out = capsys.readouterr().out
    assert "ABCD-2345" in out
    assert out.endswith("Connected\n")


def test_create_instance_error(start: Callable[..., FakeCore]) -> None:
    start([Status(code=9, message="A test.")])
    assert run_to_exit("create_instance.py") == "FAILED_PRECONDITION: A test. (do_not_retry)"
