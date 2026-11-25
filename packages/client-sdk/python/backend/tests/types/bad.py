"""Wrong use of the SDK. Each wrong line ends with the errors that mypy and pyright must give.

`run.sh types` checks that every expected error occurs and that no other error occurs. The format
of a comment is explained in check.py.
"""

# The formatter must not wrap a line, because the comment names the line of the error.
# fmt: off
from typing import Any, assert_never

from calyx_client_sdk import (
    AsyncBackendClient,
    BackendClient,
    BulkFinishedEvent,
    ConnectionUpdateEvent,
    Event,
    MessageReceivedEvent,
    MessageStatusEvent,
    OtherEvent,
    decode_event,
    verify_webhook,
)
from calyx_client_sdk.models import BulkMessageInput, MetaCredentialsInput, Provider

ID = "2c1b4fd8-6f3e-4a35-9d1e-3b0f4c5d6e7f"
CREDENTIALS = MetaCredentialsInput(phone_number_id="123", access_token="a", app_secret="b")
MESSAGE = BulkMessageInput(to="34600000000", text="Hi")
client = BackendClient("https://calyx.example.com", "key")
async_client = AsyncBackendClient("https://calyx.example.com", "key")


def wrong_arguments() -> None:
    client.get_client(42)  # expect: mypy=arg-type pyright=reportArgumentType
    client.create_client(name="Shop", color="red")  # expect: mypy=call-arg pyright=reportCallIssue
    client.send_message(ID, to="34600000000", text=None)  # expect: mypy=arg-type pyright=reportArgumentType
    client.get_client()  # expect: mypy=call-arg pyright=reportCallIssue
    client.get_client(ID, 5)  # expect: mypy=call-arg pyright=reportCallIssue
    flag: str = client.remove_instance(ID)  # expect: mypy=assignment pyright=reportAssignmentType
    print(flag)


def wrong_results() -> None:
    sent = client.send_message(ID, to="34600000000", text="Hi")
    print(sent.message_id)  # expect: mypy=attr-defined pyright=reportAttributeAccessIssue,reportUnknownMemberType,reportUnknownArgumentType
    instance = client.get_instance(ID)
    print(instance.status_changed_at.year)  # expect: mypy=union-attr pyright=reportOptionalMemberAccess


def wrong_create_instance() -> None:
    client.create_instance(  # expect: mypy=call-overload pyright=reportCallIssue
        provider=Provider.META_CLOUD, display_name="A", meta_credentials=CREDENTIALS, locale="es-ES"
    )
    client.create_instance(provider="WHATSAPP_WEB", display_name="A")  # expect: mypy=call-overload pyright=reportCallIssue
    client.create_instance(provider=Provider.META_CLOUD, display_name="A")  # expect: mypy=call-overload pyright=reportCallIssue


def wrong_create_bulk_job() -> None:
    client.create_bulk_job(ID, recipients=["34600000000"])  # expect: mypy=call-overload pyright=reportCallIssue
    client.create_bulk_job(  # expect: mypy=call-overload pyright=reportCallIssue
        ID, recipients=["34600000000"], text="Hi", messages=[MESSAGE]
    )


async def missing_await() -> None:
    print(async_client.get_me().user)  # expect: mypy=attr-defined pyright=reportAttributeAccessIssue,reportUnknownMemberType,reportUnknownArgumentType
    async_client.cancel_pairing(ID)  # expect: mypy=unused-coroutine pyright=reportUnusedCoroutine
    await client.get_me()  # expect: mypy=misc pyright=reportGeneralTypeIssues


def wrong_webhook(body: bytes, parsed: dict[str, Any], headers: dict[str, str], asgi: list[tuple[bytes, bytes]]) -> None:
    verify_webhook(parsed, headers, "secret")  # expect: mypy=arg-type pyright=reportArgumentType
    verify_webhook(body, headers, "secret", 60)  # expect: mypy=call-arg pyright=reportCallIssue
    verify_webhook(body, asgi, "secret")  # expect: mypy=arg-type pyright=reportArgumentType
    decode_event(body.decode())  # expect: mypy=arg-type pyright=reportArgumentType


def wrong_events(body: bytes) -> None:
    event: Event = decode_event(body)
    event = body  # expect: mypy=assignment pyright=reportAssignmentType
    event = decode_event(body)
    if isinstance(event, MessageReceivedEvent):
        print(event.data.messageId)  # expect: mypy=attr-defined pyright=reportAttributeAccessIssue,reportUnknownMemberType,reportUnknownArgumentType
        print(event.data.text.lower())  # expect: mypy=union-attr pyright=reportOptionalMemberAccess
    match event:
        case MessageReceivedEvent() | MessageStatusEvent() | ConnectionUpdateEvent():
            event.id = "x"  # expect: mypy=misc pyright=reportAttributeAccessIssue
        case BulkFinishedEvent() | OtherEvent():
            pass
        case _:
            assert_never(event)  # expect: mypy=arg-type pyright=reportArgumentType
