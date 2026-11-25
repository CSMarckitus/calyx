"""The streams of the core. `create_instance` and `subscribe` of the clients give them."""

# The types of gRPC use a name that the standard library of pyright does not have.
# pyright: reportUnknownMemberType=false

import asyncio
import threading
import time
from types import TracebackType
from typing import Self

import grpc
from calyx_client_sdk import ClientCode, Event
from calyx_client_sdk._checks import config_error
from calyx_client_sdk.events import event_error, parse_event_json, read_event

from ._base import StreamPlan
from ._channel import (
    AsyncCall,
    AsyncChannel,
    Result,
    SyncCall,
    SyncChannel,
    finish,
    is_forked,
    limit_error,
    timed_out,
)
from ._errors import client_error
from ._generated.models import CreateInstanceResponse
from ._proto.calyx.core.v1 import event_pb2
from ._values import utc_text


class _SyncStream[T]:
    def __init__(self, channel: SyncChannel, plan: StreamPlan[T]) -> None:
        """The SDK makes this object. Use `create_instance` or `subscribe` of `CoreClient`."""
        self._channel = channel
        self._plan = plan
        self._start: float | None = None
        self._call: SyncCall | None = None
        self._ended = False
        self._reading = threading.Lock()

    def __enter__(self) -> Self:
        """Open nothing: the call starts at the first read."""
        return self

    def __exit__(
        self,
        kind: type[BaseException] | None,
        error: BaseException | None,
        trace: TracebackType | None,
    ) -> None:
        """Stop the call."""
        self._stop()

    def __iter__(self) -> Self:
        """Give the stream itself."""
        return self

    def __next__(self) -> T:
        """Wait for the next item."""
        # gRPC breaks the whole client when two reads of one call run together.
        if not self._reading.acquire(blocking=False):
            if self._ended:
                raise StopIteration
            self._channel.check(None)
            raise config_error("Read a stream in one thread at a time.")
        try:
            item = None if self._ended else self._read()
        finally:
            self._reading.release()
        if item is None:
            raise StopIteration
        return item

    def _read(self) -> T | None:
        item = None
        try:
            data = self._receive()
            if data is not None:
                item = self._plan.read(data)
        finally:
            if item is None:
                self._stop()
        return item

    def _receive(self) -> bytes | None:
        self._channel.check(None)
        plan = self._plan
        if self._start is None:
            self._call, self._start = self._channel.stream(plan.path, plan.body, plan.time_limit)
        _check_limit(self._start, plan.time_limit)
        result = self._wait(self._start)
        # Another thread or a signal handler can end the stream during the wait.
        if self._ended or result is None:
            return None
        if isinstance(result, Result):
            _finish(result, closed=self._channel.closed, endless=plan.endless)
            return None
        return result

    def _wait(self, start: float) -> bytes | Result | None:
        """No error has this frame, because it has the call."""
        call = self._call
        if call is None:
            return None
        # The stream ended before `_stop` could see the call.
        if self._ended:
            call.cancel()
        try:
            data = next(call, None)
        except grpc.RpcError as error:
            data = None
            # The error is the call itself, and its traceback has the call.
            error.__traceback__ = None
        if data is not None:
            return data
        status = call.code()
        return Result(
            status=status,
            data=None,
            detail=call.details(),
            trailers=call.trailing_metadata(),
            timed_out=timed_out(status, start, self._plan.time_limit),
        )

    def _stop(self) -> None:
        self._ended = True
        # The connection is the one of the parent process. gRPC uses it when it drops the call.
        if is_forked():
            return
        call, self._call = self._call, None
        if call is not None:
            call.cancel()


class _AsyncStream[T]:
    def __init__(self, channel: AsyncChannel, plan: StreamPlan[T]) -> None:
        """The SDK makes this object. Use `create_instance` or `subscribe` of `AsyncCoreClient`."""
        self._channel = channel
        self._plan = plan
        self._start: float | None = None
        self._call: AsyncCall | None = None
        self._ended = False
        self._reading = threading.Lock()

    async def __aenter__(self) -> Self:
        """Open nothing: the call starts at the first read."""
        return self

    async def __aexit__(
        self,
        kind: type[BaseException] | None,
        error: BaseException | None,
        trace: TracebackType | None,
    ) -> None:
        """Stop the call."""
        self._stop()

    def __aiter__(self) -> Self:
        """Give the stream itself."""
        return self

    async def __anext__(self) -> T:
        """Wait for the next item."""
        # The same rule as in the sync stream.
        if not self._reading.acquire(blocking=False):
            if self._ended:
                raise StopAsyncIteration
            self._channel.check(None)
            raise config_error("Read a stream in one task at a time.")
        try:
            item = None if self._ended else await self._read()
        finally:
            self._reading.release()
        if item is None:
            raise StopAsyncIteration
        return item

    async def _read(self) -> T | None:
        item = None
        try:
            data = await self._receive()
            if data is not None:
                item = self._plan.read(data)
        finally:
            if item is None:
                self._stop()
        return item

    async def _receive(self) -> bytes | None:
        self._channel.check(None)
        plan = self._plan
        if self._start is None:
            self._call, self._start = self._channel.stream(plan.path, plan.body, plan.time_limit)
        _check_limit(self._start, plan.time_limit)
        result = await self._wait(self._start)
        # Another task can end the stream during the wait.
        if self._ended or result is None:
            return None
        if isinstance(result, Result):
            _finish(result, closed=self._channel.closed, endless=plan.endless)
            return None
        return result

    async def _wait(self, start: float) -> bytes | Result | None:
        """No error has this frame, because it has the call."""
        call = self._call
        if call is None:
            return None
        # A task of its own: after close(), gRPC gives a CancelledError, as a cancelled task does.
        # When this wait is cancelled, `_stop` ends the call, and so the task.
        reader = asyncio.create_task(_read_call(call))
        await asyncio.wait({reader})
        data = None if reader.cancelled() else reader.result()
        if data is not None:
            return data
        status = await call.code()
        return Result(
            status=status,
            data=None,
            detail=await call.details(),
            trailers=await call.trailing_metadata(),
            timed_out=timed_out(status, start, self._plan.time_limit),
        )

    def _stop(self) -> None:
        self._ended = True
        # The connection is the one of the parent process. gRPC uses it when it drops the call.
        if is_forked():
            return
        call, self._call = self._call, None
        if call is not None:
            self._channel.stop(call)

    def __del__(self) -> None:
        # gRPC itself keeps an async call that nobody uses, until the end of the client.
        if self._call is not None:
            self._stop()


async def _read_call(call: AsyncCall) -> bytes | None:
    try:
        data = await call.read()
    except grpc.RpcError:
        return None
    # Or the EOF of gRPC.
    return data if isinstance(data, bytes) else None


def _check_limit(start: float, time_limit: float | None) -> None:
    # gRPC can still give the items that it has.
    if time_limit is not None and time.monotonic() - start >= time_limit:
        raise limit_error(None)


def _finish(result: Result, *, closed: bool, endless: bool) -> None:
    finish(result, None, closed=closed)
    if endless:
        raise client_error(ClientCode.STREAM_FAILED, "The core ended the event stream.", None)


class InstanceStream(_SyncStream[CreateInstanceResponse]):
    """The updates of a new instance or of a new pairing. `CoreClient.create_instance` gives it.

    Example:

        with client.create_instance("sales", provider=provider) as stream:
            for update in stream:
                if update.pairing_code:
                    show(update.pairing_code)

    Read it in a `with` block: the end of the block stops the call, and the core then stops the
    pairing. The call starts at the first read. The stream ends after the update CONNECTED or an
    error, and a read after the end gives no item. Use one stream in one thread at a time.
    """


class AsyncInstanceStream(_AsyncStream[CreateInstanceResponse]):
    """The updates of a new instance or of a new pairing. `AsyncCoreClient.create_instance` gives
    it.

    Example:

        async with client.create_instance("sales", provider=provider) as stream:
            async for update in stream:
                if update.pairing_code:
                    show(update.pairing_code)

    Read it in an `async with` block: the end of the block stops the call, and the core then stops
    the pairing. The call starts at the first read. The stream ends after the update CONNECTED or an
    error, and a read after the end gives no item. Use one stream in one task at a time. A cancelled
    read, for example by `asyncio.timeout`, ends the stream too.
    """


class EventStream(_SyncStream[Event]):
    """The events of the instances. `CoreClient.subscribe` gives it.

    Example:

        with client.subscribe() as stream:
            for event in stream:
                match event:
                    case MessageReceivedEvent(data=message):
                        print(message.from_, message.text)

    Read it in a `with` block: the end of the block stops the call. The call starts at the first
    read. Only an error ends the stream, and a read after the end gives no item. Use one stream in
    one thread at a time.
    """


class AsyncEventStream(_AsyncStream[Event]):
    """The events of the instances. `AsyncCoreClient.subscribe` gives it.

    Example:

        async with client.subscribe() as stream:
            async for event in stream:
                match event:
                    case MessageReceivedEvent(data=message):
                        print(message.from_, message.text)

    Read it in an `async with` block: the end of the block stops the call. The call starts at the
    first read. Only an error ends the stream, and a read after the end gives no item. Use one
    stream in one task at a time. A cancelled read, for example by `asyncio.timeout`, ends the
    stream too.
    """


def read_core_event(event: event_pb2.Event) -> Event:
    """Converts an event of the core to the one of a webhook (CONTRACT.md part 10, rule 5)."""
    timestamp = utc_text(event.occurred_at) if event.HasField("occurred_at") else None
    if timestamp is None:
        raise event_error(ClientCode.INVALID_EVENT, "The event has no valid time.")
    return read_event(
        {
            "id": event.id,
            "event": event.type,
            "instance": event.instance_name,
            "timestamp": timestamp,
            "data": parse_event_json(event.payload_json.encode()),
        }
    )
