"""The fork and the event loop (the rules of the gRPC objects)."""

import asyncio
import os
import subprocess
import sys
import textwrap
import threading
from collections.abc import Iterable
from typing import Any

import pytest
from calyx_client_sdk import CalyxError, ClientCode
from fake_core import (
    EVENT,
    EVENT_READ,
    KEY,
    MAX_WAIT,
    SEND,
    TOKEN,
    Answer,
    FakeCall,
    FakeCore,
    answer_empty,
    check_client_error,
    check_config_error,
    sending,
)

from calyx_client_sdk_core import AsyncCoreClient, CoreClient, _channel

# The fork. Each test runs a program of its own, because the rule is for a whole process.

PROGRAM = """
import asyncio, gc, os, sys, threading
from calyx_client_sdk import CalyxError
from calyx_client_sdk_core import AsyncCoreClient, CoreClient

address, token = sys.argv[1:]

def make():
    return CoreClient(address, token, insecure=True)

def make_async():
    return AsyncCoreClient(address, token, insecure=True)

def check(function, *arguments):
    try:
        function(*arguments)
    except CalyxError as error:
        assert (error.source, error.code) == ("client", "invalid_config"), error
        assert error.detail == "Make no core call before the fork.", error.detail
    else:
        raise AssertionError("no error")

def list_async(client):
    asyncio.run(client.list_instances())

def read_async(stream):
    async def main():
        await anext(stream)
    asyncio.run(main())

def in_child(function):
    child = os.fork()
    if child == 0:
        code = 1
        try:
            function()
            code = 0
        finally:
            sys.stderr.flush()
            EXIT(code)
    _, status = os.waitpid(child, 0)
    assert status == 0, status
"""


def run(body: str, *, exit: str = "os._exit", uses_async: bool = False) -> FakeCore:
    """Runs a program. `EXIT` is how its child process ends. `uses_async`: the program has an async
    call before its fork."""
    unary = threading.Event()

    def answer(seen: FakeCall) -> Iterable[Answer]:
        if "Subscribe" not in seen.path:
            unary.set()
            yield b""
            return
        yield EVENT
        # The program makes a call when its child process is gone. A stream that ended before gets
        # no second event.
        unary.wait(MAX_WAIT)
        if not seen.ended.wait(0.2):
            yield EVENT
        seen.wait_for_end()

    program = PROGRAM.replace("EXIT", exit) + textwrap.dedent(body)
    # With a thread, os.fork() gives a warning.
    command = [sys.executable, "-W", "error", "-W", "ignore:This process:DeprecationWarning"]
    # gRPC writes a line at each fork of a process that uses it.
    environment = {**os.environ, "GRPC_VERBOSITY": "ERROR"}
    environment.pop("GRPC_ENABLE_FORK_SUPPORT", None)
    if uses_async:
        # After an async call, the fork handlers of gRPC sometimes stop the child process (signal
        # 6), also when it makes no call. This is no solution for a program: see the progress note.
        environment["GRPC_ENABLE_FORK_SUPPORT"] = "0"
    with FakeCore(answer) as core:
        done = subprocess.run(
            [*command, "-c", program, core.address, TOKEN],
            timeout=8,
            capture_output=True,
            text=True,
            check=False,
            env=environment,
        )
        assert (done.returncode, done.stdout, done.stderr) == (0, "", "")
    return core


# Gunicorn ends a process with sys.exit, and multiprocessing with os._exit.
each_exit = pytest.mark.parametrize("exit", ["os._exit", "sys.exit"])


@each_exit
def test_a_fork_after_a_call(exit: str) -> None:
    core = run(
        """
        client, unused, unused_async = make(), make(), make_async()
        client.list_instances()

        def child():
            check(client.list_instances)
            check(unused.list_instances)
            check(list_async, unused_async)
            check(make().list_instances)
            check(list_async, make_async())
            check(next, make().subscribe())
            check(read_async, make_async().subscribe())
            check(lambda: make().send_plain_text(instance_name="i", to="1", text="t"))
            # Closing is possible, and it changes nothing.
            client.close()
            unused.close()
            asyncio.run(unused_async.close())
            check(client.list_instances)

        in_child(child)
        # The parent goes on, with the same connection.
        client.list_instances()
        client.close()
        """,
        exit=exit,
    )
    assert len(core.calls) == 2
    assert core.calls[0].peer == core.calls[1].peer


# Not with sys.exit: 1 of 160 runs did not end. A program with `grpc.aio` alone also hangs (4 of 400
# on 3.14): the child waits for a lock of gRPC or Python that no thread opens.
def test_a_fork_after_an_async_call() -> None:
    core = run(
        """
        client = make_async()
        list_async(client)

        def child():
            check(list_async, client)
            check(make().list_instances)
            check(list_async, make_async())
            check(read_async, client.subscribe())
            asyncio.run(client.close())
            check(list_async, client)

        in_child(child)
        """,
        uses_async=True,
    )
    assert len(core.calls) == 1


def test_the_error_of_a_send_has_its_key() -> None:
    run(
        """
        make().list_instances()

        def child():
            try:
                make().send_plain_text(instance_name="i", to="1", text="t", idempotency_key="k")
            except CalyxError as error:
                assert (error.code, error.idempotency_key) == ("invalid_config", "k")
            else:
                raise AssertionError("no error")

        in_child(child)
        """
    )


def test_a_stream_before_the_fork() -> None:
    # A stream that the program did not read is no gRPC use.
    core = run(
        """
        client = make()
        stream = client.subscribe()

        def child():
            next(stream)
            stream.__exit__(None, None, None)

        in_child(child)
        next(stream)
        client.list_instances()
        in_child(lambda: check(make().list_instances))
        """
    )
    assert len(core.calls) == 3


DROP = "del globals()['stream']; gc.collect()"
# A child process that keeps the stream, that drops it, and that drops all at its end.
each_end = pytest.mark.parametrize(
    ("exit", "end"),
    [("os._exit", "pass"), ("os._exit", DROP), ("sys.exit", "pass")],
    ids=["keep", "drop", "sys.exit"],
)


@each_end
def test_a_stream_that_runs_at_the_fork(exit: str, end: str) -> None:
    core = run(
        f"""
        client = make()
        stream = client.subscribe()
        next(stream)

        def child():
            check(next, stream)
            assert next(stream, None) is None
            stream.__exit__(None, None, None)
            client.close()
            {end}

        in_child(child)
        client.list_instances()
        # The child process did not end the stream of the parent.
        assert next(stream).id
        client.close()
        """,
        exit=exit,
    )
    assert len(core.calls) == 2
    assert core.calls[0].peer == core.calls[1].peer


# Not with sys.exit: it closes the event loop that the child has, and the parent loses its wake-ups,
# because the two processes share one epoll. This occurs with asyncio alone too.
@pytest.mark.parametrize("end", ["pass", DROP], ids=["keep", "drop"])
def test_an_async_stream_that_runs_at_the_fork(end: str) -> None:
    core = run(
        f"""
        client = make_async()
        stream = client.subscribe()

        async def read():
            return await anext(stream, None)

        async def close():
            async with client, stream:
                pass

        def child():
            check(asyncio.run, read())
            assert asyncio.run(read()) is None
            asyncio.run(close())
            {end}

        with asyncio.Runner() as runner:
            runner.run(read())
            # asyncio itself does not work in a child process of a loop that runs.
            in_child(child)
            runner.run(client.list_instances())
            assert runner.run(read()).id
            runner.run(close())
        """,
        uses_async=True,
    )
    assert len(core.calls) == 2
    assert core.calls[0].peer == core.calls[1].peer


def test_a_call_that_runs_at_the_fork() -> None:
    # A thread has the lock of the client for a moment at the start of each call. The child process
    # has a copy of the lock that nobody opens.
    core = run(
        """
        client = make()
        client.list_instances()
        lock = client._channel._lock
        lock.acquire()

        def child():
            def use():
                check(client.list_instances)
                client.close()
            # Not the thread that has the lock.
            thread = threading.Thread(target=use)
            thread.start()
            thread.join(2)
            assert not thread.is_alive()

        in_child(child)
        lock.release()
        """
    )
    assert len(core.calls) == 1


def test_a_fork_before_the_first_call() -> None:
    core = run(
        """
        client, other = make(), make_async()

        def child():
            client.list_instances()
            list_async(other)
            client.close()

        in_child(child)
        in_child(child)
        client.list_instances()
        list_async(other)
        """
    )
    assert len(core.calls) == 6


def test_a_fork_of_a_process_that_the_program_forked() -> None:
    core = run(
        """
        client = make()

        def child():
            client.list_instances()
            in_child(lambda: check(make().list_instances))

        in_child(child)
        in_child(child)
        """
    )
    assert len(core.calls) == 2


def test_a_forked_process_does_not_use_the_connection(monkeypatch: pytest.MonkeyPatch) -> None:
    with FakeCore(sending(EVENT, wait=True)) as core:
        with CoreClient(core.address, TOKEN, insecure=True) as client:
            stream = client.subscribe()
            next(stream)
            with monkeypatch.context() as patch:
                # As in a child process.
                patch.setattr(_channel, "_first_process", os.getpid() + 1)
                stream.__exit__(None, None, None)
                client.close()
                assert not core.calls[0].ended.wait(0.2)
            stream.__exit__(None, None, None)
            assert core.calls[0].ended.wait(1)


def test_a_forked_process_does_not_use_the_async_connection(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    async def main() -> None:
        async with AsyncCoreClient(core.address, TOKEN, insecure=True) as client:
            stream = client.subscribe()
            await anext(stream)
            with monkeypatch.context() as patch:
                patch.setattr(_channel, "_first_process", os.getpid() + 1)
                await stream.__aexit__(None, None, None)
                await client.close()
                assert not await asyncio.to_thread(core.calls[0].ended.wait, 0.2)
            await stream.__aexit__(None, None, None)
            assert await asyncio.to_thread(core.calls[0].ended.wait, 1)

    with FakeCore(sending(EVENT, wait=True)) as core:
        asyncio.run(main(), debug=True)


# The event loop.


def check_loop_error(error: CalyxError, key: str | None = None) -> None:
    check_config_error(error, key)
    assert error.detail == "Use an AsyncCoreClient in one event loop only."


def test_a_second_event_loop() -> None:
    async def first() -> None:
        await client.list_instances()

    async def second() -> None:
        with pytest.raises(CalyxError) as caught:
            await client.send_plain_text(**SEND, idempotency_key=KEY)
        check_loop_error(caught.value, KEY)
        stream = client.subscribe()
        with pytest.raises(CalyxError) as caught:
            await anext(stream)
        check_loop_error(caught.value)
        assert await anext(stream, None) is None
        # Another client is possible.
        async with AsyncCoreClient(core.address, TOKEN, insecure=True) as other:
            await other.list_instances()

    with FakeCore(answer_empty) as core:
        client = AsyncCoreClient(core.address, TOKEN, insecure=True)
        asyncio.run(first(), debug=True)
        asyncio.run(second(), debug=True)
        assert len(core.calls) == 2


def test_the_first_call_chooses_the_event_loop() -> None:
    async def make() -> tuple[AsyncCoreClient, Any]:
        client = AsyncCoreClient(core.address, TOKEN, insecure=True)
        return client, client.subscribe()

    async def use(client: AsyncCoreClient, stream: Any) -> None:
        async with client, stream:
            assert await anext(stream) == EVENT_READ

    with FakeCore(sending(EVENT, wait=True)) as core:
        asyncio.run(use(*asyncio.run(make())), debug=True)


@pytest.mark.parametrize("calls", [0, 1])
def test_close_in_a_second_event_loop(calls: int, caplog: pytest.LogCaptureFixture) -> None:
    async def first() -> None:
        for _ in range(calls):
            await client.list_instances()

    async def second() -> None:
        async with client:
            pass
        await client.close()
        with pytest.raises(CalyxError) as caught:
            await client.list_instances()
        check_client_error(caught.value, ClientCode.ABORTED)

    with FakeCore(answer_empty) as core:
        client = AsyncCoreClient(core.address, TOKEN, insecure=True)
        asyncio.run(first(), debug=True)
        asyncio.run(second(), debug=True)
    assert caplog.records == []


def test_a_stream_of_an_event_loop_that_ended(caplog: pytest.LogCaptureFixture) -> None:
    async def first() -> Any:
        stream = client.subscribe()
        assert await anext(stream) == EVENT_READ
        return stream

    async def second(stream: Any) -> None:
        async with stream:
            with pytest.raises(CalyxError) as caught:
                await anext(stream)
            check_loop_error(caught.value)
        assert await asyncio.to_thread(core.calls[0].ended.wait, 1)
        assert await anext(stream, None) is None
        await client.close()

    with FakeCore(sending(EVENT, EVENT, wait=True)) as core:
        client = AsyncCoreClient(core.address, TOKEN, insecure=True)
        asyncio.run(second(asyncio.run(first(), debug=True)), debug=True)
    assert caplog.records == []


class LoopThread:
    """An event loop that runs in another thread."""

    def __init__(self) -> None:
        self.loop = asyncio.new_event_loop()
        self.loop.set_debug(True)
        self.thread = threading.Thread(target=self.loop.run_forever)

    def __enter__(self) -> "LoopThread":
        self.thread.start()
        return self

    def __exit__(self, *details: Any) -> None:
        self.loop.call_soon_threadsafe(self.loop.stop)
        self.thread.join()
        self.loop.close()

    def submit(self, step: Any) -> Any:
        return asyncio.run_coroutine_threadsafe(step, self.loop)


def test_close_while_the_event_loop_runs_in_another_thread() -> None:
    started = threading.Event()

    def handle(seen: FakeCall) -> None:
        started.set()
        seen.wait_for_end()

    with FakeCore(handle) as core, LoopThread() as other:
        client = AsyncCoreClient(core.address, TOKEN, insecure=True)
        call = other.submit(client.list_instances())
        assert started.wait(2)
        asyncio.run(client.close(), debug=True)
        error = call.exception(2)
        assert isinstance(error, CalyxError)
        check_client_error(error, ClientCode.ABORTED)
        assert core.calls[0].ended.wait(1)


def test_a_stream_while_its_event_loop_runs_in_another_thread() -> None:
    async def read(stream: Any) -> Any:
        return await anext(stream, None)

    async def use(stream: Any) -> None:
        async with stream:
            with pytest.raises(CalyxError) as caught:
                await anext(stream)
            check_loop_error(caught.value)

    with FakeCore(sending(EVENT, wait=True)) as core, LoopThread() as other:
        client = AsyncCoreClient(core.address, TOKEN, insecure=True)
        stream = client.subscribe()
        assert other.submit(read(stream)).result(2) == EVENT_READ
        waits = other.submit(read(stream))
        asyncio.run(use(stream), debug=True)
        assert waits.result(2) is None
        assert core.calls[0].ended.wait(1)
        other.submit(client.close()).result(2)


def test_a_stream_stops_in_its_own_event_loop() -> None:
    threads: list[int] = []

    class FakeGrpcCall:
        def cancel(self) -> None:
            threads.append(threading.get_ident())

    async def stop() -> None:
        call: Any = FakeGrpcCall()
        client._channel.stop(call)

    with FakeCore(answer_empty) as core, LoopThread() as other:
        client = AsyncCoreClient(core.address, TOKEN, insecure=True)
        other.submit(client.list_instances()).result(2)
        asyncio.run(stop())
        # The loop runs the cancellation before this.
        other.submit(client.close()).result(2)
        assert threads == [other.thread.ident]


class EndedLoop:
    """An event loop that closes after the SDK saw that it runs."""

    def is_running(self) -> bool:
        return True

    def call_soon_threadsafe(self, *arguments: Any) -> None:
        raise RuntimeError("Event loop is closed")


def test_the_event_loop_closes_while_the_sdk_uses_it() -> None:
    async def main() -> None:
        client = AsyncCoreClient(core.address, TOKEN, insecure=True)
        streams = [client.subscribe(), client.subscribe()]
        for stream in streams:
            await anext(stream)
        loop: Any = EndedLoop()
        client._channel._loop = loop
        await streams[0].__aexit__(None, None, None)
        assert await asyncio.to_thread(core.calls[0].ended.wait, 1)
        await client.close()
        assert await asyncio.to_thread(core.calls[1].ended.wait, 1)

    with FakeCore(sending(EVENT, wait=True)) as core:
        asyncio.run(main(), debug=True)


def test_a_sync_client_has_no_event_loop() -> None:
    async def main() -> None:
        client.list_instances()

    with FakeCore(answer_empty) as core, CoreClient(core.address, TOKEN, insecure=True) as client:
        asyncio.run(main())
        asyncio.run(main())
        assert len(core.calls) == 2
