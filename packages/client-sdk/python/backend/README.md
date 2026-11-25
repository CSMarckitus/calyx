# Calyx client SDK for Python

A Python client for the HTTP API of the Calyx backend. It sends WhatsApp messages, pairs numbers,
and checks webhooks. It has a sync client and an async client, with full types for your IDE.

```text
your program ──HTTPS──▶ Calyx backend ──▶ Calyx core ──▶ WhatsApp
your program ◀──signed webhooks────────── Calyx core
```

> **Caution:** WhatsApp Web through Calyx is an unofficial integration. Its use is against the Terms
> of Service of WhatsApp. WhatsApp can ban a number, and no setup prevents a ban. Never send
> unsolicited messages.

Status: version 0.1.0, early alpha. The package is not on PyPI. The versions stay at `0.x` while
the contracts change.

| I want to... | Read |
|---|---|
| Send my first message | [Start here](#start-here) |
| Know what to do after an error | [Errors](#errors) |
| Send from a queue | [Celery](#celery) |
| Receive messages and events | [Webhooks](#webhooks) |
| Add a WhatsApp number from a web panel | [Pairing](#pairing) |
| Send one notice to many people who asked for it | [Bulk jobs](#bulk-jobs) |
| Create a client or an instance | [Create calls](#create-calls) |
| Find a method, a time limit or a type | [The client](#the-client) |
| Move from Evolution API | [Coming from Evolution API](#coming-from-evolution-api) |
| Know the limits of the SDK | [Known limits](#known-limits) |

## Start here

### Before you start

Ask the operator of your Calyx server for these three values:

| Value | Environment variable of the examples |
|---|---|
| The base URL of the backend, for example `https://calyx.example.com` | `CALYX_URL` |
| An API key. It starts with `cx_`. Keep it secret. | `CALYX_API_KEY` |
| The id of an instance that is connected. It is a UUID. | `CALYX_INSTANCE_ID` |

An instance is one WhatsApp number that Calyx manages. You also need Python 3.12 or later.

### Install

Get the Calyx repository. In the folder of your program, make a virtual environment. Install the
package with the path of the Calyx repository:

```bash
python3 -m venv .venv
source .venv/bin/activate  # Windows: .venv\Scripts\activate
pip install /path/to/calyx/packages/client-sdk/python/backend
```

This package installs no gRPC. The import name is `calyx_client_sdk`. For a program on the private
network of the core, there is also `calyx-client-sdk-core`, in the folder `python/core`.

### The first message

1. Set the variables. For `CALYX_TO`, use your own second phone: the country code and the number,
   with digits only.

   ```bash
   export CALYX_URL=https://calyx.example.com
   export CALYX_API_KEY=cx_...
   export CALYX_INSTANCE_ID=...
   export CALYX_TO=...
   ```

2. Save this code as `first_message.py`.
3. Run `python first_message.py`. It can wait for some minutes, because the core sends with its own
   delays.

<!-- examples/first_message.py -->
```python
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
```

### The first message with asyncio

`AsyncBackendClient` has the same methods and arguments. Write `await` before each call.

<!-- examples/first_message_async.py -->
```python
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
```

### If it fails

| Code | What to do |
|---|---|
| `network_error` | Check `CALYX_URL` and your network. |
| `unauthenticated` | Check `CALYX_API_KEY`. |
| `not_found` | Check `CALYX_INSTANCE_ID`. `client.list_instances()` gives the instances of your key. |
| `instance_not_connected` | Ask the operator to connect the instance, or [pair it](#pairing). |
| `invalid_request` | Read `error.detail` and `error.errors`. For example, the number has a wrong format. |
| `duplicate_message` | The same text went to this number in the last 10 minutes, with another key. |
| `idempotency_key_conflict` | You changed the text or the number after a send with this key. Delete `order-42.key` for a new message. |

## The idempotency key

The key is the name of one message. With the same key, Calyx sends the message one time at most
within 24 hours.

- [ ] Make the key with `new_idempotency_key()`, one key for each message.
- [ ] Store the key before the call, for example with the order.
- [ ] To send again after an error, use the same key and the same text.
- [ ] Do not send again after 24 hours. Calyx forgets the key, and a second message goes out.

| Rule | Reason |
|---|---|
| A key has 1 to 255 printable ASCII characters, with no space. | Another key gives `invalid_config`. No request goes out. |
| An empty key gives `invalid_config`. | Your program lost its stored key. |
| Without a key, the SDK makes one. It is in `sent.idempotency_key` and in each error. | When the process stops, this key is lost. |
| The same key with another text gives `idempotency_key_conflict`. | A key belongs to one message. |
| Put no personal data in a key, for example a phone number. | Logs can have the key. |

**Cancellation.** A cancelled async call raises `asyncio.CancelledError`, with no key and no result.
The backend goes on with the send. Send again with the stored key: no second message goes out. A
cancelled call with no stored key cannot be repeated safely.

## Errors

A call that fails raises one class, `CalyxError`. The SDK never sends a request again by itself.

| Field | Meaning |
|---|---|
| `code` | What happened. Compare it with `BackendCode` or `ClientCode`. A newer server can give other codes. |
| `retry` | What to do now, a `RetryAdvice`. See the next table. |
| `retry_after` | The minimum wait in seconds. 0 when the server gave none. |
| `detail` | A text for you. Write it in your log. `str(error)` has it only when `source` is `Source.CLIENT`. |
| `source` | `Source.BACKEND` or `Source.CLIENT` (the SDK found the problem). |
| `status` | The HTTP status. 0 when the source is the SDK. |
| `errors` | The fields that the backend refused, each with `location` and `message`. |
| `request_id` | The id of the request in the logs of the backend. Write it in your log. |
| `idempotency_key` | The key of the send. |
| `metadata` | Only for the core client. Empty here. |

| `RetryAdvice` | What to do | Examples of codes |
|---|---|---|
| `RETRY_NOW` | Nothing happened, or the result is unknown. Call again with the same key. | `timeout`, `network_error`, `send_outcome_unknown` |
| `RETRY_LATER` | Wait for `retry_after` seconds or more. Call again with the same key. | `send_in_progress`, `instance_not_connected`, `rate_limited` |
| `CHECK_IF_SENT` | The key cannot send again. Look at the chat. Then use a new key, after 10 minutes or more. | `idempotency_key_outcome_unknown` |
| `DO_NOT_RETRY` | The same call fails again. Change the call or the setup. | `duplicate_message`, `invalid_request`, `unauthenticated` |
| `UNKNOWN` | The SDK has no advice. After a send, the message can be out: look at the chat, and keep the same key. After a [create call](#create-calls), first list the records. | `internal`, a code that the SDK does not know |

The recipe, with each advice. Put your own `key` in the call, as in the first message.

<!-- examples/errors.py -->
```python
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
```

`duplicate_message` protects your number: the same text to the same person within 10 minutes looks
like spam. It also refuses a repeated one-time code. No option turns it off.

The codes of the SDK. Python adds no code to the shared list, and the backend client never gives
`aborted`.

| `ClientCode` | Meaning |
|---|---|
| `timeout` | The time limit passed. |
| `network_error` | No connection, or the connection stopped. |
| `invalid_response` | The answer is not from Calyx, for example an HTML page of a proxy. |
| `stream_failed` | A pairing stream stopped too early. |
| `invalid_config` | An argument or the setup is wrong. No request went out. |
| `invalid_signature`, `stale_timestamp`, `invalid_event` | A webhook is wrong. |

`invalid_response` has the advice `RETRY_NOW` when the answer is not JSON. It has `UNKNOWN` when a
successful answer, or a pairing event, is JSON with the wrong shape.

## Create calls

`create_client` and `create_instance` have no idempotency key. When the answer does not arrive, the
advice is `UNKNOWN`: the record can exist. List the records before a new call. A second call can
make a second record.

<!-- examples/create_client.py -->
```python
import os
import time

from calyx_client_sdk import BackendClient, CalyxError, RetryAdvice
from calyx_client_sdk.models import OrgClient

calyx = BackendClient(os.environ["CALYX_URL"], os.environ["CALYX_API_KEY"])


def find_or_create(name: str) -> OrgClient:
    # Two clients can have the same name, so your program must use a name one time only.
    for client in calyx.list_clients().items:
        if client.name == name:
            return client
    return calyx.create_client(name=name)


try:
    client = find_or_create("Shop Ana")
except CalyxError as error:
    if error.retry is not RetryAdvice.UNKNOWN:
        raise
    # The answer did not arrive, and the client can exist. A create call has no idempotency key.
    # Give the backend time to finish, and list the clients before a new call.
    time.sleep(15)
    client = find_or_create("Shop Ana")

print("Client id:", client.id)
```

## Celery

A send can wait for minutes, because the core sends with its own delays. So a queue is the best
place for a send. When Celery runs the task two times, the instance sends one message. The example
also reads `CELERY_BROKER_URL` and `CELERY_RESULT_BACKEND`.

<!-- examples/celery_task.py -->
```python
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
```

- [ ] The key and its time are arguments of the task. Make them one time, and store them. Never make
      them in the task.
- [ ] For an answer to a webhook, make the key from the event, for example `f"reply-{event.id}"`.
      Then a second delivery of the event sends no second answer.
- [ ] The retries stop before 24 hours.
- [ ] The time limit of the task is more than 360 seconds, the default of a send.
- [ ] No `CalyxError` leaves the task.
- [ ] `acks_late` is for a worker that stops. For a child process that is killed, also set
      `reject_on_worker_lost=True`. Without it, Celery marks the task as failed and does not run it
      again.

**Why no `CalyxError`.** Celery stores its results as JSON by default. It stores the name and the
text of an exception, and makes the exception again with only the text. `CalyxError` needs named
arguments, so `result.get()` raises a plain `Exception`. The `code` and the `retry` are lost.
`self.retry(exc=error)` has the same problem after the last try. We saw this with a Celery 5.7
worker. `SendNotConfirmed` has only texts, so it comes back as it was.

A task argument must be simple data. Do not pass a client as an argument: its pickle has the API
key. Make the client one time in the module, as the example does. Forked workers can share it.

## Webhooks

The core sends a POST request for each event. `verify_webhook` checks the signature and the time,
and gives an event. The examples read the secret of the webhook from `CALYX_WEBHOOK_SECRET`.

Ask the operator to set the webhook, or set it with `update_instance(instance_id, webhooks=[...])`.
Give one `WebhookInput(url=..., event="message.received", secret=...)` for each event. The list
replaces the old list.

- [ ] Give the raw body (`bytes`). JSON that your program parsed has other bytes.
- [ ] Give `request.headers` as it is. Django, Flask, FastAPI and Starlette work.
- [ ] Store the `id` of each event, and ignore an `id` that you handled. Calyx can deliver an event
      more than one time. An attacker can send an old request again for 300 seconds. Delete the ids
      that are some days old.
- [ ] Answer in less than 5 seconds. Do slow work, for example a send, in a task.
- [ ] Use the exact URL, to the last slash. After a 301 or 302 redirect, the core sends a GET request
      with no body, and the event is lost.
- [ ] In Django with `LoginRequiredMiddleware`, add `@login_not_required` to the view. Without it, the
      login redirect loses each event.
- [ ] Keep the clock of your server correct. A wrong clock gives `stale_timestamp`, and each event
      is lost.

| Your answer | What Calyx does |
|---|---|
| 2xx | The event is delivered. Also for `OtherEvent` and `InvalidPayloadEvent`: a new delivery has the same data. |
| 400, after `invalid_signature`, `stale_timestamp` or `invalid_event` | It does not deliver the event again. This is true for each 4xx answer, also 429. Do not answer with a redirect: the core follows 307 and 308 with the body. |
| 5xx, or no answer in 5 seconds | It tries again: at most 4 times in all, in a few seconds (more time with Redis). Then the event is lost. |

Use `match` on the class of the event. The type checker then knows the type of `data`. For the
annotation of your own function, use `Event`: it is the union of the six classes.

| Event class | Event name | `data` |
|---|---|---|
| `MessageReceivedEvent` | `message.received` | `MessageReceived`: `message_id`, `chat_id`, `from_`, `text`, ... |
| `MessageStatusEvent` | `message.status` | `MessageStatus`: `message_id`, `status`, ... |
| `ConnectionUpdateEvent` | `connection.update` | `ConnectionUpdate`: `status` |
| `BulkFinishedEvent` | `bulk.finished` | `BulkFinished`: `job_id`, `status`, `sent_messages`, ... |
| `OtherEvent` | All other names | Plain JSON |
| `InvalidPayloadEvent` | One of the four names | Plain JSON that the SDK cannot read. `reason` says why. Log it. |

`event.instance` is the name of the instance: `"i_" + instance_id.replace("-", "")`. To answer a
message, call `send_message(instance_id, to=message.chat_id, ...)` in a task. A broadcast list or a
status post cannot get an answer.

### Django

<!-- examples/django_webhook.py -->
```python
import logging
import os

from django.db import models, transaction
from django.http import HttpRequest, HttpResponse
from django.urls import path
from django.views.decorators.csrf import csrf_exempt
from django.views.decorators.http import require_POST

from calyx_client_sdk import (
    BulkFinishedEvent,
    CalyxError,
    ConnectionUpdateEvent,
    InvalidPayloadEvent,
    MessageReceivedEvent,
    MessageStatusEvent,
    OtherEvent,
    verify_webhook,
)

log = logging.getLogger(__name__)
SECRET = os.environ["CALYX_WEBHOOK_SECRET"]


# Put this class in models.py of your app, without Meta.
class HandledEvent(models.Model):
    id = models.CharField(primary_key=True, max_length=255)

    class Meta:
        app_label = "shop"


# Calyx has no CSRF token. The signature shows who sent the request.
@csrf_exempt
@require_POST
def calyx_webhook(request: HttpRequest) -> HttpResponse:
    try:
        # request.body is the raw body. Parsed JSON does not pass the check.
        event = verify_webhook(request.body, request.headers, SECRET)
    except CalyxError as error:
        log.warning("Calyx webhook refused: %s", error.code)
        return HttpResponse(status=400)

    # When the code in the block fails, the id is not stored, and the next delivery is handled.
    with transaction.atomic():
        _, is_new = HandledEvent.objects.get_or_create(id=event.id)
        if is_new:
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
    return HttpResponse(status=204)


# Put this list in urls.py. The URL of the webhook in Calyx must be the same, to the last slash.
urlpatterns = [path("calyx/webhook", calyx_webhook)]
```

### Flask

The webhook, and a view that sends one message. A web view needs a short `time_limit`. The view
does not use a key after 23 hours.

<!-- examples/flask_webhook.py -->
```python
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
```

### FastAPI

The same two routes with `AsyncBackendClient`.

<!-- examples/fastapi_app.py -->
```python
import asyncio
import logging
import os
import sqlite3
import time
from contextlib import closing
from typing import Annotated

from fastapi import Depends, FastAPI, Request, Response
from fastapi.responses import JSONResponse

from calyx_client_sdk import (
    AsyncBackendClient,
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
app = FastAPI()
# One client for the whole program. It holds no connection, so it needs no lifespan.
client = AsyncBackendClient(os.environ["CALYX_URL"], os.environ["CALYX_API_KEY"])
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


async def raw_body(request: Request) -> bytes:
    return await request.body()


# A "def" route runs in a thread of FastAPI, so the database does not stop the event loop.
@app.post("/calyx/webhook")
def calyx_webhook(request: Request, body: Annotated[bytes, Depends(raw_body)]) -> Response:
    try:
        event = verify_webhook(body, request.headers, SECRET)
    except CalyxError as error:
        log.warning("Calyx webhook refused: %s", error.code)
        return Response(status_code=400)

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
    return Response(status_code=204)


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
@app.post("/orders/{order_id}/ready")
async def order_ready(order_id: str) -> JSONResponse:
    key, made_at, sent = await asyncio.to_thread(load_send, order_id)
    if sent:
        return JSONResponse({"state": "sent"}, 200)
    if time.time() - made_at > KEY_LIFE:
        return JSONResponse({"state": "check the chat"}, 409)
    try:
        await client.send_message(
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
                return JSONResponse({"state": "pending"}, 202)
            case RetryAdvice.DO_NOT_RETRY if error.code != BackendCode.DUPLICATE_MESSAGE:
                return JSONResponse({"state": "failed"}, 502)
            case _:
                return JSONResponse({"state": "check the chat"}, 409)
    await asyncio.to_thread(save_sent, order_id)
    return JSONResponse({"state": "sent"}, 200)
```

For a webhook with no secret, use `decode_event(body)`. Nothing shows who sent such a body.

## Pairing

Pairing links a WhatsApp number to an instance. A person scans a QR code with the phone.

| Step | Method |
|---|---|
| A new instance | `create_instance(provider=Provider.WHATSAPP_WEB, display_name=..., locale="es-ES")`. It starts the pairing, and `result.pairing.qr_text` is the first QR text. |
| An instance that needs a new pairing | `pair_instance(instance_id)` |
| Follow the pairing | `follow_pairing(instance_id)` gives `PairingUpdate` items, and then `PairingDone` or `PairingFailed`. |
| Stop the pairing | `cancel_pairing(instance_id)` |

The operator must allow WhatsApp Web for your organization, and its owner must accept the terms.
Before that, `create_instance` gives `whatsapp_web_not_allowed` or
`whatsapp_web_not_acknowledged`.

A pairing lasts up to 3 minutes. A worker of Gunicorn or Celery must not wait for that time. So a
background thread reads the stream and stores the last item, and the web view reads the store.
After `create_instance`, store `result.pairing` and start the same thread. A restart of the worker
stops the thread. Then call `start_pairing` again: it follows the pairing that runs.

<!-- examples/pairing_panel.py -->
```python
import logging
import os
import threading
import time
from typing import Literal, TypedDict

from calyx_client_sdk import (
    BackendClient,
    BackendCode,
    CalyxError,
    PairingDone,
    PairingFailed,
    PairingUpdate,
    RetryAdvice,
)
from calyx_client_sdk.models import InstanceStatus

log = logging.getLogger(__name__)
client = BackendClient(os.environ["CALYX_URL"], os.environ["CALYX_API_KEY"])


class PanelState(TypedDict):
    state: Literal["waiting", "connected", "ended", "error"]
    qr_text: str | None


# A dict works in one process. With more processes, store the state in your cache or database.
# The QR text is a secret: do not log it, and show it only to the owner of the number.
states: dict[str, PanelState] = {}


def save(
    instance_id: str,
    state: Literal["waiting", "connected", "ended", "error"],
    qr_text: str | None = None,
) -> PanelState:
    states[instance_id] = PanelState(state=state, qr_text=qr_text)
    return states[instance_id]


def panel_state(instance_id: str) -> PanelState | None:
    """Your web view calls it every 3 seconds, and the page shows the QR code."""
    return states.get(instance_id)


def start_pairing(instance_id: str) -> PanelState:
    """Your web view calls it when a person starts the pairing, and after a restart."""
    try:
        pairing = client.pair_instance(instance_id, pairing="qr").pairing
        state = save(instance_id, "waiting", pairing.qr_text)
    except CalyxError as error:
        if error.code == BackendCode.INSTANCE_CONNECTED:
            return save(instance_id, "connected")
        if error.code != BackendCode.PAIRING_IN_PROGRESS:
            raise
        # Follow the running pairing. Its first item has the QR text of now.
        state = save(instance_id, "waiting")
    # The stream lasts for minutes, so it runs in the background and not in the web view.
    name = f"pairing {instance_id}"
    threading.Thread(target=follow, args=[instance_id], name=name, daemon=True).start()
    return state


def follow(instance_id: str) -> None:
    """Follow the pairing to its end. The SDK never opens a broken stream again: this code does."""
    end: Literal["connected", "ended", "error"] = "error"
    try:
        for _ in range(100):
            try:
                end = wait_for_end(instance_id)
                break
            except CalyxError as error:
                log.warning("Pairing of %s: %s: %s", instance_id, error.code, error.detail)
                if error.retry not in (RetryAdvice.RETRY_NOW, RetryAdvice.RETRY_LATER):
                    break
                time.sleep(error.retry_after or 2)
    finally:
        # Also after an error of your own code, so the page never waits without end.
        save(instance_id, end)


def wait_for_end(instance_id: str) -> Literal["connected", "ended"]:
    try:
        with client.follow_pairing(instance_id) as stream:
            for item in stream:
                match item:
                    case PairingUpdate(pairing=pairing):
                        save(instance_id, "waiting", pairing.qr_text)
                    case PairingDone():
                        return "connected"
                    case PairingFailed(code=code, detail=detail):
                        log.info("Pairing of %s: %s: %s", instance_id, code, detail)
                        return "ended"
    except CalyxError as error:
        if error.code != BackendCode.NOT_FOUND:
            raise

    # No pairing runs and the backend keeps no failure. Read the instance.
    try:
        instance = client.get_instance(instance_id)
    except CalyxError as error:
        if error.code == BackendCode.NOT_FOUND:
            # The backend deletes a new instance when its pairing fails.
            return "ended"
        raise
    return "connected" if instance.status == InstanceStatus.CONNECTED else "ended"
```

A `not_found` of `follow_pairing` on a new follow means that no pairing runs and that the backend
keeps no failure. Read `get_instance`: the status `CONNECTED` means done.

Rules for a stream:

1. Read it in a `with` or `async with` block. The end of the block closes the connection. An async
   stream with no block can make `asyncio` write error lines when the loop ends.
2. Use one stream in one thread or task at a time. When the block ends while another task reads,
   Python raises `RuntimeError`.
3. A cancelled read, for example by `asyncio.timeout`, ends the stream. Follow the instance again.
4. A stream has no time limit, also when the client has one. `follow_pairing(..., time_limit=...)`
   sets a limit for the whole stream.
5. The QR text and the pairing code are secrets: do not store them and do not log them. `repr` of an
   item does not show them.

## Bulk jobs

A bulk job is one notice for people who asked for it, for example an appointment reminder. The core
enforces its delays, and no option turns them off. Never use a bulk job for unsolicited messages.
The example reads the numbers, with commas between them, from `CALYX_RECIPIENTS`.

<!-- examples/bulk_job.py -->
```python
import os
import sys
import time
from pathlib import Path

from calyx_client_sdk import BackendClient, new_idempotency_key
from calyx_client_sdk.models import BulkJobStatus

client = BackendClient(os.environ["CALYX_URL"], os.environ["CALYX_API_KEY"])

# Only people who asked for this message. Never send unsolicited messages.
recipients = [number.strip() for number in os.environ["CALYX_RECIPIENTS"].split(",")]

# One key for each job, stored before the call. The same key gives the same job.
# Calyx forgets a key after 24 hours. After that, the same key sends to everyone again.
key_file = Path("reminder-2026-11-23.key")
if not key_file.exists():
    key_file.write_text(new_idempotency_key())
if time.time() - key_file.stat().st_mtime > 23 * 60 * 60:
    sys.exit("The key is too old. Find the job with list_bulk_jobs.")

job = client.create_bulk_job(
    os.environ["CALYX_INSTANCE_ID"],
    recipients=recipients,
    text="Your appointment is tomorrow at 10:00.",
    idempotency_key=key_file.read_text(),
).data

# The core sends the messages with its own delays. The webhook bulk.finished gives the end too.
while job.status in (BulkJobStatus.QUEUED, BulkJobStatus.RUNNING):
    time.sleep(10)
    job = client.get_bulk_job(job.id)
    print(job.status, f"{job.progress_percent} %")

print("Sent:", job.sent_messages, "Failed or unknown:", job.failed_messages)
```

## The client

| Argument | Meaning |
|---|---|
| `base_url` | It starts with `http://` or `https://`. Any other value gives `invalid_config`. |
| `api_key` | Without it, the SDK sends no `Authorization` header. |
| `time_limit` | The limit in seconds of each call. A `time_limit` of a call wins. |
| `ca_file` | A PEM file with a private certificate authority. The system certificates stay valid. |

The client holds no connection. There is nothing to close, and threads, event loops and forked
processes can share one client.

| Call | Default time limit | Reason |
|---|---|---|
| `send_message` | 360 seconds | The backend waits up to 5 minutes for the core. |
| `create_bulk_job` | 150 seconds | The backend needs up to 120 seconds. |
| Other calls | 120 seconds | The backend needs up to 30 seconds. |
| `follow_pairing` | None | The connect and the TLS handshake have 30 seconds each. A silence of 45 seconds gives `stream_failed`. |

| Methods | |
|---|---|
| Messages | `send_message` |
| Bulk jobs | `create_bulk_job`, `list_bulk_jobs`, `get_bulk_job`, `cancel_bulk_job` |
| Instances | `list_instances`, `create_instance`, `get_instance`, `update_instance`, `remove_instance` |
| Pairing | `pair_instance`, `follow_pairing`, `cancel_pairing` |
| Clients | `list_clients`, `create_client`, `get_client`, `update_client`, `delete_client` |
| API keys | `list_api_keys`, `revoke_api_key` |
| You and your organization | `get_me`, `get_organization`, `update_organization` |

- **Names.** Each name is in snake_case. The fields of the HTTP API keep their names. The event
  fields change: `messageId` is `message_id`, and `from` is `from_`, because `from` is a word of
  Python.
- **Arguments.** The id comes first. All other arguments have names. `None` leaves a field out, and
  an empty list stays an empty list.
- **Answers.** Each answer is a read-only dataclass of `calyx_client_sdk.models`. A send gives
  `Sent`, with `data` and `idempotency_key`. `remove_instance` gives 204 when the instance is gone,
  and 202 when the removal goes on.
- **Known values.** Classes such as `Provider`, `InstanceStatus` and `BulkJobStatus` have the known
  values. The type of a field is `str`, because a newer server can send other values.
- **`get_me`.** Most fields of `get_me().organization` are `None` when `user.role` is `client`.
- **Types.** The package passes `mypy --strict` and strict `pyright`. Write
  `provider=Provider.WHATSAPP_WEB` in the call. After `p = Provider.WHATSAPP_WEB`, mypy reads `p` as
  `str` and refuses it: write `p: Final = Provider.WHATSAPP_WEB`. Django and Celery have no types of
  their own, so strict `pyright` needs stubs for them.

## Known limits

| Area | Limit |
|---|---|
| DNS | The time limit of a sync call does not cover the DNS lookup. This is also true for the connect limit of a sync stream. |
| DNS, async | After a slow lookup, the call ends at its limit. But `asyncio.run` waits for the thread of the lookup. |
| Proxy | The SDK ignores `HTTP_PROXY` and `HTTPS_PROXY`. When only a proxy gives access, the call gives `network_error` (`RETRY_NOW`). |
| Certificates | `SSL_CERT_FILE` and `SSL_CERT_DIR` replace the system certificates. |
| TLS, sync, Windows and macOS | Threads of one client make their TLS handshakes one after the other. The wait has no limit. We read this in the source of `truststore`, and did not run it. |
| Large request, async | The server can answer early and close. With a request of more than about 1 MB, the async client then gives `network_error`, and the sync client gives the answer. |
| Coroutine | `coro.close()` on a call that waits raises `RuntimeError` ("coroutine ignored GeneratorExit"). Cancel the task. |
| Pickle | A pickle of a client has the API key, and it fails after the first HTTPS call. Do not give it to a spawned process: make a client there. Forked processes can share one. |
| Signals, sync | A `TimeoutError` of a `SIGALRM` handler becomes a `CalyxError`. During a connect with a second address, it is lost. |
| Hash | An `OtherEvent` or an `InvalidPayloadEvent` with a dict in `data` has no hash. Store the ids, not the events. |
| Size | An answer of more than 32 MiB, or a pairing event of more than 1 MiB, gives `invalid_response`. |

## Coming from Evolution API

Calyx is a different system, not a copy. This table has the names of Evolution API v2. Check them in
the documentation of your version.

| Evolution API | Calyx |
|---|---|
| An `apikey` header | An API key of your organization. The client sends it. |
| `instanceName` in the URL | The `id` of the instance |
| `POST /instance/create` and a QR code | `create_instance`, then `follow_pairing` ([Pairing](#pairing)) |
| `GET /instance/connect/{instance}` | `pair_instance` |
| `GET /instance/connectionState/{instance}` | `get_instance(...).status` |
| `POST /message/sendText/{instance}` with `number` and `text` | `send_message(instance_id, to=..., text=...)` |
| A retry after a timeout, which can send two times | A retry with the same stored key, which sends one time within 24 hours |
| The webhook event `MESSAGES_UPSERT` | `MessageReceivedEvent`, signed with HMAC-SHA256 ([Webhooks](#webhooks)) |
| The webhook event `MESSAGES_UPDATE` | `MessageStatusEvent` |
| The webhook event `CONNECTION_UPDATE` | `ConnectionUpdateEvent` |
| An optional `delay` for each message | Minimum delays in the core. No option turns them off. |

## Versions

Update the backend before the SDK. The backend refuses fields that it does not know.

The license of the SDK is Apache-2.0. The backend and the core are AGPL-3.0-only.

## Work on this package

Run all the Python tools in Docker, from `packages/client-sdk/python`. Do not install them on the
host.

| Command | What it does |
|---|---|
| `docker/run.sh test` | The tests on Python 3.12 and 3.14 |
| `docker/run.sh lint` | `ruff` |
| `docker/run.sh types` | `mypy` and `pyright` in strict mode, also on the examples |
| `docker/run.sh readme-check` | The Python code of this file is the code of `examples/` |
| `docker/run.sh wheel-check` | The contents of the wheel |
| `docker/run.sh lock` | The lock file of the tools |

Each Python block of this file is a whole file of `examples/`, with the name of the file in a
comment above it. Change the example first, and then copy it here. The tests run each example
against a fake backend. No test sends a real message.
