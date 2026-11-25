"""The pairing stream: `follow_pairing` of the clients gives it."""

from dataclasses import dataclass
from functools import partial
from types import TracebackType
from typing import Final, Self

from ._call import Operation, Prepared, client_error, read_nothing
from ._fields import WrongShapeError, read
from ._generated.codes import ClientCode
from ._generated.models import Pairing
from ._http import AsyncTransport, SyncTransport
from ._sse import SseEvent
from .errors import RetryAdvice

FOLLOW_PAIRING: Final = Operation(
    id="followPairing",
    method="GET",
    path="/api/v1/instances/{id}/pairing",
    time_limit=None,
    read=read_nothing,
)


@dataclass(frozen=True, slots=True, kw_only=True)
class PairingUpdate:
    """The pairing has a new state, for example a new QR code. More items follow."""

    pairing: Pairing
    """The state of the pairing now: the status, the QR text, the pairing code and a message. In a
    QR pairing, use the newest one, because the QR code changes. Keep the QR text and the pairing
    code secret: with one of them, a person can link the WhatsApp account."""


@dataclass(frozen=True, slots=True, kw_only=True)
class PairingDone:
    """The pairing is finished and the instance is connected. It is the last item."""

    status: str
    """Always CONNECTED."""


@dataclass(frozen=True, slots=True, kw_only=True)
class PairingFailed:
    """The pairing ended and the instance is not connected. It is the last item."""

    code: str
    """pairing_expired (not finished in time), pairing_failed or pairing_cancelled. The class
    `PairingFailureCode` has the known values."""

    detail: str
    """A sentence for people that says why the pairing ended."""


class PairingStream:
    """The items of one pairing. `BackendClient.follow_pairing` gives it.

    Example:

        with client.follow_pairing(instance_id) as stream:
            for item in stream:
                if isinstance(item, PairingUpdate):
                    show(item.pairing.qr_text)

    Read it in a `with` block: the end of the block closes the connection. The connection opens at
    the first read. The stream ends after `PairingDone`, `PairingFailed` or an error, and a read
    after the end gives no item. Use one stream in one thread at a time.
    """

    def __init__(self, transport: SyncTransport, prepared: Prepared[object]) -> None:
        """The SDK makes this object. Use `BackendClient.follow_pairing`."""
        self._items = transport.follow(prepared, partial(_read_item, prepared))

    def __enter__(self) -> Self:
        """Open nothing: the connection opens at the first read."""
        return self

    def __exit__(
        self,
        kind: type[BaseException] | None,
        error: BaseException | None,
        trace: TracebackType | None,
    ) -> None:
        """Close the connection. The pairing goes on until `cancel_pairing`."""
        self._items.close()

    def __iter__(self) -> Self:
        """Give the stream itself."""
        return self

    def __next__(self) -> PairingUpdate | PairingDone | PairingFailed:
        """Wait for the next item."""
        item = next(self._items)
        if not isinstance(item, PairingUpdate):
            self._items.close()
        return item


class AsyncPairingStream:
    """The items of one pairing. `AsyncBackendClient.follow_pairing` gives it.

    Example:

        async with client.follow_pairing(instance_id) as stream:
            async for item in stream:
                if isinstance(item, PairingUpdate):
                    show(item.pairing.qr_text)

    Read it in an `async with` block: the end of the block closes the connection. The connection
    opens at the first read. The stream ends after `PairingDone`, `PairingFailed` or an error, and
    a read after the end gives no item. Use one stream in one task at a time.
    A cancelled read, for example by `asyncio.timeout`, ends the stream too. Follow the instance
    again.
    """

    def __init__(self, transport: AsyncTransport, prepared: Prepared[object]) -> None:
        """The SDK makes this object. Use `AsyncBackendClient.follow_pairing`."""
        self._items = transport.follow(prepared, partial(_read_item, prepared))

    async def __aenter__(self) -> Self:
        """Open nothing: the connection opens at the first read."""
        return self

    async def __aexit__(
        self,
        kind: type[BaseException] | None,
        error: BaseException | None,
        trace: TracebackType | None,
    ) -> None:
        """Close the connection. The pairing goes on until `cancel_pairing`."""
        await self._items.aclose()

    def __aiter__(self) -> Self:
        """Give the stream itself."""
        return self

    async def __anext__(self) -> PairingUpdate | PairingDone | PairingFailed:
        """Wait for the next item."""
        item = await anext(self._items)
        if not isinstance(item, PairingUpdate):
            await self._items.aclose()
        return item


def _read_item(
    prepared: Prepared[object], event: SseEvent
) -> PairingUpdate | PairingDone | PairingFailed:
    try:
        match event.name:
            case "update":
                return PairingUpdate(pairing=read(Pairing, event.data))
            case "done":
                return read(PairingDone, event.data)
            case _:
                return read(PairingFailed, event.data)
    except WrongShapeError as error:
        # A new follow can get the same shape, so "retry now" would be an endless loop.
        raise client_error(
            prepared,
            ClientCode.INVALID_RESPONSE,
            f'The backend sent a "{event.name}" event with data of the wrong shape. {error}',
            retry=RetryAdvice.UNKNOWN,
        ) from error
