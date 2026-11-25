# @calyx/client-sdk (TypeScript)

Typed TypeScript clients for the Calyx backend (HTTP) and the Calyx core (gRPC), with a check for
the signature of webhooks.

Status: version 0.1, early alpha. The package is private and not published yet.
The versions stay at `0.x` while the contracts change.

## 1. Installation and the choice of a client

Use Node 22.12 or later. The package is ESM, and a CommonJS program can load it with `require()`
(see "Use from JavaScript" below). Install it from a local pack until it is published:

```bash
pnpm --filter @calyx/client-sdk build
pnpm --filter @calyx/client-sdk exec pnpm pack
```

The package has no root entry. Import only the entry that you need.

| Entry | Contents | Loads gRPC |
|---|---|---|
| `@calyx/client-sdk/backend` | `createBackendClient` | no |
| `@calyx/client-sdk/core` | `createCoreClient` (Node, Bun and Deno only) | yes |
| `@calyx/client-sdk/webhooks` | `verifyWebhook`, `parseEvent`, `CalyxEvent` | no |
| `@calyx/client-sdk/errors` | `CalyxError`, `isCalyxError`, the code types | no |

The `backend`, `webhooks` and `errors` entries have no run-time dependency and no `node:` import.
A bundle test checks this. The `core` entry loads gRPC (`@grpc/grpc-js`). In a web build, a
bundler takes the `browser` file of the `core` entry. That file has no gRPC code, and
`createCoreClient` throws a `CalyxError` with the code `unsupported_runtime`.

Choose a client with this rule:

1. Use the backend client when your program calls Calyx over HTTP with an API key.
2. Use the core client only when your program runs on the private network of the core, and
   needs no users or tenants. See section 3.
3. Use `webhooks` in the server that receives the webhooks of Calyx.

The options of `createBackendClient`:

- `baseUrl`: the address of the backend. The SDK removes a final `/`.
- `apiKey`: sent as `Authorization: Bearer <apiKey>`. Without it, the SDK sends no `Authorization`
  header. Do this in a browser app that calls its own server. Never put an API key in a browser.
- `timeoutMs`: the time limit of every call. The default is 290 seconds for `sendMessage`,
  150 seconds for `createBulkJob` and 2 minutes for the other calls. A `timeoutMs` in the options
  of one call has priority. `followPairing` has no time limit.
- `fetch`: a custom `fetch`. It must honour the `AbortSignal` of the request. If it does not,
  `timeoutMs` cannot stop the call.

In Node, the SDK sends `User-Agent: calyx-typescript-sdk/<version>`. A browser keeps its own value.

The SDK reads at most 32 MiB of one answer body and at most 1 MiB of one pairing event. A larger
answer or event gives the error code `invalid_response`. These limits have no option.

A call takes the path parameters and the body fields in one object. The names are the names of
the wire (`snake_case`). The SDK never converts them. The names of the calls are the
`operationId`s of the backend. The types come from the OpenAPI document of the backend.

## 2. Examples

Send one message. The SDK makes the idempotency key and returns it.

```ts
import { createBackendClient } from "@calyx/client-sdk/backend";

const calyx = createBackendClient({ baseUrl: "https://calyx.example.com", apiKey });
const { data, idempotencyKey } = await calyx.sendMessage({ id: instanceId, to: "34600000000", text: "Hello" });
console.log(data.message_id, data.replayed, idempotencyKey);
```

Send again after an unknown outcome. Use the same idempotency key.

```ts
try {
  await calyx.sendMessage(params);
} catch (err) {
  if (isCalyxError(err) && err.retry === "retry_now") {
    // The message goes out at most once. Look at the replayed field of the answer.
    await calyx.sendMessage(params, { idempotencyKey: err.idempotencyKey });
  }
}
```

Handle an error by its retry advice.

```ts
import { isCalyxError } from "@calyx/client-sdk/errors";

try {
  await calyx.sendMessage(params);
} catch (err) {
  if (isCalyxError(err, "duplicate_message")) console.warn(err.retry, err.detail, err.requestId);
  else throw err;
}
```

Pair an instance and follow the QR codes.

```ts
await calyx.pairInstance({ id: instanceId, pairing: "qr" });
for await (const item of calyx.followPairing({ id: instanceId })) {
  if (item.event === "update") showQr(item.data);
  else console.log(item.event, item.data); // "done" or "failed": the loop ends after it
}
```

`followPairing` never opens the stream again. It ends with a `CalyxError` in these cases:

- No data and no ping arrive for 45 seconds, or the stream ends before `done` or `failed`:
  the code is `stream_failed`. Call `followPairing` again, or read the instance with
  `getInstance`. A 404 on the new call means that no pairing runs and that the
  backend keeps no failure. Read the instance.
- The `AbortSignal` of the program fires: the code is `aborted`.
- A `break` in the loop ends the loop with no error.

A `break` or an abort closes the connection. It does not cancel the pairing. `cancelPairing` does.

Receive a webhook. Give the raw body, never parsed JSON.

```ts
import { verifyWebhook } from "@calyx/client-sdk/webhooks";

// In your HTTP handler. `rawBody` is a string or a Uint8Array.
const event = await verifyWebhook({ rawBody, headers: req.headers, secret });
if (event.event === "message.received") console.log(event.data);
```

### `getMe`

`getMe` returns the user of the API key and the organization, with the type `MeOrganization` of
the OpenAPI document. `id` and `name` always have a value. The other six fields are for an owner.
They are `undefined` when `user.role` is `client`. For an owner, `meta_webhook` and
`whatsapp_web_acknowledged_at` can be `null`.

```ts
const { user, organization } = await calyx.getMe();
console.log(user.role, organization.name, organization.instance_count);
```

### Use from JavaScript

The package is plain JavaScript with type files, so a JavaScript program uses the same entries
and calls. Only the type checks are lost. An ES module program uses `import`, as in the examples
above. A CommonJS program uses `require()`:

```js
const { createBackendClient } = require("@calyx/client-sdk/backend");
const { isCalyxError } = require("@calyx/client-sdk/errors");

async function main() {
  const calyx = createBackendClient({ baseUrl: "https://calyx.example.com", apiKey });
  const { data } = await calyx.sendMessage({ id: instanceId, to: "34600000000", text: "Hello" });
  console.log(data.message_id);
}

main().catch((err) => console.error(isCalyxError(err) ? err.code : err));
```

`require()` of this package needs Node 22.12 or later. A test loads every entry with `require()`.

## 3. The core client

Use the core client only on the private network of the core. The core has no users and no
tenants. It is meant for a private network, so access control is the work of the operator (or of
the backend). The core client sends the token of the core, `GRPC_AUTH_TOKEN`.

```ts
import { createCoreClient } from "@calyx/client-sdk/core";
import { isCalyxError } from "@calyx/client-sdk/errors";

const core = createCoreClient({ address: "core:50051", token, tls: false });
```

The options of `createCoreClient`:

- `address`: `host:port` of the core.
- `token`: sent as the metadata `authorization: Bearer <token>` on every call. The SDK does not use
  gRPC call credentials, because grpc-js refuses them on a channel without TLS.
- `tls`: required, with no default. Choose one value:
  - `false`: a private network without TLS.
  - `true`: TLS with the root certificates of Node (its default CA store, plus `NODE_EXTRA_CA_CERTS`).
  - `{ ca, cert, key }`: TLS with these contents. Give the contents, not file paths. Giving only
    one of `cert` and `key` makes grpc-js throw a plain `Error`, not a `CalyxError`.
- `timeoutMs`: the time limit of every call. The defaults are 6 minutes for `sendPlainText` and
  2 minutes for the other calls. A `timeoutMs` in the options of one call has priority. A stream
  has no time limit.

With `tls`, `address` must be a host name, not an IP address. grpc-js puts the host in the TLS
server name, and Node 26 refuses an IP address there. `createCoreClient` checks this. For an IP
address with `tls`, it throws the client error `invalid_config` at once.

The client sends a keepalive ping every 2 minutes while a call or a stream runs. When the core host
disappears, each of them gets `UNAVAILABLE` within about 2 minutes and 20 seconds, or its own time
limit if that passes first. After `UNAVAILABLE`, the next call opens a new connection. After a time
limit, the next calls can use the dead connection until the connection fails. The SDK never sends a
request again and never opens a stream again.

Known limit: after 2 minutes without a call, `@grpc/grpc-js` sends the ping just before the next call.
The core counts this ping as a fault when it sent nothing since the ping before it. The core allows
two faults in a row. So a program whose only calls often end before the first answer can get
`RESOURCE_EXHAUSTED` (`retry_later`). A call ends in this way when the program cancels it, or when its
own time limit passes. `grpc-js` then writes a line to the console, and waits longer between two
pings. The core may have started the refused call, so a retry must use the same idempotency key.

Listen to the events of one instance.

```ts
const core = createCoreClient({ address: "core:50051", token, tls: false });
try {
  for await (const event of core.subscribe({ instanceNames: ["i_..."] })) {
    if (event.event === "message.received") console.log(event.data);
  }
} catch (err) {
  // A broken stream ends the loop with a CalyxError, for example UNAVAILABLE.
  // Call subscribe again. The events between the two streams are lost.
  if (!isCalyxError(err)) throw err;
}
```

Rules for the streams:

1. The filters of `subscribe` are `instanceNames` and `eventTypes`. An absent or empty list means
   all.
2. Always `break` or abort the stream (the `signal` option). A dropped iterator keeps the call
   open.
3. After a broken `subscribe`, call `subscribe` again. The SDK never does it for you.
4. A `Subscribe` stream has no normal end. A stream that ends with the status `OK` gives
   `stream_failed`.

`createInstance` returns the pairing stream of the core. A `break` or an abort cancels the stream.
The core then stops the pairing. The instance stays in `NEEDS_AUTH`.

`getBulkJobs` gives the state of 1 to 50 jobs in one call. The core leaves out an unknown job, and
the order of the answer is not defined.

The three sends (`sendPlainText`, `sendBulkSameMessage` and `sendBulkCustomMessages`) follow the
rule of the backend. The SDK uses the idempotency key of the options, or makes a UUID. It returns
the key in the result and in every error.

```ts
const { data, idempotencyKey } = await core.sendPlainText({ instanceName: "i_...", to: "34600000000", text: "Hello" });
```

A call after `close()` rejects with the client error `aborted`. `close()` closes the channel.
`close()` does not end an open stream. Break or abort the streams first.

Caution: `subscribe` delivers each event at most once. The core keeps 256 events for each
subscriber. When a slow program fills this buffer, the core drops the new events. Events that occur
while no stream is open are also lost. A program that must not lose an event uses webhooks. With
`REDIS_URL` set, the core worker retries them.

Import the core client in a web build by mistake. The bundler takes the `browser` file
of the `core` entry. The code runs, and `createCoreClient` throws `unsupported_runtime`.

```ts
import { createCoreClient } from "@calyx/client-sdk/core";
import { isCalyxError } from "@calyx/client-sdk/errors";

try {
  createCoreClient({ address: "core:50051", token, tls: false });
} catch (err) {
  if (isCalyxError(err, "unsupported_runtime")) console.error(err.detail);
}
```

## 4. Errors and the retry recipe

Every failure is a `CalyxError` (except the setup error of `tls` in section 3). It has these fields:

| Field | Meaning |
|---|---|
| `source` | `backend`, `core` or `client` (an error of the SDK). |
| `code` | The error code. Lower case for the backend and the client. |
| `status` | The HTTP status (backend), the gRPC status code number (core), or `0` (client). |
| `detail` | A text for people. Log it. |
| `retry` | The advice. See the recipe below. |
| `requestId` | The `X-Request-Id` of the backend. Log it. |
| `retryAfter` | The minimum wait, in whole seconds. It comes from the `Retry-After` header of the backend or the `RetryInfo` detail of the core. Unset or 0 means no wait. |
| `idempotencyKey` | On a send, the key of the call. Every error of the call has it. |
| `errors` | The field errors of the backend. |
| `metadata` | The `ErrorInfo` metadata of the core, for example `at`. |

Use `isCalyxError(err)` to narrow an error. Use `isCalyxError(err, "duplicate_message")` to
narrow the code too. A code that the SDK does not know stays a string with `retry: "unknown"`.
The SDK never fails because of a new code.

The retry recipe:

1. Catch the error and read `err.retry`.
2. Act as the table says. The SDK never acts on `retry` and never sends a request again.
3. Always send again with the same idempotency key, except for `check_if_sent`.

| `retry` | What the program does | Backend codes | Core codes | Client codes |
|---|---|---|---|---|
| `retry_now` | The outcome is unknown, or nothing happened. Send the same request again now, with the same idempotency key. | `send_outcome_unknown`, `bulk_outcome_unknown`, `change_outcome_unknown` | `UNAVAILABLE`, `DEADLINE_EXCEEDED`, `CANCELLED` | `timeout`, `network_error`, `invalid_response`, `aborted`, `stream_failed` |
| `retry_later` | The request can succeed later. Wait for `retryAfter` seconds when it is set; it is the minimum wait. Then send the same request with the same idempotency key. | `send_in_progress`, `instance_queue_full`, `rate_limited`, `instance_not_connected`, `pairing_in_progress`, `core_unavailable` | `ERROR_REASON_SEND_IN_PROGRESS`, `ERROR_REASON_INSTANCE_QUEUE_FULL`, `RESOURCE_EXHAUSTED` | none |
| `check_if_sent` | The outcome is unknown, and the idempotency key cannot send again. Check the chat first. To send again, use a new idempotency key after the time in `err.metadata.at` plus 10 minutes. Only a core error has `metadata`. A backend error has no `at`: its `detail` gives the earliest time as text, or wait 10 minutes. | `idempotency_key_outcome_unknown` | `ERROR_REASON_SEND_OUTCOME_UNKNOWN` | none |
| `do_not_retry` | The same request fails again or sends a duplicate. Change the request or the setup first. | `duplicate_message`, `idempotency_key_conflict`, `invalid_request`, `unauthenticated`, `csrf_check_failed`, `owner_only`, `api_key_not_allowed`, `instance_creation_not_allowed`, `whatsapp_web_not_allowed`, `whatsapp_web_not_acknowledged`, `not_found`, `quota_exceeded`, `phone_number_id_in_use`, `pairing_not_supported`, `instance_in_error`, `instance_connected`, `instance_not_ready`, `client_has_instances`, `default_client`, `email_in_use`, `token_invalid`, `too_many_messages`, `core_rejected_token` | `ERROR_REASON_DUPLICATE_MESSAGE`, `ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT`, `INVALID_ARGUMENT`, `NOT_FOUND`, `ALREADY_EXISTS`, `PERMISSION_DENIED`, `FAILED_PRECONDITION`, `OUT_OF_RANGE`, `UNIMPLEMENTED`, `UNAUTHENTICATED` | `invalid_signature`, `stale_timestamp`, `invalid_event`, `unsupported_runtime`, `invalid_config` |
| `unknown` | The SDK has no advice. | `internal`, `conflict`, and every code that the SDK does not know | `UNKNOWN`, `ABORTED` (without a reason), `INTERNAL`, `DATA_LOSS`, and every code that the SDK does not know | `timeout`, `network_error`, `invalid_response` and `aborted` of `createClient` and `createInstance` |

The client codes of this version are `timeout`, `aborted`, `network_error`, `invalid_response`
(an answer that is not JSON, for example an HTML page from a proxy), `stream_failed`,
`invalid_signature`, `stale_timestamp`, `invalid_event`, `unsupported_runtime` (the `core`
entry in a web build) and `invalid_config` (an IP address with `tls` in `createCoreClient`). The backend has 35 codes. The `errors` entry exports the code types.

Two outcome-unknown codes differ:

- `send_outcome_unknown` (504): the backend did not get the answer of the core. A retry with the
  same idempotency key is safe.
- `idempotency_key_outcome_unknown` (409): the core could not learn the result of the first send.
  The core never sends again with this idempotency key.

A retry of a send after `timeout` with the same idempotency key is safe. The backend continues
the send when the program stops waiting. A retry with the same idempotency key gets
`send_in_progress` while the message waits. After the send, it gets `replayed: true`.

`CalyxErrorInit` has the optional field `retry`, which sets the advice of an error that you make
yourself.

Caution: a create call can fail after the backend made the client or the instance. The calls
`createClient` and `createInstance` of the backend client have no idempotency key. A client error
of these two calls (`timeout`, `network_error`, `invalid_response` or `aborted`) has the advice
`unknown`, not `retry_now`. After this error, list the clients or the instances before you call
again. A second call can make a duplicate.

## 5. Check a webhook

The core signs a webhook when the webhook has a secret. It sends two headers:
`X-Calyx-Timestamp` (Unix seconds) and `X-Calyx-Signature`
(`sha256=` and the hex of `HMAC-SHA256(secret, timestamp + "." + rawBody)`).

Caution: give `rawBody` as the bytes that you received (`string` or `Uint8Array`). Never give
JSON that your program parsed and encoded again. The bytes can change, and the check then fails.

`verifyWebhook({ rawBody, headers, secret, toleranceSeconds?, now? })`:

1. Refuses an empty secret, and a missing header (`invalid_signature`).
2. Refuses a timestamp more than `toleranceSeconds` (default 300) from `now`
   (`stale_timestamp`).
3. Compares the signature in constant time (`invalid_signature`).
4. Returns the typed event, or throws `invalid_event` when the body is not a Calyx event.

When the check fails, answer 401. Do not process the body.

Caution: a receiver whose clock is wrong by more than `toleranceSeconds` refuses every delivery.
The core does not retry a 4xx answer, so these events are lost. Keep the clock of the server
correct. Do not set `toleranceSeconds: Infinity`: it turns off the replay window.

A retry delivers an event again. Ignore an event `id` that you already handled.

`CalyxEvent` is keyed on `event`. The events `connection.update`, `message.received`,
`message.status` and `bulk.finished` have typed data. The events `whatsmeow.*` and `meta.webhook`
have `data: unknown`. An event name that this SDK version does not know still parses. It matches
the catch-all member. Check `event.event` before you use `data`.

The `instance` field is the core name of the instance: `i_` and the backend instance `id`
without hyphens. Use it to match a webhook to an instance of the backend.

A webhook with no secret has no signature. The SDK has no function for it. Use `JSON.parse`
and the exported `CalyxEvent` type.

## 6. Runtimes

- Tested: Node 22.23 and Node 26.13 (the SDK suite). The package says `engines.node >= 22.12`,
  because `require()` of an ES module needs no flag from Node 22.12.
- The `core` entry needs Node, Bun or Deno. Bun and Deno are expected to work for the `core`
  entry, but are not tested.
- Expected to work, but not tested: Bun, Deno and edge runtimes, for the `backend`, `webhooks`
  and `errors` entries. They use `fetch`, WebCrypto and no `node:` module.
- Edge platforms limit the time of a request. A `sendMessage` that waits for the queue of the
  core can last longer than that limit (up to 290 seconds). The platform then ends the call, and
  the SDK gives `timeout` or `aborted`. Both have `retry: "retry_now"`. Send again with the same
  idempotency key.

A time limit above 300 seconds does not help on Node: the HTTP client ends the call at 300
seconds. The SDK gives this case the code `timeout`.

## 7. Supported backend and core

The SDK types come from the contracts of these commits. Use a backend and a core built from
these commits or later:

| Contract | Commit |
|---|---|
| Event payload messages of `event.proto` (core) | `e99e5c3` |
| Enum of the error codes in `backend.json` (backend) | `413d833` |
| Enums of the status fields in `backend.json` (backend) | `4b91e13` |
| License label of `backend.json` (backend) | `195a2e4` |

The backend refuses request fields that it does not know (400 `invalid_request`). A newer SDK can
send a field that an older backend does not know. Update the backend first.

## 8. Notice

Calyx is an unofficial integration. Using WhatsApp Web through it is against the Terms of
Service of WhatsApp. WhatsApp can ban a number, and Calyx cannot promise that it will not. The
guardrails of the core make abuse hard, but they do not make a number safe.

The SDK is Apache-2.0. The backend and the core are AGPL-3.0-only.
