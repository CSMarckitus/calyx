"""The check of a webhook: its signature and its time."""

import hashlib
import hmac
import re
import time
from collections.abc import Iterable, Mapping
from datetime import datetime
from typing import Any, cast

from ._generated.codes import ClientCode
from .events import Event, check_body, event_error, parse_event_json, read_event


def verify_webhook(
    body: bytes,
    headers: Mapping[str, str] | Iterable[tuple[str, str]],
    secret: str,
    *,
    tolerance: int = 300,
    now: datetime | None = None,
) -> Event:
    """Check that Calyx sent a webhook, and read its event.

    Example:

        match verify_webhook(body, headers, secret):
            case MessageReceivedEvent(id=id, data=message):
                print(id, message.from_, message.text)
            case InvalidPayloadEvent(reason=reason):
                print(reason)

    The result is an `Event`: a `MessageReceivedEvent`, a `MessageStatusEvent`, a
    `ConnectionUpdateEvent`, a `BulkFinishedEvent`, an `OtherEvent` or an `InvalidPayloadEvent`.
    Use `match` or `isinstance` on the class of the event. mypy does not narrow the event after a
    comparison of its `event` field.

    - `body`: the raw body of the request. JSON that your program parsed and wrote again has other
      bytes, and then the check fails.
    - `headers`: all the headers of the request. The letter case of a name has no meaning.
    - `secret`: the secret of the webhook. For a webhook with no secret, use `decode_event`.
    - `tolerance`: the maximum number of seconds between the time of the webhook and now.
    - `now`: the time of the check, for tests. It needs a time zone.

    It raises `CalyxError` with one of these codes of `ClientCode`:

    - `INVALID_SIGNATURE`: the secret is wrong or empty, a header is missing, or the body changed.
    - `STALE_TIMESTAMP`: the webhook is too old, or the clock of your server is wrong. With a
      wrong clock, each delivery is refused and these events are lost.
    - `INVALID_EVENT`: the body is not an event.
    - `INVALID_CONFIG`: an argument is wrong.

    Answer 400 after the first three. Then Calyx does not deliver the event again.
    """
    check_body(body)
    pairs = _header_pairs(headers)
    key = _secret_bytes(secret)
    # bool is a kind of int.
    if type(tolerance) is not int or tolerance <= 0:
        raise event_error(
            ClientCode.INVALID_CONFIG, "The tolerance must be an integer of seconds above 0."
        )
    time_now = time.time() if now is None else _unix_time(now)

    if not key:
        raise event_error(ClientCode.INVALID_SIGNATURE, "The webhook secret is empty.")
    timestamp = _header(pairs, "x-calyx-timestamp")
    signature = _header(pairs, "x-calyx-signature")
    if not timestamp or not signature:
        raise event_error(
            ClientCode.INVALID_SIGNATURE,
            "The X-Calyx-Timestamp or the X-Calyx-Signature header is missing.",
        )
    # Python refuses to convert a very long number.
    if not re.fullmatch("[0-9]{1,18}", timestamp):
        raise event_error(
            ClientCode.INVALID_SIGNATURE, "The X-Calyx-Timestamp header is not a number of seconds."
        )
    if abs(time_now - int(timestamp)) > tolerance:
        raise event_error(
            ClientCode.STALE_TIMESTAMP,
            f"The time of the webhook is more than {tolerance} seconds before or after now.",
        )
    digest = hmac.new(key, f"{timestamp}.".encode() + body, hashlib.sha256).hexdigest()
    # compare_digest refuses a str that is not ASCII, and a header can have any character.
    if not hmac.compare_digest(
        f"sha256={digest}".encode(), signature.encode(errors="replace").lower()
    ):
        raise event_error(ClientCode.INVALID_SIGNATURE, "The signature does not match.")

    return read_event(parse_event_json(body))


# The checks take `object`, because a program without a type checker can pass any value.
def _header_pairs(headers: object) -> list[tuple[str, str]]:
    # The headers of Flask are not a Mapping, but they have items() too.
    items = getattr(headers, "items", None)
    pairs: Any = items() if callable(items) else headers
    if isinstance(pairs, Iterable):
        found = [_pair(item) for item in cast(Iterable[object], pairs)]
        if None not in found:
            return cast(list[tuple[str, str]], found)
    raise event_error(
        ClientCode.INVALID_CONFIG,
        "The headers must be a mapping or pairs, and each name and value a string.",
    )


def _pair(item: object) -> tuple[str, str] | None:
    match item:
        case (str(name), str(value)):
            return name, value
        case _:
            return None


def _header(pairs: list[tuple[str, str]], wanted: str) -> str | None:
    return next((value for name, value in pairs if name.lower() == wanted), None)


def _secret_bytes(secret: object) -> bytes:
    if isinstance(secret, str):
        try:
            return secret.encode()
        except UnicodeEncodeError:
            pass
    raise event_error(ClientCode.INVALID_CONFIG, "The secret must be a string of valid Unicode.")


def _unix_time(now: object) -> float:
    if not isinstance(now, datetime) or now.utcoffset() is None:
        raise event_error(ClientCode.INVALID_CONFIG, "The time now must be a datetime with a zone.")
    return now.timestamp()
