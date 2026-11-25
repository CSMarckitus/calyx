"""Wrong use of the SDK. Each wrong line ends with the errors that mypy and pyright must give.

`run.sh types` checks that every expected error occurs and that no other error occurs. The format
of a comment is explained in check.py of the backend package.
"""

# The formatter must not wrap a line, because the comment names the line of the error.
# fmt: off
from typing import assert_never

from calyx_client_sdk import (
    BulkFinishedEvent,
    ConnectionUpdateEvent,
    MessageReceivedEvent,
    MessageStatusEvent,
    OtherEvent,
)

from calyx_client_sdk_core import AsyncCoreClient, CoreClient
from calyx_client_sdk_core.models import (
    MetaCloudSettings,
    PairingCodeSettings,
    Provider,
    WhatsAppWebSettings,
)

NAME = "sales"
WEB = WhatsAppWebSettings(locale="es-ES")
client = CoreClient("127.0.0.1:50051", "token", insecure=True)
async_client = AsyncCoreClient("127.0.0.1:50051", "token", insecure=True)


def wrong_arguments() -> None:
    client.get_bulk_job(job_id=42)  # expect: mypy=arg-type pyright=reportArgumentType
    client.remove_instance(instance_name=NAME, force=True)  # expect: mypy=call-arg pyright=reportCallIssue
    client.remove_instance(NAME)  # expect: mypy=call-arg pyright=reportCallIssue
    client.get_instance_status()  # expect: mypy=call-arg pyright=reportCallIssue
    client.send_plain_text(instance_name=NAME, to="34600000000", text=None)  # expect: mypy=arg-type pyright=reportArgumentType
    client.get_bulk_jobs(job_ids=None)  # expect: mypy=arg-type pyright=reportArgumentType
    client.send_bulk_same_message(instance_name=NAME, text="Hi")  # expect: mypy=call-arg pyright=reportCallIssue
    client.send_bulk_custom_messages(instance_name=NAME)  # expect: mypy=call-arg pyright=reportCallIssue
    client.get_bulk_jobs()  # expect: mypy=call-arg pyright=reportCallIssue
    client.send_bulk_custom_messages(instance_name=NAME, messages=["Hi"])  # expect: mypy=list-item pyright=reportArgumentType
    CoreClient("127.0.0.1:50051", "token", True)  # expect: mypy=call-arg pyright=reportCallIssue
    CoreClient("127.0.0.1:50051")  # expect: mypy=call-arg pyright=reportCallIssue


async def missing_lists() -> None:
    await async_client.send_bulk_same_message(instance_name=NAME, text="Hi")  # expect: mypy=call-arg pyright=reportCallIssue
    await async_client.send_bulk_custom_messages(instance_name=NAME)  # expect: mypy=call-arg pyright=reportCallIssue
    await async_client.get_bulk_jobs()  # expect: mypy=call-arg pyright=reportCallIssue


def wrong_create_instance() -> None:
    client.create_instance(NAME)  # expect: mypy=call-arg pyright=reportCallIssue
    async_client.create_instance(NAME)  # expect: mypy=call-arg pyright=reportCallIssue
    client.create_instance(NAME, provider=None)  # expect: mypy=arg-type pyright=reportArgumentType
    client.create_instance(NAME, provider="WHATSAPP_WEB")  # expect: mypy=arg-type pyright=reportArgumentType
    client.create_instance(NAME, provider=Provider.WHATSAPP_WEB)  # expect: mypy=arg-type pyright=reportArgumentType
    client.create_instance(NAME, provider=PairingCodeSettings(phone_number="34600000000"))  # expect: mypy=arg-type pyright=reportArgumentType
    async_client.create_instance(NAME, provider=PairingCodeSettings(phone_number="1"))  # expect: mypy=arg-type pyright=reportArgumentType
    client.create_instance(NAME, provider=WhatsAppWebSettings)  # expect: mypy=arg-type pyright=reportArgumentType
    client.create_instance(NAME, WEB)  # expect: mypy=call-arg pyright=reportCallIssue
    WhatsAppWebSettings(pairing_code="34600000000")  # expect: mypy=arg-type pyright=reportArgumentType
    MetaCloudSettings(phone_number_id="123", access_token="a")  # expect: mypy=call-arg pyright=reportCallIssue


def wrong_results() -> None:
    sent = client.send_plain_text(instance_name=NAME, to="34600000000", text="Hi")
    print(sent.message_id)  # expect: mypy=attr-defined pyright=reportAttributeAccessIssue,reportUnknownArgumentType,reportUnknownMemberType
    job = client.get_bulk_job(job_id="job")
    print(job.finished_at.year)  # expect: mypy=union-attr pyright=reportOptionalMemberAccess
    job.id = "x"  # expect: mypy=misc pyright=reportAttributeAccessIssue
    for instance in client.list_instances():  # expect: mypy=attr-defined pyright=reportGeneralTypeIssues,reportUnknownVariableType
        print(instance)  # expect: pyright=reportUnknownArgumentType


async def missing_await() -> None:
    print(async_client.get_bulk_job(job_id="job").status)  # expect: mypy=attr-defined pyright=reportAttributeAccessIssue,reportUnknownArgumentType,reportUnknownMemberType
    async_client.cancel_bulk_job(job_id="job")  # expect: mypy=unused-coroutine pyright=reportUnusedCoroutine
    await client.list_instances()  # expect: mypy=misc pyright=reportGeneralTypeIssues
    with async_client.subscribe() as stream:  # expect: mypy=attr-defined pyright=reportGeneralTypeIssues,reportUnknownVariableType
        print(stream)  # expect: pyright=reportUnknownArgumentType
    for event in async_client.subscribe():  # expect: mypy=attr-defined pyright=reportGeneralTypeIssues,reportUnknownVariableType
        print(event)  # expect: pyright=reportUnknownArgumentType


def wrong_events() -> None:
    client.subscribe(["sales"])  # expect: mypy=call-arg pyright=reportCallIssue
    event = next(client.subscribe())
    if isinstance(event, MessageReceivedEvent):
        print(event.data.text.lower())  # expect: mypy=union-attr pyright=reportOptionalMemberAccess
    match event:
        case MessageReceivedEvent() | MessageStatusEvent() | ConnectionUpdateEvent():
            event.id = "x"  # expect: mypy=misc pyright=reportAttributeAccessIssue
        case BulkFinishedEvent() | OtherEvent():
            pass
        case _:
            assert_never(event)  # expect: mypy=arg-type pyright=reportArgumentType
