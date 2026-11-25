# Changelog

All changes of `calyx-client-sdk-core` are in this file. The format follows
[Keep a Changelog](https://keepachangelog.com/). The versions stay at `0.x` while the contracts
change.

## 0.1.0

First version, early alpha. The package is not published.

Added:

- `CoreClient` and `AsyncCoreClient`: one method for each of the 10 calls of the core, with a time
  limit for the whole call.
- The streams `create_instance` and `subscribe`. The events have the classes of `calyx-client-sdk`.
- `recipients`, `messages` and `job_ids` are required. The core refuses an empty list.
- Sends with an idempotency key, and `CalyxError` with the retry advice, as in `calyx-client-sdk`.
- `calyx_client_sdk_core.models`: the classes of the answers, of the nested arguments and of the
  known values, generated from the `.proto` files. `repr` hides the secrets, also the QR text and the
  pairing code.
- A keepalive ping every 2 minutes while a call or a stream runs. When the core host disappears, they
  get `UNAVAILABLE` within about 2 minutes and 20 seconds.
- A check that refuses a call after a fork, and a call in a second event loop.
- A README with examples for a send, a pairing and the events. The tests run each example against a
  fake core.

Requirements: Python 3.12 or later, `calyx-client-sdk` of the same version, `grpcio` 1.80 or later,
`protobuf` 6.33.5 or later (below 8) and `googleapis-common-protos` 1.76 or later.
