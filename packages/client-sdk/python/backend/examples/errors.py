import logging
import os
import time
from typing import Literal

from calyx_client_sdk import BackendClient, BackendCode, CalyxError, RetryAdvice

log = logging.getLogger(__name__)
client = BackendClient(os.environ["CALYX_URL"], os.environ["CALYX_API_KEY"])
INSTANCE_ID = os.environ["CALYX_INSTANCE_ID"]


def send(
    *, to: str, text: str, key: str
) -> Literal["sent", "later", "check the chat", "duplicate", "failed"]:
    """Send a message. `key` is the stored key of this message.

    It can wait for minutes, so do not call it in a web view. In an `async def`, use
    `AsyncBackendClient` and `asyncio.sleep`, and never `BackendClient`.
    """
    for _ in range(5):
        try:
            client.send_message(INSTANCE_ID, to=to, text=text, idempotency_key=key)
            return "sent"
        except CalyxError as error:
            log.warning("%s: %s (request %s)", error.code, error.detail, error.request_id)
            match error.retry:
                case RetryAdvice.RETRY_NOW:
                    # The result is unknown. The same key sends no second message.
                    wait = 1
                case RetryAdvice.RETRY_LATER:
                    wait = error.retry_after or 30
                case RetryAdvice.CHECK_IF_SENT | RetryAdvice.UNKNOWN:
                    # The message can be in the chat. A person looks there first.
                    return "check the chat"
                case RetryAdvice.DO_NOT_RETRY if error.code == BackendCode.DUPLICATE_MESSAGE:
                    # The same text went to this recipient in the last 10 minutes.
                    return "duplicate"
                case RetryAdvice.DO_NOT_RETRY:
                    # The same request fails again. Change it first.
                    return "failed"
        time.sleep(wait)
    # Call again with the same key, but not after 24 hours.
    return "later"
