"""The pairing stream of the two clients, against a fake backend."""

import asyncio
import gc
import json
import signal
import socket
import sys
import threading
import time
from collections.abc import Awaitable, Iterator
from dataclasses import FrozenInstanceError, dataclass
from pathlib import Path
from typing import Any, Self, assert_never

import certificates
import pytest
from fake_server import Connection, FakeServer, SilentAddresses

import calyx_client_sdk
from calyx_client_sdk import (
    AsyncBackendClient,
    AsyncPairingStream,
    BackendClient,
    CalyxError,
    ClientCode,
    PairingDone,
    PairingFailed,
    PairingStream,
    PairingUpdate,
    RetryAdvice,
    Source,
    _http,
)
from calyx_client_sdk import __version__ as version
from calyx_client_sdk.models import Pairing, PairingFailureCode
from calyx_client_sdk.pairing import FOLLOW_PAIRING

BACKEND_JSON = Path(__file__).parents[4] / "shared-types" / "openapi" / "backend.json"
DOCUMENT: dict[str, Any] = json.loads(BACKEND_JSON.read_text())
ID = "0b5e1a52-0c7e-4a4b-9a53-0f1c8d1a7c11"
API_KEY = "cx_secret"
QR = "private-qr"

type Item = PairingUpdate | PairingDone | PairingFailed

HEAD = b"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n"
UPDATE_DATA = {"status": "NEEDS_AUTH", "pairing_code": None, "qr_text": QR, "message": "Scan"}
DONE_DATA = {"status": "CONNECTED"}
FAILED_DATA = {"code": "pairing_expired", "detail": "Too late."}

UPDATED = PairingUpdate(
    pairing=Pairing(status="NEEDS_AUTH", pairing_code=None, qr_text=QR, message="Scan")
)
DONE = PairingDone(status="CONNECTED")


def event(name: str, data: object) -> bytes:
    return f"event: {name}\ndata: {json.dumps(data)}\n\n".encode()


UPDATE_EVENT = event("update", UPDATE_DATA)
DONE_EVENT = event("done", DONE_DATA)
PING = b": ping\n\n"


# The same tests run on both clients: `Blocking` reads an async stream like a sync one.


class Blocking:
    """Runs each step of an async stream in a task of its own."""

    def __init__(self, stream: AsyncPairingStream, runner: asyncio.Runner) -> None:
        self._stream = stream
        self._runner = runner

    def _run[T](self, step: Awaitable[T]) -> T:
        async def main() -> T:
            return await step

        return self._runner.run(main())

    def __enter__(self) -> Self:
        assert self._run(self._stream.__aenter__()) is self._stream
        return self

    def __exit__(self, *details: Any) -> None:
        # A value that is true would stop the exception.
        assert not self._run(self._stream.__aexit__(*details))

    def __iter__(self) -> Self:
        assert self._stream.__aiter__() is self._stream
        return self

    def __next__(self) -> Item:
        try:
            return self._run(self._stream.__anext__())
        except StopAsyncIteration:
            pass
        raise StopIteration


type Stream = PairingStream | Blocking


@dataclass(frozen=True, slots=True)
class Follow:
    runner: asyncio.Runner | None
    """None for the sync client."""

    def __call__(
        self,
        url: str,
        *,
        id: str = ID,
        time_limit: float | None = None,
        client_time_limit: float | None = None,
        **options: Any,
    ) -> Stream:
        options = {"api_key": API_KEY, "time_limit": client_time_limit, **options}
        if self.runner is None:
            return BackendClient(url, **options).follow_pairing(id, time_limit=time_limit)
        client = AsyncBackendClient(url, **options)
        return Blocking(client.follow_pairing(id, time_limit=time_limit), self.runner)


@pytest.fixture(params=["sync", "async"])
def follow(request: pytest.FixtureRequest) -> Iterator[Follow]:
    if request.param == "sync":
        yield Follow(None)
    else:
        with asyncio.Runner() as runner:
            yield Follow(runner)


@pytest.fixture(autouse=True)
def no_open_socket() -> Iterator[None]:
    yield
    # A socket that nobody closed gives its warning here, in the test that left it open.
    gc.collect()


def url_of(server: FakeServer, scheme: str = "http") -> str:
    return f"{scheme}://127.0.0.1:{server.port}"


def sending(*steps: bytes | float, head: bytes = HEAD, end: bool = False) -> Any:
    """Makes a server that sends bytes, and waits for some seconds between them."""

    def handle(connection: Connection) -> None:
        connection.read_request()
        for step in (head, *steps):
            if isinstance(step, bytes):
                connection.sock.sendall(step)
            else:
                time.sleep(step)
        if end:
            connection.sock.shutdown(socket.SHUT_WR)

    return handle


def read_all(stream: Stream, server: FakeServer) -> tuple[list[Item], CalyxError | None]:
    """Reads a stream to its end. Then the connection is closed, also inside the block."""
    items: list[Item] = []
    failure = None
    with stream:
        try:
            for item in stream:
                items.append(item)
        except CalyxError as error:
            failure = error
        assert server.client_closed_all()
        for _ in range(2):
            assert next(stream, None) is None
    # The SDK never opens a stream again.
    assert (server.connections, len(server.requests)) == (1, 1)
    return items, failure


def check_error(
    error: CalyxError | None,
    code: str,
    detail: str,
    retry: RetryAdvice = RetryAdvice.RETRY_NOW,
) -> None:
    assert error is not None
    assert (error.source, error.code, error.status) == (Source.CLIENT, code, 0)
    assert error.detail == detail
    assert error.retry is retry
    current: BaseException | None = error
    while current is not None:
        for secret in (API_KEY, QR):
            assert secret not in f"{current!r} {current} {current.__dict__}"
        current = current.__cause__ or current.__context__


# The request and the items.


def test_the_operation_is_the_one_of_the_document() -> None:
    (path, method) = next(
        (path, method)
        for path, item in DOCUMENT["paths"].items()
        for method, operation in item.items()
        if operation["operationId"] == FOLLOW_PAIRING.id
    )
    assert (FOLLOW_PAIRING.method, FOLLOW_PAIRING.path) == (method.upper(), path)
    assert FOLLOW_PAIRING.time_limit is None


def test_the_request(follow: Follow) -> None:
    with FakeServer(sending(DONE_EVENT)) as server:
        assert read_all(follow(url_of(server)), server) == ([DONE], None)
        (request,) = server.requests
    assert request.line == f"GET /api/v1/instances/{ID}/pairing HTTP/1.1"
    assert sorted(request.headers) == [
        ("Accept", "text/event-stream"),
        ("Accept-Encoding", "identity"),
        ("Authorization", f"Bearer {API_KEY}"),
        ("Host", f"127.0.0.1:{server.port}"),
        ("User-Agent", f"calyx-python-sdk/{version}"),
    ]


def test_the_items(follow: Follow) -> None:
    code = {**UPDATE_DATA, "pairing_code": "ABCD-2345", "qr_text": None, "new": 1}
    stream = [UPDATE_EVENT, PING, event("other", 1), event("update", code), DONE_EVENT]
    with FakeServer(sending(*stream, UPDATE_EVENT)) as server:
        items, error = read_all(follow(url_of(server)), server)
    assert error is None
    second = Pairing(status="NEEDS_AUTH", pairing_code="ABCD-2345", qr_text=None, message="Scan")
    assert items == [UPDATED, PairingUpdate(pairing=second), DONE]


def test_a_pairing_that_failed(follow: Follow) -> None:
    with FakeServer(sending(UPDATE_EVENT, event("failed", FAILED_DATA))) as server:
        items, error = read_all(follow(url_of(server)), server)
    assert error is None
    failed = PairingFailed(code=PairingFailureCode.PAIRING_EXPIRED, detail="Too late.")
    assert items == [UPDATED, failed]


def test_single_bytes(follow: Follow) -> None:
    def handle(connection: Connection) -> None:
        connection.read_request()
        connection.sock.sendall(HEAD)
        connection.send_slowly(UPDATE_EVENT[-40:], pause=0.002)
        connection.send_slowly(DONE_EVENT.replace(b"\n", b"\r\n"), pause=0.002)

    with FakeServer(handle) as server:
        items, error = read_all(follow(url_of(server)), server)
    assert (items, error) == ([DONE], None)


def test_a_chunked_answer_over_tls(follow: Follow, tmp_path: Path) -> None:
    authority = certificates.make_authority(tmp_path / "ca")
    head = HEAD.replace(b"\r\n\r\n", b"; Charset=utf-8\r\nTransfer-Encoding: chunked\r\n\r\n")
    chunks = [f"{len(e):x}\r\n".encode() + e + b"\r\n" for e in (UPDATE_EVENT, DONE_EVENT)]
    server_side = sending(*chunks, head=head.replace(b"text/event", b"Text/Event"))
    with FakeServer(server_side, tls=authority.server_context("IP:127.0.0.1")) as server:
        stream = follow(url_of(server, "https"), ca_file=authority.ca_file)
        assert read_all(stream, server) == ([UPDATED, DONE], None)


def test_the_connection_opens_at_the_first_read(follow: Follow) -> None:
    with FakeServer(sending(UPDATE_EVENT)) as server:
        with follow(url_of(server)) as stream:
            time.sleep(0.05)
            assert server.connections == 0
            assert next(stream) == UPDATED
            assert server.connections == 1
        assert server.client_closed_all()
        assert next(stream, None) is None


def test_a_block_with_no_read(follow: Follow) -> None:
    with FakeServer(sending(UPDATE_EVENT)) as server:
        with follow(url_of(server)) as stream:
            pass
        assert next(stream, None) is None
        assert server.connections == 0


def test_two_streams_and_a_call_of_one_client() -> None:
    def handle(connection: Connection) -> None:
        if connection.read_request().line.startswith("DELETE"):
            connection.answer(204)
        else:
            connection.sock.sendall(HEAD + UPDATE_EVENT)

    with FakeServer(handle) as server:
        client = BackendClient(url_of(server), API_KEY)
        with client.follow_pairing(ID) as first, client.follow_pairing(ID) as second:
            assert [next(first), next(second)] == [UPDATED, UPDATED]
            client.cancel_pairing(ID)
            assert server.connections == 3


# The answers that are not a stream.


def test_no_pairing_runs(follow: Follow) -> None:
    problem = {"code": "not_found", "title": "Not Found", "status": 404, "detail": "No pairing."}
    body = json.dumps(problem).encode()

    def handle(connection: Connection) -> None:
        connection.read_request()
        kind = "Content-Type: application/problem+json\r\nX-Request-Id: r1\r\nRetry-After: 7\r\n"
        connection.answer(404, body, kind)

    with FakeServer(handle) as server:
        items, error = read_all(follow(url_of(server)), server)
    assert items == []
    assert error is not None
    assert (error.source, error.code, error.status) == (Source.BACKEND, "not_found", 404)
    assert (error.detail, error.request_id, error.retry_after) == ("No pairing.", "r1", 7)
    assert error.retry is RetryAdvice.DO_NOT_RETRY


@pytest.mark.parametrize(
    ("status", "headers", "detail"),
    [
        (200, "Content-Type: application/json\r\n", "200 with a body that is not an event stream."),
        (200, "Content-Type: text/event-streams\r\n", "200 with a body that is not an event strea"),
        (200, "", "200 with a body that is not an event stream."),
        (204, "", "204 with a body that is not an event stream."),
        (307, "Location: /other\r\n", "307. The SDK does not follow redirects."),
        (500, "Content-Type: text/event-stream\r\n", "500 with a body that is not problem+json."),
    ],
)
def test_an_answer_that_is_not_a_stream(
    follow: Follow, status: int, headers: str, detail: str
) -> None:
    def handle(connection: Connection) -> None:
        connection.read_request()
        # With no end of the body: the SDK does not read it when the status is 2xx.
        body = DONE_EVENT if status != 204 else b""
        connection.answer(status, body, headers)

    with FakeServer(handle) as server:
        items, error = read_all(follow(url_of(server)), server)
    assert items == []
    assert error is not None
    check_error(error, ClientCode.INVALID_RESPONSE, error.detail)
    assert error.detail.startswith(f"The backend answered {detail}")


def test_the_body_of_a_2xx_answer_is_not_read(follow: Follow) -> None:
    head = b"HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nContent-Length: 100\r\n\r\n"
    with FakeServer(sending(b"<html>", head=head)) as server:
        start = time.monotonic()
        _, error = read_all(follow(url_of(server)), server)
        assert time.monotonic() - start < 1
    assert error is not None
    assert error.code == ClientCode.INVALID_RESPONSE


@pytest.mark.parametrize("over", [0, 1])
def test_the_size_limit_of_an_error(
    follow: Follow, monkeypatch: pytest.MonkeyPatch, over: int
) -> None:
    body = b'{"code":"not_found"}'
    monkeypatch.setattr(_http, "_MAX_ANSWER", len(body) - over)

    def handle(connection: Connection) -> None:
        connection.read_request()
        connection.answer(404, body)

    with FakeServer(handle) as server:
        _, error = read_all(follow(url_of(server)), server)
    if over:
        detail = "The backend answered with a body that is larger than the SDK reads."
        check_error(error, ClientCode.INVALID_RESPONSE, detail)
    else:
        assert error is not None
        assert error.code == "not_found"


# The events that are not valid.


def check_bad_event(
    follow: Follow, data: bytes, detail: str, retry: RetryAdvice = RetryAdvice.RETRY_NOW
) -> None:
    # A good event before it, in the same chunk, gives its item.
    with FakeServer(sending(HEAD + UPDATE_EVENT + data + DONE_EVENT, head=b"")) as server:
        items, error = read_all(follow(url_of(server)), server)
    assert items == [UPDATED]
    check_error(error, ClientCode.INVALID_RESPONSE, detail, retry)


def test_an_event_that_is_not_json(follow: Follow) -> None:
    detail = 'The backend sent a "update" event with data that is not JSON.'
    check_bad_event(follow, f"event: update\ndata: {QR}\n\n".encode(), detail)


@pytest.mark.parametrize(
    ("name", "data", "problem"),
    [
        ("update", {**UPDATE_DATA, "status": 1}, "Pairing.status is not a string."),
        ("update", {"status": QR}, "Pairing.pairing_code is missing."),
        ("update", [QR], "Pairing is not an object."),
        ("update", None, "Pairing is not an object."),
        ("done", {}, "PairingDone.status is missing."),
        ("failed", {"code": "pairing_failed"}, "PairingFailed.detail is missing."),
    ],
)
def test_an_event_of_the_wrong_shape(follow: Follow, name: str, data: object, problem: str) -> None:
    detail = f'The backend sent a "{name}" event with data of the wrong shape. {problem}'
    # A new follow can get the same shape, so "retry now" would be an endless loop.
    check_bad_event(follow, event(name, data), detail, RetryAdvice.UNKNOWN)


@pytest.mark.parametrize("over", [0, 1])
def test_the_size_limit_of_an_event(follow: Follow, over: int) -> None:
    text = ":" + "x" * (1024 * 1024 - len(DONE_EVENT) - 2 + over) + "\n"
    with FakeServer(sending(text.encode() + DONE_EVENT)) as server:
        items, error = read_all(follow(url_of(server)), server)
    if over:
        assert items == []
        detail = "The backend sent an event that is too large."
        check_error(error, ClientCode.INVALID_RESPONSE, detail)
    else:
        assert (items, error) == ([DONE], None)


# A stream that breaks.


@pytest.mark.parametrize(
    ("head", "end", "detail"),
    [
        (HEAD, b"", "The pairing stream ended before done or failed."),
        (HEAD, DONE_EVENT[:-1], "The pairing stream ended before done or failed."),
        (
            HEAD.replace(b"\r\n\r\n", b"\r\nContent-Length: 5000\r\n\r\n"),
            b"",
            "The pairing stream broke.",
        ),
        (HEAD, None, "The pairing stream broke."),
    ],
    ids=["end", "end-in-event", "cut-off", "reset"],
)
def test_a_stream_that_ends_too_early(
    follow: Follow, head: bytes, end: bytes | None, detail: str
) -> None:
    def handle(connection: Connection) -> None:
        sending(UPDATE_EVENT, end or b"", head=head, end=end is not None)(connection)
        if end is None:
            time.sleep(0.2)
            # A close with a linger of 0 s sends a reset.
            linger = b"\1\0\0\0\0\0\0\0"
            connection.sock.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, linger)
            connection.sock.close()

    with FakeServer(handle) as server:
        items, error = read_all(follow(url_of(server)), server)
    assert items == [UPDATED]
    check_error(error, ClientCode.STREAM_FAILED, detail)


NO_CONNECTION = "The connection to the backend failed."


@pytest.mark.parametrize("head", [b"", HEAD[:30], b"HTTP/1.1 404 No\r\nContent-Length: 9\r\n\r\n{"])
def test_an_answer_that_is_cut_off(follow: Follow, head: bytes) -> None:
    with FakeServer(sending(head=head, end=True)) as server:
        items, error = read_all(follow(url_of(server)), server)
    assert items == []
    check_error(error, ClientCode.NETWORK_ERROR, NO_CONNECTION)


def test_connection_refused(follow: Follow) -> None:
    with socket.socket() as closed:
        closed.bind(("127.0.0.1", 0))
        with (
            pytest.raises(CalyxError) as caught,
            follow(f"http://127.0.0.1:{closed.getsockname()[1]}") as stream,
        ):
            next(stream)
    check_error(caught.value, ClientCode.NETWORK_ERROR, NO_CONNECTION)
    assert caught.value.__cause__ is not None


# The limits with no data.


def test_the_limits_of_the_contract() -> None:
    assert (_http._CONNECT_LIMIT, _http._READ_LIMIT) == (30, 45)


@pytest.mark.parametrize(
    ("steps", "head", "items"),
    [
        ([UPDATE_EVENT], HEAD, [UPDATED]),
        ([PING], HEAD, []),
        ([], HEAD, []),
        ([], HEAD[:30], []),
        ([], b"", []),
        ([], b"HTTP/1.1 500 No\r\nContent-Length: 9\r\n\r\n{", []),
    ],
    ids=["after-event", "after-ping", "after-head", "in-head", "no-answer", "in-error"],
)
def test_a_silent_stream(
    follow: Follow,
    monkeypatch: pytest.MonkeyPatch,
    steps: list[bytes],
    head: bytes,
    items: list[Item],
) -> None:
    monkeypatch.setattr(_http, "_READ_LIMIT", 1)
    with FakeServer(sending(*steps, head=head)) as server:
        start = time.monotonic()
        found, error = read_all(follow(url_of(server)), server)
        assert 0.95 < time.monotonic() - start < 1.8
    assert found == items
    check_error(error, ClientCode.STREAM_FAILED, "The pairing stream sent no data for 1 s.")
    assert error is not None
    assert error.__cause__ is not None


def test_a_time_limit_that_is_longer_does_not_change_the_read_limit(
    follow: Follow, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(_http, "_READ_LIMIT", 0.5)
    with FakeServer(sending()) as server:
        start = time.monotonic()
        _, error = read_all(follow(url_of(server), time_limit=5), server)
        assert 0.45 < time.monotonic() - start < 1.3
    check_error(error, ClientCode.STREAM_FAILED, "The pairing stream sent no data for 0.5 s.")


def test_pings_keep_the_stream_open(follow: Follow, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(_http, "_READ_LIMIT", 0.5)
    steps: list[bytes | float] = [0.2, PING] * 5
    with FakeServer(sending(*steps, DONE_EVENT)) as server:
        assert read_all(follow(url_of(server)), server) == ([DONE], None)


def test_the_code_of_the_program_is_not_silence(
    follow: Follow, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(_http, "_READ_LIMIT", 0.3)
    with FakeServer(sending(UPDATE_EVENT, 0.7, DONE_EVENT)) as server:
        with follow(url_of(server)) as stream:
            assert next(stream) == UPDATED
            time.sleep(0.6)
            assert next(stream) == DONE
        assert server.client_closed_all()


def test_the_connect_limit(follow: Follow, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(_http, "_CONNECT_LIMIT", 0.5)
    with SilentAddresses() as silent:
        start = time.monotonic()
        with (
            pytest.raises(CalyxError) as caught,
            follow(f"http://{silent.HOSTS[0]}:{silent.port}") as stream,
        ):
            next(stream)
        assert 0.45 < time.monotonic() - start < 1.3
    check_error(caught.value, ClientCode.NETWORK_ERROR, NO_CONNECTION)


def test_the_limit_of_the_tls_handshake(follow: Follow, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(_http, "_CONNECT_LIMIT", 0.5)
    with FakeServer(lambda connection: None) as server:
        start = time.monotonic()
        with (
            pytest.raises(CalyxError) as caught,
            follow(url_of(server, "https")) as stream,
        ):
            next(stream)
        assert 0.45 < time.monotonic() - start < 1.3
        assert server.client_closed_all()
    check_error(caught.value, ClientCode.NETWORK_ERROR, NO_CONNECTION)


def test_a_server_that_does_not_read_the_request(
    follow: Follow, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(_http, "_READ_LIMIT", 0.5)

    def handle(connection: Connection) -> None:
        connection.wait()

    with FakeServer(handle, receive_buffer=1 << 16) as server:
        start = time.monotonic()
        with (
            pytest.raises(CalyxError) as caught,
            follow(url_of(server), api_key="x" * 20_000_000) as stream,
        ):
            next(stream)
        assert 0.45 < time.monotonic() - start < 1.5
        assert server.client_closed_all()
    assert caught.value.code == ClientCode.STREAM_FAILED


# The time limit.

TOO_LATE = "The backend did not answer in time."


@pytest.mark.parametrize(
    ("steps", "head", "items"),
    [
        ([UPDATE_EVENT], HEAD, [UPDATED]),
        ([UPDATE_EVENT, *[0.1, PING] * 20], HEAD, [UPDATED]),
        ([], b"", []),
        ([], b"HTTP/1.1 500 No\r\nContent-Length: 9\r\n\r\n{", []),
    ],
    ids=["silent", "pings", "no-answer", "in-error"],
)
def test_the_time_limit_of_a_call(
    follow: Follow, steps: list[bytes | float], head: bytes, items: list[Item]
) -> None:
    with FakeServer(sending(*steps, head=head)) as server:
        stream = follow(url_of(server), time_limit=0.6)
        # The time starts at the first read.
        time.sleep(0.3)
        start = time.monotonic()
        found, error = read_all(stream, server)
        assert 0.55 < time.monotonic() - start < 1.4
    assert found == items
    check_error(error, ClientCode.TIMEOUT, TOO_LATE)


def test_the_time_limit_stops_a_connect(follow: Follow) -> None:
    with SilentAddresses() as silent:
        start = time.monotonic()
        url = f"http://{silent.HOSTS[0]}:{silent.port}"
        with pytest.raises(CalyxError) as caught, follow(url, time_limit=0.4) as stream:
            next(stream)
        assert 0.35 < time.monotonic() - start < 1.2
    check_error(caught.value, ClientCode.TIMEOUT, TOO_LATE)


@pytest.mark.parametrize("apart", [False, True], ids=["one-chunk", "two-chunks"])
def test_the_time_limit_counts_the_code_of_the_program(follow: Follow, apart: bool) -> None:
    steps = [UPDATE_EVENT, 0.1, DONE_EVENT] if apart else [UPDATE_EVENT + DONE_EVENT]
    with FakeServer(sending(*steps)) as server:
        with follow(url_of(server), time_limit=0.4) as stream:
            assert next(stream) == UPDATED
            time.sleep(0.5)
            # The data waits in the client, but the limit passed.
            with pytest.raises(CalyxError) as caught:
                next(stream)
            assert server.client_closed_all()
            assert next(stream, None) is None
    check_error(caught.value, ClientCode.TIMEOUT, TOO_LATE)


def test_a_read_after_the_end_and_the_time_limit(follow: Follow) -> None:
    with (
        FakeServer(sending(DONE_EVENT)) as server,
        follow(url_of(server), time_limit=0.3) as stream,
    ):
        assert next(stream) == DONE
        time.sleep(0.4)
        assert next(stream, None) is None


def test_the_limit_of_the_client_does_not_stop_the_stream(follow: Follow) -> None:
    with FakeServer(sending(0.5, UPDATE_EVENT, 0.5, DONE_EVENT)) as server:
        stream = follow(url_of(server), client_time_limit=0.2)
        assert read_all(stream, server) == ([UPDATED, DONE], None)


def test_the_limit_of_the_call_can_be_longer_than_the_one_of_the_client(follow: Follow) -> None:
    with FakeServer(sending(0.5, DONE_EVENT)) as server:
        stream = follow(url_of(server), client_time_limit=0.2, time_limit=3)
        assert read_all(stream, server) == ([DONE], None)


@pytest.mark.parametrize("limit", [5.0, sys.float_info.max])
def test_a_long_time_limit(follow: Follow, limit: float) -> None:
    with FakeServer(sending(DONE_EVENT)) as server:
        assert read_all(follow(url_of(server), time_limit=limit), server) == ([DONE], None)


@pytest.mark.parametrize("limit", [0, -1, float("inf"), float("nan")])
def test_a_bad_time_limit_fails_before_the_first_read(follow: Follow, limit: float) -> None:
    with pytest.raises(CalyxError) as caught:
        follow("http://localhost", time_limit=limit)
    assert caught.value.code == ClientCode.INVALID_CONFIG


@pytest.mark.parametrize("id", ["", "..", 7])
def test_a_bad_id_fails_before_the_first_read(follow: Follow, id: Any) -> None:
    with pytest.raises(CalyxError) as caught:
        follow("http://localhost", id=id)
    assert caught.value.code == ClientCode.INVALID_CONFIG


def test_a_ca_file_that_cannot_be_loaded(follow: Follow) -> None:
    with follow("https://localhost:1", ca_file="/missing.pem") as stream:
        with pytest.raises(CalyxError) as caught:
            next(stream)
        assert next(stream, None) is None
    assert caught.value.code == ClientCode.INVALID_CONFIG


# The program stops the stream.


def test_a_break_closes_the_connection_at_the_end_of_the_block(follow: Follow) -> None:
    with FakeServer(sending(UPDATE_EVENT)) as server:
        with follow(url_of(server)) as stream:
            for item in stream:
                assert item == UPDATED
                break
            assert not server.client_closed_all(0.1)
        assert server.client_closed_all()
        assert (server.connections, len(server.requests)) == (1, 1)


@pytest.mark.parametrize("kind", [ValueError, KeyboardInterrupt, CalyxError, StopIteration])
def test_an_exception_of_the_program_passes_unchanged(
    follow: Follow, kind: type[BaseException]
) -> None:
    if kind is CalyxError:
        mine: BaseException = CalyxError(source=Source.CORE, code="x", status=1, detail="")
    else:
        mine = kind("mine")
    with FakeServer(sending(UPDATE_EVENT)) as server:
        with pytest.raises(kind) as caught, follow(url_of(server)) as stream:
            next(stream)
            raise mine
        assert caught.value is mine
        assert (mine.__cause__, mine.__context__) == (None, None)
        assert server.client_closed_all()


def test_keyboard_interrupt_in_a_read(follow: Follow) -> None:
    main = threading.main_thread().ident
    assert main is not None

    def handle(connection: Connection) -> None:
        sending(UPDATE_EVENT)(connection)
        connection.wait(0.2)
        signal.pthread_kill(main, signal.SIGINT)

    with FakeServer(handle) as server:
        with follow(url_of(server)) as stream:
            assert next(stream) == UPDATED
            with pytest.raises(KeyboardInterrupt):
                next(stream)
            assert server.client_closed_all()
            assert next(stream, None) is None


def test_a_stream_with_no_block_closes_at_its_end() -> None:
    with FakeServer(sending(UPDATE_EVENT, DONE_EVENT)) as server:
        stream = BackendClient(url_of(server)).follow_pairing(ID)
        assert list(stream) == [UPDATED, DONE]
        assert server.client_closed_all()

    async def main() -> None:
        stream = AsyncBackendClient(url_of(server)).follow_pairing(ID)
        assert [item async for item in stream] == [UPDATED, DONE]
        assert await asyncio.to_thread(server.client_closed_all)

    with FakeServer(sending(UPDATE_EVENT, DONE_EVENT)) as server:
        asyncio.run(main())


# The program must use a block. Without one, the garbage collection closes the stream, with no
# warning.
def test_a_stream_that_nobody_closes(caplog: pytest.LogCaptureFixture) -> None:
    with FakeServer(sending(UPDATE_EVENT)) as server:
        for _ in BackendClient(url_of(server)).follow_pairing(ID):
            break
        assert server.client_closed_all()

    async def leave() -> None:
        async for _ in AsyncBackendClient(url_of(server)).follow_pairing(ID):
            break

    async def main() -> None:
        await leave()
        gc.collect()
        assert await asyncio.to_thread(server.client_closed_all)

    with FakeServer(sending(UPDATE_EVENT)) as server:
        asyncio.run(main())
    with FakeServer(sending(UPDATE_EVENT)) as server:
        asyncio.run(leave())
        assert server.client_closed_all()
    assert caplog.records == []


# asyncio.


async def read_first(
    server: FakeServer, *, time_limit: float | None = None
) -> tuple[AsyncPairingStream, "asyncio.Task[Item]"]:
    """Reads one item, and starts a read that waits."""
    stream = AsyncBackendClient(url_of(server)).follow_pairing(ID, time_limit=time_limit)
    assert await anext(stream) == UPDATED

    async def read() -> Item:
        return await anext(stream)

    task = asyncio.create_task(read())
    await asyncio.sleep(0.1)
    return stream, task


@pytest.mark.parametrize("limit", [None, 5])
def test_a_cancellation_passes_unchanged(limit: float | None) -> None:
    async def main() -> None:
        stream, task = await read_first(server, time_limit=limit)
        assert task.cancel("stop")
        with pytest.raises(asyncio.CancelledError) as caught:
            await task
        assert caught.value.args == ("stop",)
        assert await asyncio.to_thread(server.client_closed_all)
        assert await anext(stream, None) is None

    with FakeServer(sending(UPDATE_EVENT)) as server:
        asyncio.run(main())


@pytest.mark.parametrize("scheme", ["http", "https"])
def test_a_cancellation_of_the_first_read(scheme: str) -> None:
    async def main() -> None:
        stream = AsyncBackendClient(url_of(server, scheme)).follow_pairing(ID)

        async def read() -> None:
            async with stream:
                async for _ in stream:
                    pass

        task = asyncio.create_task(read())
        await asyncio.sleep(0.2)
        assert task.cancel("stop")
        with pytest.raises(asyncio.CancelledError) as caught:
            await task
        assert caught.value.args == ("stop",)
        assert await asyncio.to_thread(server.client_closed_all)

    with FakeServer(lambda connection: None) as server:
        asyncio.run(main())
        assert server.connections == 1


def test_a_cancellation_in_the_block() -> None:
    async def main() -> None:
        async def read() -> None:
            async with AsyncBackendClient(url_of(server)).follow_pairing(ID) as stream:
                await anext(stream)
                await asyncio.sleep(5)

        task = asyncio.create_task(read())
        await asyncio.sleep(0.2)
        assert task.cancel("stop")
        with pytest.raises(asyncio.CancelledError) as caught:
            await task
        assert caught.value.args == ("stop",)
        assert task.cancelled()
        assert await asyncio.to_thread(server.client_closed_all)

    with FakeServer(sending(UPDATE_EVENT)) as server:
        asyncio.run(main())


def test_a_time_limit_of_the_program_is_not_an_sdk_timeout() -> None:
    async def main() -> None:
        with pytest.raises(TimeoutError):
            async with (
                asyncio.timeout(0.3),
                AsyncBackendClient(url_of(server)).follow_pairing(ID) as stream,
            ):
                async for item in stream:
                    assert item == UPDATED
        assert await asyncio.to_thread(server.client_closed_all)

    with FakeServer(sending(UPDATE_EVENT)) as server:
        asyncio.run(main())


def test_a_cancellation_at_the_time_limit_wins() -> None:
    async def main() -> None:
        _, task = await read_first(server, time_limit=0.4)
        asyncio.get_running_loop().call_later(0.35, task.cancel)
        # The loop stops until both times are in the past, so it handles them in one step.
        time.sleep(0.6)  # noqa: ASYNC251
        with pytest.raises(asyncio.CancelledError):
            await task
        assert await asyncio.to_thread(server.client_closed_all)

    with FakeServer(sending(UPDATE_EVENT)) as server:
        asyncio.run(main())


def test_a_read_lets_other_tasks_run() -> None:
    async def main() -> None:
        ticks = 0

        async def tick() -> None:
            nonlocal ticks
            while True:
                ticks += 1
                await asyncio.sleep(0.02)

        task = asyncio.create_task(tick())
        async with AsyncBackendClient(url_of(server)).follow_pairing(ID) as stream:
            assert [item async for item in stream] == [UPDATED, DONE]
        assert ticks > 10
        task.cancel()

    with FakeServer(sending(UPDATE_EVENT, 0.5, DONE_EVENT)) as server:
        asyncio.run(main())


# The public classes.


def test_the_public_names() -> None:
    names = {
        "PairingStream",
        "AsyncPairingStream",
        "PairingUpdate",
        "PairingDone",
        "PairingFailed",
    }
    assert set(calyx_client_sdk.__all__) >= names


def test_the_items_are_frozen() -> None:
    for item, name in [(UPDATED, "pairing"), (DONE, "status")]:
        with pytest.raises(FrozenInstanceError):
            setattr(item, name, None)


def describe(item: Item) -> str:
    # A type checker sees that the match is complete.
    match item:
        case PairingUpdate(pairing=pairing):
            return pairing.message
        case PairingDone(status=status):
            return status
        case PairingFailed(code=PairingFailureCode.PAIRING_EXPIRED):
            return "expired"
        case PairingFailed(code=code):
            return code
        case _:
            assert_never(item)


def test_match() -> None:
    failed = [PairingFailed(code=code, detail="") for code in ("pairing_expired", "new_code")]
    assert [describe(item) for item in (UPDATED, DONE, *failed)] == [
        "Scan",
        "CONNECTED",
        "expired",
        "new_code",
    ]


@pytest.mark.parametrize(("kind", "schema"), [(PairingDone, "Done"), (PairingFailed, "Failed")])
def test_the_fields_are_the_ones_of_the_document(kind: type[Any], schema: str) -> None:
    properties = DOCUMENT["components"]["schemas"][schema]["properties"]
    assert set(kind.__dataclass_fields__) == set(properties)
