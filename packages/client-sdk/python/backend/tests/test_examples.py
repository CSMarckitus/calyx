"""Runs each example of the README against a fake backend."""

import asyncio
import hashlib
import hmac
import importlib.util
import json
import os
import runpy
import sqlite3
import sys
import threading
import time
from collections.abc import Callable, Iterable, Iterator
from contextlib import closing
from pathlib import Path
from types import ModuleType
from typing import Any

import pytest
import readme_check
from fake_server import Connection, FakeServer, Request

from calyx_client_sdk import CalyxError, Source

EXAMPLES = Path(__file__).parents[1] / "examples"
ID = "0b5e1a52-0c7e-4a4b-9a53-0f1c8d1a7c11"
TO = "34600000000"
SECRET = "test-secret-not-real"
QR = "private-qr"

type Answer = (
    tuple[int, object] | tuple[int, object, str] | bytes | Callable[[Connection], None] | None
)
"""A status with a JSON body (and headers), raw bytes, code, or None for a connection that breaks."""


class Backend:
    """A fake backend that gives its answers in their order."""

    def __init__(self) -> None:
        self._answers: list[Answer] = []
        self._server = FakeServer(self._handle)

    def __enter__(self) -> "Backend":
        self._server.__enter__()
        return self

    def __exit__(self, *details: Any) -> None:
        self._server.__exit__(None, None, None)

    @property
    def url(self) -> str:
        return f"http://127.0.0.1:{self._server.port}"

    @property
    def requests(self) -> list[Request]:
        return self._server.requests

    @property
    def lines(self) -> list[str]:
        return [r.line.removesuffix(" HTTP/1.1") for r in self.requests]

    @property
    def keys(self) -> list[str]:
        return [key for r in self.requests for key in r.all("Idempotency-Key")]

    def body(self, number: int) -> Any:
        return json.loads(self.requests[number].body)

    def give(self, *answers: Answer) -> None:
        self._answers.extend(answers)

    @property
    def unused(self) -> int:
        return len(self._answers)

    def _handle(self, connection: Connection) -> None:
        connection.read_request()
        match self._answers.pop(0):
            case None:
                connection.sock.close()
            case bytes(raw):
                connection.sock.sendall(raw)
            case (status, body, headers):
                connection.answer(status, json.dumps(body).encode(), headers)
            case (status, body):
                connection.answer(status, json.dumps(body).encode())
            case code:
                code(connection)


def problem(status: int, code: str, *, retry_after: int | None = None) -> Answer:
    headers = "Content-Type: application/problem+json\r\n"
    if retry_after is not None:
        headers += f"Retry-After: {retry_after}\r\n"
    return status, {"status": status, "code": code, "detail": "A test."}, headers


def sent(message_id: str = "m1", *, replayed: bool = False) -> Answer:
    return 200, {"message_id": message_id, "replayed": replayed}


@pytest.fixture
def backend(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> Iterator[Backend]:
    with Backend() as server:
        monkeypatch.setenv("CALYX_URL", server.url)
        monkeypatch.setenv("CALYX_API_KEY", "cx_test")
        monkeypatch.setenv("CALYX_INSTANCE_ID", ID)
        monkeypatch.setenv("CALYX_WEBHOOK_SECRET", SECRET)
        monkeypatch.setenv("CALYX_TO", TO)
        monkeypatch.setenv("CALYX_RECIPIENTS", "34600000001, 34600000002")
        monkeypatch.setenv("CELERY_BROKER_URL", "memory://")
        monkeypatch.setenv("CELERY_RESULT_BACKEND", "cache+memory://")
        # Some examples make a file in the working folder.
        monkeypatch.chdir(tmp_path)
        yield server
    assert server.unused == 0
    for request in server.requests:
        assert request.all("Authorization") == ["Bearer cx_test"]


@pytest.fixture
def sleeps(monkeypatch: pytest.MonkeyPatch) -> list[float]:
    """The waits of an example. They end at once."""
    found: list[float] = []
    monkeypatch.setattr(time, "sleep", found.append)
    return found


def run(name: str) -> None:
    """Runs an example as a program."""
    runpy.run_path(str(EXAMPLES / name), run_name="__main__")


@pytest.fixture
def load(monkeypatch: pytest.MonkeyPatch) -> Callable[[str], ModuleType]:
    """Imports an example, as a program does with its own module."""

    def load(name: str) -> ModuleType:
        module_name = f"example_{name.removesuffix('.py')}"
        spec = importlib.util.spec_from_file_location(module_name, EXAMPLES / name)
        assert spec is not None and spec.loader is not None
        module = importlib.util.module_from_spec(spec)
        monkeypatch.setitem(sys.modules, module_name, module)
        spec.loader.exec_module(module)
        return module

    return load


def test_every_example_has_a_test() -> None:
    tests = Path(__file__).read_text()
    for path in EXAMPLES.iterdir():
        assert f'"{path.name}"' in tests, f"No test runs {path.name}."


# The first message, in sync and async.

FIRST = pytest.mark.parametrize("name", ["first_message.py", "first_message_async.py"])


@FIRST
def test_first_message(
    name: str, backend: Backend, capsys: pytest.CaptureFixture[str], tmp_path: Path
) -> None:
    key_file = tmp_path / "order-42.key"
    backend.give(sent(), sent(replayed=True))

    run(name)
    assert backend.lines == [f"POST /api/v1/instances/{ID}/messages"]
    assert backend.body(0) == {"to": TO, "text": "Your order 42 is ready."}
    assert backend.keys == [key_file.read_text()]
    assert "m1" in capsys.readouterr().out

    # A second run of the program uses the stored key.
    run(name)
    assert backend.keys == [key_file.read_text()] * 2
    assert "True" in capsys.readouterr().out


@FIRST
def test_first_message_error(name: str, backend: Backend, tmp_path: Path) -> None:
    backend.give(problem(401, "unauthenticated"))
    with pytest.raises(SystemExit) as stopped:
        run(name)
    assert str(stopped.value) == "unauthenticated: A test. (do_not_retry)"


@FIRST
def test_first_message_stores_the_key_before_the_call(
    name: str, backend: Backend, tmp_path: Path
) -> None:
    backend.give(None)
    with pytest.raises(SystemExit) as stopped:
        run(name)
    assert str(stopped.value).startswith("network_error: ")
    assert backend.keys == [(tmp_path / "order-42.key").read_text()]


# The error recipe.


@pytest.mark.parametrize(
    ("answers", "waits", "result"),
    [
        ([sent()], [], "sent"),
        ([None, sent()], [1], "sent"),
        ([problem(504, "send_outcome_unknown"), sent(replayed=True)], [1], "sent"),
        ([problem(409, "send_in_progress", retry_after=7), sent()], [7], "sent"),
        ([problem(429, "rate_limited"), sent()], [30], "sent"),
        ([None] * 5, [1] * 5, "later"),
        ([problem(409, "idempotency_key_outcome_unknown")], [], "check the chat"),
        ([problem(409, "duplicate_message")], [], "duplicate"),
        ([problem(400, "invalid_request")], [], "failed"),
        ([problem(500, "internal")], [], "check the chat"),
        ([problem(409, "a_new_code")], [], "check the chat"),
        # An accepted send, in an answer with the wrong shape.
        ([(200, {"message_id": 1})], [], "check the chat"),
    ],
)
def test_errors(
    answers: list[Answer],
    waits: list[float],
    result: str,
    backend: Backend,
    sleeps: list[float],
    load: Callable[[str], ModuleType],
) -> None:
    backend.give(*answers)
    example = load("errors.py")
    assert example.send(to=TO, text="Hi", key="order-42") == result
    assert sleeps == waits
    assert backend.keys == ["order-42"] * len(answers)


# Celery.


class Celery:
    """The Celery example, with its tasks in this process."""

    def __init__(self, example: ModuleType) -> None:
        self.example = example
        example.app.conf.update(task_always_eager=True, task_store_eager_result=True)
        self.waits: list[float] = []
        task = example.send_order_message
        retry = task.retry

        def record(*, countdown: float) -> Any:
            self.waits.append(countdown)
            return retry(countdown=countdown)

        # Celery ignores a countdown in an eager run.
        task.retry = record

    def send(self, *, key: str = "key-1", age: float = 0) -> Any:
        return self.example.send_order_message.delay(TO, "Hi", key, time.time() - age)


@pytest.fixture
def celery(backend: Backend, load: Callable[[str], ModuleType]) -> Celery:
    return Celery(load("celery_task.py"))


def test_celery_sends_with_the_key_of_its_argument(backend: Backend, celery: Celery) -> None:
    backend.give(sent(), sent(replayed=True))
    assert celery.send().get() == "m1"
    # The broker delivers the task again.
    assert celery.send().get() == "m1"
    assert backend.keys == ["key-1", "key-1"]


def test_celery_retries_with_the_same_key(backend: Backend, celery: Celery) -> None:
    backend.give(None, problem(409, "send_in_progress", retry_after=45), *[None] * 6, sent())
    assert celery.send().get() == "m1"
    assert backend.keys == ["key-1"] * 9
    assert celery.waits == [10, 45, 40, 80, 160, 320, 600, 600]


@pytest.mark.parametrize("code", ["duplicate_message", "idempotency_key_outcome_unknown"])
def test_celery_does_not_retry(backend: Backend, celery: Celery, code: str) -> None:
    backend.give(problem(409, code))
    result = celery.send()
    stored = celery.example.app.AsyncResult(result.id)
    assert stored.state == "FAILURE"
    assert type(stored.result) is celery.example.SendNotConfirmed
    assert stored.result.args[0] == code
    assert "can be in the chat" in stored.result.args[2]
    assert celery.waits == []


def test_celery_stops_before_24_hours(backend: Backend, celery: Celery) -> None:
    result = celery.send(age=23 * 3600 + 1)
    assert type(result.result) is celery.example.SendNotConfirmed
    assert backend.requests == []

    # The next try would be too late.
    backend.give(None, problem(429, "rate_limited", retry_after=3600))
    for age in (23 * 3600 - 5, 22 * 3600 + 60):
        assert type(celery.send(age=age).result) is celery.example.SendNotConfirmed
    assert celery.waits == []


def test_celery_json_results_lose_a_calyx_error(backend: Backend, celery: Celery) -> None:
    """The README says it. `SendNotConfirmed` of the example is the solution."""
    app = celery.example.app
    error = CalyxError(source=Source.CLIENT, code="timeout", status=0, detail="A test.")
    app.backend.store_result("id-1", error, "FAILURE")
    read = app.AsyncResult("id-1").result
    assert type(read) is Exception
    text = "timeout (status 0): A test."
    assert str(read) == f"<class 'calyx_client_sdk.errors.CalyxError'>(['{text}'])"


# Webhooks.


def event(name: str, data: object, *, id: str = "evt-1") -> bytes:
    return json.dumps(
        {
            "id": id,
            "event": name,
            "instance": "i_1",
            "timestamp": "2026-11-22T10:00:00Z",
            "data": data,
        }
    ).encode()


MESSAGE: dict[str, object] = {
    "messageId": "3EB0",
    "chatId": f"{TO}@s.whatsapp.net",
    "from": f"{TO}@s.whatsapp.net",
    "isGroup": False,
    "timestamp": 1790000000,
    "type": "text",
    "text": "private text",
}
STATUS = {"messageId": "3EB0", "chatId": TO, "status": "delivered"}
BULK = {
    "jobId": ID,
    "status": "COMPLETED",
    "totalMessages": 2,
    "sentMessages": 2,
    "failedMessages": 0,
}
EVENTS = [
    (event("message.received", MESSAGE), "3EB0"),
    (event("message.status", STATUS), "delivered"),
    (event("connection.update", {"status": "CONNECTED"}), "CONNECTED"),
    (event("bulk.finished", BULK), "COMPLETED"),
    (event("whatsmeow.Receipt", {"a": 1}), "whatsmeow.Receipt"),
    (event("message.received", {"messageId": 1}), "MessageReceived"),
]


def sign(body: bytes, *, secret: str = SECRET, age: float = 0) -> dict[str, str]:
    timestamp = str(int(time.time() - age))
    mac = hmac.new(secret.encode(), f"{timestamp}.".encode() + body, hashlib.sha256)
    return {
        "Content-Type": "application/json",
        "X-Calyx-Timestamp": timestamp,
        "X-Calyx-Signature": f"sha256={mac.hexdigest()}",
    }


type Post = Callable[[str, bytes, dict[str, str]], tuple[int, bytes]]


def flask_post(example: ModuleType) -> Post:
    client = example.app.test_client()

    def post(path: str, body: bytes, headers: dict[str, str]) -> tuple[int, bytes]:
        answer = client.post(path, data=body, headers=headers)
        return answer.status_code, answer.data

    return post


def fastapi_post(example: ModuleType) -> Post:
    """Calls the ASGI app: the test client of Starlette needs one more package."""

    def post(path: str, body: bytes, headers: dict[str, str]) -> tuple[int, bytes]:
        sent: list[dict[str, Any]] = []

        async def receive() -> dict[str, Any]:
            return {"type": "http.request", "body": body, "more_body": False}

        async def send(message: dict[str, Any]) -> None:
            sent.append(message)

        scope = {
            "type": "http",
            "asgi": {"version": "3.0"},
            "http_version": "1.1",
            "method": "POST",
            "scheme": "http",
            "path": path,
            "raw_path": path.encode(),
            "root_path": "",
            "query_string": b"",
            "headers": [(k.lower().encode(), v.encode()) for k, v in headers.items()],
            "client": ("127.0.0.1", 1),
            "server": ("127.0.0.1", 80),
        }
        asyncio.run(example.app(scope, receive, send))
        return sent[0]["status"], b"".join(message.get("body", b"") for message in sent[1:])

    return post


@pytest.fixture(scope="module")
def django_settings(tmp_path_factory: pytest.TempPathFactory) -> None:
    """Django reads its settings one time in a process."""
    import django
    from django.conf import settings

    settings.configure(
        ALLOWED_HOSTS=["*"],
        MIDDLEWARE=["django.middleware.csrf.CsrfViewMiddleware"],
        DATABASES={
            "default": {
                "ENGINE": "django.db.backends.sqlite3",
                "NAME": tmp_path_factory.mktemp("django") / "test.db",
            }
        },
        LOGGING_CONFIG=None,
    )
    django.setup()


def django_post(example: ModuleType) -> Post:
    from django.test import Client, override_settings

    client = Client(enforce_csrf_checks=True, raise_request_exception=False)

    def post(path: str, body: bytes, headers: dict[str, str]) -> tuple[int, bytes]:
        with override_settings(ROOT_URLCONF=example):
            answer = client.post(path, body, "application/json", headers=headers)
        return answer.status_code, answer.content

    return post


@pytest.fixture(params=["django_webhook.py", "flask_webhook.py", "fastapi_app.py"])
def webhook(
    request: pytest.FixtureRequest, backend: Backend, load: Callable[[str], ModuleType]
) -> Iterator[Post]:
    name: str = request.param
    if name != "django_webhook.py":
        example = load(name)
        yield flask_post(example) if name.startswith("flask") else fastapi_post(example)
        return

    request.getfixturevalue("django_settings")
    from django.apps import apps
    from django.db import connection

    # Django keeps each model class that it saw, and this module is new in each test.
    apps.all_models.pop("shop", None)
    example = load(name)
    with connection.schema_editor() as editor:
        editor.create_model(example.HandledEvent)
    yield django_post(example)
    with connection.schema_editor() as editor:
        editor.delete_model(example.HandledEvent)
    connection.close()


WEBHOOK = "/calyx/webhook"


@pytest.mark.parametrize(("body", "text"), EVENTS)
def test_webhook_reads_each_event(
    webhook: Post, caplog: pytest.LogCaptureFixture, body: bytes, text: str
) -> None:
    caplog.set_level("INFO")
    assert webhook(WEBHOOK, body, sign(body)) == (204, b"")
    assert caplog.text.count(text) == 1
    assert "private text" not in caplog.text
    # A known event with data that the SDK cannot read is visible.
    errors = [r for r in caplog.records if r.levelname == "ERROR"]
    assert len(errors) == (1 if text == "MessageReceived" else 0)


def test_webhook_ignores_a_handled_event(webhook: Post, caplog: pytest.LogCaptureFixture) -> None:
    caplog.set_level("INFO")
    body, text = EVENTS[0]
    other = event("message.received", MESSAGE, id="evt-2")
    for each in (body, body, other):
        assert webhook(WEBHOOK, each, sign(each)) == (204, b"")
    assert caplog.text.count(text) == 2


@pytest.mark.parametrize("wrong", ["signature", "time", "header", "event"])
def test_webhook_refuses(webhook: Post, caplog: pytest.LogCaptureFixture, wrong: str) -> None:
    body = b"[]" if wrong == "event" else EVENTS[0][0]
    headers = sign(
        body,
        secret="other" if wrong == "signature" else SECRET,
        age=400 if wrong == "time" else 0,
    )
    if wrong == "header":
        del headers["X-Calyx-Signature"]
    status, _ = webhook(WEBHOOK, body, headers)
    assert status == 400
    code = {"time": "stale_timestamp", "event": "invalid_event"}.get(wrong, "invalid_signature")
    assert code in caplog.text


def test_webhook_that_fails_is_handled_again(
    webhook: Post, caplog: pytest.LogCaptureFixture, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The id is not stored when the handling fails, so the next delivery is not lost."""
    caplog.set_level("INFO")
    body, text = EVENTS[0]
    module = next(m for n, m in sys.modules.items() if n.startswith("example_"))

    def fail(*_: object) -> None:
        raise RuntimeError("A test.")

    with monkeypatch.context() as patch:
        patch.setattr(module.log, "info", fail)
        if hasattr(module, "app"):
            with pytest.raises(RuntimeError):
                module.app.testing = True
                webhook(WEBHOOK, body, sign(body))
        else:
            assert webhook(WEBHOOK, body, sign(body))[0] == 500
    caplog.clear()
    assert webhook(WEBHOOK, body, sign(body)) == (204, b"")
    assert caplog.text.count(text) == 1


# One message from a web view.


@pytest.fixture(params=["flask_webhook.py", "fastapi_app.py"])
def order_ready(
    request: pytest.FixtureRequest, backend: Backend, load: Callable[[str], ModuleType]
) -> Callable[[], tuple[int, Any]]:
    name: str = request.param
    example = load(name)
    post = flask_post(example) if name.startswith("flask") else fastapi_post(example)

    def send() -> tuple[int, Any]:
        status, body = post("/orders/42/ready", b"", {})
        return status, json.loads(body)

    return send


def test_view_sends_one_message(
    order_ready: Callable[[], tuple[int, Any]], backend: Backend
) -> None:
    backend.give(None, problem(409, "send_in_progress"), sent())
    assert order_ready() == (202, {"state": "pending"})
    assert order_ready() == (202, {"state": "pending"})
    assert order_ready() == (200, {"state": "sent"})
    # A sent order makes no call.
    assert order_ready() == (200, {"state": "sent"})
    assert len(backend.keys) == 3
    assert len(set(backend.keys)) == 1
    assert backend.body(0) == {"to": TO, "text": "Your order 42 is ready."}


@pytest.mark.parametrize(
    ("answer", "expected"),
    [
        (problem(409, "duplicate_message"), (409, {"state": "check the chat"})),
        (problem(409, "idempotency_key_outcome_unknown"), (409, {"state": "check the chat"})),
        (problem(500, "internal"), (409, {"state": "check the chat"})),
        ((200, {"message_id": 1}), (409, {"state": "check the chat"})),
        (problem(400, "invalid_request"), (502, {"state": "failed"})),
    ],
)
def test_view_errors(
    order_ready: Callable[[], tuple[int, Any]], backend: Backend, answer: Answer, expected: object
) -> None:
    backend.give(answer)
    assert order_ready() == expected


def test_view_does_not_use_an_old_key(
    order_ready: Callable[[], tuple[int, Any]], backend: Backend
) -> None:
    def change_age(seconds: float) -> None:
        with closing(sqlite3.connect("shop.db")) as db, db:
            db.execute("UPDATE order_sends SET made_at = ?", (time.time() - seconds,))

    backend.give(None, None)
    assert order_ready()[0] == 202
    change_age(23 * 3600 - 60)
    assert order_ready()[0] == 202
    change_age(23 * 3600 + 1)
    assert order_ready() == (409, {"state": "check the chat"})
    assert len(backend.requests) == 2


# Pairing.


STREAM = b"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n"


def items(*items: tuple[str, object]) -> bytes:
    return "".join(f"event: {name}\ndata: {json.dumps(data)}\n\n" for name, data in items).encode()


def sse(*given: tuple[str, object]) -> bytes:
    """A pairing stream that ends after its items."""
    body = items(*given)
    return STREAM + f"Content-Length: {len(body)}\r\n\r\n".encode() + body


PAIRING = {"status": "NEEDS_AUTH", "pairing_code": None, "qr_text": QR, "message": "Scan it."}
UPDATE = ("update", PAIRING)
DONE = ("done", {"status": "CONNECTED"})
FAILED = ("failed", {"code": "pairing_expired", "detail": "Too late."})
STARTED: Answer = (200, {"pairing": PAIRING})


def instance(status: str) -> Answer:
    return (
        200,
        {
            "id": ID,
            "client_id": ID,
            "display_name": "Shop",
            "provider": "WHATSAPP_WEB",
            "send_only": False,
            "enabled": True,
            "state": "active",
            "status": status,
            "status_changed_at": None,
            "status_stale": False,
            "device_identity": None,
            "locale": None,
            "phone_number_id": None,
            "webhooks": [],
            "alert": None,
        },
    )


NOT_FOUND = problem(404, "not_found")


def join(instance_id: str) -> None:
    for thread in threading.enumerate():
        if thread.name == f"pairing {instance_id}":
            thread.join(5)
            assert not thread.is_alive()


@pytest.mark.parametrize(
    ("answers", "waits", "state"),
    [
        pytest.param([sse(UPDATE, DONE)], [], "connected", id="done"),
        pytest.param([sse(UPDATE, FAILED)], [], "ended", id="failed"),
        pytest.param([NOT_FOUND, instance("CONNECTED")], [], "connected", id="404-connected"),
        pytest.param([NOT_FOUND, instance("NEEDS_AUTH")], [], "ended", id="404-needs-auth"),
        pytest.param([NOT_FOUND, NOT_FOUND], [], "ended", id="404-deleted"),
        pytest.param([NOT_FOUND, None, NOT_FOUND, NOT_FOUND], [2], "ended", id="404-break"),
        pytest.param([sse(UPDATE), None, sse(DONE)], [2, 2], "connected", id="break-done"),
        pytest.param(
            [sse(UPDATE), NOT_FOUND, instance("CONNECTED")], [2], "connected", id="break-404"
        ),
        pytest.param(
            [problem(429, "rate_limited", retry_after=9), sse(DONE)], [9], "connected", id="later"
        ),
        pytest.param([problem(401, "unauthenticated")], [], "error", id="error"),
        pytest.param([sse(("update", {}))], [], "error", id="wrong-shape"),
        pytest.param([None] * 100, [2] * 100, "error", id="backend-down"),
    ],
)
def test_pairing_panel(
    answers: list[Answer],
    waits: list[float],
    state: str,
    backend: Backend,
    sleeps: list[float],
    load: Callable[[str], ModuleType],
) -> None:
    panel = load("pairing_panel.py")
    backend.give(STARTED, *answers)
    panel.start_pairing(ID)
    join(ID)
    assert panel.panel_state(ID) == {"state": state, "qr_text": None}
    assert sleeps == waits
    assert backend.lines[0] == f"POST /api/v1/instances/{ID}/pair"
    assert backend.body(0) == {"pairing": "qr"}


def test_pairing_panel_shows_the_last_item(
    backend: Backend, load: Callable[[str], ModuleType]
) -> None:
    panel = load("pairing_panel.py")
    assert panel.panel_state(ID) is None
    sent, go_on = threading.Event(), threading.Event()

    def stream(connection: Connection) -> None:
        head = STREAM + b"Connection: close\r\n\r\n"
        connection.sock.sendall(head + items(("update", {**PAIRING, "qr_text": "second-qr"})))
        sent.set()
        go_on.wait(5)
        connection.sock.sendall(items(DONE))

    backend.give(STARTED, stream)
    assert panel.start_pairing(ID) == {"state": "waiting", "qr_text": QR}
    assert sent.wait(5)
    end = time.monotonic() + 2
    while panel.panel_state(ID)["qr_text"] != "second-qr" and time.monotonic() < end:
        time.sleep(0.01)
    assert panel.panel_state(ID) == {"state": "waiting", "qr_text": "second-qr"}
    go_on.set()
    join(ID)
    assert panel.panel_state(ID) == {"state": "connected", "qr_text": None}


def test_pairing_panel_follows_a_running_pairing(
    backend: Backend, load: Callable[[str], ModuleType]
) -> None:
    """For example after a restart, or after two clicks."""
    panel = load("pairing_panel.py")
    backend.give(problem(409, "pairing_in_progress"), sse(UPDATE, DONE))
    assert panel.start_pairing(ID)["state"] == "waiting"
    join(ID)
    assert panel.panel_state(ID) == {"state": "connected", "qr_text": None}


def test_pairing_panel_of_a_connected_instance(
    backend: Backend, load: Callable[[str], ModuleType]
) -> None:
    panel = load("pairing_panel.py")
    backend.give(problem(409, "instance_connected"))
    assert panel.start_pairing(ID) == {"state": "connected", "qr_text": None}
    assert panel.panel_state(ID) == {"state": "connected", "qr_text": None}


def test_pairing_panel_start_error(backend: Backend, load: Callable[[str], ModuleType]) -> None:
    panel = load("pairing_panel.py")
    backend.give(problem(409, "instance_in_error"))
    with pytest.raises(CalyxError) as caught:
        panel.start_pairing(ID)
    assert caught.value.code == "instance_in_error"
    assert panel.panel_state(ID) is None


def test_pairing_panel_shows_an_error_of_its_own_code(
    backend: Backend, load: Callable[[str], ModuleType], monkeypatch: pytest.MonkeyPatch
) -> None:
    panel = load("pairing_panel.py")
    panel.save(ID, "waiting", QR)

    def fail(_: str) -> None:
        raise RuntimeError("A test.")

    monkeypatch.setattr(panel, "wait_for_end", fail)
    with pytest.raises(RuntimeError):
        panel.follow(ID)
    assert panel.panel_state(ID) == {"state": "error", "qr_text": None}


# Bulk jobs and create calls.


def bulk_job(status: str, *, sent: int = 0) -> Answer:
    return (
        200,
        {
            "id": ID,
            "instance_id": ID,
            "status": status,
            "total_messages": 2,
            "sent_messages": sent,
            "failed_messages": 0,
            "progress_percent": sent * 50.0,
            "estimated_finish_at": None,
            "created_at": "2026-11-22T10:00:00Z",
            "replayed": False,
        },
    )


def test_bulk_job(
    backend: Backend,
    sleeps: list[float],
    capsys: pytest.CaptureFixture[str],
    tmp_path: Path,
) -> None:
    backend.give(bulk_job("QUEUED"), bulk_job("RUNNING", sent=1), bulk_job("COMPLETED", sent=2))
    run("bulk_job.py")
    assert backend.keys == [(tmp_path / "reminder-2026-11-23.key").read_text()]
    assert backend.body(0) == {
        "recipients": ["34600000001", "34600000002"],
        "text": "Your appointment is tomorrow at 10:00.",
    }
    assert backend.lines[1:] == [f"GET /api/v1/bulk-jobs/{ID}"] * 2
    assert "COMPLETED" in capsys.readouterr().out


def test_bulk_job_does_not_use_an_old_key(backend: Backend, tmp_path: Path) -> None:
    key_file = tmp_path / "reminder-2026-11-23.key"
    key_file.write_text("old-key")
    old = time.time() - 23 * 3600 - 1
    os.utime(key_file, (old, old))
    with pytest.raises(SystemExit) as stopped:
        run("bulk_job.py")
    assert "too old" in str(stopped.value)
    assert backend.requests == []


def test_bulk_job_uses_a_stored_key(backend: Backend, tmp_path: Path) -> None:
    key_file = tmp_path / "reminder-2026-11-23.key"
    key_file.write_text("stored-key")
    recent = time.time() - 23 * 3600 + 60
    os.utime(key_file, (recent, recent))
    backend.give(bulk_job("COMPLETED", sent=2))
    run("bulk_job.py")
    assert backend.keys == ["stored-key"]


def org_client(name: str) -> dict[str, object]:
    return {
        "id": ID,
        "name": name,
        "is_default": False,
        "can_create_instances": False,
        "max_instances": 0,
        "instance_count": 0,
        "user_email": None,
        "pending_invitation": None,
    }


def clients(*names: str) -> Answer:
    return 200, {"items": [org_client(name) for name in names]}


CREATED: Answer = (201, org_client("Shop Ana"))


@pytest.mark.parametrize(
    ("answers", "waits", "lines"),
    [
        pytest.param([clients("Other"), CREATED], [], ["GET", "POST"], id="new"),
        pytest.param([clients("Shop Ana")], [], ["GET"], id="exists"),
        pytest.param(
            [clients(), None, clients("Shop Ana")], [15], ["GET", "POST", "GET"], id="made"
        ),
        pytest.param(
            [clients(), None, clients(), CREATED],
            [15],
            ["GET", "POST", "GET", "POST"],
            id="not-made",
        ),
    ],
)
def test_create_client(
    answers: Iterable[Answer],
    waits: list[float],
    lines: list[str],
    backend: Backend,
    sleeps: list[float],
    capsys: pytest.CaptureFixture[str],
) -> None:
    backend.give(*answers)
    run("create_client.py")
    assert backend.lines == [f"{method} /api/v1/clients" for method in lines]
    assert sleeps == waits
    assert ID in capsys.readouterr().out


def test_create_client_keeps_the_other_errors(backend: Backend, sleeps: list[float]) -> None:
    backend.give(problem(403, "owner_only"))
    with pytest.raises(CalyxError) as caught:
        run("create_client.py")
    assert caught.value.code == "owner_only"
    assert sleeps == []


# The check of `run.sh readme-check`.

BLOCK = "Text.\n\n<!-- examples/a.py -->\n```python\nprint(1)\n```\n"


@pytest.mark.parametrize(
    ("readme", "text"),
    [
        (BLOCK, None),
        (BLOCK.replace("(1)", "(2)"), "examples/a.py is not the same"),
        (BLOCK.replace("<!-- examples/a.py -->\n", ""), "line 3: a Python block has no marker"),
        (BLOCK.replace("-->\n", "-->\n\n"), "line 5: a Python block has no marker"),
        (BLOCK.replace("python", "py"), "line 4: a Python block has no marker"),
        (BLOCK.replace("<!--", "1. <!--"), "line 4: a Python block has no marker"),
        (BLOCK + "  ```python\n  print(2)\n  ```\n", "a Python block has no marker"),
        (BLOCK.replace("a.py", "b.py"), "examples/b.py does not exist"),
        ("Text.\n", "examples/a.py has no block"),
        (BLOCK + BLOCK, "examples/a.py has 2 blocks"),
        (BLOCK + "```bash\nls\n```\n", None),
        (BLOCK + "   ```text\n   a\n   ```\n", None),
        (BLOCK + "~~~python\nprint(2)\n~~~\n", "line 7: a Python block has no marker"),
        (BLOCK + "~~~bash\nls\n~~~\n", "line 7: use"),
        (BLOCK + "```\nprint(2)\n```\n", "line 7: use"),
        (BLOCK + "> ```python\n> print(2)\n> ```\n", "line 7: a Python block has no marker"),
        (BLOCK + "- ```python\n  print(2)\n  ```\n", "line 7: a Python block has no marker"),
        (BLOCK + "```bash\nls\n", "line 7: the block has no end"),
        (BLOCK.removesuffix("```\n"), "line 4: the block has no end"),
        ("```python\nprint(1)\n```\n", "line 1: a Python block has no marker"),
    ],
)
def test_readme_check(tmp_path: Path, readme: str, text: str | None) -> None:
    (tmp_path / "examples").mkdir()
    (tmp_path / "examples" / "a.py").write_text("print(1)\n")
    (tmp_path / "README.md").write_text(readme)
    problems = readme_check.check(tmp_path)
    if text is None:
        assert problems == []
    else:
        assert any(text in problem for problem in problems), problems
