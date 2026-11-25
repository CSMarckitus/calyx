from collections.abc import Iterator
from dataclasses import dataclass, field
from typing import Final

from ._call import Prepared, client_error
from ._fields import NotJsonError, parse_json
from ._generated.codes import ClientCode
from .errors import CalyxError

_MAX_EVENT: Final = 1024 * 1024
_KNOWN_EVENTS: Final = frozenset({"update", "done", "failed"})


@dataclass(frozen=True, slots=True, kw_only=True)
class SseEvent:
    """One event of a pairing stream."""

    name: str
    """update, done or failed."""

    data: object = field(repr=False)
    """The JSON of the event."""


class EventParser:
    """Reads the events of a pairing stream (Server-Sent Events). It does no I/O."""

    def __init__(self, prepared: Prepared[object]) -> None:
        self._prepared = prepared
        # The start of a line, from the last chunk.
        self._line = bytearray()
        # True when the last line ended with CR. An LF that follows belongs to it.
        self._after_cr = False
        # The bytes of the event now, with the ends of the lines and the comments.
        self._size = 0
        self._name = b""
        self._data: list[bytes] = []

    def feed(self, chunk: bytes) -> Iterator[SseEvent]:
        """Gives the events that the chunk completes. The known events only."""
        # A line of bytes ends with CR, LF or CRLF only, as in Server-Sent Events.
        for part in chunk.splitlines(keepends=True):
            self._size += len(part)
            # A check for each part, so a line or an event with no end cannot fill the memory.
            if self._size > _MAX_EVENT:
                raise self._error("The backend sent an event that is too large.")
            if part == b"\n" and self._after_cr:
                self._after_cr = False
                continue
            self._after_cr = part.endswith(b"\r")
            self._line += part.rstrip(b"\r\n")
            if part.endswith((b"\r", b"\n")):
                line = bytes(self._line)
                self._line.clear()
                event = self._end_line(line)
                if event is not None:
                    yield event

    def early_end(self) -> CalyxError:
        """Gives the error of a stream that ended."""
        return client_error(
            self._prepared,
            ClientCode.STREAM_FAILED,
            "The pairing stream ended before done or failed.",
        )

    def _end_line(self, line: bytes) -> SseEvent | None:
        if line == b"":
            return self._end_event()
        # A comment, for example a ping, has an empty name.
        name, _, value = line.partition(b":")
        value = value.removeprefix(b" ")
        if name == b"event":
            self._name = value
        elif name == b"data":
            self._data.append(value)
        return None

    def _end_event(self) -> SseEvent | None:
        name, data = self._name.decode(errors="replace"), self._data
        self._name, self._data, self._size = b"", [], 0
        if name not in _KNOWN_EVENTS or not data:
            return None
        try:
            return SseEvent(name=name, data=parse_json(b"\n".join(data)))
        except NotJsonError as error:
            raise self._error(
                f'The backend sent a "{name}" event with data that is not JSON.'
            ) from error

    def _error(self, detail: str) -> CalyxError:
        return client_error(self._prepared, ClientCode.INVALID_RESPONSE, detail)
