# Calyx core client for Python

A Python client for the gRPC API of the Calyx core. It has the 10 calls and the 2 streams of the
core, in sync and in async, with full types for your IDE.

```text
your program ──gRPC──▶ Calyx core ──▶ WhatsApp        (one private network)
```

> **Caution:** WhatsApp Web through Calyx is an unofficial integration. Its use is against the Terms
> of Service of WhatsApp. WhatsApp can ban a number, and no setup prevents a ban. Never send
> unsolicited messages. The core always applies its delays, also to a direct call.

Status: version 0.1.0, early alpha. The package is not on PyPI. The versions stay at `0.x` while
the contracts change.

## When to use it

The core is a server that runs alone. The backend is another server, in front of the core. It adds
users, API keys and HTTPS.

| Your setup | Use |
|---|---|
| You run only the core | This package. Your program runs on the private network of the core. |
| You have a backend | The backend client, `calyx-client-sdk`. It calls the backend over HTTPS with an API key, and it installs no gRPC. Use this package only for a thing that only the core gives, for example the stream of all events. |

> **Caution:** With a backend, never make, rename or remove an instance with this package. The
> backend does not know it, and its records become wrong.

> **Caution:** The core has no users and no tenants. Its token gives full access to every instance.
> The core serves no TLS. Never open its port to the internet.

## Start here

### Before you start

You need a running core. `docs/installation-options.md` and `apps/core/README.md` of the Calyx
repository say how to start one. When another person runs your core, ask that person for these
values:

| Value | Environment variable of the examples |
|---|---|
| The host and the port of the core, for example `127.0.0.1:50051` | `CALYX_CORE_ADDRESS` |
| The core token (`GRPC_AUTH_TOKEN` of the core). Keep it secret. | `CALYX_CORE_TOKEN` |
| The name of an instance that is connected, for example `sales`. No instance yet? See [Pairing](#pairing). | `CALYX_INSTANCE_NAME` |

An instance is one WhatsApp number that Calyx manages. The backend names an instance `i_` and its id
without hyphens. You also need Python 3.12 or later.

### Install

Get the Calyx repository. In the folder of your program, make a virtual environment. Install the two
packages in one command, with their paths in the Calyx repository:

```bash
python3 -m venv .venv
source .venv/bin/activate  # Windows: .venv\Scripts\activate
pip install /path/to/calyx/packages/client-sdk/python/backend \
  /path/to/calyx/packages/client-sdk/python/core
```

The import name is `calyx_client_sdk_core`. The errors, the events, `Sent` and
`new_idempotency_key` come from `calyx_client_sdk`.

### The first message

1. Set the variables. For `CALYX_TO`, use your own second phone: the country code and the number,
   with digits only.

   ```bash
   export CALYX_CORE_ADDRESS=127.0.0.1:50051
   export CALYX_CORE_TOKEN=...
   export CALYX_INSTANCE_NAME=sales
   export CALYX_TO=...
   ```

2. Save this code as `send.py`.
3. Run `python send.py`. It can wait for some minutes, because the core sends with its own delays.

<!-- examples/send.py -->
```python
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
```

`AsyncCoreClient` has the same methods and arguments. Write `await` before each call, and
`async with` and `async for` for a client and a stream. `create_instance` and `subscribe` have no
`await`.

The key rules are the ones of the backend client: one key for each message, stored before the call,
and never used after 24 hours. A cancelled async call gives no key and no result.

## Errors

A call that fails raises `CalyxError` of `calyx_client_sdk`. The SDK never sends a call again by
itself. The README of the backend package, section "Errors", has all the fields, the meaning of each
`retry` and a recipe for a send. Here are the differences:

| Field | In the core client |
|---|---|
| `source` | `Source.CORE` or `Source.CLIENT` (the SDK found the problem) |
| `code` | Compare it with `CoreCode` or `ClientCode`. It is a reason of the core, or the name of a gRPC status. |
| `status` | The number of the gRPC status. 0 when the source is the SDK. |
| `retry_after` | The minimum wait in seconds. 0 when the core gave none. |
| `metadata` | More facts of the core, for example `at`. |
| `errors`, `request_id` | Always empty |

| Code | `retry` | What to do |
|---|---|---|
| `CoreCode.UNAVAILABLE` | `RETRY_NOW` | No connection, or the instance is not connected. Wait for `error.retry_after or 1` seconds: it is 60 when the instance is not connected. Then call again with the same key. |
| `CoreCode.SEND_IN_PROGRESS` | `RETRY_LATER` | The first send with this key is not finished. Wait for `retry_after`, and call again with the same key. |
| `CoreCode.INSTANCE_QUEUE_FULL` | `RETRY_LATER` | Too many direct messages wait. Wait for `retry_after`, and call again with the same key. |
| `CoreCode.SEND_OUTCOME_UNKNOWN` | `CHECK_IF_SENT` | The core does not know if the message is out, and the key cannot send again. Look at the chat. Use a new key after `metadata["at"]` plus 10 minutes. The code `send_outcome_unknown` of the backend is another one: its advice is `RETRY_NOW`. |
| `CoreCode.DUPLICATE_MESSAGE` | `DO_NOT_RETRY` | A send of the same text to this recipient is in progress, or it went out or got an unknown result less than 10 minutes ago. |
| `CoreCode.IDEMPOTENCY_KEY_CONFLICT` | `DO_NOT_RETRY` | The key belongs to another message of this instance. |
| `CoreCode.UNAUTHENTICATED` | `DO_NOT_RETRY` | Check the core token. |
| `CoreCode.NOT_FOUND` | `DO_NOT_RETRY` | Check the name of the instance. `client.list_instances()` gives the names. |
| `ClientCode.TIMEOUT` | `RETRY_NOW` | The time limit passed. Call again with the same key. |
| `ClientCode.ABORTED` | `RETRY_NOW` | Your program closed the client. Call again with the same key and a new client. |
| `ClientCode.INVALID_CONFIG` | `DO_NOT_RETRY` | An argument is wrong, or a rule of this file ([fork](#fork), [event loop](#event-loop), one reader). No call went out. |

`invalid_response` has the advice `RETRY_NOW` when the answer is no protobuf message. It has
`UNKNOWN` when the answer has the wrong shape.

> **Caution:** After a send that failed, never send again with a new key at once. The first message
> can be out. The same key sends no second message.

## Streams

Both streams start at the first read, and the end of the `with` block stops them. They have no time
limit, unless you give `time_limit`: it counts the whole stream. Use one reader for each stream:
a second thread or task that reads gets `invalid_config`. A cancelled read ends an async stream.

### Pairing

`create_instance` makes an instance, or pairs an instance again that has the status `NEEDS_AUTH`.
The class of `provider` chooses the provider:

| `provider` | What the stream gives |
|---|---|
| `WhatsAppWebSettings(locale="es-ES")` | QR codes. `pairing_code` is the QR text, and your program draws it. Show only the newest one. |
| `WhatsAppWebSettings(locale="es-ES", pairing_code=PairingCodeSettings(phone_number=...))` | One code that the person types on the phone |
| `MetaCloudSettings(phone_number_id=..., access_token=..., app_secret=...)` | One update. There is no pairing. |

This program also reads the phone number, with digits only, from `CALYX_PHONE`:

<!-- examples/create_instance.py -->
```python
import os
import sys

from calyx_client_sdk import CalyxError

from calyx_client_sdk_core import CoreClient
from calyx_client_sdk_core.models import PairingCodeSettings, WhatsAppWebSettings

# The class of the provider object chooses the provider. Without `pairing_code`, the core gives QR
# codes: each update has a new QR text, and your program draws it.
provider = WhatsAppWebSettings(
    locale="es-ES",
    pairing_code=PairingCodeSettings(phone_number=os.environ["CALYX_PHONE"]),
)

try:
    with (
        CoreClient(
            os.environ["CALYX_CORE_ADDRESS"], os.environ["CALYX_CORE_TOKEN"], insecure=True
        ) as client,
        client.create_instance(os.environ["CALYX_INSTANCE_NAME"], provider=provider) as stream,
    ):
        # Read to the end. When the program stops before it, the core stops the pairing.
        for update in stream:
            print(update.status, update.message)
            if update.pairing_code is not None:
                # A secret: show it only to the owner of the number.
                print("Type this code on the phone:", update.pairing_code)
except CalyxError as error:
    sys.exit(f"{error.code}: {error.detail} ({error.retry})")

# The stream ended with no error.
print("Connected")
```

- The stream ends after the update `CONNECTED`.
- `DEADLINE_EXCEEDED` means that nobody used the code in time. Run the program again: it pairs the
  same instance.
- After another error, read `get_instance_status`. With `NEEDS_AUTH`, call `create_instance` again.
- The code is a secret: with it, a person can link a WhatsApp account to the instance. Do not store
  it and do not log it. `repr` of an update does not show it.

### Events

<!-- examples/subscribe.py -->
```python
import logging
import os
import sys
import time

from calyx_client_sdk import (
    BulkFinishedEvent,
    CalyxError,
    ClientCode,
    ConnectionUpdateEvent,
    InvalidPayloadEvent,
    MessageReceivedEvent,
    MessageStatusEvent,
    OtherEvent,
    RetryAdvice,
)

from calyx_client_sdk_core import CoreClient

log = logging.getLogger(__name__)


def follow(client: CoreClient) -> None:
    """Handle the events until the stream breaks.

    The core keeps 256 events for a slow program, and drops the events after them. Do slow work in
    another thread or process.
    """
    with client.subscribe() as stream:
        for event in stream:
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
                    log.error("Event %s cannot be read: %s", event.id, reason)
                case OtherEvent():
                    log.info("Event %s is not used", event.event)


def main() -> None:
    with CoreClient(
        os.environ["CALYX_CORE_ADDRESS"], os.environ["CALYX_CORE_TOKEN"], insecure=True
    ) as client:
        # The SDK never opens a broken stream again: this loop does. The core delivers each event
        # at most once, so the events between two streams are lost.
        while True:
            try:
                follow(client)
            except CalyxError as error:
                # One event that is not valid ends the stream. A new stream gives the next events.
                fatal = error.retry is RetryAdvice.DO_NOT_RETRY
                if fatal and error.code != ClientCode.INVALID_EVENT:
                    sys.exit(f"{error.code}: {error.detail} ({error.retry})")
                log.warning("The stream ended: %s: %s", error.code, error.detail)
                time.sleep(error.retry_after or 2)


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    main()
```

> **Caution:** `subscribe` loses events. The core delivers each event at most once. An event that
> occurs while no stream is open is lost. The core keeps 256 events for a slow program, and drops
> the events after them.

A program that must not lose an event uses webhooks:

1. Give `webhooks=[WebhookConfig(url=..., event="message.received", secret=...)]` to
   `create_instance` or `update_instance_config`.
2. Run the core with `REDIS_URL` and its worker. The worker tries each delivery 4 times, and the
   queue survives a restart. Without Redis, the core tries 4 times in about 6 seconds, and then the
   event is lost.
3. Check each delivery with `verify_webhook` of `calyx_client_sdk`. The README of the backend
   package, section "Webhooks", has examples for Django, Flask and FastAPI.

The events have the classes of the webhook events. `event.instance` is the name of the instance. For
the annotation of your own function, import `Event` from `calyx_client_sdk`: it is the union of the
event classes.
The core never ends the stream of `subscribe`. A stream that ends gives an error, for example
`stream_failed`. A core host that disappears gives `UNAVAILABLE` within about 2 minutes and 20 seconds:
the client sends a keepalive ping every 2 minutes while a call or a stream runs.

## Fork

gRPC does not work in a child process of `os.fork` when the parent used it before. The SDK refuses
each call of such a child process: `invalid_config`, "Make no core call before the fork." It cannot
see a gRPC use outside the SDK.

- [ ] Make no core call before the fork. To make a client is safe: it opens nothing before its first
      call. So a client of a module is correct for Gunicorn and Celery, when no import makes a call.
- [ ] Or start the processes with `spawn` or `forkserver`, and make a client in each one.
- [ ] Never fork after an async call. The fork handlers of gRPC can stop the child process with
      signal 6 or 11, also when the child makes no core call.

`GRPC_ENABLE_FORK_SUPPORT=0` is no solution. With it, a child process that ends with `sys.exit`, or
that drops a stream, can end the stream of the parent. The next read of the parent then hangs.

## Event loop

Use an `AsyncCoreClient` in one event loop only. A call in another loop gives `invalid_config`. So
do not call `asyncio.run` for each message with one client: make the client inside the function that
`asyncio.run` runs.

## The client

| Argument | Meaning |
|---|---|
| `address` | The host and the port, with no scheme: `core.internal:50051`, `127.0.0.1:50051` or `[::1]:50051` |
| `token` | The core token. The SDK sends it with each call. |
| `insecure` | `True` uses no TLS: the token and the messages travel in clear text. Only for a private network that you trust. |
| `ca_file` | A PEM file with a private certificate authority. The system certificates stay valid. |
| `time_limit` | The limit in seconds of each call that is not a stream. A `time_limit` of a call wins. |

- **TLS.** Without `insecure`, the SDK uses TLS, for a proxy in front of the core. It trusts the
  system certificates, and not the list of gRPC.
- **No proxy.** The SDK ignores `HTTP_PROXY` and `HTTPS_PROXY`. A proxy would get the token.
- **One connection.** The first call opens it. Threads can share a `CoreClient`. Use a `with` block,
  or call `close()`. A call after `close()` gives `aborted`.

| Call | Default time limit | Reason |
|---|---|---|
| `send_plain_text` | 360 seconds | The slowest normal send waits 4 minutes in the core. At a shorter limit, the core removes a message that still waits. |
| Other calls | 120 seconds | They end fast. This is also true for the two bulk sends. The limit stops a call that hangs. |
| `create_instance`, `subscribe` | None | A stream stays open. |

| Methods | |
|---|---|
| Messages | `send_plain_text` |
| Bulk jobs | `send_bulk_same_message`, `send_bulk_custom_messages`, `get_bulk_job`, `get_bulk_jobs`, `cancel_bulk_job` |
| Instances | `create_instance` (stream), `list_instances`, `get_instance_status`, `update_instance_config`, `remove_instance` |
| Events | `subscribe` (stream) |

The three sends give `Sent`, with `data` and `idempotency_key`. The lists `recipients`, `messages` and
`job_ids` are required, and the core refuses an empty list. The classes of the answers, of the
nested arguments and of the known values are in `calyx_client_sdk_core.models`. No public name has a
type of gRPC or protobuf. `BulkJob`, `BulkJobStatus`, `DeviceIdentity`, `InstanceStatus` and
`Provider` are in the `models` modules of both packages, with other contents: check the import.

## Known limits

| Area | Limit |
|---|---|
| Protobuf names | A program that also loads its own generated code of the Calyx `.proto` files can get a conflict of names. |
| Core host that disappears | Each stream and each call that runs gets `UNAVAILABLE` within about 2 minutes and 20 seconds, and the next call opens a new connection. A `time_limit` that passes first gives `timeout`. The next calls can then use the dead connection until the connection fails, also in a new client while the old one is open. |
| Core that hangs | gRPC sends a ping only when its 2-minute timer ends during a call. With short calls only, and a core host that still answers TCP, a ping may never go out. After `timeout` errors in a row, close each client of this core, then make a new one. |
| Core that was down | gRPC waits longer after each failed connection. In our run, the calls gave `UNAVAILABLE` for 23 more seconds after an outage of 100 seconds. |
| Close | gRPC can keep a connection that is not ready for up to 20 seconds after `close()`. |
| Async and fork | In a child process that ends with `sys.exit`, `grpc.aio` sometimes hangs. We saw it in a program with `grpc.aio` alone. |
| Size | An answer of more than 4 MiB gives `RESOURCE_EXHAUSTED`. |

## Versions

| Package | Versions |
|---|---|
| `calyx-client-sdk` | Exactly the version of this package |
| `grpcio` | 1.80 up to the line below 2 |
| `protobuf` | 6.33.5 up to the line below 8 |
| `googleapis-common-protos` | 1.76 up to the line below 2 |

The tests run on Python 3.12 and 3.14, with the newest versions at the time of the lock file. A
smaller test uses the lowest versions, and the newest ones, on Python 3.12.

Update the core before the SDK. The license of the SDK is Apache-2.0. The core is AGPL-3.0-only.

## Work on this package

Run all the Python tools in Docker, from `packages/client-sdk/python`. Do not install them on the
host.

| Command | What it does |
|---|---|
| `docker/run.sh test-core` | The tests on Python 3.12 and 3.14, then `test-packaging` |
| `docker/run.sh test-packaging` | Both wheels in clean environments. It needs the network. |
| `docker/run.sh protoc` | The code of `_proto`, from the `.proto` files |

The README of the backend package has the other commands. They cover this package too. Never edit
`_proto` and `_generated`: run `pnpm --filter @calyx/client-sdk-python generate`. Change an example
first, and then copy it here. The tests run each example against a fake core. No test sends a real
message.
