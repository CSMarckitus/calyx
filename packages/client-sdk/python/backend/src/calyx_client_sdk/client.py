from ._call import make_config, prepare
from ._generated.methods import AsyncMethods, SyncMethods
from ._http import AsyncTransport, SyncTransport
from .pairing import FOLLOW_PAIRING, AsyncPairingStream, PairingStream


class BackendClient(SyncMethods):
    """A client for the HTTP API of the Calyx backend. `AsyncBackendClient` is for `asyncio`.

    Example:

        client = BackendClient("https://calyx.example.com", "cx_...")
        key = new_idempotency_key()  # Store the key before the call.
        sent = client.send_message(instance_id, to="34600000000", text="Hi", idempotency_key=key)
        print(sent.data.message_id)

    The client holds no connection: each call opens one and closes it. So there is nothing to
    close, and threads and forked processes can use one client. The SDK never repeats a request by
    itself.
    """

    def __init__(
        self,
        base_url: str,
        api_key: str | None = None,
        *,
        time_limit: float | None = None,
        ca_file: str | None = None,
    ) -> None:
        """Check the arguments. The SDK sends nothing before the first call.

        Args:
            base_url: The URL of the backend, for example `https://calyx.example.com`.
            api_key: The API key of a backend user. Keep it secret: use it only in a server program.
            time_limit: The time limit in seconds of each call with no `time_limit` of its own.
                Without it, each method has its own default.
            ca_file: The path of a PEM file with a private certificate authority. The SDK trusts it
                and the system certificates. The SDK reads the file at the first HTTPS call.

        Raises:
            CalyxError: An argument has a bad value. The code is invalid_config.
        """
        super().__init__(make_config(base_url, api_key, time_limit), SyncTransport(ca_file))

    def follow_pairing(self, instance_id: str, *, time_limit: float | None = None) -> PairingStream:
        """Follow the pairing of an instance as a stream of items.

        Example:

            with client.follow_pairing(instance_id) as stream:
                for item in stream:
                    match item:
                        case PairingUpdate(pairing=pairing):
                            show(pairing.qr_text or pairing.pairing_code)
                        case PairingDone():
                            print("Connected")
                        case PairingFailed(code=code, detail=detail):
                            print(code, detail)

        The connection opens at the first read, and it closes at the end of the block. A break does
        not cancel the pairing: `cancel_pairing` does. The SDK never opens the stream again. After a
        stream_failed error, follow the instance again, or read it with `get_instance`. A 404
        not_found on a new follow means that no pairing runs and that the backend keeps no failure.
        Read the instance with `get_instance`: the status CONNECTED means that the pairing is done.

        Args:
            instance_id: The id of the instance, a UUID. Any other value gives 404 not_found.
            time_limit: The time limit of the whole stream, in seconds. It starts at the first read,
                and it counts the code of your program between two reads. At its end, the next read
                raises timeout. Without it, the whole stream has no limit: the `time_limit` of
                the SDK client does not apply.

        Returns:
            PairingStream: The items: `PairingUpdate`, and then `PairingDone` or
                `PairingFailed` as the last one.

        Raises:
            CalyxError: The id is empty, "." or "..", or the time limit has a bad value
                (invalid_config). Each other error comes from a read of the stream, and
                ends it. Read `retry` of the error to know what to do. Errors of the backend: 401
                unauthenticated, 404 not_found (no pairing runs).
                A stream that breaks, that ends too early, or that sends nothing for 45 seconds
                gives stream_failed. No connection in 30 seconds, or a connection that fails
                before the stream starts, gives network_error.
        """
        prepared = prepare(self._config, FOLLOW_PAIRING, path_id=instance_id, time_limit=time_limit)
        return PairingStream(self._transport, prepared)


class AsyncBackendClient(AsyncMethods):
    """A client for the HTTP API of the Calyx backend, for `asyncio`. `BackendClient` is for sync
    programs.

    Example:

        client = AsyncBackendClient("https://calyx.example.com", "cx_...")
        key = new_idempotency_key()  # Store the key before the call.
        sent = await client.send_message(
            instance_id, to="34600000000", text="Hi", idempotency_key=key
        )
        print(sent.data.message_id)

    The client holds no connection: each call opens one and closes it. So there is nothing to
    close, and event loops, threads and forked processes can use one client. The SDK never repeats
    a request by itself.
    """

    def __init__(
        self,
        base_url: str,
        api_key: str | None = None,
        *,
        time_limit: float | None = None,
        ca_file: str | None = None,
    ) -> None:
        """Check the arguments. The SDK sends nothing before the first call.

        Args:
            base_url: The URL of the backend, for example `https://calyx.example.com`.
            api_key: The API key of a backend user. Keep it secret: use it only in a server program.
            time_limit: The time limit in seconds of each call with no `time_limit` of its own.
                Without it, each method has its own default.
            ca_file: The path of a PEM file with a private certificate authority. The SDK trusts it
                and the system certificates. The SDK reads the file at the first HTTPS call.

        Raises:
            CalyxError: An argument has a bad value. The code is invalid_config.
        """
        super().__init__(make_config(base_url, api_key, time_limit), AsyncTransport(ca_file))

    def follow_pairing(
        self, instance_id: str, *, time_limit: float | None = None
    ) -> AsyncPairingStream:
        """Follow the pairing of an instance as a stream of items.

        Example:

            async with client.follow_pairing(instance_id) as stream:
                async for item in stream:
                    match item:
                        case PairingUpdate(pairing=pairing):
                            show(pairing.qr_text or pairing.pairing_code)
                        case PairingDone():
                            print("Connected")
                        case PairingFailed(code=code, detail=detail):
                            print(code, detail)

        The connection opens at the first read, and it closes at the end of the block. A break does
        not cancel the pairing: `cancel_pairing` does. The SDK never opens the stream again. After a
        stream_failed error, follow the instance again, or read it with `get_instance`. A 404
        not_found on a new follow means that no pairing runs and that the backend keeps no failure.
        Read the instance with `get_instance`: the status CONNECTED means that the pairing is done.

        Args:
            instance_id: The id of the instance, a UUID. Any other value gives 404 not_found.
            time_limit: The time limit of the whole stream, in seconds. It starts at the first read,
                and it counts the code of your program between two reads. At its end, the next read
                raises timeout. Without it, the whole stream has no limit: the `time_limit` of
                the SDK client does not apply.

        Returns:
            AsyncPairingStream: The items: `PairingUpdate`, and then `PairingDone` or
                `PairingFailed` as the last one.

        Raises:
            CalyxError: The id is empty, "." or "..", or the time limit has a bad value
                (invalid_config). Each other error comes from a read of the stream, and
                ends it. Read `retry` of the error to know what to do. Errors of the backend: 401
                unauthenticated, 404 not_found (no pairing runs).
                A stream that breaks, that ends too early, or that sends nothing for 45 seconds
                gives stream_failed. No connection in 30 seconds, or a connection that fails
                before the stream starts, gives network_error.
        """
        prepared = prepare(self._config, FOLLOW_PAIRING, path_id=instance_id, time_limit=time_limit)
        return AsyncPairingStream(self._transport, prepared)
