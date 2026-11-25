from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Final

from calyx_client_sdk import ClientCode, RetryAdvice, Sent
from calyx_client_sdk._checks import check_time_limit, config_error
from calyx_client_sdk.idempotency import resolve_idempotency_key
from google.protobuf.message import DecodeError, Message

from ._channel import AsyncChannel, Config, SyncChannel
from ._errors import client_error
from ._values import WrongArgumentError, WrongShapeError

# 3 years. gRPC ends a call at once when its limit is much larger.
MAX_LIMIT: Final = 100_000_000.0


@dataclass(frozen=True, slots=True, kw_only=True)
class Rpc[A: Message]:
    """One RPC of the core. `A` is the protobuf class of its answer."""

    path: str

    parse: Callable[[bytes], A]

    time_limit: float | None
    """The default time limit in seconds. None for a stream: it has no default limit."""


@dataclass(frozen=True, slots=True, kw_only=True)
class StreamPlan[T]:
    """The call of one stream, before it starts. `T` is the class of its items."""

    path: str

    body: bytes = field(repr=False)

    time_limit: float | None
    """The time limit in seconds of the whole stream."""

    read: Callable[[bytes], T]

    endless: bool
    """The status OK is then an error: the core never ends this stream."""


class SyncCoreBase:
    """The generated methods of `CoreClient` use it."""

    def __init__(self, config: Config) -> None:
        self._config = config
        self._channel = SyncChannel(config)

    def _call[A: Message, T](
        self,
        rpc: Rpc[A],
        request: Callable[[], Message],
        read: Callable[[A], T],
        *,
        time_limit: float | None,
        idempotency_key: str | None = None,
    ) -> T:
        body = make_body(request, idempotency_key)
        limit = choose_limit(self._config, rpc, time_limit, idempotency_key)
        data = self._channel.unary(rpc.path, body, idempotency_key, limit)
        return read_answer(rpc, read, data, idempotency_key)

    def _send[A: Message, T](
        self,
        rpc: Rpc[A],
        request: Callable[[str], Message],
        read: Callable[[A], T],
        *,
        idempotency_key: str | None,
        time_limit: float | None,
    ) -> Sent[T]:
        # The key is first, so each later error of the call has it.
        key = resolve_idempotency_key(idempotency_key)
        data = self._call(
            rpc, lambda: request(key), read, time_limit=time_limit, idempotency_key=key
        )
        return Sent(data=data, idempotency_key=key)


class AsyncCoreBase:
    """The generated methods of `AsyncCoreClient` use it."""

    def __init__(self, config: Config) -> None:
        self._config = config
        self._channel = AsyncChannel(config)

    async def _call[A: Message, T](
        self,
        rpc: Rpc[A],
        request: Callable[[], Message],
        read: Callable[[A], T],
        *,
        time_limit: float | None,
        idempotency_key: str | None = None,
    ) -> T:
        body = make_body(request, idempotency_key)
        limit = choose_limit(self._config, rpc, time_limit, idempotency_key)
        data = await self._channel.unary(rpc.path, body, idempotency_key, limit)
        return read_answer(rpc, read, data, idempotency_key)

    async def _send[A: Message, T](
        self,
        rpc: Rpc[A],
        request: Callable[[str], Message],
        read: Callable[[A], T],
        *,
        idempotency_key: str | None,
        time_limit: float | None,
    ) -> Sent[T]:
        key = resolve_idempotency_key(idempotency_key)
        data = await self._call(
            rpc, lambda: request(key), read, time_limit=time_limit, idempotency_key=key
        )
        return Sent(data=data, idempotency_key=key)


def plan_stream[A: Message, T](
    config: Config,
    rpc: Rpc[A],
    request: Callable[[], Message],
    read: Callable[[A], T],
    *,
    time_limit: float | None,
    endless: bool,
) -> StreamPlan[T]:
    """Checks the arguments of a stream. It sends nothing."""
    return StreamPlan(
        path=rpc.path,
        body=make_body(request, None),
        time_limit=choose_limit(config, rpc, time_limit, None),
        read=lambda data: read_answer(rpc, read, data, None),
        endless=endless,
    )


def make_body(request: Callable[[], Message], key: str | None) -> bytes:
    try:
        return request().SerializeToString()
    except WrongArgumentError as error:
        raise config_error(str(error), key) from error


def choose_limit[A: Message](
    config: Config, rpc: Rpc[A], time_limit: float | None, key: str | None
) -> float | None:
    if time_limit is not None:
        limit = check_time_limit(time_limit, key)
    elif rpc.time_limit is None:
        # A short limit of the client must not stop a stream.
        return None
    else:
        limit = config.time_limit or rpc.time_limit
    return min(limit, MAX_LIMIT)


def read_answer[A: Message, T](
    rpc: Rpc[A], read: Callable[[A], T], data: bytes | None, key: str | None
) -> T:
    """Reads the bytes here, not in gRPC. gRPC would give the status INTERNAL for a bad answer."""
    message = None
    reason = "The answer has no message."
    if data is not None:
        try:
            message = rpc.parse(data)
        # The pure Python version of protobuf gives a UnicodeDecodeError.
        except (DecodeError, UnicodeDecodeError) as error:
            reason = str(error)
    if message is None:
        # Outside the "except": the error of Python can hold the bytes, and it would be the context.
        raise client_error(
            ClientCode.INVALID_RESPONSE,
            "The core answered with bytes that are not the expected message.",
            key,
        ) from ValueError(reason)
    try:
        return read(message)
    except WrongShapeError as error:
        # A repeated call gets the same shape, so "retry now" would be an endless loop.
        raise client_error(
            ClientCode.INVALID_RESPONSE,
            f"The core answered with a message of the wrong shape. {error}",
            key,
            retry=RetryAdvice.UNKNOWN,
        ) from error
