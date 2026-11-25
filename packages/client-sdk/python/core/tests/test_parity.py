"""The two clients must not drift: only the coroutine or the iterator differs."""

import inspect
import types
from typing import Any, get_type_hints

import pytest

import calyx_client_sdk_core
from calyx_client_sdk_core import (
    AsyncCoreClient,
    AsyncEventStream,
    AsyncInstanceStream,
    CoreClient,
    EventStream,
    InstanceStream,
)
from calyx_client_sdk_core._proto.calyx.core.v1 import event_pb2, instance_pb2, message_pb2

CLIENTS = [CoreClient, AsyncCoreClient]
UNARY = 10
STREAMS = {
    "create_instance": (InstanceStream, AsyncInstanceStream),
    "subscribe": (EventStream, AsyncEventStream),
}
NAMES = sorted(name for name in dir(CoreClient) if not name.startswith("_"))
CALLS = [name for name in NAMES if name != "close"]
each_client = pytest.mark.parametrize("kind", CLIENTS, ids=["sync", "async"])


def described(function: Any) -> dict[str, Any]:
    """Gives all that a program, a type checker or an IDE sees of a method."""
    return {
        "signature": inspect.signature(function),
        "annotations": inspect.get_annotations(function),
        "types": get_type_hints(function),
        "docstring": function.__doc__,
        "name": function.__name__,
    }


def test_the_clients_are_public() -> None:
    assert set(calyx_client_sdk_core.__all__) >= {"AsyncCoreClient", "CoreClient"}


def test_both_clients_have_the_same_names() -> None:
    assert NAMES == sorted(name for name in dir(AsyncCoreClient) if not name.startswith("_"))
    assert len(CALLS) == UNARY + len(STREAMS)


@pytest.mark.parametrize("name", sorted({*NAMES, "__init__"} - set(STREAMS)))
def test_a_method_is_the_same_in_both_clients(name: str) -> None:
    sync, async_ = getattr(CoreClient, name), getattr(AsyncCoreClient, name)
    texts = described(sync), described(async_)
    if name == "close":
        # Only the async client has an event loop.
        sync_text, async_text = (text.pop("docstring").rstrip() for text in texts)
        assert async_text.startswith(sync_text)
        assert "event loop" in async_text
    assert texts[0] == texts[1]
    assert not inspect.iscoroutinefunction(sync)
    assert inspect.iscoroutinefunction(async_) is (name != "__init__")


@pytest.mark.parametrize("name", STREAMS)
def test_only_the_iterator_of_a_stream_differs(name: str) -> None:
    sync, async_ = getattr(CoreClient, name), getattr(AsyncCoreClient, name)
    types = get_type_hints(sync), get_type_hints(async_)
    assert tuple(t.pop("return") for t in types) == STREAMS[name]
    assert types[0] == types[1]
    parameters = [list(inspect.signature(f).parameters.values()) for f in (sync, async_)]
    assert parameters[0] == parameters[1]
    texts = [inspect.getdoc(f) or "" for f in (sync, async_)]
    # The lines have other breaks.
    async_text = texts[1].replace("async ", "").replace("Async", "")
    assert texts[0].split() == async_text.split()
    assert not any(inspect.iscoroutinefunction(f) for f in (sync, async_))


@pytest.mark.parametrize("method", ["enter", "exit", "iter", "next"])
@pytest.mark.parametrize("name", STREAMS)
def test_the_two_streams_are_the_same(name: str, method: str) -> None:
    kinds = STREAMS[name]
    sync, async_ = getattr(kinds[0], f"__{method}__"), getattr(kinds[1], f"__a{method}__")
    assert list(inspect.signature(sync).parameters) == list(inspect.signature(async_).parameters)
    if method != "next":
        assert get_type_hints(sync).get("return") == get_type_hints(async_).get("return")
    assert sync.__doc__
    assert sync.__doc__ == async_.__doc__
    assert not inspect.iscoroutinefunction(sync)
    assert inspect.iscoroutinefunction(async_) is (method != "iter")


@pytest.mark.parametrize("name", STREAMS)
def test_the_items_of_the_two_streams_are_the_same(name: str) -> None:
    items = [types.get_original_bases(kind)[0].__args__ for kind in STREAMS[name]]
    assert items[0] == items[1]


@pytest.mark.parametrize("name", ["enter", "exit"])
def test_both_clients_are_context_managers(name: str) -> None:
    sync = getattr(CoreClient, f"__{name}__")
    async_ = getattr(AsyncCoreClient, f"__a{name}__")
    assert inspect.signature(sync) == inspect.signature(async_)
    assert get_type_hints(sync) == get_type_hints(async_)
    assert sync.__doc__ == async_.__doc__
    assert inspect.iscoroutinefunction(async_)


@each_client
def test_each_class_has_only_its_own_context_manager(kind: type[Any]) -> None:
    assert hasattr(kind, "__enter__") is (kind is CoreClient)
    assert hasattr(kind, "__aenter__") is (kind is AsyncCoreClient)


@pytest.mark.parametrize("name", CALLS)
@each_client
def test_the_ide_has_the_text_of_each_argument(kind: type[Any], name: str) -> None:
    method = getattr(kind, name)
    text = inspect.getdoc(method) or ""
    parameters = list(inspect.signature(method).parameters.values())[1:]
    by_position = [p.name for p in parameters if p.kind is not p.KEYWORD_ONLY]
    assert by_position == (["instance_name"] if name == "create_instance" else [])
    assert all(f"\n    {p.name}: " in text for p in parameters)
    assert parameters[-1].name == "time_limit"
    assert "\nReturns:\n" in text
    assert "\nRaises:\n    CalyxError: " in text


def test_the_constructor_has_a_text_for_each_argument() -> None:
    text = inspect.getdoc(CoreClient.__init__) or ""
    parameters = inspect.signature(CoreClient).parameters
    for name in parameters:
        assert f"\n    {name}: " in text
    by_position = [p.name for p in parameters.values() if p.kind is not p.KEYWORD_ONLY]
    assert by_position == ["address", "token"]
    assert {p.name: p.default for p in parameters.values() if p.kind is p.KEYWORD_ONLY} == {
        "insecure": False,
        "ca_file": None,
        "time_limit": None,
    }


@each_client
def test_each_class_has_the_rules_in_its_text(kind: type[Any]) -> None:
    text = " ".join((inspect.getdoc(kind) or "").split())
    for word in ("Example:", "private network", "fork", "close"):
        assert word in text
    assert ("event loop" in text) is (kind is AsyncCoreClient)


@each_client
def test_no_public_attribute_shows_grpc_or_protobuf(kind: type[Any]) -> None:
    client = kind("127.0.0.1:50051", "token")
    assert [name for name in vars(client) if not name.startswith("_")] == []
    for name in NAMES:
        for hint in get_type_hints(getattr(kind, name)).values():
            for module in {getattr(hint, "__module__", ""), repr(hint)}:
                assert "grpc" not in module
                assert "protobuf" not in module
                assert "_proto" not in module


def test_the_package_shows_only_its_own_names() -> None:
    public = {name for name in vars(calyx_client_sdk_core) if not name.startswith("_")}
    modules = {
        name
        for name in public
        if isinstance(getattr(calyx_client_sdk_core, name), types.ModuleType)
    }
    assert public - modules <= set(calyx_client_sdk_core.__all__)
    assert modules <= {"client", "models", "streams"}


def test_the_defaults_of_the_streams() -> None:
    defaults = {
        name: {
            p.name: p.default
            for p in inspect.signature(getattr(CoreClient, name)).parameters.values()
            if p.default is not p.empty
        }
        for name in STREAMS
    }
    assert defaults == {
        "create_instance": {
            "webhooks": (),
            "is_used_only_for_send_messages": None,
            "time_limit": None,
        },
        "subscribe": {"instance_names": (), "event_types": (), "time_limit": None},
    }


def test_each_rpc_has_a_method() -> None:
    files = (event_pb2, instance_pb2, message_pb2)
    rpcs = [m for f in files for s in f.DESCRIPTOR.services_by_name.values() for m in s.methods]
    unary = [m.name for m in rpcs if not m.server_streaming]
    assert len(unary) == UNARY
    assert {m.name.lower() for m in rpcs} == {name.replace("_", "") for name in CALLS}
