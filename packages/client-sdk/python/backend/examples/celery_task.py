import os
import time

from celery import Celery, Task

from calyx_client_sdk import BackendClient, CalyxError, RetryAdvice

app = Celery(
    "shop", broker=os.environ["CELERY_BROKER_URL"], backend=os.environ["CELERY_RESULT_BACKEND"]
)
client = BackendClient(os.environ["CALYX_URL"], os.environ["CALYX_API_KEY"])
INSTANCE_ID = os.environ["CALYX_INSTANCE_ID"]

# Calyx forgets a key after 24 hours. After that, a retry sends a second message.
KEY_LIFE = 23 * 60 * 60

LOOK = "The message can be in the chat. Look there before you send with a new key."


class SendNotConfirmed(Exception):
    """It has only texts, so a JSON result backend can read it again."""


# Make the key with new_idempotency_key(), and store it and its time with the order. Then queue:
#     send_order_message.delay(order.phone, text, order.key, order.key_made_at)
# acks_late: when the worker stops, the broker can deliver the task again. The key makes it safe.
@app.task(bind=True, acks_late=True, max_retries=None)
def send_order_message(self: Task, to: str, text: str, key: str, key_made_at: float) -> str:
    key_end = key_made_at + KEY_LIFE
    if time.time() > key_end:
        raise SendNotConfirmed("key_too_old", "The key is too old.", LOOK)
    try:
        sent = client.send_message(INSTANCE_ID, to=to, text=text, idempotency_key=key)
    except CalyxError as error:
        wait = max(error.retry_after, min(10 * 2**self.request.retries, 600))
        again = error.retry in (RetryAdvice.RETRY_NOW, RetryAdvice.RETRY_LATER)
        if again and time.time() + wait < key_end:
            raise self.retry(countdown=wait) from error
        # CalyxError needs named arguments, and a JSON result backend cannot make it again.
        raise SendNotConfirmed(error.code, error.detail, LOOK) from error
    return sent.data.message_id
