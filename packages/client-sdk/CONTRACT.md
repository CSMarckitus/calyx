# The shared contract of the Calyx client SDKs

Every SDK language follows this contract. A language README lists its own runtime values, for
example a time limit that depends on its HTTP client, with the reason for each.

The contract has eleven parts:

1. [Terms](#1-terms)
2. [The backend operations](#2-the-backend-operations)
3. [The core calls](#3-the-core-calls)
4. [The error shape](#4-the-error-shape)
5. [The code lists](#5-the-code-lists)
6. [The retry advice](#6-the-retry-advice)
7. [The idempotency key](#7-the-idempotency-key)
8. [The server time numbers](#8-the-server-time-numbers)
9. [The stream rules](#9-the-stream-rules)
10. [Webhooks and events](#10-webhooks-and-events)
11. [VB6](#11-vb6)

The shared test data is in [`testdata/`](./testdata). Each language test reads it.

## 1. Terms

Use these terms with one meaning each.

- **Instance**: one WhatsApp number that Calyx manages.
- **Instance name**: the name of an instance in the core. It is `i_` and the backend instance
  UUID without hyphens. A program uses it to match a webhook event to a backend instance.
- **Pairing**: the steps that link an instance to a WhatsApp number. The program shows a QR
  code or a pairing code.
- **API key**: the bearer token of a backend user.
- **Login cookie**: the cookie that a person gets after a login in a browser.
- **Core token**: the bearer token of the core. The core client sends it as call metadata.
- **Idempotency key**: the value of the `Idempotency-Key` header (backend) or of the
  `idempotency_key` field (core).
- **Time limit**: how long the SDK waits for an answer. `timeout` is only an error code.
- **The wire**: the bytes that the backend and the core send and receive.

## 2. The backend operations

Types come from the `.proto` files and `backend.json` by generation. No request or answer type
is written by hand. The hand-written parts are the operation table, the code constants and the
event payload types. A test checks each one against the contract.

The backend client has 23 operations. Each one uses the `operationId` of `backend.json`, in the
naming style of the language. A test fails when `backend.json` has an operation that is in
neither list.

**The 23 included operations:**

| Group | Operations |
|---|---|
| API keys | `listAPIKeys`, `revokeAPIKey` |
| Clients | `listClients`, `createClient`, `getClient`, `updateClient`, `deleteClient` |
| Instances | `listInstances`, `createInstance`, `getInstance`, `updateInstance`, `removeInstance`, `pairInstance`, `cancelPairing`, `followPairing` |
| Messages and bulk jobs | `sendMessage`, `createBulkJob`, `listBulkJobs`, `getBulkJob`, `cancelBulkJob` |
| Current user and organization | `getMe`, `getOrganization`, `updateOrganization` |

Only `followPairing` is a stream. The other 22 operations are normal calls.

**The 14 excluded operations:**

| Operation | Reason |
|---|---|
| `login` | A browser-session flow. |
| `logout` | It needs a login cookie. |
| `createAPIKey`, `inviteClientUser` | They need a login cookie. An API key cannot create a credential. |
| `requestPasswordReset`, `confirmPasswordReset` | Browser flows for a person, with no API key. |
| `acceptInvitation` | A browser flow for a person, with no API key. |
| `changePassword` | A leaked API key could take control of the login from its owner. It needs a login cookie. |
| `acknowledgeWhatsAppWeb` | A person must accept the WhatsApp Web risk. The integration is unofficial, and Calyx never promises that a number cannot be banned. It needs a login cookie. |
| `lookupInvitation`, `lookupPasswordReset` | Browser flows for a person, with no API key. The page shows the email of the token before the person sets a password. |
| `cancelInvitation`, `removeClientAccess` | They need a login cookie. A person decides who can log in to a client. |
| `followEvents` | The stream of changes for the screen of a browser. It needs a login cookie. |

Rules for the backend client:

1. Remove a final `/` from the base URL. Encode each path parameter.
2. Send `Authorization: Bearer <API key>` when the program sets an API key. Otherwise send no
   `Authorization` header.
3. Send `Content-Type: application/json` when the request has a body.
4. Return the parsed body of a `2xx` answer. Return nothing for a `204` answer. An operation
   can also return the HTTP status when its `2xx` codes differ in meaning. Go `RemoveInstance`
   returns 202 or 204.
5. The SDK does not refuse an answer because it has unknown fields. A language with typed
   answers can refuse an answer that lacks a required field or has a wrong JSON type.
6. Keep the field names of the wire, or rename them to the style of the language. The README says
   which.
7. An answer body has at most 32 MiB. A larger body gives the client error `invalid_response`.
8. The backend client sends `User-Agent: calyx-<language>-sdk/<version>` where the runtime
   allows it. A browser keeps its own value.

## 3. The core calls

The core client has 10 unary calls and 2 streams. Each one uses the RPC name of the `.proto`
files, in the naming style of the language. A test fails when an RPC has no method.

| Service | Unary calls | Streams |
|---|---|---|
| `InstanceService` | `RemoveInstance`, `UpdateInstanceConfig`, `ListInstances`, `GetInstanceStatus` | `CreateInstance` (the core pairing) |
| `MessageService` | `SendPlainText`, `SendBulkSameMessage`, `SendBulkCustomMessages`, `GetBulkJob`, `GetBulkJobs`, `CancelBulkJob` | none |
| `EventService` | none | `Subscribe` |

Rules for the core client:

1. Send the core token as `authorization: Bearer <core token>` call metadata on every call.
2. The core has no users and no tenants. Use the core client only on the private network of the
   core.
3. A language that has gRPC only through a native extension puts the core client in a separate
   package. A program that uses only the backend client installs nothing of gRPC. The README
   says so.

## 4. The error shape

Every SDK reports a failure with one error shape. The field names below use the style of the
wire. A language can change their letter case.

| Field | Meaning |
|---|---|
| `source` | `backend`, `core` or `client`. |
| `code` | The error code. It is an open string: a code that a newer server adds stays valid. |
| `status` | The HTTP status (backend), the gRPC status code number (core), or 0 (client). |
| `detail` | A text for the developer. It never holds the API key or the core token. |
| `retry` | The retry advice. See part 6. |
| `errors` | The field errors of a backend answer: a location and a message for each one. |
| `requestId` | Backend only: the `X-Request-Id` header. |
| `retryAfter` | The minimum wait in seconds: the `Retry-After` header (backend) or the `RetryInfo` detail (core). A language without a value gives 0 or leaves the field absent; its README says which. |
| `idempotencyKey` | The send calls only. See part 7. |
| `metadata` | Core only: the metadata of the `ErrorInfo` detail, for example `at`. |

Rules:

1. The backend sends an error as `application/problem+json`. The `code` field of the body is the
   error code.
2. The core sends its reason in a `google.rpc.ErrorInfo` detail with the domain `calyx.core.v1`.
   The SDK uses the reason as the code. Without this detail, the code is the canonical name of
   the gRPC status.
3. The SDK reads `Retry-After` only as a number of seconds.
4. An answer that is not problem+json (for example an HTML page from a proxy), or an answer
   larger than 32 MiB, gives the client code `invalid_response`.
5. The SDK keeps the cause of a client error when the language has a way to do so.
6. A language can rename a field when its runtime reserves the name. The README lists the new
   name. PHP uses `errorCode`, because `Exception::$code` is an integer.
7. A cancellation of the runtime (for example `CancelledError` in Python) passes unchanged. It
   carries no idempotency key.

## 5. The code lists

There are three code lists. They never share a string, so a program can check `code` alone.

1. **Backend codes** (35). They are lower case. The list is the `Problem.code` enum of
   `backend.json`. `api_key_not_allowed` is about how the caller logged in. `owner_only` is about
   the role of the caller.
2. **Core codes.** They are the `ErrorReason` names of the `.proto` files, without
   `ERROR_REASON_UNSPECIFIED`, and the 16 canonical gRPC status names, without `OK`. The
   reasons start with `ERROR_REASON_`. The status names are upper case.
3. **Client codes.** They occur in the SDK, not on the server:
   - `timeout`: the time limit passed.
   - `aborted`: the program canceled the call.
   - `network_error`: no connection, or a connection that stops.
   - `invalid_response`: the answer is not JSON, not problem+json, or larger than the size limit.
   - `stream_failed`: a stream broke or ended too early.
   - `invalid_signature`, `stale_timestamp`, `invalid_event`: webhook and event errors.
   - `invalid_config`: the SDK setup is wrong.

A language can add client codes for its own runtime. Its README lists them.

A code that is not in a list stays a valid string. Its retry advice is `unknown`. The SDK never
fails because of a new code.

## 6. The retry advice

The `retry` field tells the program what to do. The SDK never acts on it.

| Value | What the program does |
|---|---|
| `retry_now` | The outcome is unknown, or nothing happened. Send the same request again now, with the same idempotency key. |
| `retry_later` | The request can succeed later. Wait (`retryAfter` when it is set), then send the same request with the same idempotency key. `retryAfter` is a minimum, not a promise. |
| `check_if_sent` | The outcome is unknown, and the idempotency key cannot send again. Check the chat first. For a core error, send again with a new key after `metadata.at` plus 10 minutes. A backend error has no `at`: its `detail` gives the earliest time as text, or wait 10 minutes. |
| `do_not_retry` | The same request fails again or sends a duplicate. Change the request or the setup first. |
| `unknown` | The SDK has no advice. |

Old names: `same_key` is now `retry_now`, `same_key_later` is `retry_later`, `new_key_after` is `check_if_sent`, and `never` is `do_not_retry`.

The map from code to value is in [`testdata/retry-advice.json`](./testdata/retry-advice.json).
It has one row for each code: `{source, code, retry}`. This document does not copy the map.
Each language test compares its own map with the file in both directions.

A 2xx answer that is JSON with the wrong shape can have the advice `unknown`. The README says so.

Two codes look alike and differ:

- `send_outcome_unknown` (backend, 504): the backend did not get the answer of the core. A retry
  with the same idempotency key is safe.
- `idempotency_key_outcome_unknown` (backend, 409) and `ERROR_REASON_SEND_OUTCOME_UNKNOWN`
  (core): the core could not learn the result of the first send. The core never sends again with
  this idempotency key.

### Create calls

A create call has no idempotency key. When the answer does not arrive, the program cannot know if
the backend made the record. A second call can make a second record.

So the advice is `unknown` for some errors of `createClient` and `createInstance`. These names are
the `operationId` values of the backend. The error has the source `client`. Its code is `timeout`,
`network_error`, `invalid_response` or `aborted`. Every other error keeps the advice of
`retry-advice.json`.

The advice is also `unknown` when nothing was sent, because not every SDK can see the difference.
The core call `CreateInstance` is not in the rule. The program gives the name of the instance, so a
repeated call does not make a second instance.

The operations, the codes and the value are in [`testdata/create-calls.json`](./testdata/create-calls.json).
The Go, TypeScript, PHP and Python tests compare their own list with the file in both directions.
A language that never gives one of the codes can leave it out of its list, and its test says so.
The PHP backend client never gives `aborted`.
VB6 has no create call.

## 7. The idempotency key

These calls send a message or start a bulk job: the backend `sendMessage` and `createBulkJob`,
and the core `SendPlainText`, `SendBulkSameMessage` and `SendBulkCustomMessages`.

1. When the program gives an idempotency key, the SDK uses it.
2. When the program gives none, the SDK makes one. The format is a UUID version 4.
3. The backend call sends the key in the `Idempotency-Key` header. The core call sends it in the
   `idempotency_key` field.
4. The SDK returns the key with the answer.
5. The SDK puts the same key in every error of that call.
6. The SDK never sends a request again by itself. The program decides, with the retry advice.
7. The SDK never changes a request object of the program.
8. A cancellation of the runtime (for example `CancelledError` in Python) passes unchanged. It
   carries no idempotency key.

## 8. The server time numbers

These numbers come from the servers. They do not depend on a language.

| Number | Source |
|---|---|
| 5 minutes | The backend waits up to 5 minutes for a send in the core queue (`sendDeadline`). The slowest normal send on WhatsApp Web takes 4 minutes. |
| 60 + 60 seconds | `createBulkJob`: up to 60 seconds to read the body (`bulkBodyReadTimeout`) and 60 seconds for the core call (`bulkCreateDeadline`). |
| 30 seconds | The backend answers listBulkJobs within 30 seconds (listTimeout). |
| 15 seconds | The backend sends a ping on the pairing stream every 15 seconds (`pingInterval`). |
| 45 seconds | The pairing stream has failed when no data and no ping arrive for 45 seconds. |
| 2 minutes | The core pings idle connections every 2 minutes, so a long stream through a proxy stays open. |
| 1 minute | The core accepts a client ping at most once a minute, and only while a call runs (`keepalivePolicy`). |
| 256 events | The core keeps 256 events for each `Subscribe` stream (`subscriberBuffer`). |
| 5 minutes | The default tolerance of a webhook timestamp. |
| 10 minutes | The wait of the advice `check_if_sent`. |

Rules for time limits:

1. The default limit of a single-message send (backend `sendMessage`, core `SendPlainText`)
   is longer than the 5 minutes that the server waits. If the SDK cancels a core send, the core
   removes the message from its queue, so a core limit must be longer than 5 minutes. A runtime
   can force a shorter backend limit. TypeScript uses 290 seconds, because `fetch` stops after
   300 seconds. Then the language README gives the reason. The safe retry in rule 5 makes this
   acceptable.
2. The default limit of every other normal call (the bulk sends included) must be longer than
   its longest server wait.
3. Streams have no default limit.
4. A default limit never shortens a limit that the program sets.
5. A retry with the same idempotency key after `timeout` is safe. The backend continues the
   core call when the program stops waiting. The retry gets `send_in_progress` while the message
   waits, and `replayed: true` after the send.

A sync client of a language without a limit for the DNS lookup says so in its README.

The default time limits of the backend client are:

| Operation | Longest server wait | Default SDK time limit |
|---|---|---|
| `sendMessage` | 5 minutes | 6 minutes |
| `createBulkJob` | 120 seconds | 150 seconds |
| `listBulkJobs` | 30 seconds | 120 seconds |
| Every other operation | 10 seconds | 120 seconds |

Rule 2 applies to every row. Rule 1 gives the exception of `sendMessage` in TypeScript
(290 seconds).

VB6 has its own values: 360 seconds for `sendMessage` and 150 seconds for every other operation.
Both values are settable.

## 9. The stream rules

There are three streams: the backend `followPairing`, the core `CreateInstance` and the core
`Subscribe`.

1. A stream starts when the program starts to read it.
2. The SDK never opens a stream again by itself. The reason: the program must decide what to
   do with the events that it missed.
3. A broken stream gives the error of the break to the reader. The reader ends.
4. **Backend `followPairing`.** The stream uses Server-Sent Events (SSE) with the
   `Authorization` header.
   1. The items are `update`, `done` and `failed`. Pings and comments give no item.
   2. The stream ends after `done` or `failed`.
   3. The SDK sends `Accept: text/event-stream`. A `2xx` answer that is not an event stream, and
      data that is not JSON, give `invalid_response`.
   4. The SDK gives `stream_failed` when no data and no ping arrive for 45 seconds, or when the
      stream ends before `done` or `failed`.
   5. After `stream_failed`, the program can follow the instance again, or read it with
      `getInstance`. A `404` on a new follow means that no pairing runs and that the
      backend keeps no failure. Read the instance.
   6. When the program stops the reader, the SDK closes the connection. This does not cancel the
      pairing. `cancelPairing` does.
   7. One SSE event has at most 1 MiB. The size is the bytes that the SDK reads for one event,
      line terminators included. It counts every line up to and including the empty line, with
      comments and pings, and starts again at every empty line. The SDK checks it while it reads.
      A larger event gives `invalid_response` and closes the stream.
5. **Core `CreateInstance`.** This stream is the pairing. When the program stops the reader, the
   core stops the pairing. The instance stays in the state `NEEDS_AUTH`.
6. **Core `Subscribe`.**
   1. The stream has no normal end. A stream that ends with status `OK` gives `stream_failed`.
   2. A broken stream gives the gRPC status name as the code, for example `UNAVAILABLE`.
   3. The core delivers each event at most once. It has no replay.
   4. The core drops new events when a slow program fills the buffer of 256 events. It writes
      only a log line. Events that occur while no stream is open are lost.
   5. A program that must not lose an event uses webhooks. With `REDIS_URL` set, the core worker
      delivers webhooks with retries.
   6. An empty or absent filter means all instances and all event types.
7. **Core keepalive.** The core client finds a core host that disappears.
   1. The client sends a gRPC keepalive ping every 2 minutes. It waits 20 seconds for the answer.
   2. The client sends the pings only while a call or a stream runs. The core counts a ping with no
      call as a fault, and it closes a connection that makes too many faults.
   3. When the answer does not arrive, the connection closes. Each stream and each call that runs
      gives `UNAVAILABLE`. The source is `core`, and the advice is `retry_now`. This comes
      within about 2 minutes and 20 seconds.
   4. A time limit that passes earlier gives its own error, for example `timeout`. After a time
      limit, the next calls can use the dead connection until the connection fails.
   5. The SDK does not make the call again. After `UNAVAILABLE`, the next call opens a new connection.
   6. A connection that the program made (`NewFromConn` in Go) has only the keepalive that the
      program gave it.

## 10. Webhooks and events

### The signature

The core signs a webhook when the webhook has a secret. It sends two headers:

- `X-Calyx-Timestamp`: the Unix time in seconds, as decimal text.
- `X-Calyx-Signature`: `sha256=` and the lower-case hex of
  `HMAC-SHA256(secret, timestamp + "." + rawBody)`.

Caution: use the raw body, as the program received it. Parsing the JSON and encoding it again can
change the bytes, and the check then fails.

The check has these steps, in this order:

1. Refuse an empty secret (`invalid_signature`).
2. Read the two headers. The header names ignore letter case. A missing header gives
   `invalid_signature`.
3. Refuse a timestamp more than the tolerance before or after now (`stale_timestamp`). The
   default tolerance is 5 minutes.
4. Compute the HMAC. Compare it with the signature in constant time (`invalid_signature`).
5. Decode the body as an event (`invalid_event`).

Caution: when the clock of the receiver is wrong by more than the tolerance, the receiver
refuses every delivery. The core does not retry a 4xx answer, so these events are lost.

A webhook without a secret has no signature. The SDK offers no check for it. The program decodes
the body as an event. The case file for the signature is
`packages/shared-types/testdata/webhook-signature.json`, because the core tests it too.

### The event model

Every event has the same envelope:

| Field | Meaning |
|---|---|
| `id` | The event identifier. A retry delivers the same `id` again, so the program ignores an `id` that it already handled. |
| `event` | The event name. |
| `instance` | The instance name. |
| `timestamp` | The time of the event, in RFC 3339. |
| `data` | The payload of the event. |

The typed events and their payloads come from `event.proto`:

- `connection.update`
- `message.received`
- `message.status`
- `bulk.finished`

Other events (`whatsmeow.*` and `meta.webhook`) keep `data` as an untyped value.

Rules:

1. A body is an event when `id`, `event` and `instance` are non-empty strings, `timestamp` is a
   string and `data` is present. Otherwise the error is `invalid_event`. An empty `id` is refused,
   because programs remove duplicate events by `id`.
2. The SDK ignores unknown fields, so an older SDK reads the events of a newer core.
3. The status fields stay open. A newer core can send a new status value.
4. The status values are:
   - `connection.update`: the `InstanceStatus` names without `INSTANCE_STATUS_`, for example
     `CONNECTED`.
   - `bulk.finished`: the `BulkJobStatus` names without `BULK_JOB_STATUS_`, for example
     `COMPLETED`.
   - `message.status`: the `MessageDeliveryStatus` names without
     `MESSAGE_DELIVERY_STATUS_`, in lower case, for example `delivered`.
5. An event from the core `Subscribe` stream converts to the same envelope. `instance_name`
   becomes `instance`, `type` becomes `event`, `payload_json` becomes `data`, and `occurred_at`
   becomes `timestamp`, each in the naming style of the language. An invalid `payload_json`
   gives `invalid_event`.
6. A language can give a known event with a wrong payload as an own event class, in place of
   `invalid_event`.

The test data for events is `testdata/events.json`.

## 11. VB6

This part applies to the VB6 SDK only. Each rule names the rule that it replaces for VB6.

1. **Operations (replaces the 23 included operations of part 2).** The VB6 SDK has 7 backend
   operations. Its generator lists the included and the excluded operations. The generator fails
   on an operation of `backend.json` that is in neither list.
2. **Types (replaces "no type is written by hand" of part 2).** The request bodies,
   `CalyxSendResult` and `CalyxError` are written by hand. The generator checks their field
   names against `backend.json`. The other answers are VBA-JSON objects with the wire names.
3. **Streams and webhooks (replaces parts 3, 9 and 10).** The VB6 SDK has no core client, no
   stream and no webhook check. The owner or the client user pairs instances in the frontend.
4. **Tests.** The tests that need VB6 run by hand on Windows.
5. **Answer size (replaces rule 7 of part 2).** VB6 does not follow part 2 rule 7 yet. The run
   on Windows decides it.
