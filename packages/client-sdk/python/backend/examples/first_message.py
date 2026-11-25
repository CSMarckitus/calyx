import os
import sys
from pathlib import Path

from calyx_client_sdk import BackendClient, CalyxError, new_idempotency_key

client = BackendClient(os.environ["CALYX_URL"], os.environ["CALYX_API_KEY"])

# 1. Store the key BEFORE the call. A file stands in for your database.
#    A second run uses the same key, so it sends no second message.
#    Calyx forgets a key after 24 hours. Do not send again after that time.
key_file = Path("order-42.key")
if not key_file.exists():
    key_file.write_text(new_idempotency_key())
key = key_file.read_text()

# 2. Send the message.
try:
    sent = client.send_message(
        os.environ["CALYX_INSTANCE_ID"],
        to=os.environ["CALYX_TO"],
        text="Your order 42 is ready.",
        idempotency_key=key,
    )
except CalyxError as error:
    sys.exit(f"{error.code}: {error.detail} ({error.retry})")

print("Message id:", sent.data.message_id, "Sent by an earlier run:", sent.data.replayed)
