"""Right use of every operation. `run.sh types` runs mypy and pyright on it: no error is allowed."""

from collections.abc import Iterable, Iterator, Mapping
from datetime import UTC, datetime
from typing import Any, Literal, assert_never, assert_type

from calyx_client_sdk import (
    AsyncBackendClient,
    AsyncPairingStream,
    BackendClient,
    BulkFinished,
    BulkFinishedEvent,
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
    PairingDone,
    PairingFailed,
    PairingStream,
    PairingUpdate,
    Sent,
    decode_event,
    verify_webhook,
)
from calyx_client_sdk.models import (
    ApiKeyList,
    BulkJob,
    BulkJobList,
    BulkMessageInput,
    BulkPacingInput,
    ClientList,
    CreateInstanceResult,
    DeviceIdentity,
    Instance,
    InstanceList,
    Me,
    MetaCredentialsInput,
    Organization,
    OrgClient,
    PairResult,
    Provider,
    RangeInput,
    SendMessageResult,
    WebhookInput,
)

ID = "2c1b4fd8-6f3e-4a35-9d1e-3b0f4c5d6e7f"
HOOK = WebhookInput(url="https://example.com/hook", event="message.received")
CREDENTIALS = MetaCredentialsInput(phone_number_id="123", access_token="a", app_secret="b")
PACING = BulkPacingInput(delay=RangeInput(min=1000, max=2000))


def sync_calls(client: BackendClient) -> None:
    assert_type(client.list_api_keys(), ApiKeyList)
    client.revoke_api_key(ID, time_limit=5)
    assert_type(client.list_clients(), ClientList)
    assert_type(client.create_client(name="Shop", max_instances=3), OrgClient)
    assert_type(client.get_client(ID), OrgClient)
    assert_type(client.update_client(ID, name="Shop 2", can_create_instances=True), OrgClient)
    client.delete_client(ID)
    assert_type(client.list_instances(), InstanceList)
    assert_type(client.get_instance(ID), Instance)
    assert_type(client.update_instance(ID, enabled=False, webhooks=[HOOK]), Instance)
    assert_type(client.remove_instance(ID), int)
    assert_type(client.pair_instance(ID, locale="es-ES", phone="34600000000"), PairResult)
    client.cancel_pairing(ID)
    sent = client.send_message(ID, to="34600000000", text="Hi", idempotency_key="key")
    assert_type(sent, Sent[SendMessageResult])
    assert_type(sent.data.message_id, str)
    assert_type(client.list_bulk_jobs(ID), BulkJobList)
    assert_type(client.get_bulk_job(ID), BulkJob)
    assert_type(client.cancel_bulk_job(ID), BulkJob)
    assert_type(client.get_me(), Me)
    assert_type(client.get_organization(), Organization)
    assert_type(client.update_organization(name="Acme"), Organization)


def sync_create_instance(client: BackendClient) -> None:
    # The constants of Provider and plain strings both pick an overload.
    web = client.create_instance(provider=Provider.WHATSAPP_WEB, display_name="A", locale="es-ES")
    assert_type(web, CreateInstanceResult)
    web_again = client.create_instance(
        provider="WHATSAPP_WEB",
        display_name="A",
        locale="es-ES",
        device_identity=DeviceIdentity.CHROME_WINDOWS,
        pairing="qr",
        send_only=True,
        webhooks=[HOOK],
        client_id=ID,
    )
    assert_type(web_again, CreateInstanceResult)
    meta = client.create_instance(
        provider=Provider.META_CLOUD, display_name="B", meta_credentials=CREDENTIALS
    )
    assert_type(meta, CreateInstanceResult)
    meta_again = client.create_instance(
        provider="META_CLOUD", display_name="B", meta_credentials=CREDENTIALS, webhooks=[]
    )
    assert_type(meta_again, CreateInstanceResult)


def sync_create_bulk_job(client: BackendClient) -> None:
    texts = client.create_bulk_job(ID, recipients=["34600000000"], text="Hi", pacing=PACING)
    assert_type(texts, Sent[BulkJob])
    messages = client.create_bulk_job(
        ID, messages=[BulkMessageInput(to="34600000000", text="Hi")], idempotency_key="key"
    )
    assert_type(messages, Sent[BulkJob])


def sync_pairing(client: BackendClient) -> None:
    stream = client.follow_pairing(ID, time_limit=60)
    assert_type(stream, PairingStream)
    with stream:
        for item in stream:
            assert_type(item, PairingUpdate | PairingDone | PairingFailed)
            match item:
                case PairingUpdate(pairing=pairing):
                    assert_type(pairing.qr_text, str | None)
                case PairingDone(status=status):
                    assert_type(status, str)
                case PairingFailed(code=code, detail=detail):
                    assert_type(code, str)
                    assert_type(detail, str)
                case _:
                    assert_never(item)


async def async_calls(client: AsyncBackendClient) -> None:
    assert_type(await client.list_api_keys(), ApiKeyList)
    await client.revoke_api_key(ID, time_limit=5)
    assert_type(await client.list_clients(), ClientList)
    assert_type(await client.create_client(name="Shop", max_instances=3), OrgClient)
    assert_type(await client.get_client(ID), OrgClient)
    assert_type(await client.update_client(ID, name="Shop 2"), OrgClient)
    await client.delete_client(ID)
    assert_type(await client.list_instances(), InstanceList)
    assert_type(await client.get_instance(ID), Instance)
    assert_type(await client.update_instance(ID, enabled=False, webhooks=[HOOK]), Instance)
    assert_type(await client.remove_instance(ID), int)
    assert_type(await client.pair_instance(ID, locale="es-ES"), PairResult)
    await client.cancel_pairing(ID)
    sent = await client.send_message(ID, to="34600000000", text="Hi", idempotency_key="key")
    assert_type(sent, Sent[SendMessageResult])
    assert_type(sent.data.message_id, str)
    assert_type(await client.list_bulk_jobs(ID), BulkJobList)
    assert_type(await client.get_bulk_job(ID), BulkJob)
    assert_type(await client.cancel_bulk_job(ID), BulkJob)
    assert_type(await client.get_me(), Me)
    assert_type(await client.get_organization(), Organization)
    assert_type(await client.update_organization(name="Acme"), Organization)


async def async_create_instance(client: AsyncBackendClient) -> None:
    web = await client.create_instance(
        provider=Provider.WHATSAPP_WEB, display_name="A", locale="es-ES"
    )
    assert_type(web, CreateInstanceResult)
    web_again = await client.create_instance(
        provider="WHATSAPP_WEB", display_name="A", locale="es-ES", phone="34600000000"
    )
    assert_type(web_again, CreateInstanceResult)
    meta = await client.create_instance(
        provider=Provider.META_CLOUD, display_name="B", meta_credentials=CREDENTIALS
    )
    assert_type(meta, CreateInstanceResult)
    meta_again = await client.create_instance(
        provider="META_CLOUD", display_name="B", meta_credentials=CREDENTIALS
    )
    assert_type(meta_again, CreateInstanceResult)


async def async_create_bulk_job(client: AsyncBackendClient) -> None:
    texts = await client.create_bulk_job(ID, recipients=["34600000000"], text="Hi")
    assert_type(texts, Sent[BulkJob])
    messages = await client.create_bulk_job(
        ID, messages=[BulkMessageInput(to="34600000000", text="Hi")], pacing=PACING
    )
    assert_type(messages, Sent[BulkJob])


async def async_pairing(client: AsyncBackendClient) -> None:
    stream = client.follow_pairing(ID, time_limit=60)
    assert_type(stream, AsyncPairingStream)
    async with stream:
        async for item in stream:
            assert_type(item, PairingUpdate | PairingDone | PairingFailed)
            match item:
                case PairingUpdate(pairing=pairing):
                    assert_type(pairing.pairing_code, str | None)
                case PairingDone(status=status):
                    assert_type(status, str)
                case PairingFailed(code=code, detail=detail):
                    assert_type(code, str)
                    assert_type(detail, str)
                case _:
                    assert_never(item)


def webhook(body: bytes, headers: Mapping[str, str], secret: str) -> None:
    handle(verify_webhook(body, headers, secret))


def handle(event: Event) -> None:
    assert_type(event.id, str)
    assert_type(event.event, str)
    assert_type(event.instance, str)
    assert_type(event.timestamp, str)
    match event:
        case MessageReceivedEvent(data=message):
            assert_type(event.event, Literal["message.received"])
            assert_type(message, MessageReceived)
            assert_type(message.from_, str)
            assert_type(message.text, str | None)
            assert_type(message.timestamp, int)
        case MessageStatusEvent(data=status):
            assert_type(status, MessageStatus)
            assert_type(status.status, str)
            print(status.status == DeliveryStatus.READ)
        case ConnectionUpdateEvent(data=update):
            assert_type(update, ConnectionUpdate)
            assert_type(update.status, str)
        case BulkFinishedEvent(data=job):
            assert_type(job, BulkFinished)
            assert_type(job.error, str | None)
        case OtherEvent(event=name, data=data):
            assert_type(name, str)
            assert_type(data, Any)
        case InvalidPayloadEvent(reason=reason):
            assert_type(reason, str)
            assert_type(event.data, Any)
        case _:
            assert_never(event)


def handle_any(value: object, later: list[Event]) -> None:
    # A task queue gives an object.
    if isinstance(value, Event):
        assert_type(value, Event)
        handle(value)
    for event in later:
        handle(event)


class FlaskHeaders:
    """The shape of the headers of Flask: no Mapping and no Sequence."""

    def __iter__(self) -> Iterator[tuple[str, str]]:
        return iter([("X-Calyx-Timestamp", "1")])

    def items(self, lower: bool = False) -> Iterable[tuple[str, str]]:
        return iter([("x-calyx-timestamp" if lower else "X-Calyx-Timestamp", "1")])


def webhook_headers(body: bytes, headers: dict[str, str]) -> None:
    verify_webhook(body, FlaskHeaders(), "secret")
    verify_webhook(body, headers.items(), "secret")


def webhook_arguments(body: bytes, pairs: list[tuple[str, str]]) -> None:
    by_pairs = verify_webhook(body, pairs, "secret", tolerance=60, now=datetime.now(UTC))
    with_no_check = decode_event(body)
    for event in (by_pairs, with_no_check):
        assert_type(event, Event)
        assert_type(
            event,
            MessageReceivedEvent
            | MessageStatusEvent
            | ConnectionUpdateEvent
            | BulkFinishedEvent
            | OtherEvent
            | InvalidPayloadEvent,
        )
        if isinstance(event, MessageReceivedEvent):
            assert_type(event.data.push_name, str | None)
    # A program makes an event in its tests.
    MessageStatusEvent(
        id="evt-1",
        instance="i_1",
        timestamp="2026-11-22T10:00:00Z",
        data=MessageStatus(message_id="m1", chat_id="34600000000", status=DeliveryStatus.SENT),
    )
