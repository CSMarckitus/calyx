from collections.abc import Sequence
from types import TracebackType
from typing import Self

from ._base import plan_stream
from ._channel import make_config
from ._generated import convert
from ._generated.methods import CREATE_INSTANCE, SUBSCRIBE, AsyncCoreMethods, SyncCoreMethods
from ._generated.models import MetaCloudSettings, WebhookConfig, WhatsAppWebSettings
from .streams import (
    AsyncEventStream,
    AsyncInstanceStream,
    EventStream,
    InstanceStream,
    read_core_event,
)


class CoreClient(SyncCoreMethods):
    """A client for the gRPC API of the Calyx core. `AsyncCoreClient` is for `asyncio`.

    Example:

        with CoreClient("127.0.0.1:50051", token, insecure=True) as client:
            key = new_idempotency_key()  # Store the key before the call.
            sent = client.send_plain_text(
                instance_name="sales", to="34600000000", text="Hi", idempotency_key=key
            )
            print(sent.data.message_id)

    The core has no users and no tenants. Use this client only on the private network of the core.

    The first call opens one connection, and threads can use it together. It stays open until
    `close`, or until the end of the `with` block. Make no core call before a fork: after one, each
    call of the forked process gives invalid_config. The SDK never repeats a call by itself.
    """

    def __init__(
        self,
        address: str,
        token: str,
        *,
        insecure: bool = False,
        ca_file: str | None = None,
        time_limit: float | None = None,
    ) -> None:
        """Check the arguments. The SDK sends nothing before the first call.

        Args:
            address: The host and the port of the core, for example `127.0.0.1:50051`. The port is
                the `GRPC_PORT` of the core.
            token: The core token: the `GRPC_AUTH_TOKEN` of the core. Keep it secret.
            insecure: `True` uses no TLS. The token and the messages then travel in clear text, so
                use it only on a private network that you trust.
            ca_file: The path of a PEM file with a private certificate authority. The SDK trusts it
                and the system certificates. The SDK reads the file at the first call.
            time_limit: The time limit in seconds of each call with no `time_limit` of its own.
                Without it, each method has its own default.

        Raises:
            CalyxError: An argument has a bad value. The code is invalid_config.
        """
        super().__init__(make_config(address, token, insecure, ca_file, time_limit))

    def create_instance(
        self,
        instance_name: str,
        *,
        provider: WhatsAppWebSettings | MetaCloudSettings,
        webhooks: Sequence[WebhookConfig] = (),
        is_used_only_for_send_messages: bool | None = None,
        time_limit: float | None = None,
    ) -> InstanceStream:
        """Create an instance, or authenticate one with the status NEEDS_AUTH again.

        Example:

            provider = WhatsAppWebSettings(locale="es-ES")
            with client.create_instance("sales", provider=provider) as stream:
                for update in stream:
                    if update.status == InstanceStatus.CONNECTED:
                        print("Connected")
                    elif update.pairing_code:
                        show(update.pairing_code)

        The call starts at the first read, and it stops at the end of the block. For WHATSAPP_WEB
        the stream is the pairing. Each QR code is a new update, and the last update is CONNECTED.
        When your program stops before it, the core stops the pairing and the instance stays in
        NEEDS_AUTH. META_CLOUD gives one update. The SDK never opens the stream again. After an
        error, read the instance with `get_instance_status`. When it is in NEEDS_AUTH, call this
        method again.

        A second pairing writes the new settings before it starts, so a pairing that fails keeps
        them. To remove all webhooks, use `update_instance_config`.

        Args:
            instance_name: The name of the instance in Calyx, unique and not empty. WhatsApp does
                not see it.
            provider: `WhatsAppWebSettings` connects through WhatsApp Web, which is unofficial. A
                new pairing needs its `locale`. `MetaCloudSettings` connects through the official
                Meta Cloud API. The class chooses the provider.
            webhooks: The webhooks, at most one per event, and none for an instance used only for
                sending. Empty keeps the stored webhooks of a second pairing.
            is_used_only_for_send_messages: WHATSAPP_WEB only: True connects only to send. `None` is
                False for a new instance, and it keeps the stored value of a second pairing.
            time_limit: The time limit of the whole stream, in seconds. It starts at the first read,
                and it counts the code of your program between two reads. At its end, the next read
                raises timeout. Without it, the whole stream has no limit: the `time_limit` of
                the SDK client does not apply.

        Returns:
            InstanceStream: The updates, as `CreateInstanceResponse` objects.

        Raises:
            CalyxError: An argument has a bad value (invalid_config). Each other error comes from a
                read of the stream, and ends it. Read `retry` of the error to know what to do.
                Compare `code` with `CoreCode` and `ClientCode`. No connection, or a connection that
                breaks, gives `CoreCode.UNAVAILABLE`.
        """
        plan = plan_stream(
            self._config,
            CREATE_INSTANCE,
            lambda: convert.create_instance_request(
                instance_name=instance_name,
                is_used_only_for_send_messages=is_used_only_for_send_messages,
                webhooks=webhooks,
                provider=provider,
            ),
            convert.read_create_instance_response,
            time_limit=time_limit,
            endless=False,
        )
        return InstanceStream(self._channel, plan)

    def subscribe(
        self,
        *,
        instance_names: Sequence[str] = (),
        event_types: Sequence[str] = (),
        time_limit: float | None = None,
    ) -> EventStream:
        """Follow the events of the instances, from the moment of the first read.

        Example:

            with client.subscribe(event_types=["message.received"]) as stream:
                for event in stream:
                    match event:
                        case MessageReceivedEvent(data=message):
                            print(message.from_, message.text)

        The call starts at the first read, and it stops at the end of the block. The core never ends
        the stream, and the SDK never opens it again: after an error, call this method again.

        The core delivers each event at most once. An event that occurs while no stream is open is
        lost. The core keeps 256 events for a slow program, and drops the events after them. A
        program that must not lose an event uses `webhooks` of `create_instance` or
        `update_instance_config`, and a core with `REDIS_URL`. The core then retries each delivery.

        Use `match` or `isinstance` on the class of the event. mypy does not narrow the event after
        a comparison of its `event` field.

        Args:
            instance_names: The names of the instances. Empty means every instance.
            event_types: The names of the events, for example message.received. Empty means every
                event. An event must match both lists.
            time_limit: The time limit of the whole stream, in seconds. It starts at the first read,
                and it counts the code of your program between two reads. At its end, the next read
                raises timeout. Without it, the whole stream has no limit: the `time_limit` of
                the SDK client does not apply.

        Returns:
            EventStream: The events. Each one is an `Event` of `calyx_client_sdk`, as for a
                webhook: a `MessageReceivedEvent`, a `MessageStatusEvent`, a
                `ConnectionUpdateEvent`, a `BulkFinishedEvent`, an `OtherEvent` or an
                `InvalidPayloadEvent`.

        Raises:
            CalyxError: An argument has a bad value (invalid_config). Each other error comes from a
                read of the stream, and ends it. Read `retry` of the error to know what to do.
                Compare `code` with `CoreCode` and `ClientCode`. No connection, or a connection that
                breaks, gives `CoreCode.UNAVAILABLE`. A stream that the core ends with no error
                gives stream_failed. An event that is not valid gives invalid_event.
        """
        plan = plan_stream(
            self._config,
            SUBSCRIBE,
            lambda: convert.subscribe_request(
                instance_names=instance_names, event_types=event_types
            ),
            read_core_event,
            time_limit=time_limit,
            endless=True,
        )
        return EventStream(self._channel, plan)

    def close(self) -> None:
        """Close the connection. A call that runs, and each later call, gives aborted.

        A second `close` does nothing. The result of a send that it stops is unknown.
        """
        self._channel.close()

    def __enter__(self) -> Self:
        return self

    def __exit__(
        self,
        kind: type[BaseException] | None,
        error: BaseException | None,
        trace: TracebackType | None,
    ) -> None:
        self.close()


class AsyncCoreClient(AsyncCoreMethods):
    """A client for the gRPC API of the Calyx core, for `asyncio`. `CoreClient` is for sync programs.

    Example:

        async with AsyncCoreClient("127.0.0.1:50051", token, insecure=True) as client:
            key = new_idempotency_key()  # Store the key before the call.
            sent = await client.send_plain_text(
                instance_name="sales", to="34600000000", text="Hi", idempotency_key=key
            )
            print(sent.data.message_id)

    The core has no users and no tenants. Use this client only on the private network of the core.

    The first call opens one connection. It stays open until `close`, or until the end of the
    `async with` block. Use a client in one event loop only: a call in another event loop gives
    invalid_config. Make no core call before a fork: after one, each call of the forked process
    gives invalid_config. The SDK never repeats a call by itself.
    """

    def __init__(
        self,
        address: str,
        token: str,
        *,
        insecure: bool = False,
        ca_file: str | None = None,
        time_limit: float | None = None,
    ) -> None:
        """Check the arguments. The SDK sends nothing before the first call.

        Args:
            address: The host and the port of the core, for example `127.0.0.1:50051`. The port is
                the `GRPC_PORT` of the core.
            token: The core token: the `GRPC_AUTH_TOKEN` of the core. Keep it secret.
            insecure: `True` uses no TLS. The token and the messages then travel in clear text, so
                use it only on a private network that you trust.
            ca_file: The path of a PEM file with a private certificate authority. The SDK trusts it
                and the system certificates. The SDK reads the file at the first call.
            time_limit: The time limit in seconds of each call with no `time_limit` of its own.
                Without it, each method has its own default.

        Raises:
            CalyxError: An argument has a bad value. The code is invalid_config.
        """
        super().__init__(make_config(address, token, insecure, ca_file, time_limit))

    def create_instance(
        self,
        instance_name: str,
        *,
        provider: WhatsAppWebSettings | MetaCloudSettings,
        webhooks: Sequence[WebhookConfig] = (),
        is_used_only_for_send_messages: bool | None = None,
        time_limit: float | None = None,
    ) -> AsyncInstanceStream:
        """Create an instance, or authenticate one with the status NEEDS_AUTH again.

        Example:

            provider = WhatsAppWebSettings(locale="es-ES")
            async with client.create_instance("sales", provider=provider) as stream:
                async for update in stream:
                    if update.status == InstanceStatus.CONNECTED:
                        print("Connected")
                    elif update.pairing_code:
                        show(update.pairing_code)

        The call starts at the first read, and it stops at the end of the block. For WHATSAPP_WEB
        the stream is the pairing. Each QR code is a new update, and the last update is CONNECTED.
        When your program stops before it, the core stops the pairing and the instance stays in
        NEEDS_AUTH. META_CLOUD gives one update. The SDK never opens the stream again. After an
        error, read the instance with `get_instance_status`. When it is in NEEDS_AUTH, call this
        method again.

        A second pairing writes the new settings before it starts, so a pairing that fails keeps
        them. To remove all webhooks, use `update_instance_config`.

        Args:
            instance_name: The name of the instance in Calyx, unique and not empty. WhatsApp does
                not see it.
            provider: `WhatsAppWebSettings` connects through WhatsApp Web, which is unofficial. A
                new pairing needs its `locale`. `MetaCloudSettings` connects through the official
                Meta Cloud API. The class chooses the provider.
            webhooks: The webhooks, at most one per event, and none for an instance used only for
                sending. Empty keeps the stored webhooks of a second pairing.
            is_used_only_for_send_messages: WHATSAPP_WEB only: True connects only to send. `None` is
                False for a new instance, and it keeps the stored value of a second pairing.
            time_limit: The time limit of the whole stream, in seconds. It starts at the first read,
                and it counts the code of your program between two reads. At its end, the next read
                raises timeout. Without it, the whole stream has no limit: the `time_limit` of
                the SDK client does not apply.

        Returns:
            AsyncInstanceStream: The updates, as `CreateInstanceResponse` objects.

        Raises:
            CalyxError: An argument has a bad value (invalid_config). Each other error comes from a
                read of the stream, and ends it. Read `retry` of the error to know what to do.
                Compare `code` with `CoreCode` and `ClientCode`. No connection, or a connection that
                breaks, gives `CoreCode.UNAVAILABLE`.
        """
        plan = plan_stream(
            self._config,
            CREATE_INSTANCE,
            lambda: convert.create_instance_request(
                instance_name=instance_name,
                is_used_only_for_send_messages=is_used_only_for_send_messages,
                webhooks=webhooks,
                provider=provider,
            ),
            convert.read_create_instance_response,
            time_limit=time_limit,
            endless=False,
        )
        return AsyncInstanceStream(self._channel, plan)

    def subscribe(
        self,
        *,
        instance_names: Sequence[str] = (),
        event_types: Sequence[str] = (),
        time_limit: float | None = None,
    ) -> AsyncEventStream:
        """Follow the events of the instances, from the moment of the first read.

        Example:

            async with client.subscribe(event_types=["message.received"]) as stream:
                async for event in stream:
                    match event:
                        case MessageReceivedEvent(data=message):
                            print(message.from_, message.text)

        The call starts at the first read, and it stops at the end of the block. The core never ends
        the stream, and the SDK never opens it again: after an error, call this method again.

        The core delivers each event at most once. An event that occurs while no stream is open is
        lost. The core keeps 256 events for a slow program, and drops the events after them. A
        program that must not lose an event uses `webhooks` of `create_instance` or
        `update_instance_config`, and a core with `REDIS_URL`. The core then retries each delivery.

        Use `match` or `isinstance` on the class of the event. mypy does not narrow the event after
        a comparison of its `event` field.

        Args:
            instance_names: The names of the instances. Empty means every instance.
            event_types: The names of the events, for example message.received. Empty means every
                event. An event must match both lists.
            time_limit: The time limit of the whole stream, in seconds. It starts at the first read,
                and it counts the code of your program between two reads. At its end, the next read
                raises timeout. Without it, the whole stream has no limit: the `time_limit` of
                the SDK client does not apply.

        Returns:
            AsyncEventStream: The events. Each one is an `Event` of `calyx_client_sdk`, as for a
                webhook: a `MessageReceivedEvent`, a `MessageStatusEvent`, a
                `ConnectionUpdateEvent`, a `BulkFinishedEvent`, an `OtherEvent` or an
                `InvalidPayloadEvent`.

        Raises:
            CalyxError: An argument has a bad value (invalid_config). Each other error comes from a
                read of the stream, and ends it. Read `retry` of the error to know what to do.
                Compare `code` with `CoreCode` and `ClientCode`. No connection, or a connection that
                breaks, gives `CoreCode.UNAVAILABLE`. A stream that the core ends with no error
                gives stream_failed. An event that is not valid gives invalid_event.
        """
        plan = plan_stream(
            self._config,
            SUBSCRIBE,
            lambda: convert.subscribe_request(
                instance_names=instance_names, event_types=event_types
            ),
            read_core_event,
            time_limit=time_limit,
            endless=True,
        )
        return AsyncEventStream(self._channel, plan)

    async def close(self) -> None:
        """Close the connection. A call that runs, and each later call, gives aborted.

        A second `close` does nothing. The result of a send that it stops is unknown. In another
        event loop, `close` does not always wait for the end: when the event loop of the first call
        runs in another thread, that loop closes the connection.
        """
        await self._channel.close()

    async def __aenter__(self) -> Self:
        return self

    async def __aexit__(
        self,
        kind: type[BaseException] | None,
        error: BaseException | None,
        trace: TracebackType | None,
    ) -> None:
        await self.close()
