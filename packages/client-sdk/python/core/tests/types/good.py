"""Right use of every call. `run.sh types` runs mypy and pyright on it: no error is allowed."""

from datetime import datetime
from typing import Any, Final, Literal, assert_never, assert_type

from calyx_client_sdk import (
    BulkFinished,
    BulkFinishedEvent,
    ConnectionUpdate,
    ConnectionUpdateEvent,
    Event,
    InvalidPayloadEvent,
    MessageReceived,
    MessageReceivedEvent,
    MessageStatus,
    MessageStatusEvent,
    OtherEvent,
    Sent,
)

from calyx_client_sdk_core import (
    AsyncCoreClient,
    AsyncEventStream,
    AsyncInstanceStream,
    CoreClient,
    EventStream,
    InstanceStream,
)
from calyx_client_sdk_core.models import (
    BulkJob,
    BulkJobStatus,
    BulkPacing,
    CreateInstanceResponse,
    DeviceIdentity,
    GetBulkJobsResponse,
    GetInstanceStatusResponse,
    InstanceStatus,
    IntRange,
    ListInstancesResponse,
    MetaCloudSettings,
    PairingCodeSettings,
    PlainTextPayload,
    Provider,
    RemoveInstanceResponse,
    SendPlainTextResponse,
    UpdateInstanceConfigResponse,
    WebhookConfig,
    WebhookSettings,
    WhatsAppWebSettings,
)

NAME = "sales"
TO = "34600000000"
KEY: Final = "key"
HOOK = WebhookConfig(url="https://example.com/hook", event="message.received", secret="secret")
META = MetaCloudSettings(phone_number_id="123", access_token="a", app_secret="b")
WEB = WhatsAppWebSettings(locale="es-ES")
PACING = BulkPacing(delay=IntRange(min=1000, max=2000), pause_every=None)
MESSAGE = PlainTextPayload(to=TO, text="Hi", delay_ms=1500)


def clients(token: str) -> None:
    with CoreClient("127.0.0.1:50051", token, insecure=True) as client:
        assert_type(client, CoreClient)
    CoreClient("core.internal:50051", token, ca_file="/ca.pem", time_limit=5).close()


async def async_clients(token: str) -> None:
    async with AsyncCoreClient("127.0.0.1:50051", token, insecure=True) as client:
        assert_type(client, AsyncCoreClient)
    await AsyncCoreClient("core.internal:50051", token, ca_file="/ca.pem", time_limit=5).close()


def sync_calls(client: CoreClient) -> None:
    removed = client.remove_instance(instance_name=NAME, time_limit=5)
    assert_type(removed, RemoveInstanceResponse)
    updated = client.update_instance_config(
        instance_name=NAME,
        new_instance_name="shop",
        is_enabled=True,
        is_used_only_for_send_messages=None,
        webhooks=[HOOK],
        meta_cloud=META,
    )
    assert_type(updated, UpdateInstanceConfigResponse)
    client.update_instance_config(instance_name=NAME, webhooks=())
    assert_type(client.list_instances(), ListInstancesResponse)
    status = client.get_instance_status(instance_name=NAME)
    assert_type(status, GetInstanceStatusResponse)
    print(status.status == InstanceStatus.NEEDS_AUTH)
    sent = client.send_plain_text(instance_name=NAME, to=TO, text="Hi", idempotency_key=KEY)
    assert_type(sent, Sent[SendPlainTextResponse])
    assert_type(sent.data.message_id, str)
    assert_type(sent.idempotency_key, str)
    same = client.send_bulk_same_message(instance_name=NAME, recipients=[TO], text="Hi")
    assert_type(same, Sent[BulkJob])
    custom = client.send_bulk_custom_messages(
        instance_name=NAME, messages=(MESSAGE,), pacing=PACING, idempotency_key=KEY
    )
    assert_type(custom, Sent[BulkJob])
    job = client.get_bulk_job(job_id="job", time_limit=1.5)
    assert_type(job, BulkJob)
    assert_type(job.created_at, datetime)
    assert_type(job.finished_at, datetime | None)
    print(job.status == BulkJobStatus.RUNNING)
    jobs = client.get_bulk_jobs(job_ids=["job"])
    assert_type(jobs, GetBulkJobsResponse)
    assert_type(jobs.jobs, tuple[BulkJob, ...])
    assert_type(client.cancel_bulk_job(job_id="job"), BulkJob)


def instances(client: CoreClient) -> None:
    for instance in client.list_instances().instances:
        assert_type(instance.locale, str | None)
        assert_type(instance.webhooks, tuple[WebhookSettings, ...])
        print(instance.provider == Provider.WHATSAPP_WEB)


async def async_calls(client: AsyncCoreClient) -> None:
    assert_type(await client.remove_instance(instance_name=NAME), RemoveInstanceResponse)
    updated = await client.update_instance_config(instance_name=NAME, webhooks=[])
    assert_type(updated, UpdateInstanceConfigResponse)
    assert_type(await client.list_instances(), ListInstancesResponse)
    status = await client.get_instance_status(instance_name=NAME, time_limit=5)
    assert_type(status, GetInstanceStatusResponse)
    sent = await client.send_plain_text(instance_name=NAME, to=TO, text="Hi", idempotency_key=KEY)
    assert_type(sent, Sent[SendPlainTextResponse])
    same = await client.send_bulk_same_message(
        instance_name=NAME, recipients=(TO,), text="Hi", pacing=PACING
    )
    assert_type(same, Sent[BulkJob])
    custom = await client.send_bulk_custom_messages(instance_name=NAME, messages=[MESSAGE])
    assert_type(custom, Sent[BulkJob])
    assert_type(await client.get_bulk_job(job_id="job"), BulkJob)
    assert_type(await client.get_bulk_jobs(job_ids=("job",)), GetBulkJobsResponse)
    assert_type(await client.cancel_bulk_job(job_id="job"), BulkJob)


def sync_create_instance(client: CoreClient) -> None:
    by_code = WhatsAppWebSettings(
        locale="es-ES",
        pairing_code=PairingCodeSettings(phone_number=TO),
        device_identity=DeviceIdentity.CHROME_WINDOWS,
    )
    stream = client.create_instance(
        NAME,
        provider=by_code,
        webhooks=[HOOK],
        is_used_only_for_send_messages=False,
        time_limit=180,
    )
    assert_type(stream, InstanceStream)
    with stream:
        for update in stream:
            assert_type(update, CreateInstanceResponse)
            assert_type(update.pairing_code, str | None)
    with client.create_instance(NAME, provider=META) as meta:
        assert_type(meta, InstanceStream)


async def async_create_instance(client: AsyncCoreClient) -> None:
    stream = client.create_instance(NAME, provider=WEB)
    assert_type(stream, AsyncInstanceStream)
    async with stream:
        async for update in stream:
            assert_type(update, CreateInstanceResponse)
    async with client.create_instance(NAME, provider=META, webhooks=[HOOK]) as meta:
        assert_type(await anext(meta), CreateInstanceResponse)


def sync_subscribe(client: CoreClient) -> None:
    stream = client.subscribe(instance_names=[NAME], event_types=("message.received",))
    assert_type(stream, EventStream)
    with stream:
        for event in stream:
            assert_type(event, Event)
            assert_type(event.instance, str)
            assert_type(event.timestamp, str)
            match event:
                case MessageReceivedEvent(data=message):
                    assert_type(event.event, Literal["message.received"])
                    assert_type(message, MessageReceived)
                    assert_type(message.text, str | None)
                case MessageStatusEvent(data=status):
                    assert_type(status, MessageStatus)
                case ConnectionUpdateEvent(data=update):
                    assert_type(update, ConnectionUpdate)
                case BulkFinishedEvent(data=job):
                    assert_type(job, BulkFinished)
                case InvalidPayloadEvent(reason=reason):
                    assert_type(reason, str)
                case OtherEvent(event=name, data=data):
                    assert_type(name, str)
                    assert_type(data, Any)
                case _:
                    assert_never(event)


async def async_subscribe(client: AsyncCoreClient) -> None:
    stream = client.subscribe(time_limit=60)
    assert_type(stream, AsyncEventStream)
    async with stream:
        async for event in stream:
            assert_type(event, Event)
            if isinstance(event, MessageReceivedEvent):
                assert_type(event.data.push_name, str | None)
            match event:
                case MessageReceivedEvent() | MessageStatusEvent() | ConnectionUpdateEvent():
                    assert_type(event.id, str)
                case BulkFinishedEvent() | OtherEvent() | InvalidPayloadEvent():
                    pass
                case _:
                    assert_never(event)
