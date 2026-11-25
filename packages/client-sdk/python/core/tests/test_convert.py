"""Checks the generated classes and converters against the descriptors of the .proto files, so that
no message and no field is forgotten. Two samples are never equal, except a bool and an enum name.
So there are also tests that set one field only."""

import copy
import inspect
import itertools
import pickle
import re
from collections.abc import Callable, Iterator
from dataclasses import MISSING, FrozenInstanceError, fields, is_dataclass, replace
from datetime import UTC, datetime, timedelta
from typing import Any, get_type_hints

import pytest

from calyx_client_sdk_core import models
from calyx_client_sdk_core._generated import convert, methods
from calyx_client_sdk_core._generated import models as generated
from calyx_client_sdk_core._proto.calyx.core.v1 import event_pb2, instance_pb2, message_pb2
from calyx_client_sdk_core._values import WrongArgumentError, WrongShapeError

PROTOS = (event_pb2, instance_pb2, message_pb2)
RPCS = [m for p in PROTOS for s in p.DESCRIPTOR.services_by_name.values() for m in s.methods]
# These have no class: the module of the events is hand-written, and a program gives a Python list.
NO_CLASS = {"Event", "WebhookList", "Timestamp"}
PROVIDERS = ["meta_cloud", "whatsapp_web"]
WORDS = {9: "str of valid Unicode", 8: "bool", 5: "int of 32 bits"}
ZEROS = {9: "", 8: False, 5: 0, 2: 0.0}
counter = itertools.count(1)


def snake(name: str) -> str:
    return re.sub(r"(?<=[a-z])(?=[A-Z])", "_", name).lower()


def message_class(name: str) -> Any:
    return next(getattr(p, name) for p in PROTOS if hasattr(p, name))


def public_fields(descriptor: Any) -> list[Any]:
    return [f for f in descriptor.fields if f.name != "pairing_qr"]


def reachable(descriptor: Any) -> set[str]:
    """Gives the name of a message and the names of the messages in it."""
    found = {descriptor.name}
    for f in descriptor.fields:
        if f.message_type:
            found |= reachable(f.message_type)
    return found


ANSWERS = sorted(set().union(*(reachable(m.output_type) for m in RPCS)) - NO_CLASS)
CALLS = sorted(m.input_type.name for m in RPCS)
NESTED = sorted(set().union(*(reachable(m.input_type) for m in RPCS)) - NO_CLASS - set(CALLS))
each_answer = pytest.mark.parametrize("name", ANSWERS)
each_class = pytest.mark.parametrize("name", ANSWERS + NESTED)
each_call = pytest.mark.parametrize(
    ("name", "provider"),
    [(name, "") for name in CALLS if name != "CreateInstanceRequest"]
    + [("CreateInstanceRequest", provider) for provider in PROVIDERS],
)


def is_optional(f: Any) -> bool:
    # A field with "optional" is the one field of a hidden oneof. A oneof of a request can be empty.
    return f.containing_oneof is not None


def is_provider(f: Any) -> bool:
    return is_optional(f) and f.containing_oneof.name == "provider_settings"


def python_name(f: Any) -> str:
    return "provider" if is_provider(f) else f.name


def is_nullable(f: Any, request: bool) -> bool:
    return is_optional(f) or (request and f.enum_type is not None and not f.is_repeated)


def enum_names(enum: Any) -> dict[int, str]:
    prefix = enum.values_by_number[0].name.removesuffix("UNSPECIFIED")
    return {v.number: v.name.removeprefix(prefix) for v in enum.values}


def fill_field(message: Any, f: Any) -> Any:
    """Sets a field of a protobuf message. Gives the Python value of the same thing."""
    if f.is_repeated and f.message_type:
        return tuple(filled(getattr(message, f.name).add()) for _ in range(2))
    if f.is_repeated:
        getattr(message, f.name).extend([f"text {next(counter)}", f"text {next(counter)}"])
        return tuple(getattr(message, f.name))
    if f.message_type:
        getattr(message, f.name).SetInParent()
        return filled(getattr(message, f.name))
    if f.enum_type:
        number, name = list(enum_names(f.enum_type).items())[1 + next(counter) % 2]
        setattr(message, f.name, number)
        return name
    n = next(counter)
    value = {9: f"text {n} ñ", 8: True, 5: n, 2: n + 0.5}[f.type]
    setattr(message, f.name, value)
    return value


def filled(message: Any) -> Any:
    """Sets every field of a protobuf message. Gives the Python object of the same thing."""
    name = message.DESCRIPTOR.name
    if name == "Timestamp":
        date = datetime(2026, 11, 22, 10, 30, 0, 250000, UTC) + timedelta(seconds=next(counter))
        message.FromDatetime(date)
        return date
    if name == "WebhookList":
        return fill_field(message, message.DESCRIPTOR.fields[0])
    values = {f.name: fill_field(message, f) for f in public_fields(message.DESCRIPTOR)}
    return getattr(models, name)(**values)


def call_sample(name: str, provider: str) -> tuple[dict[str, Any], Any]:
    """Gives the arguments of a call and the request of the same thing, with every field."""
    message = message_class(name)()
    arguments = {
        python_name(f): fill_field(message, f)
        for f in message.DESCRIPTOR.fields
        if not is_provider(f) or f.name == provider
    }
    return arguments, message


def converter(name: str) -> Callable[..., Any]:
    """Gives the converter of a request, from its values."""
    if name in CALLS:
        return getattr(convert, snake(name))
    nested = getattr(convert, f"_{snake(name)}")
    return lambda **values: nested("x", getattr(models, name)(**values))


def empty_values(name: str) -> dict[str, Any]:
    """Gives the values of a request with nothing in it."""
    values = {}
    for f in public_fields(message_class(name).DESCRIPTOR):
        if is_provider(f):
            values["provider"] = models.WhatsAppWebSettings()
        elif f.is_repeated:
            values[f.name] = ()
        elif is_nullable(f, request=True):
            values[f.name] = None
        else:
            values[f.name] = ZEROS[f.type]
    return values


def empty_request(name: str) -> Any:
    message = message_class(name)()
    if name == "CreateInstanceRequest":
        message.whatsapp_web.SetInParent()
    return message


def on_the_wire(message: Any) -> Any:
    return type(message).FromString(message.SerializeToString())


def read(name: str, message: Any) -> Any:
    return getattr(convert, f"read_{snake(name)}")(on_the_wire(message))


def minimal(name: str) -> Any:
    """Gives an answer with only the required messages in it."""
    message = message_class(name)()
    for f in message.DESCRIPTOR.fields:
        if f.message_type and not f.is_repeated and not is_optional(f):
            getattr(message, f.name).SetInParent()
    return message


# The names


def test_the_classes_are_the_ones_of_the_descriptors() -> None:
    assert (len(ANSWERS), len(NESTED)) == (10, 7)
    enums = {
        f.enum_type.name
        for name in ANSWERS + NESTED
        for f in message_class(name).DESCRIPTOR.fields
        if f.enum_type
    }
    assert sorted(models.__all__) == sorted([*ANSWERS, *NESTED, *enums])
    assert [c for c in models.__all__ if is_dataclass(getattr(models, c))] == sorted(
        ANSWERS + NESTED
    )


def test_each_generated_class_is_public() -> None:
    made = [c for c in vars(generated).values() if inspect.isclass(c)]
    names = [c.__name__ for c in made if c.__module__ == generated.__name__]
    assert sorted(names) == sorted(models.__all__)
    for name in names:
        assert getattr(models, name) is getattr(generated, name)


@each_class
def test_the_fields_are_the_ones_of_the_message(name: str) -> None:
    kind, descriptor = getattr(models, name), message_class(name).DESCRIPTOR
    nullable = {f.name for f in public_fields(descriptor) if is_nullable(f, name in NESTED)}
    assert {f.name for f in fields(kind)} == {f.name for f in public_fields(descriptor)}
    assert {f.name for f in fields(kind) if f.default is None} == nullable
    assert {f.name for f in fields(kind) if f.default is MISSING} == {
        f.name for f in fields(kind) if f.name not in nullable
    }


@each_class
def test_no_class_shows_a_type_of_protobuf(name: str) -> None:
    for hint in get_type_hints(getattr(models, name)).values():
        assert "google" not in str(hint)
        assert "pb2" not in str(hint)


@pytest.mark.parametrize("name", models.__all__)
def test_each_class_has_a_text(name: str) -> None:
    assert (inspect.getdoc(getattr(models, name)) or "").endswith(".")


@pytest.mark.parametrize("name", [n for n in models.__all__ if n not in ANSWERS + NESTED])
def test_the_constants_are_the_names_of_the_enum(name: str) -> None:
    enum = next(p.DESCRIPTOR.enum_types_by_name[name] for p in PROTOS if hasattr(p, name))
    constants = {k: v for k, v in vars(getattr(models, name)).items() if not k.startswith("_")}
    assert constants == {text: text for text in enum_names(enum).values()}


@each_class
def test_an_object_is_read_only_and_works_with_pickle_and_copy(name: str) -> None:
    value = filled(message_class(name)())
    for f in fields(value):
        with pytest.raises(FrozenInstanceError):
            setattr(value, f.name, "other")
    assert not hasattr(value, "__dict__")
    assert pickle.loads(pickle.dumps(value)) == value
    assert copy.deepcopy(value) == value


@each_class
def test_the_constructor_takes_keywords_only(name: str) -> None:
    parameters = inspect.signature(getattr(models, name)).parameters.values()
    assert {p.kind for p in parameters} <= {inspect.Parameter.KEYWORD_ONLY}


@each_class
def test_the_repr_has_no_secret(name: str) -> None:
    secrets = {
        "CreateInstanceResponse": {"pairing_code"},
        "MetaCloudSettings": {"access_token", "app_secret"},
        "WebhookConfig": {"secret"},
    }
    value = filled(message_class(name)())
    for f in fields(value):
        shown = f"{f.name}={getattr(value, f.name)!r}" in repr(value)
        assert shown is (f.name not in secrets.get(name, set()))
        if not shown:
            assert getattr(value, f.name) not in repr(value)


# The methods


@pytest.mark.parametrize("rpc", RPCS, ids=lambda rpc: rpc.name)
def test_each_rpc_has_its_path_and_its_answer(rpc: Any) -> None:
    constant = getattr(methods, snake(rpc.name).upper())
    assert constant.path == f"/{rpc.containing_service.full_name}/{rpc.name}"
    assert constant.parse(b"") == message_class(rpc.output_type.name)()
    assert (constant.time_limit is None) is rpc.server_streaming


@pytest.mark.parametrize("rpc", [r for r in RPCS if not r.server_streaming], ids=lambda r: r.name)
@pytest.mark.parametrize("kind", [methods.SyncCoreMethods, methods.AsyncCoreMethods])
def test_the_arguments_of_a_method_are_the_fields_of_the_request(kind: Any, rpc: Any) -> None:
    parameters = inspect.signature(getattr(kind, snake(rpc.name))).parameters
    want: dict[str, Any] = {}
    for f in rpc.input_type.fields:
        optional = is_nullable(f, request=True)
        want[f.name] = None if optional else inspect.Parameter.empty
    want["time_limit"] = None
    assert {p.name: p.default for p in parameters.values() if p.name != "self"} == want
    assert all(p.kind is p.KEYWORD_ONLY for p in parameters.values() if p.name != "self")


# The requests


@each_call
def test_a_request_with_every_argument(name: str, provider: str) -> None:
    arguments, want = call_sample(name, provider)
    assert on_the_wire(converter(name)(**arguments)) == want


@pytest.mark.parametrize("name", CALLS + NESTED)
def test_a_request_with_nothing_in_it(name: str) -> None:
    assert on_the_wire(converter(name)(**empty_values(name))) == empty_request(name)


@pytest.mark.parametrize("name", CALLS + NESTED)
def test_a_request_with_one_argument(name: str) -> None:
    for f in public_fields(message_class(name).DESCRIPTOR):
        want = empty_request(name)
        values = empty_values(name) | {python_name(f): fill_field(want, f)}
        assert on_the_wire(converter(name)(**values)) == want


@pytest.mark.parametrize("name", CALLS + NESTED)
def test_an_optional_argument_can_be_empty(name: str) -> None:
    for f in public_fields(message_class(name).DESCRIPTOR):
        if is_optional(f) and not f.message_type:
            request = on_the_wire(converter(name)(**empty_values(name) | {f.name: ZEROS[f.type]}))
            assert request.HasField(f.name)
            assert getattr(request, f.name) == ZEROS[f.type]


@pytest.mark.parametrize("name", NESTED)
def test_the_defaults_of_a_request_class_add_nothing(name: str) -> None:
    required = {f.name for f in fields(getattr(models, name)) if f.default is MISSING}
    values = {k: v for k, v in empty_values(name).items() if k in required}
    assert converter(name)(**values) == message_class(name)()


@pytest.mark.parametrize("name", CALLS + NESTED)
def test_each_enum_name_of_a_request(name: str) -> None:
    for f in public_fields(message_class(name).DESCRIPTOR):
        for number, text in enum_names(f.enum_type).items() if f.enum_type else ():
            request = converter(name)(**empty_values(name) | {f.name: text})
            assert getattr(on_the_wire(request), f.name) == number


@each_call
def test_a_list_that_changes_after_the_call(name: str, provider: str) -> None:
    arguments, want = call_sample(name, provider)
    lists = {k: list(v) for k, v in arguments.items() if isinstance(v, tuple)}
    request = converter(name)(**arguments | lists)
    for value in lists.values():
        value.clear()
    assert on_the_wire(request) == want


class Wrong:
    """A place in a request, and the values that it refuses."""

    def __init__(self, path: tuple[Any, ...], f: Any, is_item: bool, nullable: bool) -> None:
        self.path = path
        self.text = "".join(f"[{key}]" if type(key) is int else f".{key}" for key in path)[1:]
        nothing = [] if nullable else [None]
        if f.is_repeated and not is_item:
            self.problem = "must be a list or a tuple."
            self.values = ["private", b"private", 98765, {"private"}, {"private": 1}, iter(["x"])]
        elif is_provider(f):
            self.problem = "must be a WhatsAppWebSettings or a MetaCloudSettings."
            self.values = ["private", {}, models.IntRange(min=98765, max=98765)]
            self.values += [message_class("MetaCloudSettings")(app_secret="private")]
        elif f.message_type:
            name = f.message_type.name
            self.problem = f"must be of the class {name}."
            self.values = ["private", {}, [], getattr(models, name)]
            self.values += [models.RemoveInstanceResponse(), message_class(name)()]
        elif f.enum_type:
            names = enum_names(f.enum_type)
            self.problem = f"must be one of {', '.join(names.values())}."
            self.values = ["private", "", "98765", "1", 1, b"CHROME_WINDOWS"]
            self.values += [names[1].lower(), f.enum_type.values_by_number[1].name]
        else:
            article = "an" if f.type == 5 else "a"
            self.problem = f"must be {article} {WORDS[f.type]}."
            self.values = {
                9: [98765, True, b"private", ["private"], "private\ud800"],
                8: [0, 1, "private", "true", []],
                5: [True, False, 98765.0, "98765", 2**31, -(2**31) - 1, 98765 * 10**30],
            }[f.type]
        self.values = [*self.values, *nothing]


def wrong_places(descriptor: Any, path: tuple[Any, ...], provider: str) -> Iterator[Wrong]:
    for f in public_fields(descriptor):
        if is_provider(f) and f.name != provider:
            continue
        place = (*path, python_name(f))
        nullable = is_nullable(f, request=True) and not is_provider(f)
        message = f.message_type
        if message and message.name == "WebhookList":
            f = message.fields[0]
            message = f.message_type
        yield Wrong(place, f, False, nullable)
        if f.is_repeated:
            place = (*place, 1)
            yield Wrong(place, f, True, False)
        if message:
            yield from wrong_places(message, place, provider)


def put(value: Any, path: tuple[Any, ...], new: Any) -> Any:
    """Gives a copy of a value with a new value at a path."""
    if not path:
        return new
    key, *rest = path
    if isinstance(value, dict):
        return value | {key: put(value[key], tuple(rest), new)}
    if isinstance(value, tuple):
        return (*value[:key], put(value[key], tuple(rest), new), *value[key + 1 :])
    return replace(value, **{key: put(getattr(value, key), tuple(rest), new)})


@each_call
def test_a_wrong_value_at_each_place(name: str, provider: str) -> None:
    arguments, _ = call_sample(name, provider)
    for wrong in wrong_places(message_class(name).DESCRIPTOR, (), provider):
        for value in wrong.values:
            with pytest.raises(WrongArgumentError) as info:
                converter(name)(**put(arguments, wrong.path, value))
            # The whole text is equal, so it has no value. No other error holds the value either.
            assert str(info.value) == f"{wrong.text} {wrong.problem}"
            assert info.value.__cause__ is None
            assert info.value.__context__ is None


def test_the_wrong_places_have_nested_fields() -> None:
    texts = {
        wrong.text
        for m in RPCS
        for provider in PROVIDERS
        for wrong in wrong_places(m.input_type, (), provider)
    }
    assert texts >= {
        "to",
        "recipients",
        "recipients[1]",
        "messages[1]",
        "messages[1].delay_ms",
        "pacing.pause_every.max",
        "webhooks[1].secret",
        "meta_cloud.app_secret",
        "provider",
        "provider.access_token",
        "provider.device_identity",
        "provider.pairing_code.phone_number",
    }


# The answers


@each_answer
def test_an_answer_with_every_field(name: str) -> None:
    message = message_class(name)()
    want = filled(message)
    assert read(name, message) == want


@each_answer
def test_a_minimal_answer(name: str) -> None:
    value = read(name, minimal(name))
    for f in message_class(name).DESCRIPTOR.fields:
        if is_optional(f):
            want: Any = None
        elif f.is_repeated:
            want = ()
        elif f.message_type:
            want = datetime(1970, 1, 1, tzinfo=UTC)
        else:
            want = "UNSPECIFIED" if f.enum_type else ZEROS[f.type]
        assert getattr(value, f.name) == want
        assert type(getattr(value, f.name)) is type(want)


@each_answer
def test_an_answer_with_one_field(name: str) -> None:
    nothing = read(name, minimal(name))
    for f in message_class(name).DESCRIPTOR.fields:
        message = minimal(name)
        value = fill_field(message, f)
        assert read(name, message) == replace(nothing, **{f.name: value})


@each_answer
def test_an_optional_text_can_be_empty(name: str) -> None:
    for f in message_class(name).DESCRIPTOR.fields:
        if is_optional(f) and not f.message_type:
            message = minimal(name)
            setattr(message, f.name, ZEROS[f.type])
            assert getattr(read(name, message), f.name) == ZEROS[f.type]


@each_answer
def test_a_required_message_that_is_missing(name: str) -> None:
    for f in message_class(name).DESCRIPTOR.fields:
        if f.message_type and not f.is_repeated and not is_optional(f):
            message = minimal(name)
            message.ClearField(f.name)
            with pytest.raises(WrongShapeError, match=rf"^{name}\.{f.name} is missing\.$"):
                read(name, message)


def test_a_required_message_that_is_missing_in_a_list() -> None:
    message = message_pb2.GetBulkJobsResponse(jobs=[minimal("BulkJob"), message_pb2.BulkJob()])
    with pytest.raises(WrongShapeError, match=r"^BulkJob\.created_at is missing\.$"):
        convert.read_get_bulk_jobs_response(message)


@each_answer
def test_each_enum_number_of_an_answer(name: str) -> None:
    for f in message_class(name).DESCRIPTOR.fields:
        if f.enum_type:
            for number, text in enum_names(f.enum_type).items() | {(99, "99"), (-1, "-1")}:
                message = minimal(name)
                setattr(message, f.name, number)
                assert getattr(read(name, message), f.name) == text


@each_answer
def test_each_date_is_aware_and_in_utc(name: str) -> None:
    value = filled(message := message_class(name)())
    for f in message.DESCRIPTOR.fields:
        if f.message_type and f.message_type.name == "Timestamp":
            date = getattr(read(name, message), f.name)
            assert date == getattr(value, f.name)
            assert date.tzinfo is UTC
            wrong = message_class(name)()
            wrong.CopyFrom(message)
            getattr(wrong, f.name).seconds = 2**60
            with pytest.raises(WrongShapeError, match="out of range"):
                read(name, wrong)


@each_answer
def test_a_message_that_changes_after_the_reading(name: str) -> None:
    message = message_class(name)()
    want = filled(message)
    value = getattr(convert, f"read_{snake(name)}")(message)
    message.Clear()
    assert value == want


# Examples with no help of the descriptors.


def test_each_class_of_provider() -> None:
    web = models.WhatsAppWebSettings(locale="es-ES", device_identity="SAFARI_MAC_OS")
    meta = models.MetaCloudSettings(phone_number_id="1", access_token="2", app_secret="3")

    def request(provider: object) -> Any:
        return convert.create_instance_request(
            instance_name="a", is_used_only_for_send_messages=None, webhooks=[], provider=provider
        )

    assert request(web) == instance_pb2.CreateInstanceRequest(
        instance_name="a",
        whatsapp_web=instance_pb2.WhatsAppWebSettings(
            locale="es-ES", device_identity=instance_pb2.DEVICE_IDENTITY_SAFARI_MAC_OS
        ),
    )
    assert request(meta) == instance_pb2.CreateInstanceRequest(
        instance_name="a",
        meta_cloud=instance_pb2.MetaCloudSettings(
            phone_number_id="1", access_token="2", app_secret="3"
        ),
    )


def test_a_pairing_with_a_code_and_with_a_qr_code() -> None:
    def settings(**values: Any) -> Any:
        return convert.create_instance_request(
            instance_name="a",
            is_used_only_for_send_messages=None,
            webhooks=(),
            provider=models.WhatsAppWebSettings(**values),
        ).whatsapp_web

    code = settings(pairing_code=models.PairingCodeSettings(phone_number="34600111222"))
    assert code.WhichOneof("pairing_method") == "pairing_code"
    assert code.pairing_code.phone_number == "34600111222"
    assert settings().WhichOneof("pairing_method") is None


def test_no_webhooks_and_an_empty_list_of_webhooks() -> None:
    def request(webhooks: object) -> Any:
        return on_the_wire(
            convert.update_instance_config_request(
                instance_name="a",
                new_instance_name=None,
                is_enabled=False,
                is_used_only_for_send_messages=None,
                webhooks=webhooks,
                meta_cloud=None,
            )
        )

    assert not request(None).HasField("webhooks")
    assert request([]).HasField("webhooks")
    assert len(request([]).webhooks.webhooks) == 0
    webhook = models.WebhookConfig(url="https://a.test", event="message.received")
    assert request([webhook]).webhooks.webhooks[0].url == "https://a.test"
    assert not request([webhook]).webhooks.webhooks[0].HasField("secret")
    assert request(None).HasField("is_enabled")
    assert not request(None).HasField("is_used_only_for_send_messages")


def test_a_bulk_job() -> None:
    job = message_pb2.BulkJob(
        id="j", status=message_pb2.BULK_JOB_STATUS_RUNNING, progress_percent=12.5, replayed=True
    )
    job.created_at.FromJsonString("2026-11-22T10:30:00.25Z")
    value = convert.read_bulk_job(job)
    assert value.status == models.BulkJobStatus.RUNNING
    assert value.created_at == datetime(2026, 11, 22, 10, 30, 0, 250000, tzinfo=UTC)
    assert value.finished_at is None
    assert value.error is None
    assert value.progress_percent == 12.5
    assert value.replayed is True


def test_the_errors_have_no_secret() -> None:
    settings = models.MetaCloudSettings(
        phone_number_id="1",
        access_token="private",
        app_secret=b"private",
    )
    with pytest.raises(WrongArgumentError) as info:
        convert.create_instance_request(
            instance_name="a", is_used_only_for_send_messages=None, webhooks=(), provider=settings
        )
    assert info.value.args == ("provider.app_secret must be a str of valid Unicode.",)
