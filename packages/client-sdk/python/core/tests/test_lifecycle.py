"""The arguments of a client, and its life from the first call to the end."""

import asyncio
import os
import re
import signal
import subprocess
import sys
import threading
import time
from collections.abc import Iterator
from concurrent.futures import ThreadPoolExecutor
from contextlib import contextmanager
from typing import Any

import grpc
import pytest
from calyx_client_sdk import CalyxError, ClientCode, CoreCode, RetryAdvice, Source
from certificates import Authority
from fake_core import (
    KEY,
    SEND,
    TOKEN,
    Call,
    FakeCall,
    FakeCore,
    RawConnection,
    answer_empty,
    check_client_error,
    check_config_error,
    raw_server,
    set_short_keepalive,
    start_proxy,
    wait_for_end,
)
from google.rpc.status_pb2 import Status

from calyx_client_sdk_core import AsyncCoreClient, CoreClient, _channel
from calyx_client_sdk_core.models import ListInstancesResponse

# The arguments of a client.

CLIENTS = pytest.mark.parametrize("kind", [CoreClient, AsyncCoreClient], ids=["sync", "async"])


@CLIENTS
@pytest.mark.parametrize(
    "address",
    [
        "core:50051",
        "core.internal:1",
        "calyx_core-1:65535",
        "127.0.0.1:50051",
        "[::1]:50051",
        "[2001:db8::1]:50051",
    ],
)
def test_a_good_address(kind: Any, address: str) -> None:
    kind(address, TOKEN)


@CLIENTS
@pytest.mark.parametrize(
    "address",
    [
        "",
        "core",
        "core:",
        ":50051",
        "core:0",
        "core:65536",
        "core:99999999",
        "core:５",
        "core:-1",
        "core:http",
        "http://core:50051",
        "https://core:50051",
        "dns:///core:50051",
        "unix:/tmp/core",
        "core:50051/",
        "user@core:50051",
        "::1:50051",
        "[::1]",
        "[]:50051",
        "co re:50051",
        "core:50051\n",
        "cañón:50051",
        "core%20:50051",
        "a:1,b:2",
        None,
        5,
        b"core:50051",
        ("core", 50051),
    ],
)
def test_a_bad_address(kind: Any, address: Any) -> None:
    with pytest.raises(CalyxError) as caught:
        kind(address, TOKEN)
    check_config_error(caught.value)
    assert "127.0.0.1:50051" in caught.value.detail


@CLIENTS
@pytest.mark.parametrize("token", ["", "a b", "a\nb", "a\0b", "a\x7f", "ñ", None, 5, b"token"])
def test_a_bad_token(kind: Any, token: Any) -> None:
    with pytest.raises(CalyxError) as caught:
        kind("core:50051", token, insecure=True)
    check_config_error(caught.value)
    assert "token" in caught.value.detail


@CLIENTS
@pytest.mark.parametrize("insecure", ["false", "no", 0, 1, None])
def test_insecure_must_be_a_bool(kind: Any, insecure: Any) -> None:
    # A text is true, so "false" would send the token in clear text.
    with pytest.raises(CalyxError) as caught:
        kind("core:50051", TOKEN, insecure=insecure)
    check_config_error(caught.value)


@CLIENTS
def test_insecure_with_a_ca_file(kind: Any) -> None:
    with pytest.raises(CalyxError) as caught:
        kind("core:50051", TOKEN, insecure=True, ca_file="ca.pem")
    check_config_error(caught.value)


@CLIENTS
def test_no_text_of_a_client_has_the_token(kind: Any) -> None:
    client = kind("core:50051", TOKEN, insecure=True)
    assert TOKEN not in repr(client) + repr(vars(client))


# The life of a client.


def test_one_client_uses_one_connection() -> None:
    with FakeCore(answer_empty) as core:
        with CoreClient(core.address, TOKEN, insecure=True) as client:
            with ThreadPoolExecutor(10) as pool:
                calls = [pool.submit(client.list_instances) for _ in range(50)]
                assert all(c.result() == ListInstancesResponse(instances=()) for c in calls)
        assert len(core.calls) == 50
        assert len({seen.peer for seen in core.calls}) == 1


def test_one_async_client_uses_one_connection() -> None:
    async def main(address: str) -> None:
        async with AsyncCoreClient(address, TOKEN, insecure=True) as client:
            results = await asyncio.gather(*(client.list_instances() for _ in range(50)))
            assert all(r == ListInstancesResponse(instances=()) for r in results)

    with FakeCore(answer_empty) as core:
        asyncio.run(main(core.address), debug=True)
        assert len(core.calls) == 50
        assert len({seen.peer for seen in core.calls}) == 1


def test_a_call_after_close() -> None:
    with FakeCore(answer_empty) as core:
        client = CoreClient(core.address, TOKEN, insecure=True)
        client.list_instances()
        client.close()
        client.close()
        with pytest.raises(CalyxError) as caught:
            client.send_plain_text(**SEND, idempotency_key=KEY)
        with client:
            with pytest.raises(CalyxError) as again:
                client.list_instances()
        assert len(core.calls) == 1
    check_client_error(caught.value, ClientCode.ABORTED, key=KEY)
    check_client_error(again.value, ClientCode.ABORTED)
    assert caught.value.detail == "The client is closed."


def test_an_async_call_after_close() -> None:
    async def main(address: str) -> None:
        client = AsyncCoreClient(address, TOKEN, insecure=True)
        await client.list_instances()
        await client.close()
        await client.close()
        with pytest.raises(CalyxError) as caught:
            await client.send_plain_text(**SEND, idempotency_key=KEY)
        check_client_error(caught.value, ClientCode.ABORTED, key=KEY)
        assert caught.value.detail == "The client is closed."
        async with client:
            with pytest.raises(CalyxError) as caught:
                await client.list_instances()
        check_client_error(caught.value, ClientCode.ABORTED)

    with FakeCore(answer_empty) as core:
        asyncio.run(main(core.address), debug=True)
        assert len(core.calls) == 1


def test_a_client_that_made_no_call() -> None:
    client = CoreClient("127.0.0.1:1", TOKEN, ca_file="/nope")
    client.close()
    with pytest.raises(CalyxError) as caught:
        client.list_instances()
    check_client_error(caught.value, ClientCode.ABORTED)

    async def main() -> None:
        other = AsyncCoreClient("127.0.0.1:1", TOKEN, ca_file="/nope")
        await other.close()
        with pytest.raises(CalyxError) as caught:
            await other.list_instances()
        check_client_error(caught.value, ClientCode.ABORTED)

    asyncio.run(main())


def test_the_block_closes_the_client_and_keeps_the_error() -> None:
    client = CoreClient("127.0.0.1:1", TOKEN, insecure=True)
    with pytest.raises(KeyError), client as entered:
        assert entered is client
        raise KeyError
    with pytest.raises(CalyxError, match="aborted"):
        client.list_instances()

    async def main() -> None:
        other = AsyncCoreClient("127.0.0.1:1", TOKEN, insecure=True)
        with pytest.raises(KeyError):
            async with other as entered:
                assert entered is other
                raise KeyError
        with pytest.raises(CalyxError, match="aborted"):
            await other.list_instances()

    asyncio.run(main())


def test_close_in_a_signal_handler_at_the_start_of_a_call(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # The handler runs in the thread of the call, between two steps.
    with FakeCore(answer_empty) as core:
        client = CoreClient(core.address, TOKEN, insecure=True)
        open_channel = client._channel._open

        def open_and_close(key: str | None) -> grpc.Channel:
            channel = open_channel(key)
            client.close()
            return channel

        monkeypatch.setattr(client._channel, "_open", open_and_close)
        with pytest.raises(CalyxError) as caught:
            client.send_plain_text(**SEND, idempotency_key=KEY)
        assert core.calls == []
    check_client_error(caught.value, ClientCode.ABORTED, key=KEY)
    assert caught.value.__context__ is None


@pytest.mark.parametrize("tls", [False, True])
def test_close_in_a_signal_handler_while_the_first_call_makes_the_channel(
    monkeypatch: pytest.MonkeyPatch, authority: Authority, tls: bool
) -> None:
    made: list[grpc.Channel] = []
    name = "secure_channel" if tls else "insecure_channel"
    make_channel = getattr(grpc, name)

    def make_and_close(*arguments: Any) -> grpc.Channel:
        channel = make_channel(*arguments)
        made.append(channel)
        client.close()
        return channel

    monkeypatch.setattr(grpc, name, make_and_close)
    with FakeCore(answer_empty) as core:
        options: Any = {"ca_file": authority.ca_file} if tls else {"insecure": True}
        client = CoreClient(core.address, TOKEN, **options)
        with pytest.raises(CalyxError) as caught:
            client.send_plain_text(**SEND, idempotency_key=KEY)
        assert core.calls == []
    check_client_error(caught.value, ClientCode.ABORTED, key=KEY)
    (channel,) = made
    # The channel is closed.
    with pytest.raises(ValueError, match="closed"):
        channel.unary_unary("/x")(b"")


def test_another_value_error_of_grpc_stays() -> None:
    with FakeCore(answer_empty) as core, CoreClient(core.address, TOKEN, insecure=True) as client:
        client.list_instances()
        channel = client._channel._channel
        assert channel is not None
        # Not by the client, so this is a fault of the SDK or of gRPC.
        channel.close()
        with pytest.raises(ValueError, match="closed"):
            client.list_instances()


@contextmanager
def waiting_core() -> Iterator[tuple[str, threading.Event]]:
    """Gives the address of a core that does not answer, and an event for its first call."""
    started = threading.Event()

    def handle(seen: FakeCall) -> None:
        started.set()
        seen.wait_for_end()

    with FakeCore(handle) as core:
        yield core.address, started
        assert all(seen.ended.wait(1) for seen in core.calls)


@contextmanager
def silent_core() -> Iterator[tuple[str, threading.Event]]:
    """The same, with a peer that only accepts the connection."""
    started = threading.Event()

    def handle(connection: RawConnection) -> None:
        started.set()
        connection.read_to_end()

    with raw_server(handle) as address:
        yield address, started


each_wait = pytest.mark.parametrize("make", [waiting_core, silent_core])


@each_wait
def test_close_stops_a_call(make: Any) -> None:
    with make() as (address, started), ThreadPoolExecutor() as pool:
        client = CoreClient(address, TOKEN, insecure=True)
        result = pool.submit(client.send_plain_text, **SEND, idempotency_key=KEY)
        assert started.wait(2)
        client.close()
        error = result.exception(2)
        assert isinstance(error, CalyxError)
        check_client_error(error, ClientCode.ABORTED, key=KEY)
        assert error.detail == "The client is closed."


@each_wait
def test_close_stops_an_async_call(make: Any) -> None:
    async def main(address: str, started: threading.Event) -> None:
        client = AsyncCoreClient(address, TOKEN, insecure=True)
        task = asyncio.create_task(client.send_plain_text(**SEND, idempotency_key=KEY))
        assert await asyncio.to_thread(started.wait, 2)
        await client.close()
        with pytest.raises(CalyxError) as caught:
            await task
        check_client_error(caught.value, ClientCode.ABORTED, key=KEY)
        assert caught.value.detail == "The client is closed."

    with make() as (address, started):
        asyncio.run(main(address, started), debug=True)


@each_wait
def test_a_cancellation_passes_unchanged(make: Any) -> None:
    async def main(address: str, started: threading.Event) -> None:
        async with AsyncCoreClient(address, TOKEN, insecure=True) as client:
            task = asyncio.create_task(client.list_instances())
            assert await asyncio.to_thread(started.wait, 2)
            assert task.cancel("stop")
            with pytest.raises(asyncio.CancelledError) as caught:
                await task
            assert caught.value.args == ("stop",)
            assert task.cancelled()

    with make() as (address, started):
        asyncio.run(main(address, started), debug=True)


def test_a_cancellation_ends_the_call_at_the_core() -> None:
    async def main(core: FakeCore) -> None:
        async with AsyncCoreClient(core.address, TOKEN, insecure=True) as client:
            with pytest.raises(TimeoutError):
                async with asyncio.timeout(0.5):
                    await client.list_instances()
            (seen,) = core.calls
            assert await asyncio.to_thread(seen.ended.wait, 1)

    with FakeCore(wait_for_end) as core:
        asyncio.run(main(core), debug=True)


def test_a_cancelled_status_of_the_core_is_no_cancellation(call: Call) -> None:
    with FakeCore(lambda seen: seen.fail(Status(code=1, message="Stop."))) as core:
        with pytest.raises(CalyxError) as caught:
            call(core.address, "list_instances")
    error = caught.value
    assert (error.source, error.code, error.detail) == (Source.CORE, CoreCode.CANCELLED, "Stop.")


@each_wait
def test_ctrl_c_stops_a_call(call: Call, make: Any) -> None:
    main = threading.main_thread().ident or 0
    with make() as (address, started):

        def interrupt() -> None:
            started.wait(2)
            time.sleep(0.2)
            signal.pthread_kill(main, signal.SIGINT)

        thread = threading.Thread(target=interrupt)
        thread.start()
        with pytest.raises(KeyboardInterrupt):
            call(address, "list_instances")
        thread.join()


def test_ctrl_c_ends_the_call_at_the_core() -> None:
    main = threading.main_thread().ident or 0

    def handle(seen: FakeCall) -> None:
        signal.pthread_kill(main, signal.SIGINT)
        seen.wait_for_end()

    with FakeCore(handle) as core, CoreClient(core.address, TOKEN, insecure=True) as client:
        with pytest.raises(KeyboardInterrupt) as caught:
            client.list_instances()
        (seen,) = core.calls
        # The exception is still here, and it must not keep the call.
        assert seen.ended.wait(1)
        assert caught.type is KeyboardInterrupt


def test_the_keepalive_values() -> None:
    options = dict(_channel._OPTIONS)
    assert options["grpc.keepalive_time_ms"] == 120_000
    assert options["grpc.keepalive_timeout_ms"] == 20_000
    # The core counts a ping with no call as a fault.
    assert options["grpc.keepalive_permit_without_calls"] == 0
    assert options["grpc.http2.ping_timeout_ms"] == 20_000
    assert options["grpc.http2.max_pings_without_data"] == 0
    # The core closes a connection that pings more often than once a minute (keepalivePolicy in
    # apps/core/internal/grpcserver).
    core_min_time = 60_000
    assert options["grpc.keepalive_time_ms"] > core_min_time


def test_a_send_to_a_core_host_that_disappears(call: Call, monkeypatch: pytest.MonkeyPatch) -> None:
    def handle(seen: FakeCall) -> None:
        proxy.cut()
        seen.wait_for_end()

    set_short_keepalive(monkeypatch)
    with FakeCore(handle) as core, start_proxy(core) as (proxy, address):
        start = time.monotonic()
        with pytest.raises(CalyxError) as raised:
            # Without the keepalive, the call ends at this limit.
            call(address, "send_plain_text", **SEND, idempotency_key=KEY, time_limit=5)
        assert time.monotonic() - start < 3
        assert len(core.calls) == 1
    error = raised.value
    assert (error.source, error.code) == (Source.CORE, CoreCode.UNAVAILABLE)
    assert (error.retry, error.idempotency_key) == (RetryAdvice.RETRY_NOW, KEY)


FORK = """
import os, sys
from calyx_client_sdk_core import CoreClient
client = CoreClient(sys.argv[1], sys.argv[2], insecure=True)
# With a thread, os.fork() gives a warning, and here it is an error.
if child := os.fork():
    _, status = os.waitpid(child, 0)
    client.list_instances()
    sys.exit(os.waitstatus_to_exitcode(status))
client.list_instances()
os._exit(0)
"""


def test_the_import_and_the_constructor_make_nothing_of_grpc() -> None:
    with FakeCore(answer_empty) as core:
        command = [sys.executable, "-W", "error", "-c", FORK, core.address, TOKEN]
        done = subprocess.run(command, timeout=8, capture_output=True, text=True, check=False)
        assert (done.returncode, done.stdout, done.stderr) == (0, "", "")
        assert len(core.calls) == 2


def test_no_call_writes_to_the_terminal() -> None:
    code = (
        "import sys; from calyx_client_sdk import CalyxError\n"
        "from calyx_client_sdk_core import CoreClient\n"
        "with CoreClient(sys.argv[1], 'token', insecure=True) as client:\n"
        "    try: client.list_instances()\n"
        "    except CalyxError as error: print(error.code)\n"
    )
    command = [sys.executable, "-W", "error", "-c", code, "127.0.0.1:1"]
    done = subprocess.run(command, timeout=8, capture_output=True, text=True, check=False)
    assert (done.stdout, done.stderr) == ("UNAVAILABLE\n", "")


def test_the_environment_of_the_tests_has_no_proxy() -> None:
    assert not [name for name in os.environ if re.search("proxy", name, re.IGNORECASE)]
