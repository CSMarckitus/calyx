"""The two clients must not drift: only the coroutine differs."""

import inspect
from typing import Any, get_overloads, get_type_hints

import pytest

import calyx_client_sdk
from calyx_client_sdk import (
    AsyncBackendClient,
    AsyncPairingStream,
    BackendClient,
    PairingStream,
)

NAMES = sorted(name for name in dir(BackendClient) if not name.startswith("_"))
OVERLOADED = {"create_bulk_job", "create_instance"}
# These are no coroutines.
STREAM = "follow_pairing"
PLAIN = {STREAM, "__init__"}


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
    assert set(calyx_client_sdk.__all__) >= {"AsyncBackendClient", "BackendClient"}


def test_both_clients_have_the_same_names() -> None:
    assert NAMES == sorted(name for name in dir(AsyncBackendClient) if not name.startswith("_"))
    assert len(NAMES) == 23


@pytest.mark.parametrize("name", sorted({*NAMES, "__init__"} - {STREAM}))
def test_a_method_is_the_same_in_both_clients(name: str) -> None:
    sync, async_ = getattr(BackendClient, name), getattr(AsyncBackendClient, name)
    assert described(sync) == described(async_)
    assert not inspect.iscoroutinefunction(sync)
    assert inspect.iscoroutinefunction(async_) is (name not in PLAIN)


def test_only_the_iterator_of_the_stream_differs() -> None:
    sync, async_ = BackendClient.follow_pairing, AsyncBackendClient.follow_pairing
    types = get_type_hints(sync), get_type_hints(async_)
    assert [t.pop("return") for t in types] == [PairingStream, AsyncPairingStream]
    assert types[0] == types[1]
    parameters = [list(inspect.signature(f).parameters.values()) for f in (sync, async_)]
    assert parameters[0] == parameters[1]
    texts = [inspect.getdoc(f) or "" for f in (sync, async_)]
    assert texts[0] == texts[1].replace("async ", "").replace("Async", "")
    assert not any(inspect.iscoroutinefunction(f) for f in (sync, async_))


@pytest.mark.parametrize("name", ["enter", "exit", "iter", "next"])
def test_the_two_streams_are_the_same(name: str) -> None:
    sync = getattr(PairingStream, f"__{name}__")
    async_ = getattr(AsyncPairingStream, f"__a{name}__")
    assert list(inspect.signature(sync).parameters) == list(inspect.signature(async_).parameters)
    assert get_type_hints(sync).get("return") == get_type_hints(async_).get("return")
    assert sync.__doc__ == async_.__doc__
    assert not inspect.iscoroutinefunction(sync)
    assert inspect.iscoroutinefunction(async_) is (name != "iter")


@pytest.mark.parametrize("name", [*NAMES, "__init__"])
def test_the_overloads_are_the_same_in_both_clients(name: str) -> None:
    sync = get_overloads(getattr(BackendClient, name))
    async_ = get_overloads(getattr(AsyncBackendClient, name))
    assert [described(o) for o in sync] == [described(o) for o in async_]
    assert len(sync) == (2 if name in OVERLOADED else 0)
    assert all(inspect.iscoroutinefunction(o) for o in async_)


@pytest.mark.parametrize("name", NAMES)
@pytest.mark.parametrize("kind", [BackendClient, AsyncBackendClient], ids=["sync", "async"])
def test_the_ide_has_the_text_of_each_argument(
    kind: type[BackendClient | AsyncBackendClient], name: str
) -> None:
    method = getattr(kind, name)
    text = inspect.getdoc(method) or ""
    for function in [method, *get_overloads(method)]:
        assert inspect.getdoc(function) == text
        parameters = list(inspect.signature(function).parameters.values())[1:]
        for parameter in parameters:
            assert f"\n    {parameter.name}: " in text
        # The id is the one argument by position, and the time limit is the last one.
        by_position = [p for p in parameters if p.kind is not p.KEYWORD_ONLY]
        assert [p.name[-3:] for p in by_position] in ([], ["_id"])
        assert parameters[-1].name == "time_limit"
        assert "\nRaises:\n    CalyxError: " in text
        returns = inspect.signature(function).return_annotation
        assert ("\nReturns:\n" in text) is (returns is not None)


def test_the_constructor_has_a_text_for_each_argument() -> None:
    text = inspect.getdoc(BackendClient.__init__) or ""
    for name in list(inspect.signature(BackendClient).parameters):
        assert f"\n    {name}: " in text
    assert BackendClient.__doc__
    assert AsyncBackendClient.__doc__
