import logging
import os
import sqlite3
import time
from contextlib import closing

from flask import Flask, request

from calyx_client_sdk import (
    BackendClient,
    BackendCode,
    BulkFinishedEvent,
    CalyxError,
    ConnectionUpdateEvent,
    InvalidPayloadEvent,
    MessageReceivedEvent,
    MessageStatusEvent,
    OtherEvent,
    RetryAdvice,
    new_idempotency_key,
    verify_webhook,
)

log = logging.getLogger(__name__)
app = Flask(__name__)
client = BackendClient(os.environ["CALYX_URL"], os.environ["CALYX_API_KEY"])
INSTANCE_ID = os.environ["CALYX_INSTANCE_ID"]
SECRET = os.environ["CALYX_WEBHOOK_SECRET"]

# Calyx forgets a key after 24 hours. After that, the same key sends a second message.
KEY_LIFE = 23 * 60 * 60

# A SQLite file stands in for your database.
DATABASE = "shop.db"
with closing(sqlite3.connect(DATABASE)) as db, db:
    db.execute("CREATE TABLE IF NOT EXISTS handled_events (id TEXT PRIMARY KEY)")
    db.execute(
        "CREATE TABLE IF NOT EXISTS order_sends"
        " (id TEXT PRIMARY KEY, key TEXT, made_at REAL, sent INTEGER DEFAULT 0)"
    )


@app.post("/calyx/webhook")
def calyx_webhook() -> tuple[str, int]:
    try:
        # get_data() is the raw body. request.json is parsed, and it does not pass the check.
        event = verify_webhook(request.get_data(), request.headers, SECRET)
    except CalyxError as error:
        log.warning("Calyx webhook refused: %s", error.code)
        return "", 400

    # When the code in the block fails, the id is not stored, and the next delivery is handled.
    with closing(sqlite3.connect(DATABASE)) as db, db:
        insert = db.execute("INSERT OR IGNORE INTO handled_events VALUES (?)", (event.id,))
        if insert.rowcount == 1:
            # Calyx waits 5 seconds for the answer. Do slow work in a task.
            match event:
                case MessageReceivedEvent(data=message):
                    log.info("Message %s in %s", message.message_id, event.instance)
                case MessageStatusEvent(data=status):
                    log.info("Message %s is %s", status.message_id, status.status)
                case ConnectionUpdateEvent(data=update):
                    log.info("Instance %s is %s", event.instance, update.status)
                case BulkFinishedEvent(data=job):
                    log.info("Bulk job %s is %s", job.job_id, job.status)
                case InvalidPayloadEvent(reason=reason):
                    log.error("Calyx event %s cannot be read: %s", event.id, reason)
                case OtherEvent():
                    log.info("Event %s is not used", event.event)
    return "", 204


def load_send(order_id: str) -> tuple[str, float, int]:
    """Give the key of an order, its time, and 1 when the message went out."""
    with closing(sqlite3.connect(DATABASE)) as db, db:
        new = (order_id, new_idempotency_key(), time.time())
        db.execute("INSERT OR IGNORE INTO order_sends (id, key, made_at) VALUES (?, ?, ?)", new)
        query = "SELECT key, made_at, sent FROM order_sends WHERE id = ?"
        key, made_at, sent = db.execute(query, (order_id,)).fetchone()
    return key, made_at, sent


def save_sent(order_id: str) -> None:
    with closing(sqlite3.connect(DATABASE)) as db, db:
        db.execute("UPDATE order_sends SET sent = 1 WHERE id = ?", (order_id,))


# Protect this route with the login of your program.
@app.post("/orders/<order_id>/ready")
def order_ready(order_id: str) -> tuple[dict[str, str], int]:
    key, made_at, sent = load_send(order_id)
    if sent:
        return {"state": "sent"}, 200
    if time.time() - made_at > KEY_LIFE:
        return {"state": "check the chat"}, 409
    try:
        client.send_message(
            INSTANCE_ID,
            to=os.environ["CALYX_TO"],  # The phone number of the order.
            text=f"Your order {order_id} is ready.",
            idempotency_key=key,
            time_limit=20,  # Less than the limit of your web server.
        )
    except CalyxError as error:
        log.warning("Order %s: %s: %s", order_id, error.code, error.detail)
        match error.retry:
            case RetryAdvice.RETRY_NOW | RetryAdvice.RETRY_LATER:
                # The message can still go out. Call again in some time.
                return {"state": "pending"}, 202
            case RetryAdvice.DO_NOT_RETRY if error.code != BackendCode.DUPLICATE_MESSAGE:
                return {"state": "failed"}, 502
            case _:
                return {"state": "check the chat"}, 409
    save_sent(order_id)
    return {"state": "sent"}, 200
