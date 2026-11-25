"""The parser of the pairing stream, with no network."""

import json
import re
from pathlib import Path
from typing import Any

import pytest

from calyx_client_sdk import CalyxError, ClientCode, RetryAdvice, Source
from calyx_client_sdk._call import Operation, make_config, prepare, read_nothing
from calyx_client_sdk._sse import EventParser, SseEvent

CASES: list[dict[str, Any]] = json.loads(
    (Path(__file__).parents[3] / "testdata" / "sse-cases.json").read_text()
)["cases"]
MAX_EVENT = 1024 * 1024
DONE = b'event: done\ndata: {"status":"CONNECTED"}\n\n'


def make_parser() -> EventParser:
    operation = Operation(
        id="followThing", method="GET", path="/things", time_limit=None, read=read_nothing
    )
    return EventParser(prepare(make_config("http://localhost", "cx_secret", None), operation))


def parse(chunks: list[bytes]) -> tuple[list[SseEvent], CalyxError | None]:
    """Reads like a pairing stream: it stops at done, at failed or at an error."""
    parser = make_parser()
    events: list[SseEvent] = []
    try:
        for chunk in chunks:
            for event in parser.feed(chunk):
                events.append(event)
                if event.name != "update":
                    return events, None
    except CalyxError as error:
        return events, error
    return events, parser.early_end()


@pytest.mark.parametrize("split", [False, True], ids=["chunks", "bytes"])
@pytest.mark.parametrize("case", CASES, ids=lambda case: re.sub(r"\W+", "-", case["name"]))
def test_the_shared_cases(case: dict[str, Any], split: bool) -> None:
    chunks = [chunk.encode() for chunk in case["chunks"]]
    if split:
        chunks = [bytes([byte]) for chunk in chunks for byte in chunk]
    events, error = parse(chunks)
    assert [{"event": e.name, "data": e.data} for e in events] == case["expected"]["items"]
    if case["expected"]["error"] is None:
        assert error is None
    else:
        assert error is not None
        assert (error.source, error.code) == (Source.CLIENT, case["expected"]["error"])
        assert error.retry is RetryAdvice.RETRY_NOW


def test_all_the_shared_cases_in_one_chunk() -> None:
    for case in CASES:
        events, _ = parse(["".join(case["chunks"]).encode()])
        assert [{"event": e.name, "data": e.data} for e in events] == case["expected"]["items"]


@pytest.mark.parametrize(
    ("data", "detail"),
    [
        (b"event: update\ndata: nope\n\n", 'a "update" event with data that is not JSON.'),
        (b'event: done\ndata: "\xff"\n\n', 'a "done" event with data that is not JSON.'),
        (b"event: done\ndata: 1\ndata: 2\n\n", 'a "done" event with data that is not JSON.'),
        (b"event: failed\ndata: NaN\n\n", 'a "failed" event with data that is not JSON.'),
        (b"event: failed\ndata:\n\n", 'a "failed" event with data that is not JSON.'),
    ],
)
def test_data_that_is_not_json(data: bytes, detail: str) -> None:
    events, error = parse([data])
    assert events == []
    assert error is not None
    assert error.code == ClientCode.INVALID_RESPONSE
    assert error.detail == f"The backend sent {detail}"


def test_an_error_comes_after_the_events_before_it() -> None:
    events, error = parse([b"event: update\ndata: 1\n\nevent: update\ndata: nope\n\n"])
    assert [event.data for event in events] == [1]
    assert error is not None
    assert error.code == ClientCode.INVALID_RESPONSE


@pytest.mark.parametrize(
    "data",
    [
        b"event: other\ndata: nope\n\n",
        b"data: nope\n\n",
        b"event: update\n\n",
        # An event with no data does not give its name to the next one.
        b"event: update\n\ndata: nope\n\n",
        b"event\nid: 1\nretry: 10\ndata: nope\n\n",
        b"event: \xff\ndata: nope\n\n",
    ],
)
def test_events_that_give_no_item(data: bytes) -> None:
    events, error = parse([data + DONE])
    assert [event.name for event in events] == ["done"]
    assert error is None


def test_the_end_of_the_stream_drops_an_event_with_no_empty_line() -> None:
    events, error = parse([DONE[:-1]])
    assert events == []
    assert error is not None
    assert error.code == ClientCode.STREAM_FAILED
    assert error.detail == "The pairing stream ended before done or failed."


def sized(size: int, *, end: bytes = b"\n\n") -> bytes:
    """Makes a done event with `size` bytes."""
    start = b"event: done\ndata: {}\n:"
    return start + b"x" * (size - len(start) - len(end)) + end


@pytest.mark.parametrize("pieces", [1, 3, 5000])
@pytest.mark.parametrize("end", [b"\n\n", b"\r\n\r\n"])
def test_the_size_limit_of_an_event(pieces: int, end: bytes) -> None:
    def chunks(data: bytes) -> list[bytes]:
        step = len(data) // pieces + 1
        return [data[i : i + step] for i in range(0, len(data), step)]

    events, error = parse(chunks(sized(MAX_EVENT, end=end)))
    assert (len(events), error) == (1, None)
    events, error = parse(chunks(sized(MAX_EVENT + 2, end=end)))
    assert events == []
    assert error is not None
    assert (error.code, error.retry) == (ClientCode.INVALID_RESPONSE, RetryAdvice.RETRY_NOW)
    assert error.detail == "The backend sent an event that is too large."


def test_the_size_limit_is_exact() -> None:
    _, error = parse([sized(MAX_EVENT + 1)])
    assert error is not None
    assert error.code == ClientCode.INVALID_RESPONSE


@pytest.mark.parametrize("line", [b"x", b"data: 1\n", b": ping\n", b"data: 1\r"])
def test_the_size_limit_stops_an_event_with_no_end(line: bytes) -> None:
    parser = make_parser()
    read = 0
    with pytest.raises(CalyxError) as caught:
        for _ in range(3 * MAX_EVENT // len(line)):
            read += len(line)
            assert list(parser.feed(line)) == []
    assert caught.value.code == ClientCode.INVALID_RESPONSE
    assert MAX_EVENT < read <= MAX_EVENT + len(line)


@pytest.mark.parametrize("ping", [b": ping\n\n", b": ping\r\n\r\n", b": ping\r\r"])
def test_an_empty_line_starts_a_new_count(ping: bytes) -> None:
    events, error = parse([ping * 1000] * 200 + [DONE])
    assert [event.name for event in events] == ["done"]
    assert error is None


def test_the_repr_of_an_event_has_no_data() -> None:
    (event,) = make_parser().feed(b'event: update\ndata: {"qr_text":"private"}\n\n')
    assert "private" not in repr(event)
