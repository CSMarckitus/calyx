"""A fake core: a gRPC server in threads. It takes and gives bytes, so a test controls each answer.
There is also a server of raw sockets, for the things that a gRPC server cannot do."""

import asyncio
import socket
import threading
import traceback
from collections.abc import Callable, Iterable, Iterator, Mapping
from concurrent.futures import ThreadPoolExecutor
from contextlib import contextmanager, suppress
from pathlib import Path
from types import TracebackType
from typing import Any, NoReturn, Self

import grpc
import pytest
from calyx_client_sdk import (
    CalyxError,
    ClientCode,
    ConnectionUpdate,
    ConnectionUpdateEvent,
    RetryAdvice,
    Source,
)
from google.protobuf.message import Message
from google.protobuf.timestamp_pb2 import Timestamp
from google.rpc.status_pb2 import Status

from calyx_client_sdk_core import AsyncCoreClient, CoreClient, _channel
from calyx_client_sdk_core._proto.calyx.core.v1 import event_pb2

TOKEN = "core-token-0123456789-0123456789-0123456789"
KEY = "key-1"
SEND: dict[str, Any] = {"instance_name": "i_1", "to": "34600000000", "text": "Hi"}
EVENT = event_pb2.Event(
    id="evt-1",
    instance_name="sales",
    type="connection.update",
    payload_json='{"status":"CONNECTED"}',
    occurred_at=Timestamp(seconds=1, nanos=123456789),
)
EVENT_READ = ConnectionUpdateEvent(
    id="evt-1",
    instance="sales",
    timestamp="1970-01-01T00:00:01.123456789Z",
    data=ConnectionUpdate(status="CONNECTED"),
)
# The longest wait of a handler. It is shorter than the limit of one test (10 s).
MAX_WAIT = 5.0
# The size that the core accepts.
MAX_REQUEST = 17 << 20


class FakeCall:
    """One call, as the core got it."""

    def __init__(self, path: str, body: bytes, context: grpc.ServicerContext) -> None:
        self.path = path
        self.body = body
        self.metadata = dict(context.invocation_metadata())
        # With no limit, gRPC gives the largest time.
        limit: float = context.time_remaining()
        self.time_limit = None if limit > 1e18 else limit
        self.peer: str = context.peer()
        self.ended = threading.Event()
        self._context = context
        context.add_callback(self.ended.set)

    def wait_for_end(self) -> bool:
        """Waits until the client stops the call. A call that the client waits for never ends."""
        return self.ended.wait(MAX_WAIT)

    def fail(self, status: Status, *, trailer: bytes | None = None) -> NoReturn:
        """Answers with an error. `trailer` is in the place of the bytes of the status."""
        details = status.SerializeToString() if trailer is None else trailer
        self._context.set_trailing_metadata((("grpc-status-details-bin", details),))
        code = next(c for c in grpc.StatusCode if c.value[0] == status.code)
        self._context.abort(code, status.message)


type Answer = Message | bytes
# More answers than one are for a stream. None gives no message, and the status OK.
type Handler = Callable[[FakeCall], Answer | Iterable[Answer] | None]


class FakeCore(grpc.GenericRpcHandler):
    """A core on a free port, or on a socket file. `handler` runs in a thread, once for each call."""

    def __init__(
        self,
        handler: Handler,
        *,
        files: tuple[Path, Path] | None = None,
        host: str = "127.0.0.1",
        unix: str | None = None,
    ) -> None:
        self.calls: list[FakeCall] = []
        self._handler = handler
        self._pool = ThreadPoolExecutor(max_workers=20)
        # The short pings of the keepalive tests are no reason to close a connection.
        options = [
            ("grpc.max_receive_message_length", MAX_REQUEST),
            ("grpc.http2.max_ping_strikes", 0),
        ]
        self._server = grpc.server(self._pool, handlers=[self], options=options)
        if unix is not None:
            self.port = self._server.add_insecure_port(f"unix:{unix}")
        elif files is None:
            self.port = self._server.add_insecure_port(f"{host}:0")
        else:
            certificate, key = files
            pair = (key.read_bytes(), certificate.read_bytes())
            credentials = grpc.ssl_server_credentials([pair])
            self.port = self._server.add_secure_port(f"{host}:0", credentials)
        self.address = f"{host}:{self.port}"

    def __enter__(self) -> Self:
        self._server.start()
        return self

    def __exit__(
        self,
        kind: type[BaseException] | None,
        error: BaseException | None,
        trace: TracebackType | None,
    ) -> None:
        self._server.stop(None).wait()
        self._pool.shutdown()

    def service(self, details: Any) -> grpc.RpcMethodHandler:
        # On the wire, one answer is a stream of one message.
        def answer(body: bytes, context: grpc.ServicerContext) -> Iterable[bytes]:
            call = FakeCall(details.method, body, context)
            self.calls.append(call)
            result = self._handler(call)
            if result is None:
                result = []
            for item in [result] if isinstance(result, Message | bytes) else result:
                yield item.SerializeToString() if isinstance(item, Message) else item

        return grpc.unary_stream_rpc_method_handler(answer)


def answer_empty(seen: FakeCall) -> bytes:
    """Each field of the answer has its default. This is no answer for a bulk job."""
    return b""


def wait_for_end(seen: FakeCall) -> None:
    seen.wait_for_end()


def sending(*steps: Answer | float, wait: bool = False) -> Any:
    """Makes a core that sends items, and waits for some seconds between them. With `wait`, the
    stream then stays open until the client stops it."""

    def handle(seen: FakeCall) -> Iterable[Answer]:
        for step in steps:
            if isinstance(step, int | float):
                seen.ended.wait(step)
            else:
                yield step
        if wait:
            seen.wait_for_end()

    return handle


type Options = Mapping[str, Any]
type Call = Callable[..., Any]


def call_sync(address: str, name: str, options: Options | None = None, /, **arguments: Any) -> Any:
    """Makes one call with a new client. `options` are for the client."""
    with CoreClient(
        address, TOKEN, **({"insecure": True} if options is None else options)
    ) as client:
        return getattr(client, name)(**arguments)


def call_async(address: str, name: str, options: Options | None = None, /, **arguments: Any) -> Any:
    async def main() -> Any:
        async with AsyncCoreClient(
            address, TOKEN, **({"insecure": True} if options is None else options)
        ) as client:
            return await getattr(client, name)(**arguments)

    return asyncio.run(main())


class RawConnection:
    """One connection of a server that is not gRPC."""

    def __init__(self, sock: socket.socket) -> None:
        self.sock = sock
        self._data = b""
        self._started = False

    def read_to_end(self) -> None:
        while self.sock.recv(1 << 16):
            pass

    def send_settings(self) -> None:
        self.sock.sendall(b"\0\0\0\4\0" + b"\0" * 4)

    def read_call(self) -> bytes:
        """Reads the HTTP/2 frames until the one with the headers of a call. Gives its stream id."""
        if not self._started:
            self._read(len(b"PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"))
            self._started = True
        while True:
            header = self._read(9)
            self._read(int.from_bytes(header[:3]))
            if header[3] == 1:
                return header[5:]

    def _read(self, size: int) -> bytes:
        while len(self._data) < size:
            data = self.sock.recv(1 << 16)
            if not data:
                raise ConnectionError("The client closed the connection.")
            self._data += data
        data, self._data = self._data[:size], self._data[size:]
        return data


@contextmanager
def raw_server(handle: Callable[[RawConnection], None]) -> Iterator[str]:
    """Gives the address of the server. `handle` runs in a thread, once for each connection."""
    sockets: list[socket.socket] = []

    def accept() -> None:
        with suppress(OSError):
            while True:
                sock, _ = listener.accept()
                sockets.append(sock)
                pool.submit(run, sock)

    def run(sock: socket.socket) -> None:
        with sock, suppress(ConnectionError):
            handle(RawConnection(sock))

    with socket.create_server(("127.0.0.1", 0)) as listener, ThreadPoolExecutor() as pool:
        pool.submit(accept)
        try:
            yield f"127.0.0.1:{listener.getsockname()[1]}"
        finally:
            for sock in (listener, *sockets):
                with suppress(OSError):
                    sock.shutdown(socket.SHUT_RDWR)


class Proxy:
    """A TCP proxy in front of a fake core."""

    def __init__(self, target: str) -> None:
        host, port = target.split(":")
        self._target = (host, int(port))
        self._cut = threading.Event()
        self.connections = 0

    def cut(self) -> None:
        """The open connections stay open but carry no more bytes, as when the host of the core
        disappears. A new connection works."""
        self._cut.set()
        self._cut = threading.Event()

    def handle(self, connection: RawConnection) -> None:
        self.connections += 1
        cut = self._cut
        with socket.create_connection(self._target) as core:
            answers = threading.Thread(target=_pump, args=(core, connection.sock, cut))
            answers.start()
            _pump(connection.sock, core, cut)
            core.shutdown(socket.SHUT_RDWR)
            answers.join()


def _pump(source: socket.socket, target: socket.socket, cut: threading.Event) -> None:
    # One side can close before the other one.
    with suppress(OSError):
        while data := source.recv(1 << 16):
            if not cut.is_set():
                target.sendall(data)
        target.shutdown(socket.SHUT_RDWR)


@contextmanager
def start_proxy(core: FakeCore) -> Iterator[tuple[Proxy, str]]:
    """Gives the proxy and its address."""
    proxy = Proxy(core.address)
    with raw_server(proxy.handle) as address:
        yield proxy, address


def set_short_keepalive(monkeypatch: pytest.MonkeyPatch) -> None:
    # gRPC sends at most one ping in a second.
    options = dict(_channel._OPTIONS)
    options["grpc.keepalive_time_ms"] = 200
    options["grpc.http2.ping_timeout_ms"] = 200
    monkeypatch.setattr(_channel, "_OPTIONS", tuple(options.items()))


def check_client_error(
    error: CalyxError,
    code: str,
    retry: RetryAdvice = RetryAdvice.RETRY_NOW,
    key: str | None = None,
) -> None:
    assert (error.source, error.code, error.status) == (Source.CLIENT, code, 0)
    assert (error.retry, error.idempotency_key) == (retry, key)
    assert TOKEN not in "".join(traceback.format_exception(error)) + repr(vars(error))


def check_config_error(error: CalyxError, key: str | None = None) -> None:
    check_client_error(error, ClientCode.INVALID_CONFIG, RetryAdvice.DO_NOT_RETRY, key)
