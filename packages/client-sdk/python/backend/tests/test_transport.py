import asyncio
import errno
import gc
import os
import signal
import socket
import subprocess
import sys
import threading
import time
from collections.abc import Callable, Iterator
from dataclasses import dataclass
from pathlib import Path
from typing import Protocol

import certificates
import httpcore2
import pytest
from certificates import Authority
from fake_server import Connection, FakeServer, SilentAddresses

from calyx_client_sdk import CalyxError, ClientCode, RetryAdvice, Source, _http
from calyx_client_sdk import __version__ as version
from calyx_client_sdk._call import (
    Operation,
    Prepared,
    RawAnswer,
    finish,
    make_config,
    prepare,
    read_model,
    read_status,
)
from calyx_client_sdk._http import AsyncTransport, SyncTransport

MAX_ANSWER = 32 * 1024 * 1024
KEY = "key-1"


@dataclass(frozen=True, slots=True, kw_only=True)
class Thing:
    id: str


class Call(Protocol):
    def __call__(
        self,
        prepared: Prepared[object],
        server: FakeServer | None = None,
        *,
        ca_file: str | None = None,
    ) -> RawAnswer: ...


def call_sync(
    prepared: Prepared[object], server: FakeServer | None = None, *, ca_file: str | None = None
) -> RawAnswer:
    try:
        return SyncTransport(ca_file).send(prepared)
    finally:
        assert server is None or server.client_closed_all()


def call_async(
    prepared: Prepared[object], server: FakeServer | None = None, *, ca_file: str | None = None
) -> RawAnswer:
    async def main() -> RawAnswer:
        try:
            return await AsyncTransport(ca_file).send(prepared)
        finally:
            # The loop must be open here. A loop that ends can close a socket that the SDK left open.
            assert server is None or await asyncio.to_thread(server.client_closed_all)

    return asyncio.run(main())


@pytest.fixture(params=[call_sync, call_async], ids=["sync", "async"])
def call(request: pytest.FixtureRequest) -> Call:
    chosen: Call = request.param
    return chosen


@pytest.fixture(autouse=True)
def no_open_socket() -> Iterator[None]:
    yield
    # A socket that nobody closed gives its warning here, in the test that left it open.
    gc.collect()


@pytest.fixture(scope="module")
def authority(tmp_path_factory: pytest.TempPathFactory) -> Authority:
    return certificates.make_authority(tmp_path_factory.mktemp("tls") / "ca")


@pytest.fixture(scope="module")
def other_authority(tmp_path_factory: pytest.TempPathFactory) -> Authority:
    return certificates.make_authority(tmp_path_factory.mktemp("tls") / "ca")


def request(
    port: int,
    *,
    method: str = "GET",
    scheme: str = "http",
    host: str = "127.0.0.1",
    limit: float = 5,
    text: str | None = None,
    key: str | None = None,
    operation_id: str = "doThing",
) -> Prepared[object]:
    return prepare(
        make_config(f"{scheme}://{host}:{port}", "cx_secret", None),
        Operation[object](
            id=operation_id,
            method=method,
            path="/api/v1/things",
            time_limit=120,
            read=read_model(Thing),
        ),
        arguments=None if text is None else {"text": text},
        idempotency_key=key,
        time_limit=limit,
    )


def answer_ok(connection: Connection) -> None:
    connection.read_request()
    connection.answer()


def check_error(error: CalyxError, code: str, retry: RetryAdvice = RetryAdvice.RETRY_NOW) -> None:
    assert (error.source, error.code, error.status) == (Source.CLIENT, code, 0)
    assert error.retry is retry


# Each case of the time limits ends at the limit of 2 s.


def check_ends_at_limit(call: Call, prepared: Prepared[object], server: FakeServer | None) -> None:
    start = time.monotonic()
    with pytest.raises(CalyxError) as caught:
        call(prepared, server)
    assert 1.9 < time.monotonic() - start < 3
    check_error(caught.value, ClientCode.TIMEOUT)
    assert caught.value.detail == "The backend did not answer in time."
    assert caught.value.idempotency_key == KEY
    assert caught.value.__cause__ is not None


def slow_header(connection: Connection) -> None:
    connection.read_request()
    connection.sock.sendall(b"HTTP/1.1 200 OK\r\nX-Text: ")
    connection.send_slowly(b"x" * 50)


def slow_body(connection: Connection) -> None:
    connection.read_request()
    connection.sock.sendall(b"HTTP/1.1 200 OK\r\nContent-Length: 1000\r\n\r\n")
    connection.send_slowly(b"x" * 50)


def read_nothing(connection: Connection) -> None:
    connection.wait()


def read_slowly(connection: Connection) -> None:
    """Reads about 1 MB/s, for 5 s."""
    for _ in range(500):
        if connection.wait(0.01):
            return
        connection.sock.recv(10_000)


@pytest.mark.parametrize("handle", [slow_header, slow_body])
def test_a_slow_answer_ends_at_the_limit(call: Call, handle: Callable[[Connection], None]) -> None:
    with FakeServer(handle) as server:
        check_ends_at_limit(call, request(server.port, method="POST", limit=2, key=KEY), server)
        assert (server.connections, len(server.requests)) == (1, 1)


def test_a_silent_tls_peer_ends_at_the_limit(call: Call) -> None:
    with FakeServer(read_nothing) as server:
        check_ends_at_limit(call, request(server.port, scheme="https", limit=2, key=KEY), server)
        assert (server.connections, len(server.requests)) == (1, 0)


@pytest.mark.parametrize("handle", [read_nothing, read_slowly])
def test_a_slow_reader_ends_at_the_limit(call: Call, handle: Callable[[Connection], None]) -> None:
    with FakeServer(handle, receive_buffer=1 << 16) as server:
        prepared = request(server.port, method="POST", limit=2, text="x" * 20_000_000, key=KEY)
        check_ends_at_limit(call, prepared, server)
        assert (server.connections, len(server.requests)) == (1, 0)


def test_addresses_that_do_not_answer_end_at_the_limit(
    call: Call, monkeypatch: pytest.MonkeyPatch
) -> None:
    with SilentAddresses() as silent:
        give_addresses(monkeypatch, *silent.HOSTS)
        prepared = request(silent.port, host="calyx.test", limit=2, key=KEY)
        check_ends_at_limit(call, prepared, None)


@pytest.mark.parametrize("status", [307, 308])
def test_no_redirect(call: Call, status: int) -> None:
    def redirect(connection: Connection) -> None:
        connection.read_request()
        location = f"Location: http://127.0.0.1:{connection.server.port}/other\r\n"
        connection.answer(status, headers=location)

    with FakeServer(redirect) as server:
        prepared = request(server.port, method="POST", text="Hello", key=KEY)
        answer = call(prepared, server)
        assert answer.status == status
        assert (server.connections, len(server.requests)) == (1, 1)
    with pytest.raises(CalyxError) as caught:
        finish(prepared, answer)
    check_error(caught.value, ClientCode.INVALID_RESPONSE)


def test_no_second_request_after_a_close(call: Call) -> None:
    def close(connection: Connection) -> None:
        connection.read_request()
        connection.sock.close()

    with FakeServer(close) as server:
        with pytest.raises(CalyxError) as caught:
            call(request(server.port, key=KEY), server)
        assert (server.connections, len(server.requests)) == (1, 1)
    check_error(caught.value, ClientCode.NETWORK_ERROR)
    assert caught.value.detail == "The connection to the backend failed."
    assert caught.value.idempotency_key == KEY
    assert caught.value.__cause__ is not None


# The request and the answer.


def test_request_and_answer(call: Call) -> None:
    body = b'{"id":"1"}' * 50_000

    def answer(connection: Connection) -> None:
        connection.read_request()
        connection.answer(201, body, "X-Request-Id: r1\r\nX-Two: a\r\nX-Two: b\r\n")

    with FakeServer(answer) as server:
        answered = call(request(server.port, method="POST", text="ñ", key=KEY), server)
        (seen,) = server.requests
        assert server.connections == 1
    assert answered.status == 201
    assert answered.body == body
    assert answered.header("x-request-id") == "r1"
    assert [value for name, value in answered.headers if name.lower() == b"x-two"] == [b"a", b"b"]
    assert seen.line == "POST /api/v1/things HTTP/1.1"
    assert seen.body == '{"text":"ñ"}'.encode()
    assert sorted(seen.headers) == [
        ("Accept-Encoding", "identity"),
        ("Authorization", "Bearer cx_secret"),
        ("Content-Length", "13"),
        ("Content-Type", "application/json"),
        ("Host", f"127.0.0.1:{server.port}"),
        ("Idempotency-Key", KEY),
        ("User-Agent", f"calyx-python-sdk/{version}"),
    ]


@pytest.mark.parametrize(("method", "lengths"), [("POST", ["0"]), ("GET", []), ("DELETE", [])])
def test_request_with_no_body(call: Call, method: str, lengths: list[str]) -> None:
    def answer(connection: Connection) -> None:
        connection.read_request()
        connection.sock.sendall(b"HTTP/1.1 204 No Content\r\n\r\n")

    with FakeServer(answer) as server:
        assert call(request(server.port, method=method), server).status == 204
        (seen,) = server.requests
    assert seen.all("Content-Length") == lengths
    assert seen.all("Transfer-Encoding") == []


def test_path_of_the_base_url(call: Call) -> None:
    with FakeServer(answer_ok) as server:
        config = make_config(f"http://localhost:{server.port}/a;b/c", None, None)
        operation = Operation(
            id="doThing", method="GET", path="/v1/{id}", time_limit=5, read=read_status
        )
        call(prepare(config, operation, path_id="x/y;z"), server)
        (seen,) = server.requests
    assert seen.line == "GET /a;b/c/v1/x%2Fy%3Bz HTTP/1.1"
    assert seen.all("Host") == [f"localhost:{server.port}"]


def test_ipv6_host_keeps_its_brackets(call: Call) -> None:
    with FakeServer(answer_ok, host="::1") as server:
        assert call(request(server.port, host="[::1]"), server).status == 200
        (seen,) = server.requests
    assert seen.all("Host") == [f"[::1]:{server.port}"]


def test_answer_with_no_length(call: Call) -> None:
    def answer(connection: Connection) -> None:
        connection.read_request()
        connection.sock.sendall(b"HTTP/1.1 200 OK\r\n\r\n{}")
        connection.sock.shutdown(socket.SHUT_WR)

    with FakeServer(answer) as server:
        assert call(request(server.port), server).body == b"{}"


def test_answer_before_the_end_of_the_request(call: Call) -> None:
    def answer(connection: Connection) -> None:
        connection.sock.recv(1000)
        connection.answer(413, b"too large")

    with FakeServer(answer) as server:
        answered = call(request(server.port, method="POST", text="x" * 5_000_000), server)
        assert server.connections == 1
    assert (answered.status, answered.body) == (413, b"too large")


def test_answer_of_a_server_that_does_not_read_the_request() -> None:
    def answer(connection: Connection) -> None:
        connection.sock.recv(1000)
        connection.answer(413, b"too large")
        # A close with unread bytes sends a reset, and the client cannot write the rest.
        time.sleep(0.3)
        connection.sock.close()

    with FakeServer(answer, receive_buffer=1 << 16) as server:
        prepared = request(server.port, method="POST", text="x" * 20_000_000)
        answered = call_sync(prepared, server)
        assert (answered.status, answered.body) == (413, b"too large")
        # The two clients differ here for a request body of more than about 1 MB: asyncio closes
        # the socket when a write fails, so the answer is lost.
        with pytest.raises(CalyxError) as caught:
            call_async(prepared, server)
    check_error(caught.value, ClientCode.NETWORK_ERROR)


def plain(body: bytes) -> bytes:
    return f"Content-Length: {len(body)}\r\n\r\n".encode() + body


def chunked(body: bytes) -> bytes:
    middle = len(body) // 2
    chunks = [f"{len(part):x}\r\n".encode() + part for part in (body[:middle], body[middle:], b"")]
    return b"Transfer-Encoding: chunked\r\n\r\n" + b"\r\n".join(chunks) + b"\r\n"


@pytest.mark.parametrize("encode", [plain, chunked])
@pytest.mark.parametrize("status", [200, 500])
def test_the_size_limit_of_an_answer(
    call: Call, encode: Callable[[bytes], bytes], status: int
) -> None:
    size = MAX_ANSWER

    def answer(connection: Connection) -> None:
        connection.read_request()
        connection.sock.sendall(f"HTTP/1.1 {status} Text\r\n".encode() + encode(b"x" * size))

    with FakeServer(answer) as server:
        assert len(call(request(server.port), server).body) == MAX_ANSWER
    size += 1
    with FakeServer(answer) as server:
        with pytest.raises(CalyxError) as caught:
            call(request(server.port, key=KEY), server)
        assert (server.connections, len(server.requests)) == (1, 1)
    check_error(caught.value, ClientCode.INVALID_RESPONSE)
    assert caught.value.detail == (
        "The backend answered with a body that is larger than the SDK reads."
    )
    assert caught.value.idempotency_key == KEY


# Errors of the connection.


@pytest.mark.parametrize(
    "data",
    [
        b"HTTP/1.1 200 OK\r\nContent-Le",
        b"HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\n{",
        b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n64\r\n{",
        b"SSH-2.0-OpenSSH_9.6\r\n",
    ],
    ids=["header", "body", "chunk", "not-http"],
)
def test_an_answer_that_is_cut_off(call: Call, data: bytes) -> None:
    def answer(connection: Connection) -> None:
        connection.read_request()
        connection.sock.sendall(data)
        connection.sock.shutdown(socket.SHUT_WR)

    with FakeServer(answer) as server:
        with pytest.raises(CalyxError) as caught:
            call(request(server.port), server)
        assert len(server.requests) == 1
    check_error(caught.value, ClientCode.NETWORK_ERROR)


def test_a_connection_that_the_peer_resets(call: Call) -> None:
    def reset(connection: Connection) -> None:
        # A close with unread bytes sends a reset.
        connection.sock.recv(10)
        connection.sock.close()

    with FakeServer(reset) as server:
        with pytest.raises(CalyxError) as caught:
            call(request(server.port, method="POST", text="x" * 5_000_000), server)
        assert server.connections == 1
    check_error(caught.value, ClientCode.NETWORK_ERROR)


def test_connection_refused(call: Call) -> None:
    with socket.socket() as closed:
        closed.bind(("127.0.0.1", 0))
        with pytest.raises(CalyxError) as caught:
            call(request(closed.getsockname()[1]))
    check_error(caught.value, ClientCode.NETWORK_ERROR)


def test_create_call_has_the_advice_unknown(call: Call) -> None:
    with socket.socket() as closed:
        closed.bind(("127.0.0.1", 0))
        with pytest.raises(CalyxError) as caught:
            call(request(closed.getsockname()[1], operation_id="createClient"))
    check_error(caught.value, ClientCode.NETWORK_ERROR, RetryAdvice.UNKNOWN)


# The name lookup.

type Address = tuple[int, int, int, str, tuple[str, int]]


def replace_lookup(monkeypatch: pytest.MonkeyPatch, lookup: Callable[[int], list[Address]]) -> None:
    """Replaces the lookup of calyx.test."""
    real = socket.getaddrinfo

    def fake(host: str | bytes, port: int, *args: int, **options: int) -> list[Address]:
        if host in ("calyx.test", b"calyx.test"):
            return lookup(port)
        return real(host, port, *args, **options)  # type: ignore[return-value]

    monkeypatch.setattr(socket, "getaddrinfo", fake)


def give_addresses(monkeypatch: pytest.MonkeyPatch, *hosts: str) -> None:
    replace_lookup(
        monkeypatch,
        lambda port: [
            (socket.AF_INET, socket.SOCK_STREAM, socket.IPPROTO_TCP, "", (host, port))
            for host in hosts
        ],
    )


def test_the_next_address_is_used(call: Call, monkeypatch: pytest.MonkeyPatch) -> None:
    # Nothing listens on the first two addresses.
    give_addresses(monkeypatch, "127.0.0.21", "127.0.0.22", "127.0.0.1")
    with FakeServer(answer_ok) as server:
        assert call(request(server.port, host="calyx.test"), server).status == 200
        assert (server.connections, len(server.requests)) == (1, 1)


SILENT = SilentAddresses.HOSTS[0]
LIVE = "127.0.0.1"
REFUSING = "127.0.0.21"


def test_a_silent_address_does_not_take_the_whole_limit(
    call: Call, monkeypatch: pytest.MonkeyPatch
) -> None:
    give_addresses(monkeypatch, SILENT, LIVE)
    with FakeServer(answer_ok) as server, SilentAddresses(server.port):
        start = time.monotonic()
        assert call(request(server.port, host="calyx.test", limit=2), server).status == 200
        assert time.monotonic() - start < 1.5
        assert (server.connections, len(server.requests)) == (1, 1)


@pytest.mark.parametrize(
    ("hosts", "sync_code", "sync_seconds"),
    [
        # A silent last address waits until the end of the limit.
        ((REFUSING, SILENT), ClientCode.TIMEOUT, 1.0),
        ((SILENT, REFUSING, SILENT), ClientCode.TIMEOUT, 1.0),
        # The last address decides, and it answers before the end of the limit.
        ((SILENT, REFUSING), ClientCode.NETWORK_ERROR, 0.5),
    ],
)
def test_silent_and_refusing_addresses(
    call: Call,
    monkeypatch: pytest.MonkeyPatch,
    hosts: tuple[str, ...],
    sync_code: str,
    sync_seconds: float,
) -> None:
    # The async client tries all the addresses at the same time, so it always waits for the silent
    # ones.
    code, seconds = (sync_code, sync_seconds) if call is call_sync else (ClientCode.TIMEOUT, 1.0)
    give_addresses(monkeypatch, *hosts)
    with SilentAddresses() as silent:
        start = time.monotonic()
        with pytest.raises(CalyxError) as caught:
            call(request(silent.port, host="calyx.test", limit=1))
        elapsed = time.monotonic() - start
    assert seconds - 0.05 < elapsed < seconds + 0.4
    check_error(caught.value, code)


def test_a_failed_connect_makes_no_reference_cycle() -> None:
    def errors() -> list[object]:
        return [item for item in gc.get_objects() if type(item) is ConnectionRefusedError]

    gc.collect()
    gc.disable()
    try:
        with socket.socket() as closed:
            closed.bind(("127.0.0.1", 0))
            with pytest.raises(CalyxError) as caught:
                call_sync(request(closed.getsockname()[1]))
        assert len(errors()) == 1
        del caught
        assert errors() == []
    finally:
        gc.enable()


def test_a_refusing_address_makes_no_reference_cycle(monkeypatch: pytest.MonkeyPatch) -> None:
    give_addresses(monkeypatch, REFUSING, LIVE)
    with FakeServer(answer_ok) as server:
        gc.collect()
        gc.disable()
        try:
            assert call_sync(request(server.port, host="calyx.test"), server).status == 200
            assert [e for e in gc.get_objects() if type(e) is ConnectionRefusedError] == []
        finally:
            gc.enable()


@pytest.mark.parametrize(
    "error",
    [
        socket.gaierror(socket.EAI_NONAME, "Name or service not known"),
        # The time limit of the system is not the limit of the call.
        TimeoutError(errno.ETIMEDOUT, "Connection timed out"),
    ],
    ids=["no-such-name", "system-timeout"],
)
def test_a_lookup_that_fails(call: Call, monkeypatch: pytest.MonkeyPatch, error: OSError) -> None:
    def fail(port: int) -> list[Address]:
        raise error

    replace_lookup(monkeypatch, fail)
    with pytest.raises(CalyxError) as caught:
        call(request(80, host="calyx.test"))
    check_error(caught.value, ClientCode.NETWORK_ERROR)


@pytest.mark.parametrize("found", [True, False])
def test_a_slow_lookup_sends_nothing(
    call: Call, monkeypatch: pytest.MonkeyPatch, found: bool
) -> None:
    def look_up_slowly(port: int) -> list[Address]:
        time.sleep(0.5)
        if not found:
            raise socket.gaierror(socket.EAI_NONAME, "Name or service not known")
        return [(socket.AF_INET, socket.SOCK_STREAM, 0, "", ("127.0.0.1", port))]

    replace_lookup(monkeypatch, look_up_slowly)
    with FakeServer(answer_ok) as server:
        with pytest.raises(CalyxError) as caught:
            call(request(server.port, host="calyx.test", limit=0.2), server)
        assert server.connections == 0
    check_error(caught.value, ClientCode.TIMEOUT)


# The time limit.


def test_a_very_large_time_limit(call: Call) -> None:
    with FakeServer(answer_ok) as server:
        assert call(request(server.port, limit=sys.float_info.max), server).status == 200


def test_a_very_small_time_limit(call: Call) -> None:
    with FakeServer(answer_ok) as server:
        with pytest.raises(CalyxError) as caught:
            call(request(server.port, limit=sys.float_info.min), server)
        assert server.requests == []
    check_error(caught.value, ClientCode.TIMEOUT)


def test_a_wait_that_ends_too_early_is_a_timeout(monkeypatch: pytest.MonkeyPatch) -> None:
    # A socket on Windows can end a wait some ms before the time of the clock.
    monkeypatch.setattr(_http, "_MAX_WAIT", 0.1)
    with FakeServer(read_nothing) as server:
        with pytest.raises(CalyxError) as caught:
            call_sync(request(server.port), server)
    check_error(caught.value, ClientCode.TIMEOUT)


# TLS.


def test_a_private_ca_is_refused(call: Call, authority: Authority) -> None:
    with FakeServer(answer_ok, tls=authority.server_context("IP:127.0.0.1")) as server:
        with pytest.raises(CalyxError) as caught:
            call(request(server.port, scheme="https"), server)
        assert (server.connections, len(server.requests)) == (1, 0)
    check_error(caught.value, ClientCode.NETWORK_ERROR)


def test_ca_file_accepts_a_private_ca(call: Call, authority: Authority) -> None:
    body = "x" * 1_000_000
    with FakeServer(answer_ok, tls=authority.server_context("DNS:localhost")) as server:
        prepared = request(server.port, scheme="https", host="localhost", method="POST", text=body)
        assert call(prepared, server, ca_file=authority.ca_file).status == 200
        (seen,) = server.requests
    assert seen.body == b'{"text":"' + body.encode() + b'"}'


def test_ca_file_does_not_accept_another_host_name(call: Call, authority: Authority) -> None:
    with FakeServer(answer_ok, tls=authority.server_context("DNS:other.test")) as server:
        with pytest.raises(CalyxError) as caught:
            prepared = request(server.port, scheme="https", host="localhost")
            call(prepared, server, ca_file=authority.ca_file)
        assert (server.connections, len(server.requests)) == (1, 0)
    check_error(caught.value, ClientCode.NETWORK_ERROR)


def test_ca_file_adds_to_the_system_certificates(
    call: Call, authority: Authority, other_authority: Authority, monkeypatch: pytest.MonkeyPatch
) -> None:
    # With this variable, the file replaces the system certificates.
    monkeypatch.setenv("SSL_CERT_FILE", authority.ca_file)
    for signer, ca_file in [
        (authority, None),
        (authority, other_authority.ca_file),
        (other_authority, other_authority.ca_file),
    ]:
        with FakeServer(answer_ok, tls=signer.server_context("IP:127.0.0.1")) as server:
            prepared = request(server.port, scheme="https")
            assert call(prepared, server, ca_file=ca_file).status == 200


def test_a_tls_peer_that_reads_slowly_ends_at_the_limit(call: Call, authority: Authority) -> None:
    tls = authority.server_context("IP:127.0.0.1")
    with FakeServer(read_slowly, tls=tls, receive_buffer=1 << 16) as server:
        prepared = request(
            server.port, scheme="https", method="POST", limit=0.5, text="x" * 20_000_000
        )
        start = time.monotonic()
        with pytest.raises(CalyxError) as caught:
            call(prepared, server, ca_file=authority.ca_file)
        assert time.monotonic() - start < 1.5
        assert server.connections == 1
    check_error(caught.value, ClientCode.TIMEOUT)


@pytest.mark.parametrize("data", [b"", b"HTTP/1.1 400 Bad Request\r\n\r\n"])
def test_a_peer_with_no_tls(call: Call, data: bytes) -> None:
    def answer(connection: Connection) -> None:
        connection.sock.recv(1 << 16)
        connection.sock.sendall(data)
        connection.sock.shutdown(socket.SHUT_WR)

    with FakeServer(answer) as server:
        with pytest.raises(CalyxError) as caught:
            call(request(server.port, scheme="https"), server)
        assert server.connections == 1
    check_error(caught.value, ClientCode.NETWORK_ERROR)


def test_a_tls_connection_with_wrong_bytes(call: Call, authority: Authority) -> None:
    def answer(connection: Connection) -> None:
        connection.read_request()
        os.write(connection.sock.fileno(), b"This is not a TLS record.")

    with FakeServer(answer, tls=authority.server_context("IP:127.0.0.1")) as server:
        with pytest.raises(CalyxError) as caught:
            call(request(server.port, scheme="https"), server, ca_file=authority.ca_file)
        assert (server.connections, len(server.requests)) == (1, 1)
    check_error(caught.value, ClientCode.NETWORK_ERROR)


def test_a_slow_tls_handshake_ends_at_the_limit(call: Call) -> None:
    def send_slowly(connection: Connection) -> None:
        # The header of a TLS record of 16 KiB.
        connection.sock.sendall(b"\x16\x03\x03\x40\x00")
        connection.send_slowly(bytes(50))

    with FakeServer(send_slowly) as server:
        start = time.monotonic()
        with pytest.raises(CalyxError) as caught:
            call(request(server.port, scheme="https", limit=0.5), server)
        assert time.monotonic() - start < 1.5
    check_error(caught.value, ClientCode.TIMEOUT)


@pytest.mark.parametrize("name", ["missing.pem", "not-a-certificate.pem", "", "a\0b"])
def test_a_ca_file_that_cannot_be_loaded(call: Call, name: str, tmp_path: Path) -> None:
    (tmp_path / "not-a-certificate.pem").write_text("Hello")
    ca_file = str(tmp_path / name) if name else ""
    with FakeServer(answer_ok) as server:
        with pytest.raises(CalyxError) as caught:
            call(request(server.port, scheme="https", key=KEY), server, ca_file=ca_file)
        assert server.connections == 0
    check_error(caught.value, ClientCode.INVALID_CONFIG, RetryAdvice.DO_NOT_RETRY)
    assert "ca_file" in caught.value.detail
    assert caught.value.idempotency_key == KEY


def test_one_tls_context_for_each_client(
    authority: Authority, monkeypatch: pytest.MonkeyPatch
) -> None:
    real = httpcore2.default_ssl_context
    made = 0

    def count() -> object:
        nonlocal made
        made += 1
        return real()

    monkeypatch.setattr(httpcore2, "default_ssl_context", count)
    sync_transport = SyncTransport(authority.ca_file)
    async_transport = AsyncTransport(authority.ca_file)
    with FakeServer(answer_ok) as server:
        sync_transport.send(request(server.port))
    assert made == 0
    with FakeServer(answer_ok, tls=authority.server_context("IP:127.0.0.1")) as server:
        prepared = request(server.port, scheme="https")
        for _ in range(2):
            sync_transport.send(prepared)
            asyncio.run(async_transport.send(prepared))
        assert (server.connections, made) == (4, 2)


# A cancellation.


@pytest.mark.parametrize("scheme", ["http", "https"])
def test_keyboard_interrupt_passes_unchanged(call: Call, scheme: str) -> None:
    main = threading.main_thread().ident
    assert main is not None

    def interrupt(connection: Connection) -> None:
        connection.wait(0.2)
        signal.pthread_kill(main, signal.SIGINT)
        connection.wait()

    with FakeServer(interrupt) as server:
        with pytest.raises(KeyboardInterrupt):
            call(request(server.port, scheme=scheme), server)
        assert server.connections == 1


@pytest.mark.parametrize("scheme", ["http", "https"])
def test_a_cancellation_passes_unchanged(scheme: str) -> None:
    async def main(server: FakeServer) -> None:
        task = asyncio.create_task(AsyncTransport().send(request(server.port, scheme=scheme)))
        await asyncio.sleep(0.2)
        assert task.cancel("stop")
        with pytest.raises(asyncio.CancelledError) as caught:
            await task
        assert caught.value.args == ("stop",)
        assert await asyncio.to_thread(server.client_closed_all)

    with FakeServer(read_nothing) as server:
        asyncio.run(main(server))
        assert server.connections == 1


def test_a_time_limit_of_the_program_is_not_an_sdk_timeout() -> None:
    async def main(server: FakeServer) -> None:
        with pytest.raises(TimeoutError):
            async with asyncio.timeout(0.2):
                await AsyncTransport().send(request(server.port))
        assert await asyncio.to_thread(server.client_closed_all)

    with FakeServer(read_nothing) as server:
        asyncio.run(main(server))


def test_a_cancellation_at_the_time_limit_wins() -> None:
    async def main(server: FakeServer) -> None:
        task = asyncio.create_task(AsyncTransport().send(request(server.port, limit=0.3)))
        await asyncio.sleep(0.1)
        asyncio.get_running_loop().call_later(0.3, task.cancel)
        # The loop stops until both times are in the past, so it handles them in one step.
        time.sleep(0.5)  # noqa: ASYNC251
        with pytest.raises(asyncio.CancelledError):
            await task
        assert await asyncio.to_thread(server.client_closed_all)

    with FakeServer(read_nothing) as server:
        asyncio.run(main(server))


# The import.


def test_the_import_does_not_load_httpcore2() -> None:
    program = (
        "import sys\n"
        "import calyx_client_sdk\n"
        "import calyx_client_sdk.errors\n"
        "import calyx_client_sdk._http\n"
        "print([name for name in ('httpcore2', 'anyio', 'h11') if name in sys.modules])\n"
    )
    run = subprocess.run(
        [sys.executable, "-c", program], check=True, capture_output=True, text=True
    )
    assert run.stdout == "[]\n"
