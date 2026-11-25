"""The gRPC channels. Only this module makes an object of gRPC, at the first call of a client."""

# The types of gRPC use a name that the standard library of pyright does not have.
# pyright: reportUnknownMemberType=false

import asyncio
import os
import re
import ssl
import threading
import time
from collections.abc import Callable, Iterable
from contextlib import suppress
from dataclasses import dataclass, field
from pathlib import Path
from typing import TYPE_CHECKING, Final

import grpc
import grpc.aio
from calyx_client_sdk import CalyxError
from calyx_client_sdk._checks import check_time_limit, config_error, is_visible_ascii

from ._errors import closed_error, core_error, timeout_error
from ._version import __version__

_OPTIONS: Final = (
    # The SDK never sends a call again.
    ("grpc.enable_retries", 0),
    # A proxy would get the token. The core is on a private network.
    ("grpc.enable_http_proxy", 0),
    ("grpc.primary_user_agent", f"calyx-python-sdk/{__version__}"),
    # The pings find a core host that disappears. The core refuses pings more often than once a
    # minute, and pings while no call runs.
    ("grpc.keepalive_time_ms", 2 * 60_000),
    ("grpc.keepalive_permit_without_calls", 0),
    ("grpc.keepalive_timeout_ms", 20_000),
    # gRPC does not use the timeout above for its pings (seen in 1.80 to 1.85). It uses this one.
    ("grpc.http2.ping_timeout_ms", 20_000),
    # Without this, gRPC limits the pings after two pings with no data.
    ("grpc.http2.max_pings_without_data", 0),
)

if TYPE_CHECKING:
    # These two are private names of the type stubs.
    from grpc import _CallFuture, _CallIterator  # pyright: ignore[reportPrivateUsage]

type SyncCall = "_CallIterator[bytes]"
type AsyncCall = grpc.aio.UnaryStreamCall[bytes, bytes]

# gRPC works after a fork only when no gRPC object existed before it.
_first_process: int | None = None


@dataclass(frozen=True, slots=True, kw_only=True)
class Config:
    """The checked arguments of a client. `make_config` makes it."""

    address: str
    token: str = field(repr=False)
    insecure: bool
    ca_file: str | None
    time_limit: float | None

    @property
    def target(self) -> str:
        # Without "dns:", gRPC reads a host with the name of one of its schemes as a scheme.
        return f"dns:///{self.address}"

    @property
    def metadata(self) -> tuple[tuple[str, str], ...]:
        # Metadata, not call credentials: call credentials refuse a channel without TLS.
        return (("authorization", f"Bearer {self.token}"),)


def make_config(
    address: str, token: str, insecure: bool, ca_file: str | None, time_limit: float | None
) -> Config:
    """Checks the arguments of a client. An error never holds the token."""
    if not _is_address(address):
        raise config_error(
            "The address must be a host and a port with no scheme, for example 127.0.0.1:50051 or "
            "[::1]:50051."
        )
    if not token or not is_visible_ascii(token):
        raise config_error(
            "The token must be a string of printable ASCII characters that is not empty and has no "
            "space."
        )
    if not _is_bool(insecure):
        raise config_error("The value of insecure must be a bool.")
    if insecure and ca_file is not None:
        raise config_error("With insecure=True there is no TLS, so ca_file is not possible.")
    return Config(
        address=address,
        token=token,
        insecure=insecure,
        ca_file=ca_file,
        time_limit=None if time_limit is None else check_time_limit(time_limit),
    )


@dataclass(frozen=True, slots=True, kw_only=True)
class Result:
    """The end of one call. An error has this and not the call of gRPC: a program that keeps the
    call also keeps the connection, after close() too."""

    status: grpc.StatusCode
    data: bytes | None
    detail: str
    trailers: Iterable[tuple[str, str | bytes]]
    timed_out: bool


def is_forked() -> bool:
    return _first_process is not None and _first_process != os.getpid()


def _use_process(key: str | None) -> None:
    """Comes before a lock: a thread can have it at the fork, and the copy of it never opens."""
    global _first_process
    if is_forked():
        raise config_error("Make no core call before the fork.", key)
    _first_process = os.getpid()


class SyncChannel:
    """The channel of a `CoreClient`. Threads can use it together."""

    def __init__(self, config: Config) -> None:
        self._config = config
        # A signal handler can call close() while its own thread has the lock.
        self._lock = threading.RLock()
        self._channel: grpc.Channel | None = None
        self._closed = False

    def unary(
        self, path: str, body: bytes, key: str | None, time_limit: float | None
    ) -> bytes | None:
        """Makes one call and gives the bytes of the answer. None: an answer with no message."""
        result = self._exchange(path, body, key, time_limit)
        return finish(result, key, closed=self._closed)

    def _exchange(
        self, path: str, body: bytes, key: str | None, time_limit: float | None
    ) -> Result:
        def make(channel: grpc.Channel) -> "_CallFuture[bytes | None]":
            method: grpc.UnaryUnaryMultiCallable[bytes, bytes | None] = channel.unary_unary(path)
            return method.future(body, timeout=time_limit, metadata=self._config.metadata)

        call, start = self._start(key, make)
        try:
            # Not result(): it raises the error of gRPC.
            failed = call.exception() is not None
        finally:
            # It does nothing to a call that ended. After a KeyboardInterrupt, the call still runs.
            call.cancel()
        status = call.code()
        return Result(
            status=status,
            data=None if failed else call.result(),
            detail=call.details(),
            trailers=call.trailing_metadata(),
            timed_out=timed_out(status, start, time_limit),
        )

    def stream(self, path: str, body: bytes, time_limit: float | None) -> tuple[SyncCall, float]:
        """Starts the call of a stream. Gives the call and the moment of its start."""

        def make(channel: grpc.Channel) -> SyncCall:
            method: grpc.UnaryStreamMultiCallable[bytes, bytes] = channel.unary_stream(path)
            return method(body, timeout=time_limit, metadata=self._config.metadata)

        return self._start(None, make)

    def check(self, key: str | None) -> None:
        _use_process(key)
        if self._closed:
            raise closed_error(key)

    @property
    def closed(self) -> bool:
        return self._closed

    def _start[C](self, key: str | None, make: Callable[[grpc.Channel], C]) -> tuple[C, float]:
        self.check(key)
        # close() cannot come between the check and the start of the call.
        with self._lock:
            channel = self._open(key)
            start = time.monotonic()
            try:
                return make(channel), start
            except ValueError:
                # The channel is closed: a signal handler called close() inside the lock.
                if not self._closed:
                    raise
        raise closed_error(key)

    def _open(self, key: str | None) -> grpc.Channel:
        if self._closed:
            raise closed_error(key)
        if self._channel is None:
            config = self._config
            if config.insecure:
                self._channel = grpc.insecure_channel(config.target, _OPTIONS)
            else:
                credentials = _make_credentials(config, key)
                self._channel = grpc.secure_channel(config.target, credentials, _OPTIONS)
            # A signal handler called close() before the client had the channel.
            if self._closed:
                self._channel.close()
                raise closed_error(key)
        return self._channel

    def close(self) -> None:
        # The connection is the one of the other process.
        if is_forked():
            return
        with self._lock:
            self._closed = True
        if self._channel is not None:
            self._channel.close()


class AsyncChannel:
    """The channel of an `AsyncCoreClient`. The tasks of one event loop can use it together."""

    def __init__(self, config: Config) -> None:
        self._config = config
        self._channel: grpc.aio.Channel | None = None
        # The event loop of the first call. The objects of gRPC belong to it.
        self._loop: asyncio.AbstractEventLoop | None = None
        self._streams: set[AsyncCall] = set()
        self._closed = False

    async def unary(
        self, path: str, body: bytes, key: str | None, time_limit: float | None
    ) -> bytes | None:
        """Makes one call and gives the bytes of the answer. None: an answer with no message."""
        result = await self._exchange(path, body, key, time_limit)
        return finish(result, key, closed=self._closed)

    async def _exchange(
        self, path: str, body: bytes, key: str | None, time_limit: float | None
    ) -> Result:
        method: grpc.aio.UnaryUnaryMultiCallable[bytes, bytes | None]
        method = self._open(key).unary_unary(path)
        start = time.monotonic()
        call = method(body, timeout=time_limit, metadata=self._config.metadata)
        try:
            # Not "await call": after close(), it gives a CancelledError, as a cancelled task does.
            status = await call.code()
        finally:
            call.cancel()
        return Result(
            status=status,
            data=await call if status is grpc.StatusCode.OK else None,
            detail=await call.details(),
            trailers=await call.trailing_metadata(),
            timed_out=timed_out(status, start, time_limit),
        )

    def stream(self, path: str, body: bytes, time_limit: float | None) -> tuple[AsyncCall, float]:
        """Starts the call of a stream. Gives the call and the moment of its start."""
        method: grpc.aio.UnaryStreamMultiCallable[bytes, bytes]
        method = self._open(None).unary_stream(path)
        start = time.monotonic()
        call = method(body, timeout=time_limit, metadata=self._config.metadata)
        self._streams.add(call)
        return call, start

    def check(self, key: str | None) -> None:
        _use_process(key)
        if self._closed:
            raise closed_error(key)
        if self._loop is not None and self._loop is not asyncio.get_running_loop():
            raise config_error("Use an AsyncCoreClient in one event loop only.", key)

    @property
    def closed(self) -> bool:
        return self._closed

    def _open(self, key: str | None) -> grpc.aio.Channel:
        self.check(key)
        if self._channel is None:
            config = self._config
            # This reads the certificates in the event loop. It takes about 5 ms, one time.
            if config.insecure:
                self._channel = grpc.aio.insecure_channel(config.target, _OPTIONS)
            else:
                credentials = _make_credentials(config, key)
                self._channel = grpc.aio.secure_channel(config.target, credentials, _OPTIONS)
            self._loop = asyncio.get_running_loop()
        return self._channel

    def stop(self, call: AsyncCall) -> None:
        """Cancels the call of a stream, in its own event loop."""
        self._streams.discard(call)
        if loop := self._get_other_loop():
            # The loop can close after the check.
            with suppress(RuntimeError):
                loop.call_soon_threadsafe(call.cancel)
                return
        call.cancel()

    async def close(self) -> None:
        # The connection is the one of the other process.
        if is_forked():
            return
        self._closed = True
        if self._channel is None:
            return
        step = self._close(self._channel)
        if loop := self._get_other_loop():
            # The loop can close after the check.
            with suppress(RuntimeError):
                asyncio.run_coroutine_threadsafe(step, loop)
                return
        await step

    async def _close(self, channel: grpc.aio.Channel) -> None:
        # Not every version of gRPC does it.
        for call in tuple(self._streams):
            call.cancel()
        await channel.close()

    def _get_other_loop(self) -> asyncio.AbstractEventLoop | None:
        """Gives the event loop of the first call when it runs in another thread."""
        loop = self._loop
        if loop is None or not loop.is_running():
            return None
        # The garbage collection can end a stream in a thread with no event loop.
        with suppress(RuntimeError):
            if loop is asyncio.get_running_loop():
                return None
        return loop


def timed_out(status: grpc.StatusCode, start: float, time_limit: float | None) -> bool:
    # The core can send this status too, but earlier. gRPC never ends a call before its limit.
    return (
        status is grpc.StatusCode.DEADLINE_EXCEEDED
        and time_limit is not None
        and time.monotonic() - start >= time_limit
    )


def finish(result: Result, key: str | None, *, closed: bool) -> bytes | None:
    if result.status is grpc.StatusCode.OK:
        return result.data
    if result.status is grpc.StatusCode.CANCELLED and closed:
        raise closed_error(key)
    if result.timed_out:
        raise limit_error(key)
    raise core_error(result.status, result.detail, result.trailers, key)


def limit_error(key: str | None) -> CalyxError:
    error = timeout_error(key)
    error.__cause__ = TimeoutError("The time limit of the gRPC call passed.")
    return error


def _make_credentials(config: Config, key: str | None) -> grpc.ChannelCredentials:
    """Trusts the system certificates and `ca_file`. gRPC alone would use its own list."""
    certificates: list[bytes] = []
    try:
        context = ssl.create_default_context()
        if config.ca_file is not None:
            context.load_verify_locations(config.ca_file)
            # First, because gRPC stops at a certificate that it cannot read.
            certificates.append(Path(config.ca_file).read_bytes())
    # ValueError and TypeError: a value that is no path.
    except (OSError, ValueError, TypeError) as error:
        raise _certificates_error(key) from error
    _load_system_folders(context)
    for certificate in context.get_ca_certs(binary_form=True):
        certificates.append(ssl.DER_cert_to_PEM_cert(certificate).encode())
    # With no certificate, gRPC would use its own list.
    if not certificates:
        raise _certificates_error(key)
    return grpc.ssl_channel_credentials(b"\n".join(certificates))


def _load_system_folders(context: ssl.SSLContext) -> None:
    """get_ca_certs() does not give the certificates of a folder before a connection uses them."""
    paths = ssl.get_default_verify_paths()
    # Not `capath`: it is None when the variable has more folders than one.
    folders = os.environ.get(paths.openssl_capath_env, paths.openssl_capath)
    for folder in filter(None, folders.split(os.pathsep)):
        # OpenSSL reads only the files with the name of a hash. It ignores what it cannot read.
        with suppress(OSError):
            for file in Path(folder).glob("[0-9a-f]" * 8 + ".[0-9]*"):
                with suppress(OSError):
                    context.load_verify_locations(file)


def _certificates_error(key: str | None) -> CalyxError:
    return config_error(
        "The TLS certificates cannot be loaded. Check ca_file. Without ca_file, check the "
        "system certificates.",
        key,
    )


# These take `object`, because a program without a type checker can pass any value.
def _is_address(value: object) -> bool:
    if not isinstance(value, str):
        return False
    match = re.fullmatch(r"(?:[A-Za-z0-9._-]+|\[[0-9A-Fa-f:.]+\]):([0-9]{1,5})", value)
    return match is not None and 1 <= int(match[1]) <= 65535


def _is_bool(value: object) -> bool:
    return isinstance(value, bool)
