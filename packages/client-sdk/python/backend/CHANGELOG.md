# Changelog

All changes of `calyx-client-sdk` are in this file. The format follows
[Keep a Changelog](https://keepachangelog.com/). The versions stay at `0.x` while the contracts
change.

## Unreleased

Changed:

- The answers of `getMe` and of the clients have new required fields. This version needs a backend
  that sends them.

## 0.1.0

First version, early alpha. The package is not published.

Added:

- `BackendClient` and `AsyncBackendClient`: one method for each of the 22 calls that an API key can
  make, and the pairing stream `follow_pairing`. Each call has a time limit for the whole call.
- Sends with an idempotency key: `send_message`, `create_bulk_job`, `new_idempotency_key` and
  `Sent`.
- `CalyxError` with the retry advice, and the code lists `BackendCode`, `CoreCode` and `ClientCode`.
  The text of an error has `detail` only when the SDK found the problem.
- `verify_webhook`, `decode_event`, the event classes and `Event`, the union of the classes.
- `calyx_client_sdk.models`: the classes of the answers, of the nested arguments and of the known
  values, generated from `backend.json`. `repr` hides the secrets, also the QR text and the pairing
  code.
- A README with examples for scripts, `asyncio`, Celery, Django, Flask and FastAPI. The tests run
  each example against a fake backend.

Requirements: Python 3.12 or later, and `httpcore2`. No gRPC code.
