"""The event classes, against the shared cases and event.proto."""

import copy
import inspect
import json
import pickle
import re
import traceback
from dataclasses import MISSING, FrozenInstanceError, fields, is_dataclass
from datetime import datetime
from pathlib import Path
from typing import Any, assert_never, get_args, get_type_hints

import pytest

import calyx_client_sdk
from calyx_client_sdk import (
    BulkFinished,
    BulkFinishedEvent,
    CalyxError,
    ClientCode,
    ConnectionUpdate,
    ConnectionUpdateEvent,
    DeliveryStatus,
    Event,
    InvalidPayloadEvent,
    MessageReceived,
    MessageReceivedEvent,
    MessageStatus,
    MessageStatusEvent,
    OtherEvent,
    RetryAdvice,
    Source,
    decode_event,
    events,
)

PACKAGES = Path(__file__).parents[4]
CASES = json.loads((PACKAGES / "client-sdk" / "testdata" / "events.json").read_text())["cases"]
PROTO = (PACKAGES / "shared-types" / "proto" / "calyx" / "core" / "v1" / "event.proto").read_text()

TYPED: dict[str, tuple[type[Any], type[Any]]] = {
    "connection.update": (ConnectionUpdateEvent, ConnectionUpdate),
    "message.received": (MessageReceivedEvent, MessageReceived),
    "message.status": (MessageStatusEvent, MessageStatus),
    "bulk.finished": (BulkFinishedEvent, BulkFinished),
}
EVENTS = {*(event for event, _ in TYPED.values()), OtherEvent, InvalidPayloadEvent}
PAYLOADS = {payload for _, payload in TYPED.values()}
each_typed = pytest.mark.parametrize("name", TYPED)

SAMPLES: dict[str, dict[str, object]] = {
    "connection.update": {"status": "CONNECTED"},
    "message.received": {
        "messageId": "m1",
        "chatId": "34600000000@s.whatsapp.net",
        "chatAltId": "1234@lid",
        "from": "34600000001@s.whatsapp.net",
        "fromAltId": "5678@lid",
        "isGroup": True,
        "pushName": "Ana",
        "timestamp": 1795255200,
        "type": "text",
        "text": "private text",
    },
    "message.status": {
        "messageId": "m1",
        "chatId": "34600000000@s.whatsapp.net",
        "chatAltId": "1234@lid",
        "status": "delivered",
    },
    "bulk.finished": {
        "jobId": "job-1",
        "status": "FAILED",
        "totalMessages": 10,
        "sentMessages": 9,
        "failedMessages": 1,
        "error": "The instance is not paired.",
    },
}


def envelope(name: str = "whatsmeow.Receipt", data: object = None, **changes: object) -> Any:
    return {
        "id": "evt-1",
        "event": name,
        "instance": "i_1",
        "timestamp": "2026-11-20T10:00:00.123456789Z",
        "data": data,
    } | changes


def decode(value: object) -> Any:
    return decode_event(json.dumps(value).encode())


def refused(body: bytes) -> CalyxError:
    with pytest.raises(CalyxError) as caught:
        decode_event(body)
    error = caught.value
    assert (error.source, error.status) == (Source.CLIENT, 0)
    assert error.code == ClientCode.INVALID_EVENT
    assert error.retry is RetryAdvice.DO_NOT_RETRY
    return error


def wire(payload: Any) -> dict[str, object]:
    """Gives the fields that are not None, with the names of the wire."""
    items = ((f.metadata.get("json", f.name), getattr(payload, f.name)) for f in fields(payload))
    return {name: value for name, value in items if value is not None}


# The shared cases.


@pytest.mark.parametrize("case", CASES, ids=lambda c: c["name"])
def test_the_shared_cases(case: dict[str, Any]) -> None:
    body, expected = case["body"].encode(), case["expected"]
    if "error" in expected:
        assert refused(body).code == expected["error"]
        return
    event: Any = decode_event(body)
    sent = json.loads(body)
    kind, payload = TYPED[expected["event"]]
    assert type(event) is kind
    assert type(event.data) is payload
    assert wire(event.data) == expected["payload"]
    assert event.event == expected["event"]
    assert (event.id, event.instance, event.timestamp) == (
        sent["id"],
        sent["instance"],
        sent["timestamp"],
    )


def test_each_attribute_of_a_received_message() -> None:
    event = decode(envelope("message.received", SAMPLES["message.received"]))
    assert event == MessageReceivedEvent(
        id="evt-1",
        instance="i_1",
        timestamp="2026-11-20T10:00:00.123456789Z",
        data=MessageReceived(
            message_id="m1",
            chat_id="34600000000@s.whatsapp.net",
            chat_alt_id="1234@lid",
            from_="34600000001@s.whatsapp.net",
            from_alt_id="5678@lid",
            is_group=True,
            push_name="Ana",
            timestamp=1795255200,
            type="text",
            text="private text",
        ),
    )


# The payloads, against event.proto.

PYTHON = {"string": str, "bool": bool, "int32": int, "int64": int}


def proto_block(kind: str, name: str) -> str:
    found = re.search(rf"^{kind} {name} {{\n(.*?)^}}", PROTO, re.MULTILINE | re.DOTALL)
    assert found, name
    return found[1]


def proto_fields(message: str) -> dict[str, tuple[type, bool]]:
    """Gives the type and the "optional" of each field of a message."""
    rows = re.findall(
        r"^  (optional )?(\w+) (\w+) = \d+;$", proto_block("message", message), re.MULTILINE
    )
    return {name: (PYTHON[kind], bool(optional)) for optional, kind, name in rows}


def json_name(proto_name: str) -> str:
    return re.sub("_(.)", lambda m: m[1].upper(), proto_name)


def test_each_payload_of_the_proto_file_has_a_class() -> None:
    found = re.findall(r'^// The payload of "(.*)"\.\nmessage (\w+) ', PROTO, re.MULTILINE)
    assert dict(found) == {name: payload.__name__ for name, (_, payload) in TYPED.items()}


@each_typed
def test_the_fields_of_a_payload_are_the_fields_of_the_message(name: str) -> None:
    payload = TYPED[name][1]
    want = proto_fields(payload.__name__)
    assert want
    hints = get_type_hints(payload)
    got = {f.name: f for f in fields(payload)}
    # "from" is a keyword of Python.
    assert list(got) == ["from_" if proto == "from" else proto for proto in want]
    for proto, (kind, optional) in want.items():
        field = got["from_" if proto == "from" else proto]
        assert field.metadata.get("json", field.name) == json_name(proto)
        assert hints[field.name] == (kind | None if optional else kind)
        assert field.default is (None if optional else MISSING)


@each_typed
def test_the_event_class_has_the_event_name(name: str) -> None:
    kind, payload = TYPED[name]
    hints = get_type_hints(kind)
    assert get_args(hints["event"]) == (name,)
    assert hints["data"] is payload


def test_the_constants_of_delivery_status() -> None:
    prefix = "MESSAGE_DELIVERY_STATUS_"
    block = proto_block("enum", "MessageDeliveryStatus")
    names = re.findall(rf"^  {prefix}(\w+) = \d+;$", block, re.MULTILINE)
    names.remove("UNSPECIFIED")
    constants = {k: v for k, v in vars(DeliveryStatus).items() if not k.startswith("_")}
    assert constants == {name: name.lower() for name in names}


# The payloads: what the SDK accepts.


@each_typed
def test_an_optional_field_can_be_absent_or_null(name: str) -> None:
    kind, payload = TYPED[name]
    for proto, (_, optional) in proto_fields(payload.__name__).items():
        if optional:
            null = SAMPLES[name] | {json_name(proto): None}
            absent = {k: v for k, v in SAMPLES[name].items() if k != json_name(proto)}
            for data in (null, absent):
                event = decode(envelope(name, data))
                assert type(event) is kind
                assert getattr(event.data, proto) is None


@each_typed
def test_an_empty_value_is_not_absent(name: str) -> None:
    empty: dict[type, object] = {str: "", bool: False, int: 0}
    wanted = proto_fields(TYPED[name][1].__name__)
    data = {json_name(proto): empty[kind] for proto, (kind, _) in wanted.items()}
    assert wire(decode(envelope(name, data)).data) == data


@each_typed
def test_a_new_status_stays_text(name: str) -> None:
    if "status" in SAMPLES[name]:
        event = decode(envelope(name, SAMPLES[name] | {"status": "new"}))
        assert type(event) is TYPED[name][0]
        assert event.data.status == "new"


@each_typed
def test_a_new_field_is_ignored(name: str) -> None:
    event = decode(envelope(name, SAMPLES[name]))
    assert decode(envelope(name, SAMPLES[name] | {"new": 1}, new=[1])) == event


# The payloads: what the SDK cannot read.


@each_typed
def test_a_missing_field_gives_an_invalid_payload(name: str) -> None:
    payload = TYPED[name][1]
    for proto, (_, optional) in proto_fields(payload.__name__).items():
        data = dict(SAMPLES[name])
        del data[json_name(proto)]
        event = decode(envelope(name, data))
        if not optional:
            assert event == InvalidPayloadEvent(
                id="evt-1",
                event=name,
                instance="i_1",
                timestamp="2026-11-20T10:00:00.123456789Z",
                data=data,
                # The name of the wire, because the reader of the text looks at the JSON.
                reason=f"{payload.__name__}.{json_name(proto)} is missing.",
            )


@each_typed
def test_a_wrong_type_gives_an_invalid_payload(name: str) -> None:
    payload = TYPED[name][1]
    for proto, (kind, _) in proto_fields(payload.__name__).items():
        data = SAMPLES[name] | {json_name(proto): 5 if kind is str else "private text"}
        event = decode(envelope(name, data))
        assert type(event) is InvalidPayloadEvent
        assert event.data == data
        assert event.reason.startswith(f"{payload.__name__}.{json_name(proto)} is not a")
        assert "private" not in event.reason


@each_typed
@pytest.mark.parametrize("data", [None, "text", 5, True, [], [{"status": "CONNECTED"}]])
def test_a_payload_that_is_no_object(name: str, data: object) -> None:
    event = decode(envelope(name, data))
    assert type(event) is InvalidPayloadEvent
    assert (event.event, event.data) == (name, data)
    assert event.reason == f"{TYPED[name][1].__name__} is not an object."


# Other events.


@pytest.mark.parametrize(
    "name", ["whatsmeow.Receipt", "meta.webhook", "new.event", "Message.Received"]
)
@pytest.mark.parametrize("data", [{"a": [1, None, {"b": 1.5}]}, [], None, "text", 0, False])
def test_an_unknown_event(name: str, data: object) -> None:
    assert decode(envelope(name, data)) == OtherEvent(
        id="evt-1",
        event=name,
        instance="i_1",
        timestamp="2026-11-20T10:00:00.123456789Z",
        data=data,
    )


# The envelope.


@pytest.mark.parametrize("name", ["id", "event", "instance", "timestamp"])
@pytest.mark.parametrize("wrong", [None, 5, True, [], {}])
def test_a_wrong_type_in_the_envelope(name: str, wrong: object) -> None:
    error = refused(json.dumps(envelope(**{name: wrong})).encode())
    assert f'"{name}"' in error.detail


@pytest.mark.parametrize("name", ["id", "event", "instance", "timestamp", "data"])
def test_a_missing_field_in_the_envelope(name: str) -> None:
    value = envelope()
    del value[name]
    assert f'"{name}"' in refused(json.dumps(value).encode()).detail


@pytest.mark.parametrize("name", ["id", "event", "instance"])
def test_an_empty_text_in_the_envelope(name: str) -> None:
    refused(json.dumps(envelope(**{name: ""})).encode())


def test_the_timestamp_is_the_text_of_the_wire() -> None:
    event = decode(envelope())
    assert event.timestamp == "2026-11-20T10:00:00.123456789Z"
    assert datetime.fromisoformat(event.timestamp).year == 2026
    # CONTRACT part 10 rule 1: any string.
    assert decode(envelope(timestamp="")).timestamp == ""


@pytest.mark.parametrize("value", [None, [], "text", 5, True, [envelope()]])
def test_a_body_that_is_no_object(value: object) -> None:
    refused(json.dumps(value).encode())


@pytest.mark.parametrize(
    "body",
    [
        b"",
        b"private text",
        b'{"id": "private text"',
        b'{"id": "\xff private text"}',
        b"[" * 200_000,
        b'{"data": NaN}',
        json.dumps(envelope()).encode("utf-16"),
    ],
)
def test_a_body_that_is_not_json(body: bytes) -> None:
    error = refused(body)
    assert "JSON" in error.detail
    assert error.__cause__ is not None
    text = "".join(traceback.format_exception(error, chain=True, limit=0))
    assert "private" not in text


@pytest.mark.parametrize("body", [{}, envelope(), json.dumps(envelope()), None, bytearray(b"{}")])
def test_a_body_that_is_not_bytes(body: Any) -> None:
    with pytest.raises(CalyxError) as caught:
        decode_event(body)
    error = caught.value
    assert (error.source, error.status) == (Source.CLIENT, 0)
    assert error.code == ClientCode.INVALID_CONFIG
    assert error.retry is RetryAdvice.DO_NOT_RETRY
    assert "raw body" in error.detail


# The public classes.


def test_the_public_names() -> None:
    names = {c.__name__ for c in EVENTS | PAYLOADS}
    assert set(calyx_client_sdk.__all__) >= names | {"DeliveryStatus", "decode_event"}


def test_event_is_the_union_of_the_event_classes() -> None:
    assert "Event" in calyx_client_sdk.__all__
    assert set(get_args(Event)) == EVENTS


def test_isinstance_accepts_event() -> None:
    assert isinstance(decode(envelope()), Event)
    assert not isinstance(None, Event)


def test_the_result_is_an_event() -> None:
    for function in (events.decode_event, events.read_event):
        assert get_type_hints(function)["return"] == Event


@pytest.mark.parametrize("kind", [*EVENTS, *PAYLOADS], ids=lambda c: c.__name__)
def test_a_class_is_a_frozen_dataclass_with_a_text(kind: type[Any]) -> None:
    assert is_dataclass(kind)
    assert (inspect.getdoc(kind) or "").endswith(".")
    parameters = inspect.signature(kind).parameters.values()
    assert {p.kind for p in parameters} == {inspect.Parameter.KEYWORD_ONLY}


@pytest.mark.parametrize("name", [*TYPED, "other"])
def test_an_event_is_read_only_and_works_with_pickle_and_copy(name: str) -> None:
    event = decode(envelope(name, SAMPLES.get(name)))
    for value in (event, decode(envelope(name)), event.data):
        if value is not None:
            with pytest.raises(FrozenInstanceError):
                setattr(value, fields(value)[0].name, "other")
            assert not hasattr(value, "__dict__")
            assert pickle.loads(pickle.dumps(value)) == value
            assert copy.deepcopy(value) == value


def describe(body: bytes) -> str:
    event = decode_event(body)
    # A type checker sees that the match is complete.
    match event:
        case MessageReceivedEvent(data=message):
            return f"{message.from_}: {message.text}"
        case MessageStatusEvent(data=MessageStatus(status=DeliveryStatus.READ)):
            return "read"
        case MessageStatusEvent(data=data):
            return data.status
        case ConnectionUpdateEvent(data=update):
            return update.status
        case BulkFinishedEvent(data=job):
            return job.job_id
        case OtherEvent(event=name):
            return name
        case InvalidPayloadEvent(reason=reason):
            return reason
        case _:
            assert_never(event)


def test_match() -> None:
    values = [envelope(name, data) for name, data in SAMPLES.items()]
    values.append(envelope("message.status", SAMPLES["message.status"] | {"status": "read"}))
    values.append(envelope())
    values.append(envelope("bulk.finished"))
    assert [describe(json.dumps(value).encode()) for value in values] == [
        "CONNECTED",
        "34600000001@s.whatsapp.net: private text",
        "delivered",
        "job-1",
        "read",
        "whatsmeow.Receipt",
        "BulkFinished is not an object.",
    ]
