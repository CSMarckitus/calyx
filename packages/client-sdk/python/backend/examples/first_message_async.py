import asyncio
import os
import sys
from pathlib import Path

from calyx_client_sdk import AsyncBackendClient, CalyxError, new_idempotency_key

client = AsyncBackendClient(os.environ["CALYX_URL"], os.environ["CALYX_API_KEY"])


async def send(key: str) -> None:
    sent = await client.send_message(
        os.environ["CALYX_INSTANCE_ID"],
        to=os.environ["CALYX_TO"],
        text="Your order 42 is ready.",
        idempotency_key=key,
    )
    print("Message id:", sent.data.message_id, "Sent by an earlier run:", sent.data.replayed)


# Store the key BEFORE the call. A file stands in for your database.
# A cancelled call has no result. With the stored key, you can send again with no second message.
# Calyx forgets a key after 24 hours. Do not send again after that time.
key_file = Path("order-42.key")
if not key_file.exists():
    key_file.write_text(new_idempotency_key())

try:
    asyncio.run(send(key_file.read_text()))
except CalyxError as error:
    sys.exit(f"{error.code}: {error.detail} ({error.retry})")
