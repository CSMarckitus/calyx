import asyncio
import math
import socket
import ssl
import time
from collections.abc import AsyncGenerator, Callable, Generator, Iterable
from contextlib import aclosing, asynccontextmanager, contextmanager
from typing import TYPE_CHECKING, Any, Final, cast
from urllib.parse import urlsplit

from ._call import Prepared, RawAnswer, check_stream, client_error
from ._generated.codes import ClientCode
from ._sse import EventParser, SseEvent
from .errors import CalyxError

# Only a call loads httpcore2, because the core client imports this package and does not use it.
if TYPE_CHECKING:
    from httpcore2 import (
        AsyncNetworkBackend,
        AsyncNetworkStream,
        NetworkBackend,
        NetworkStream,
        Response,
    )
else:
    # httpcore2 does not check the base classes.
    AsyncNetworkBackend = AsyncNetworkStream = NetworkBackend = NetworkStream = object

_MAX_ANSWER: Final = 32 * 1024 * 1024
# It matters for TLS: one write of OpenSSL sends the whole piece. A plain socket sends what fits.
_WRITE_SIZE: Final = 64 * 1024
# 23 days. A socket refuses a time limit of more than 2**31 ms.
_MAX_WAIT: Final = 2_000_000.0

# The limits of a stream, in seconds. The backend pings every 15 s (CONTRACT.md part 9).
_CONNECT_LIMIT: Final = 30.0
_READ_LIMIT: Final = 45.0


class _Transport:
    """Sends the calls of one client. It holds no socket, so a fork or another thread can use it."""

    def __init__(self, ca_file: str | None = None) -> None:
        self._ca_file = ca_file
        self._context: ssl.SSLContext | None = None

    def _tls_context(self, prepared: Prepared[object]) -> ssl.SSLContext | None:
        if urlsplit(prepared.url).scheme != "https":
            return None
        import httpcore2

        # Two threads can make it twice. Both contexts are the same, and the last one stays.
        if self._context is None:
            try:
                context = httpcore2.default_ssl_context()
                if self._ca_file is not None:
                    context.load_verify_locations(self._ca_file)
            # ValueError and TypeError: a value that is no path.
            except (OSError, ValueError, TypeError) as error:
                raise client_error(
                    prepared,
                    ClientCode.INVALID_CONFIG,
                    "The TLS certificates cannot be loaded. Check ca_file. Without ca_file, check "
                    "the system certificates.",
                ) from error
            self._context = context
        return self._context


class SyncTransport(_Transport):
    def send(self, prepared: Prepared[object]) -> RawAnswer:
        """Sends one request on one connection, and reads the whole answer."""
        deadline = _Deadline(prepared.time_limit)
        backend = _Backend(deadline)
        try:
            with self._open(prepared, backend) as response:
                return _read_answer(prepared, response)
        except _transport_errors() as error:
            raise _failure(prepared, timed_out=deadline.caused(error)) from error
        finally:
            backend.close()

    def follow[T](self, prepared: Prepared[object], make: Callable[[SseEvent], T]) -> Generator[T]:
        """Sends one request at the first read, and gives an item for each event of the answer."""
        deadline = _Deadline(prepared.time_limit)
        backend = _Backend(deadline)
        parser = EventParser(prepared)
        started = False
        try:
            with self._open(prepared, backend, _stream_limits()) as response:
                # An error has an end. An event stream does not.
                answer = _raw(response) if _is_ok(response) else _read_answer(prepared, response)
                check_stream(prepared, answer)
                started = True
                for chunk in response.iter_stream():
                    for event in parser.feed(chunk):
                        yield make(event)
                        # The program can use the rest of the time between two reads.
                        deadline.remaining()
                raise parser.early_end()
        except _transport_errors() as error:
            timed_out = deadline.caused(error)
            raise _stream_failure(prepared, error, timed_out=timed_out, started=started) from error
        finally:
            backend.close()

    @contextmanager
    def _open(
        self,
        prepared: Prepared[object],
        backend: "_Backend",
        limits: dict[str, float] | None = None,
    ) -> Generator["Response"]:
        import httpcore2

        with (
            httpcore2.ConnectionPool(
                ssl_context=self._tls_context(prepared),
                max_connections=1,
                retries=0,
                network_backend=backend,
            ) as pool,
            pool.stream(
                prepared.method,
                prepared.url,
                headers=_headers(prepared),
                content=prepared.body,
                extensions={"timeout": limits or {}},
            ) as response,
        ):
            yield response


class AsyncTransport(_Transport):
    async def send(self, prepared: Prepared[object]) -> RawAnswer:
        """Sends one request on one connection, and reads the whole answer."""
        limit = asyncio.timeout(prepared.time_limit)
        try:
            async with limit:
                return await self._exchange(prepared)
        except _transport_errors() as error:
            # A TimeoutError can also come from the system.
            raise _failure(prepared, timed_out=limit.expired()) from error

    async def _exchange(self, prepared: Prepared[object]) -> RawAnswer:
        backend = _AsyncBackend()
        try:
            async with self._open(prepared, backend) as response:
                return await _read_answer_async(prepared, response)
        finally:
            await backend.aclose()

    async def follow[T](
        self, prepared: Prepared[object], make: Callable[[SseEvent], T]
    ) -> AsyncGenerator[T]:
        """Sends one request at the first read, and gives an item for each event of the answer."""
        end = None
        if prepared.time_limit is not None:
            end = asyncio.get_running_loop().time() + prepared.time_limit
        async with aclosing(self._items(prepared, make)) as items:
            while True:
                # One limit for each read: the program can use a task for each one.
                limit = asyncio.timeout_at(end)
                try:
                    async with limit:
                        # An expired limit stops the read here, also when an item waits.
                        await asyncio.sleep(0)
                        item = await anext(items)
                # Each TimeoutError of `_items` is a CalyxError.
                except TimeoutError as error:
                    raise _failure(prepared, timed_out=True) from error
                yield item

    async def _items[T](
        self, prepared: Prepared[object], make: Callable[[SseEvent], T]
    ) -> AsyncGenerator[T]:
        """Never ends with no error: the caller stops after the last item."""
        backend = _AsyncBackend()
        parser = EventParser(prepared)
        started = False
        try:
            async with self._open(prepared, backend, _stream_limits()) as response:
                if _is_ok(response):
                    answer = _raw(response)
                else:
                    answer = await _read_answer_async(prepared, response)
                check_stream(prepared, answer)
                started = True
                async with aclosing(response.aiter_stream()) as chunks:
                    async for chunk in chunks:
                        for event in parser.feed(chunk):
                            yield make(event)
                raise parser.early_end()
        except _transport_errors() as error:
            raise _stream_failure(prepared, error, timed_out=False, started=started) from error
        finally:
            await backend.aclose()

    @asynccontextmanager
    async def _open(
        self,
        prepared: Prepared[object],
        backend: "_AsyncBackend",
        limits: dict[str, float] | None = None,
    ) -> AsyncGenerator["Response"]:
        import httpcore2

        async with (
            httpcore2.AsyncConnectionPool(
                ssl_context=self._tls_context(prepared),
                max_connections=1,
                retries=0,
                network_backend=backend,
            ) as pool,
            pool.stream(
                prepared.method,
                prepared.url,
                headers=_headers(prepared),
                content=prepared.body,
                extensions={"timeout": limits or {}},
            ) as response,
        ):
            yield response


def _is_ok(response: "Response") -> bool:
    return 200 <= response.status <= 299


def _raw(response: "Response", body: bytes = b"") -> RawAnswer:
    return RawAnswer(status=response.status, headers=response.headers, body=body)


def _read_answer(prepared: Prepared[object], response: "Response") -> RawAnswer:
    body = bytearray()
    for chunk in response.iter_stream():
        body += chunk
        if len(body) > _MAX_ANSWER:
            raise _too_large(prepared)
    return _raw(response, bytes(body))


async def _read_answer_async(prepared: Prepared[object], response: "Response") -> RawAnswer:
    body = bytearray()
    async with aclosing(response.aiter_stream()) as chunks:
        async for chunk in chunks:
            body += chunk
            if len(body) > _MAX_ANSWER:
                raise _too_large(prepared)
    return _raw(response, bytes(body))


def _stream_limits() -> dict[str, float]:
    return {"connect": _CONNECT_LIMIT, "read": _READ_LIMIT, "write": _READ_LIMIT}


def _transport_errors() -> tuple[type[Exception], ...]:
    import httpcore2

    # OSError includes TimeoutError and ssl.SSLError.
    return (
        httpcore2.TimeoutException,
        httpcore2.NetworkError,
        httpcore2.ProtocolError,
        OSError,
    )


def _headers(prepared: Prepared[object]) -> list[tuple[str, str]]:
    # httpcore2 makes a Host header with no brackets for an IPv6 address.
    return [("Host", urlsplit(prepared.url).netloc), *prepared.headers.items()]


def _too_large(prepared: Prepared[object]) -> CalyxError:
    return client_error(
        prepared,
        ClientCode.INVALID_RESPONSE,
        "The backend answered with a body that is larger than the SDK reads.",
    )


def _failure(prepared: Prepared[object], *, timed_out: bool) -> CalyxError:
    if timed_out:
        return client_error(prepared, ClientCode.TIMEOUT, "The backend did not answer in time.")
    return client_error(prepared, ClientCode.NETWORK_ERROR, "The connection to the backend failed.")


def _stream_failure(
    prepared: Prepared[object], error: Exception, *, timed_out: bool, started: bool
) -> CalyxError:
    import httpcore2

    if timed_out:
        return _failure(prepared, timed_out=True)
    if isinstance(error, httpcore2.ReadTimeout | httpcore2.WriteTimeout):
        detail = f"The pairing stream sent no data for {_READ_LIMIT:g} s."
    elif started:
        detail = "The pairing stream broke."
    else:
        return _failure(prepared, timed_out=False)
    return client_error(prepared, ClientCode.STREAM_FAILED, detail)


class _Deadline:
    """The end of a sync call. Each wait of the call gets only the time that remains."""

    def __init__(self, seconds: float | None) -> None:
        self._end = time.monotonic() + (math.inf if seconds is None else seconds)

    def remaining(self) -> float:
        """Gives a time for `settimeout`. It is never 0, because 0 means "do not wait"."""
        seconds = self._end - time.monotonic()
        if seconds <= 0:
            raise TimeoutError("The time limit of the call passed.")
        return min(seconds, _MAX_WAIT)

    def within(self, limit: float | None) -> "_Deadline":
        """Gives the deadline of one step with its own limit: this one, or an earlier one."""
        return self if limit is None or limit >= self.remaining() else _Deadline(limit)

    def caused(self, error: Exception) -> bool:
        """Tells if the time limit is the reason. Every error after the end counts, as in Go."""
        return _is_wait(error) or time.monotonic() >= self._end


def _is_wait(error: Exception) -> bool:
    # A TimeoutError of the system has an errno. The one of a wait of this module has none.
    return isinstance(error, TimeoutError) and error.errno is None


class _Backend(NetworkBackend):
    """Makes the connection of one sync call."""

    def __init__(self, deadline: _Deadline) -> None:
        self._deadline = deadline
        self._stream: _Stream | None = None

    def connect_tcp(
        self,
        host: str,
        port: int,
        timeout: float | None = None,
        local_address: str | None = None,
        socket_options: Iterable[object] | None = None,
    ) -> NetworkStream:
        import httpcore2

        # The lookup has no time limit. After a slow one, the first connect ends the call.
        addresses = socket.getaddrinfo(host, port, type=socket.SOCK_STREAM)
        deadline = self._deadline.within(timeout)
        last_error = OSError("The name lookup gave no address.")
        try:
            for index, (family, kind, protocol, _, address) in enumerate(addresses):
                try:
                    # A silent address must leave time for the next ones. Only the last one can
                    # wait until the end, so its error is the error of the call.
                    seconds = deadline.remaining() / (len(addresses) - index)
                    self.close()
                    self._stream = _Stream(socket.socket(family, kind, protocol), self._deadline)
                    self._stream.connect(address, seconds)
                    return self._stream
                except OSError as error:
                    last_error = error
            if deadline is not self._deadline and _is_wait(last_error):
                raise httpcore2.ConnectTimeout(last_error) from last_error
            raise last_error
        finally:
            # The error holds this frame, so the frame must not hold the error.
            del last_error

    def close(self) -> None:
        if self._stream is not None:
            self._stream.close()


class _Stream(NetworkStream):
    """The connection of one sync call."""

    def __init__(self, sock: socket.socket, deadline: _Deadline) -> None:
        self._socket = sock
        self._deadline = deadline

    def connect(self, address: Any, seconds: float) -> None:
        self._socket.settimeout(seconds)
        self._socket.connect(address)
        # The header and the body are two writes. The second one must not wait for the peer.
        self._socket.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)

    def start_tls(
        self,
        ssl_context: ssl.SSLContext,
        server_hostname: str | None = None,
        timeout: float | None = None,
    ) -> NetworkStream:
        import httpcore2

        # The limit covers the whole handshake, and not each read of it.
        with self._wait(timeout, httpcore2.ConnectTimeout):
            self._socket = ssl_context.wrap_socket(self._socket, server_hostname=server_hostname)
        return self

    def read(self, max_bytes: int, timeout: float | None = None) -> bytes:
        import httpcore2

        with self._wait(timeout, httpcore2.ReadTimeout):
            return self._socket.recv(max_bytes)

    def write(self, buffer: bytes, timeout: float | None = None) -> None:
        import httpcore2

        view = memoryview(buffer)
        while view:
            with self._wait(timeout, httpcore2.WriteTimeout):
                try:
                    sent = self._socket.send(view[:_WRITE_SIZE])
                except TimeoutError:
                    raise
                except OSError as error:
                    # After a WriteError, httpcore2 reads the answer. A server can answer and close
                    # before it has the whole request.
                    raise httpcore2.WriteError(error) from error
            view = view[sent:]

    @contextmanager
    def _wait(self, limit: float | None, error_type: type[Exception]) -> Generator[None]:
        """Sets the time of one wait. `error_type` is for a wait that its own limit ended."""
        deadline = self._deadline.within(limit)
        self._socket.settimeout(deadline.remaining())
        try:
            yield
        except TimeoutError as error:
            if deadline is not self._deadline and _is_wait(error):
                raise error_type(error) from error
            raise

    def close(self) -> None:
        self._socket.close()

    def get_extra_info(self, info: str) -> Any:
        return None


class _AsyncBackend(AsyncNetworkBackend):
    """Makes the connection of one async call, and closes it.

    httpcore2 does not close it when a cancellation stops the TLS handshake.
    """

    def __init__(self) -> None:
        self._stream: AsyncNetworkStream | None = None

    async def connect_tcp(
        self,
        host: str,
        port: int,
        timeout: float | None = None,  # noqa: ASYNC109 - a method of httpcore2
        local_address: str | None = None,
        socket_options: Iterable[object] | None = None,
    ) -> AsyncNetworkStream:
        import httpcore2

        # The cast is for pyright, because httpcore2 defines the class twice.
        backend = cast(AsyncNetworkBackend, httpcore2.AnyIOBackend())
        self._stream = await backend.connect_tcp(host, port, timeout=timeout)
        return self._stream

    async def aclose(self) -> None:
        if self._stream is not None:
            await self._stream.aclose()
