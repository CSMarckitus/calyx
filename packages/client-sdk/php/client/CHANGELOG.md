# Changelog

All changes of `calyx/client-sdk` are in this file. The format follows
[Keep a Changelog](https://keepachangelog.com/). The versions stay at `0.x` while the contracts
change.

## Unreleased

Changed:

- A client error of `createClient` or `createInstance` (`timeout`, `network_error` or
  `invalid_response`) has the retry advice `Unknown`. It had `RetryNow`. These calls have no
  idempotency key, so a new call can make a duplicate. List the clients or the instances first.
- `CalyxException` has an optional last constructor argument for the retry advice.
- `Me::$organization` is the new class `MeOrganization`. It had the type `mixed` and held an array.
  Read `$me->organization->name`, not `$me->organization['name']`.
- `calyx/client-sdk-core`: the core client sends a keepalive ping every 2 minutes while a call or a
  stream runs. When the core host disappears, they get `UNAVAILABLE` within about 2 minutes and
  20 seconds. Before, a stream waited with no end.
- The answers of `getMe` and of the clients have new required fields. This version needs a backend
  that sends them.

## 0.1.0

First version, early alpha. The package is not published.

Added:

- `BackendClient`: one method for each of the 22 calls of the backend, and the stream
  `followPairing`, with a time limit for each call.
- `pairingSnapshot()`: the state of a pairing in one short call, for a web panel.
- Sends with an idempotency key: `sendMessage()`, `createBulkJob()`, `IdempotencyKey::new()`,
  `Sent`, `PendingSend`, `SendPendingException` and `checkSend()`.
- `CalyxException` with the retry advice, and the code lists `BackendCode`, `CoreCode` and
  `ClientCode`. The PHP-only client codes are `pending_send_expired` and `unsupported_runtime`.
- `Webhook::verify()`, `Event` and its payload classes `MessageReceived`, `MessageStatus`,
  `ConnectionUpdate` and `BulkFinished`.
- The request and answer classes, generated from `backend.json`.
- A README with examples for Laravel and plain PHP. `ExamplesTest` runs the examples on a fake
  transport.

Requirements: PHP 8.3 or later, `ext-curl`, `ext-json` and `ext-hash`. No gRPC code.
