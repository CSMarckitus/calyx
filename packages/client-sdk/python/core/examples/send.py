import os
import sys
import time
from pathlib import Path

from calyx_client_sdk import CalyxError, new_idempotency_key

from calyx_client_sdk_core import CoreClient

# 1. Store the key BEFORE the call. A file stands in for your database.
#    A second run uses the same key, so it sends no second message.
key_file = Path("order-42.key")
if not key_file.exists():
    key_file.write_text(new_idempotency_key())
# Calyx forgets a key after 24 hours. After that, the same key sends a second message.
if time.time() - key_file.stat().st_mtime > 23 * 60 * 60:
    sys.exit("The key is too old. Look at the chat before you send with a new key.")

# 2. Send the message. insecure=True: the token and the text travel in clear text, so use it only
#    on a private network that you trust.
try:
    with CoreClient(
        os.environ["CALYX_CORE_ADDRESS"], os.environ["CALYX_CORE_TOKEN"], insecure=True
    ) as client:
        sent = client.send_plain_text(
            instance_name=os.environ["CALYX_INSTANCE_NAME"],
            to=os.environ["CALYX_TO"],
            text="Your order 42 is ready.",
            idempotency_key=key_file.read_text(),
        )
except CalyxError as error:
    sys.exit(f"{error.code}: {error.detail} ({error.retry})")

print("Message id:", sent.data.message_id, "Sent by an earlier run:", sent.data.replayed)
