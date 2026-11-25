"""The events of an instance: the bodies of its webhooks."""

from dataclasses import dataclass, field
from typing import Any, Final, Literal, cast

from ._fields import NotJsonError, WrongShapeError, parse_json, read
from ._generated.codes import ClientCode
from .errors import CalyxError, Source


class DeliveryStatus:
    """The status of a message in `MessageStatus`. A newer core can send other values."""

    SENT: Final = "sent"
    """META_CLOUD only: Meta sent the message."""

    DELIVERED: Final = "delivered"
    """The message reached a device of the recipient."""

    READ: Final = "read"
    """The message was read. WHATSAPP_WEB also gives it when its media was played, or when the
    account itself reads a received message on another device."""

    FAILED: Final = "failed"
    """META_CLOUD: Meta did not deliver the message. WHATSAPP_WEB: WhatsApp reported a server error
    for the message."""


@dataclass(frozen=True, slots=True, kw_only=True)
class ConnectionUpdate:
    """The data of a `ConnectionUpdateEvent`."""

    status: str
    """The new status of the instance, for example CONNECTED. The class `InstanceStatus` of
    `calyx_client_sdk.models` has the known values."""


@dataclass(frozen=True, slots=True, kw_only=True)
class MessageReceived:
    """The data of a `MessageReceivedEvent`."""

    message_id: str = field(metadata={"json": "messageId"})
    """The id of the message in WhatsApp."""

    chat_id: str = field(metadata={"json": "chatId"})
    """The chat of the message: a JID for WHATSAPP_WEB, a phone number for META_CLOUD."""

    chat_alt_id: str | None = field(default=None, metadata={"json": "chatAltId"})
    """WHATSAPP_WEB only: the phone number JID when `chat_id` is a LID, or the LID when it is a
    phone number JID. `None` for a group, or when it is not known."""

    from_: str = field(metadata={"json": "from"})
    """Who sent the message, in the format of `chat_id`. In a group, it is a member of the group."""

    from_alt_id: str | None = field(default=None, metadata={"json": "fromAltId"})
    """WHATSAPP_WEB only: the other address of the sender, in the format of `chat_alt_id`. `None`
    when it is not known."""

    is_group: bool = field(metadata={"json": "isGroup"})
    """WHATSAPP_WEB: true for a message in a group, for a broadcast list and for a status post.
    META_CLOUD: always false."""

    push_name: str | None = field(default=None, metadata={"json": "pushName"})
    """The name that the sender chose in WhatsApp. `None` when it is not known."""

    timestamp: int
    """When the message was sent: a Unix time in seconds."""

    type: str
    """The content type that the provider gives, for example text or image. The values depend on
    the provider."""

    text: str | None = None
    """The text of the message. WHATSAPP_WEB also gives the caption of an image, a video or a
    document. `None` when there is none."""


@dataclass(frozen=True, slots=True, kw_only=True)
class MessageStatus:
    """The data of a `MessageStatusEvent`."""

    message_id: str = field(metadata={"json": "messageId"})
    """The id of the message in WhatsApp. For a message of `send_message`, it is the `message_id`
    of the answer."""

    chat_id: str = field(metadata={"json": "chatId"})
    """The chat of the message: a JID for WHATSAPP_WEB, a phone number for META_CLOUD."""

    chat_alt_id: str | None = field(default=None, metadata={"json": "chatAltId"})
    """WHATSAPP_WEB only: the phone number JID when `chat_id` is a LID, or the LID when it is a
    phone number JID. `None` for a group, or when it is not known."""

    status: str
    """The new status of the message, for example delivered. The class `DeliveryStatus` has the
    known values."""


@dataclass(frozen=True, slots=True, kw_only=True)
class BulkFinished:
    """The data of a `BulkFinishedEvent`."""

    job_id: str = field(metadata={"json": "jobId"})
    """The id of the job, a UUID."""

    status: str
    """The last status of the job, for example COMPLETED. The class `BulkJobStatus` of
    `calyx_client_sdk.models` has the known values."""

    total_messages: int = field(metadata={"json": "totalMessages"})
    """The number of messages in the job."""

    sent_messages: int = field(metadata={"json": "sentMessages"})
    """The number of messages that WhatsApp accepted."""

    failed_messages: int = field(metadata={"json": "failedMessages"})
    """The number of messages that failed or have an unknown result. The job does not try them
    again."""

    error: str | None = None
    """Why the job failed, a sentence for people. `None` when the status is not FAILED."""


@dataclass(frozen=True, slots=True, kw_only=True)
class _Envelope:
    id: str
    """The id of the event. Calyx can deliver one event more than one time, with the same id. So
    store the ids, and ignore an event with an id that your program handled."""

    event: str
    """The name of the event."""

    instance: str
    """The name of the instance. For an instance of the backend, it is `i_` and the id of the
    instance without hyphens."""

    timestamp: str
    """The time of the event, as RFC 3339 text in UTC. The text can have more digits than a
    `datetime` keeps. `datetime.fromisoformat` reads it."""


@dataclass(frozen=True, slots=True, kw_only=True)
class ConnectionUpdateEvent(_Envelope):
    """The connection status of an instance changed."""

    event: Literal["connection.update"] = "connection.update"
    """The name of the event."""

    data: ConnectionUpdate
    """The new status."""


@dataclass(frozen=True, slots=True, kw_only=True)
class MessageReceivedEvent(_Envelope):
    """An instance received a message."""

    event: Literal["message.received"] = "message.received"
    """The name of the event."""

    data: MessageReceived
    """The message."""


@dataclass(frozen=True, slots=True, kw_only=True)
class MessageStatusEvent(_Envelope):
    """The status of a message changed, for example to delivered or read."""

    event: Literal["message.status"] = "message.status"
    """The name of the event."""

    data: MessageStatus
    """The message and its new status."""


@dataclass(frozen=True, slots=True, kw_only=True)
class BulkFinishedEvent(_Envelope):
    """A bulk job ended."""

    event: Literal["bulk.finished"] = "bulk.finished"
    """The name of the event."""

    data: BulkFinished
    """The result of the job."""


@dataclass(frozen=True, slots=True, kw_only=True)
class OtherEvent(_Envelope):
    """An event that this version of the SDK has no class for: a raw event of the provider
    (whatsmeow.* or meta.webhook), or an event of a newer core."""

    data: Any
    """The data as plain JSON: a dict, a list, a text, a number, a bool or `None`."""


@dataclass(frozen=True, slots=True, kw_only=True)
class InvalidPayloadEvent(_Envelope):
    """An event with a known name and with data that this version of the SDK cannot read. Log
    `reason`, and answer 2xx: a new delivery has the same data. A newer SDK can perhaps read it."""

    data: Any
    """The data as plain JSON: a dict, a list, a text, a number, a bool or `None`."""

    reason: str
    """What is wrong, for example "MessageReceived.chatId is missing.". It has the name of a field,
    and no value of the data."""


# Not "type Event = ...": isinstance() refuses that form.
Event = (
    MessageReceivedEvent
    | MessageStatusEvent
    | ConnectionUpdateEvent
    | BulkFinishedEvent
    | OtherEvent
    | InvalidPayloadEvent
)
"""An event of any class: `MessageReceivedEvent`, `MessageStatusEvent`, `ConnectionUpdateEvent`,
`BulkFinishedEvent`, `OtherEvent` or `InvalidPayloadEvent`. Use it in an annotation. In a `match`,
use the classes."""


def decode_event(body: bytes) -> Event:
    """Read the body of a webhook that has no secret.

    Nothing shows who sent the body. Anyone who knows the URL can send one. When the webhook has a
    secret, use `verify_webhook`.

    The result is an `Event`: a `MessageReceivedEvent`, a `MessageStatusEvent`, a
    `ConnectionUpdateEvent`, a `BulkFinishedEvent`, an `OtherEvent` or an `InvalidPayloadEvent`.
    Use `match` or `isinstance` on the class of the event. mypy does not narrow the event after a
    comparison of its `event` field.

    Give the raw body of the request. A body that is not an event raises `CalyxError` with the code
    `ClientCode.INVALID_EVENT`.
    """
    check_body(body)
    return read_event(parse_event_json(body))


def read_event(value: object) -> Event:
    """Make an event from the JSON of its envelope. Only for the Calyx packages."""
    if not isinstance(value, dict):
        raise event_error(ClientCode.INVALID_EVENT, "The event is not a JSON object.")
    envelope = cast(dict[str, object], value)
    id = _text(envelope, "id")
    name = _text(envelope, "event")
    instance = _text(envelope, "instance")
    timestamp = envelope.get("timestamp")
    if not isinstance(timestamp, str):
        raise event_error(
            ClientCode.INVALID_EVENT, 'The "timestamp" of the event is missing or is not a string.'
        )
    if "data" not in envelope:
        raise event_error(ClientCode.INVALID_EVENT, 'The event has no "data".')
    data = envelope["data"]

    try:
        match name:
            case "connection.update":
                return ConnectionUpdateEvent(
                    id=id,
                    instance=instance,
                    timestamp=timestamp,
                    data=read(ConnectionUpdate, data),
                )
            case "message.received":
                return MessageReceivedEvent(
                    id=id,
                    instance=instance,
                    timestamp=timestamp,
                    data=read(MessageReceived, data),
                )
            case "message.status":
                return MessageStatusEvent(
                    id=id, instance=instance, timestamp=timestamp, data=read(MessageStatus, data)
                )
            case "bulk.finished":
                return BulkFinishedEvent(
                    id=id, instance=instance, timestamp=timestamp, data=read(BulkFinished, data)
                )
            case _:
                return OtherEvent(
                    id=id, event=name, instance=instance, timestamp=timestamp, data=data
                )
    except WrongShapeError as error:
        return InvalidPayloadEvent(
            id=id,
            event=name,
            instance=instance,
            timestamp=timestamp,
            data=data,
            reason=str(error),
        )


def parse_event_json(text: bytes) -> object:
    """Parse the JSON of an event or of its data. Only for the Calyx packages."""
    try:
        return parse_json(text)
    except NotJsonError as error:
        raise event_error(ClientCode.INVALID_EVENT, "The event is not JSON.") from error


def check_body(body: object) -> None:
    """Check the body that a program gives. Only for the Calyx packages."""
    # `object`, because a program without a type checker can pass any value.
    if not isinstance(body, bytes):
        raise event_error(
            ClientCode.INVALID_CONFIG,
            "The body must be the raw body of the request, as bytes. Do not pass parsed JSON or "
            "decoded text.",
        )


def event_error(code: str, detail: str) -> CalyxError:
    """Make the error of an event or a webhook. Only for the Calyx packages."""
    return CalyxError(source=Source.CLIENT, code=code, status=0, detail=detail)


def _text(envelope: dict[str, object], name: str) -> str:
    value = envelope.get(name)
    if isinstance(value, str) and value != "":
        return value
    raise event_error(
        ClientCode.INVALID_EVENT, f'The "{name}" of the event is missing, empty or not a string.'
    )
