# Calyx Core (Go)

The core of Calyx: it keeps WhatsApp instances connected, sends their messages through one paced
outbound queue per instance, runs bulk jobs that survive restarts, and delivers events through
signed webhooks and a gRPC stream. It exposes gRPC only (`calyx.core.v1`, see
`packages/shared-types/proto/calyx/core/v1`).

> **Important.** The WhatsApp Web provider is an unofficial client (whatsmeow). Using it is
> against WhatsApp's Terms of Service and the account can be banned. The pacing rules lower that
> risk; nothing removes it. The Meta Cloud provider is the official API.

## Providers

| Provider | What it is | Pairing | Incoming events |
|---|---|---|---|
| `WHATSAPP_WEB` | A linked device of a personal or business WhatsApp account, through whatsmeow | QR or pairing code | From the session |
| `META_CLOUD` | The official WhatsApp Business Cloud API | Credentials of a Meta app | Meta's webhooks, on `META_WEBHOOK_PORT` |

Pacing cannot be lowered below the limits of each provider. WhatsApp Web waits at least 2 s
between messages, shows "typing...", and takes long breaks in bulk jobs.

A bulk job cannot send many messages to one person, because that looks like spam. In one job, a
recipient gets one message of `SendBulkSameMessage` and at most two of `SendBulkCustomMessages`.
The core counts each recipient after it normalizes the number. A job above the limit is refused
as a whole with `INVALID_ARGUMENT`. A gRPC message can have up to 17 MiB.

The core sends a keepalive ping every 2 minutes. It accepts a client keepalive ping at most once a
minute, and closes a connection that pings more often.

## Types of instance

An instance has one of three types. The provider and the field `is_used_only_for_send_messages`
of `CreateInstance` set the type. No type prevents a ban.

| Type | How to make it | Use it for |
|---|---|---|
| Long-lived | `WHATSAPP_WEB`, `is_used_only_for_send_messages` false (the default) | A number that receives messages and events: a chat, a support line, a bot |
| Send-only | `WHATSAPP_WEB`, `is_used_only_for_send_messages` true | A number that only sends: for example an ERP that sends invoices |
| Meta Cloud API | `META_CLOUD` | A company that can use the official API of Meta |

**Long-lived.** The core connects the instance when the core starts, and keeps it connected all the
time. When the connection drops, the core reconnects it by itself. The wait between two attempts
starts at 5 s and grows to 5 minutes. The instance receives messages and events, and it can have
webhooks.

- A send while the instance is not `CONNECTED` gets `UNAVAILABLE` with a RetryInfo detail of
  60 s. The message does not go into the queue. A bulk job waits until the instance is connected.
- When the load fails, for example at the start of the core, the core does not hold the instance.
  A send does not start a new connection: it gets `UNAVAILABLE`, and the operator connects
  the instance again.
- What the operator does after a failed load: disable and enable the instance with
  `UpdateInstanceConfig`, or restart the core. An instance in `NEEDS_AUTH` needs a new pairing
  (see "Pairing again").

**Send-only.** The core connects the instance only to send. It closes the connection after 60 s with
no work. A sleeping instance uses no connection to WhatsApp. It cannot have webhooks, and it receives
nothing while it sleeps. At the start of the core, it stays asleep, unless it has a bulk job to
continue.

- A send to a sleeping instance whose last status is `CONNECTED` wakes it: the core connects it,
  then queues the message. When the connection fails, the send fails and nothing goes into the
  queue.
- After a failed wake, the status is `DISCONNECTED` or `CONNECTING`. A send gets `UNAVAILABLE`
  and does not wake the instance. The core tries the wake again each minute (see "An instance
  that is not connected").
- What the operator does after a failed load: usually nothing, because the core tries again
  each minute. Read the `WARN` line of the log. An instance in `NEEDS_AUTH` needs a new pairing.

**Meta Cloud API.** There is no connection to keep: each send is one HTTPS call to Meta. Send-only
does not apply, and `is_used_only_for_send_messages` true gets `INVALID_ARGUMENT`. Incoming
messages and events come from the webhooks of Meta, on `META_WEBHOOK_PORT`. Meta applies its own
limits, so the pacing of the core is short (at least 100 ms between messages).

- When the core loads the instance, it checks the credentials with Meta. When Meta cannot be
  reached, the status is `DISCONNECTED`, and the core checks again after 30 s.
- A send while the instance is not `CONNECTED` gets `UNAVAILABLE`, as for the other types.
- When Meta refuses the credentials, the status is `NEEDS_AUTH`. The operator calls
  `CreateInstance` again with the same `instance_name` and new credentials (see "Pairing again").
- After any other failed load, the operator disables and enables the instance with
  `UpdateInstanceConfig`, or restarts the core.

## Run it

Requirements: Go 1.27, a C compiler (the SQLite driver uses cgo), and `protoc` with
`protoc-gen-go` and `protoc-gen-go-grpc` only to regenerate the gRPC code.

```bash
pnpm --filter @calyx/core dev          # GRPC_PORT and GRPC_AUTH_TOKEN are required (see "Environment")
pnpm --filter @calyx/core test         # go test -race ./...
pnpm --filter @calyx/core vet
pnpm --filter @calyx/core build        # binaries in apps/core/dist
pnpm --filter @calyx/shared-types proto:generate:go
```

To call a running core by hand, use `apps/core/scripts/try.sh` (run it without arguments to see
the commands). It needs `grpcurl` and `jq`. The pairing and send commands use a real WhatsApp
account and ask for confirmation first.

With Docker, from the repository root:

```bash
docker compose --profile core up core
```

## Environment

The core checks the whole environment at start. When a value is wrong or missing, it prints one
line for each problem and stops with status 1. It also refuses placeholder values from
`.env.example` (hosts and emails under `example.com`, `example.org` and `example.net`, and the
values `changeme`, `change-me` and `replace-me`).

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `GRPC_PORT` | yes | none | Port of the gRPC API (1 to 65535). |
| `GRPC_AUTH_TOKEN` | yes | none | At least 32 characters. Every call needs `authorization: Bearer <token>`. Make one: `openssl rand -hex 32`. |
| `GRPC_HOST` | no | `127.0.0.1` (`0.0.0.0` in the image) | Interface of the gRPC API. |
| `DATABASE_URL` | no | `file:./data/core.sqlite` | SQLite file with instances, bulk jobs and the WhatsApp sessions. Back it up; it holds session keys in clear. |
| `REDIS_URL` | no (the worker needs it) | none | `redis://` or `rediss://` URL. Webhooks go through Redis (asynq) and the worker (`cmd/worker`) delivers them with retries. Without it the core delivers them itself. |
| `LOG_FORMAT` | no | `text` (`json` in the image) | `text` or `json`. |
| `META_WEBHOOK_PORT` | no | none | Opens `/meta/webhook` for Meta Cloud instances, on every interface. Set it with `META_WEBHOOK_VERIFY_TOKEN`. |
| `META_WEBHOOK_VERIFY_TOKEN` | with `META_WEBHOOK_PORT` | none | Verify token of the Meta webhook. |
| `META_GRAPH_API_VERSION` | no | `v23.0` | Form `v23.0`. Check the current version in Meta's docs. |

For local runs, set `GRPC_PORT` and `GRPC_AUTH_TOKEN` first, for example
`GRPC_PORT=50051 GRPC_AUTH_TOKEN=$(openssl rand -hex 32) pnpm --filter @calyx/core dev`.

## Webhooks

Body: `{ "id", "event", "instance", "data", "timestamp" }`. With a secret, the request carries
`X-Calyx-Timestamp` (Unix seconds) and
`X-Calyx-Signature: sha256=<hex HMAC-SHA256(secret, timestamp + "." + body)>`.

Events: `connection.update`, `message.received`, `message.status`, `bulk.finished`, and the raw
`whatsmeow.<EventType>` or `meta.webhook`. `message.received` and `message.status` carry
`chatAltId` / `fromAltId`: the phone number when the chat is a LID, or the LID when it is a phone
number, if known.

**gRPC stream.** `EventService.Subscribe` sends its response headers when the subscription starts to
listen. An event that the core publishes after the headers reaches the client. When the core stops,
it ends each stream with `UNAVAILABLE` ("The core stops.") and then stops gracefully.
The response headers also have `calyx-version`, the version of the core. A client reads it from the headers.
The header is absent when the version is empty.
Start the core binary with `--version` to print the version and exit. This command needs no configuration.

**Calls during a stop.** After 5 s the core cuts the calls that still run, and closes every session.
A pairing that ends after this point opens no session, and a `RemoveInstance` call that reaches its
logout after this point changes nothing. Both get `UNAVAILABLE` with the message "The core is
stopping." A pairing that was complete keeps its credentials, so the core loads the instance at the
next start. Call `RemoveInstance` again after the start.

## Retries and duplicates

A client that loses the answer of a send does not know if the message left. The core gives two
tools so that a retry does not send the message twice: the idempotency key and the repeat guard.

**The idempotency key.** `SendPlainText`, `SendBulkSameMessage` and `SendBulkCustomMessages` have
an optional field, `idempotency_key`.

- Format: 1 to 255 printable ASCII characters (0x21 to 0x7E). Any other value gets
  `INVALID_ARGUMENT`.
- The key belongs to one instance. The core remembers it for 24 hours.
- A retry with the same key and the same request sends nothing. A single send returns the stored
  message id with `replayed: true`. A bulk call returns the same job with `replayed: true`.
- The same key with another request gets `ALREADY_EXISTS`.

**The repeat guard.** The guard applies to every single send, with a key or without one. The core
refuses the same message text to the same recipient from the same instance when the earlier send
is in progress, or finished less than 10 minutes ago. An earlier send with an unknown outcome
also triggers the guard. The refusal is `ALREADY_EXISTS`. The reason is that the same
message in a short time looks like spam. No option disables the guard.

**The reasons.** Each of these errors has a `google.rpc.ErrorInfo` detail. Its `Reason` is the
name of a value of the enum `ErrorReason`, and its `Domain` is `calyx.core.v1`.

| Reason | gRPC code | Meaning | Metadata |
|---|---|---|---|
| `ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT` | `ALREADY_EXISTS` | The key belongs to another message. | none |
| `ERROR_REASON_SEND_IN_PROGRESS` | `ABORTED` | A send with this key is not finished. A RetryInfo detail gives the minimum wait. | none |
| `ERROR_REASON_SEND_OUTCOME_UNKNOWN` | `ALREADY_EXISTS` | The core does not know if the message left. | `at` |
| `ERROR_REASON_DUPLICATE_MESSAGE` | `ALREADY_EXISTS` | The repeat guard refused the message. | `at`, and `message_id` if the earlier send is done |
| `ERROR_REASON_INSTANCE_QUEUE_FULL` | `RESOURCE_EXHAUSTED` | Too many direct messages wait on this instance. A RetryInfo detail gives the minimum wait. | none |

A single send with a key writes one Info line, "Send finished.", with `instance`, `outcome`
(`SENT`, `NOT_SENT` or `UNKNOWN`), `message_id` and `key_hash`. The `key_hash` is the first 16
hex characters of the SHA-256 of the key. The line never holds the key, the recipient or the
text. Do not put personal data, for example a phone number, in an idempotency key.

Error messages never contain the key, the message text or the recipient. Times are UTC, in the
format `time.RFC3339`.

Example in Go:

```go
import (
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
)

// calyxReason returns the reason and the metadata of an error of the core.
func calyxReason(err error) (string, map[string]string) {
	st, ok := status.FromError(err)
	if !ok {
		return "", nil
	}
	for _, detail := range st.Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok && info.Domain == "calyx.core.v1" {
			return info.Reason, info.Metadata
		}
	}
	return "", nil
}
```

**What to do:**

1. Make a new UUID for each message. Never reuse a key for another message.
2. After a lost answer or a timeout, retry with the same key.
3. After `ERROR_REASON_SEND_IN_PROGRESS`, wait for the time in RetryInfo and retry with the same key.
4. After `ERROR_REASON_SEND_OUTCOME_UNKNOWN`, check the chat on the phone first. To send the
   message again, use a new key after the time in the error message.

**The meaning of `UNAVAILABLE`.** On every send path, `UNAVAILABLE` means "not sent": it is safe to
retry. Any other error from the provider means "unknown outcome": the message may have left. The
refusals of the core (a full queue, an invalid request, an instance that is not ready) mean "not
sent".

- Meta Cloud returns `UNAVAILABLE` only when the connection to Meta could not open. A 5xx answer,
  a timeout and any other network error are an unknown outcome.
- A bulk job follows the same rule. A bulk message that gets an unknown outcome fails and the job
  continues. The core does not send it again, because a duplicate is worse than a gap.
- A single send with an unknown outcome keeps its key for 24 hours. A retry with that key gets
  `ERROR_REASON_SEND_OUTCOME_UNKNOWN`. To send again, use a new key, after the check in step 4.

When the core starts, it marks every send that was in progress as an unknown outcome.

**An instance that is not connected.** `SendPlainText` reads the status of the instance first, as
`GetInstanceStatus` does. When the status is not `CONNECTED`, the message does not go into the
queue:

- A send with a known idempotency key gets its stored answer: the replayed result,
  `ERROR_REASON_SEND_IN_PROGRESS`, `ERROR_REASON_SEND_OUTCOME_UNKNOWN` or
  `ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT`. The core gives this answer without a connection to
  WhatsApp, also in `NEEDS_AUTH` or when the instance is disabled.
- Any other send gets `UNAVAILABLE` at once, with a RetryInfo detail of 60 s. The core writes no
  record for it.
- A send does not start a connection of a long-lived instance that is not loaded in the core. This
  happens when its load failed, for example at the start of the core. To connect it again, disable
  and enable it with `UpdateInstanceConfig`, or restart the core. An instance in `NEEDS_AUTH` needs
  a new pairing (see "Pairing again").
- A send-only instance whose wake failed keeps a status other than `CONNECTED` (`DISCONNECTED`
  or `CONNECTING`). A send to it gets `UNAVAILABLE` and does not wake it.
- The core tries to wake such an instance again each minute, with no growing waits. The first
  try is one minute after the start of the core. Each next try is one minute after the end of the
  last one. The core tries the instances one after the other, and each wake has a limit of 20 s.
  The core does not try an instance that is disabled, already loaded, or in `NEEDS_AUTH` or
  `ERROR`.
- A wake sends no message. It starts the outbox, so a bulk job that did not finish continues
  with its normal pacing. The instance sleeps again after 60 s with no work.
- The log has one `WARN` line for the first failed wake of an instance, and one `INFO` line when
  a wake works after a failure.
- Until a wake works, the core refuses the sends to the instance.

## Pairing again

An instance goes to `NEEDS_AUTH` when it cannot authenticate. A WhatsApp Web instance has no valid
linked device: for example, the phone removed the device. A Meta Cloud instance has credentials
that Meta refused. To authenticate it again, call `CreateInstance` with the same `instance_name`
and the same provider. This call is a re-pair. A call for an instance in another status gets
`ALREADY_EXISTS`. The rules below apply to both providers.

A re-pair keeps each stored value that the request does not carry:

| The request | Result |
|---|---|
| has no webhooks (or an empty list) | The stored webhooks stay, with their secrets. |
| has webhooks | They replace the stored list. |
| has no `is_used_only_for_send_messages` | The stored flag stays. |
| has `is_used_only_for_send_messages` | It sets the flag. |

The core checks the merged configuration (the stored values plus the request) with the usual
rules. For example, a re-pair that sends webhooks for an instance that stays send-only gets
`INVALID_ARGUMENT`, and the core changes nothing.

The core writes the merged configuration before the pairing starts. When the pairing fails, or
the caller closes the stream, the instance stays in `NEEDS_AUTH` with that configuration. The
values that the request did not carry do not change.

A re-pair of a disabled instance gets `FAILED_PRECONDITION`, and the core changes nothing. Enable the
instance with `UpdateInstanceConfig`, then pair it again. While an instance pairs, the core refuses
every `UpdateInstanceConfig` call for it with `FAILED_PRECONDITION`, as it does for `RemoveInstance`.
Cancel the pairing first.

While an instance pairs and has no live session, the core also refuses the calls that need a session
(for example `SendText` to a send-only instance and `CreateBulkJob`) with `FAILED_PRECONDITION`. The
message is "Instance <name> is being paired. Try again when the pairing ends." The pairing opens the
session, and a second session of the same device must never open. The wake loop skips the instance.

Before a re-pair opens its session, it waits until the last session of the instance is closed. When a
`RemoveInstance` call deletes the instance during this wait, the re-pair gets `NOT_FOUND` with the
message "Instance with name <name> not found." The core opens no session. Call `CreateInstance`
again to create the instance.

A re-pair cannot remove all webhooks. To remove all webhooks of an instance:

1. Call `UpdateInstanceConfig` with the `instance_name` of the instance.
2. Set `webhooks` to a `WebhookList` with no entries: `"webhooks": {}` in JSON.
3. Do not set the other fields. A field that is not set keeps its value.
4. Call `ListInstances` and make sure that the instance has no webhooks.

## Device identity and locale

> **Important.** These values change what the phone shows and what the locale fields say. They do
> not change the risk of a ban. WhatsApp still sees the same IP address, app version and protocol
> for every instance of one core.

Each WhatsApp Web instance has a device identity and a locale. Meta Cloud instances have neither.

**Device identity.** The phone shows it in its list of linked devices, for a QR pairing and for a
pairing code. Set `whatsapp_web.device_identity` of `CreateInstance` to one entry of the list:

| `DeviceIdentity` | The phone shows |
|---|---|
| `DEVICE_IDENTITY_CHROME_WINDOWS` | Chrome (Windows) |
| `DEVICE_IDENTITY_EDGE_WINDOWS` | Edge (Windows) |
| `DEVICE_IDENTITY_FIREFOX_WINDOWS` | Firefox (Windows) |
| `DEVICE_IDENTITY_FIREFOX_LINUX` | Firefox (Linux) |
| `DEVICE_IDENTITY_SAFARI_MAC_OS` | Safari (Mac OS) |

Without a value, the core picks one entry at random. A re-pair links a new device, so it picks
again unless the request names one. The core logs "Device identity chosen." with the entry and
`requested` or `random`. An unknown value gets `INVALID_ARGUMENT`. When WhatsApp refuses a request
for a pairing code, the core answers `FAILED_PRECONDITION`. WhatsApp gives no reason: check the
phone number first, then try another entry.

**Locale.** A language and a country, in the form `es-ES` (two lower-case letters, a hyphen, two
upper-case letters). The instance sends it to WhatsApp at the pairing and at every later
connection. The core checks only the form, not that the codes exist.

- A new pairing needs `whatsapp_web.locale`. Without it, the core answers `INVALID_ARGUMENT`
  ("locale is required for a new pairing, for example es-ES") and stores nothing.
- A re-pair without a locale keeps the stored one. An instance paired before the core stored a
  locale needs one at its next pairing.
- The core never guesses a locale, for example from the phone number.

**Where to read them.** The last update of `CreateInstance` (`CONNECTED`) and `ListInstances`
return `device_identity` and `locale`. An instance paired before the core stored them shows
`DEVICE_IDENTITY_UNSPECIFIED` and no locale. Until its next pairing, the phone keeps the label of
the old pairing ("whatsmeow" or "Chrome (Linux)"), and the instance sends the locale `en-US`.

**What changed for callers:**

| Change | Who sees it |
|---|---|
| A new pairing without a locale is refused | Every caller that pairs a WhatsApp Web instance |
| New fields `device_identity` and `locale` | Callers that ignore them see no change |
| The phone shows a `Browser (OS)` pair, not "whatsmeow" | Phone owners |
| The backend API: `device_name` becomes `device_identity` (closed list), and a new `locale` | Clients of the backend API (see `apps/backend/README.md`) |

## Data kept in the database

The database file holds the session keys of every WhatsApp Web instance. Keep it private and back
it up.

whatsmeow also keeps a list of phone number <-> LID pairs of the contacts that each device sees
(the table `whatsmeow_lid_map`). This list is shared by all the devices, and it does not record
which device found each pair. Because of this:

- When you remove an instance, its device keys go with it.
- When you remove the last WhatsApp Web instance, the core deletes the whole list.
- When other WhatsApp Web instances remain, the pairs found by the removed instance stay in the
  list. The core cannot tell them apart from the pairs that the other instances use.

The pairs are personal data (phone numbers of contacts). They give no access to an account. To
delete them, remove all the WhatsApp Web instances, or stop the core and delete the database file.

## Verification

- **Tests:** `go test -race ./...`, with fakes instead of WhatsApp and the Graph API. They never
  touch a real account.
- **Stress tests:** `TestOutboxInvariants` and `TestOrchestratorInvariants` in `internal/engine`
  run random operations from a seed that is printed with any failure. Each subtest is named after
  the invariant it checks.

What this does not prove: anything about whatsmeow, WhatsApp or Meta themselves.
These checks never use a real WhatsApp session. The author ran the manual checks with a real
number on 2026-11-25.

## Layout

```
cmd/core        bootstrap and graceful shutdown
cmd/worker      asynq webhook worker
internal/
  database      SQLite, embedded migrations, whatsmeow's store in the same file
  engine        provider interface, pacing, bulk planner and store, outbox, orchestrator
  providers/whatsapp  whatsmeow provider
  providers/meta      Meta Cloud provider and its webhook endpoint
  events        event bus, webhook dispatcher, asynq producer and worker
  grpcserver    gRPC server, token check, handlers
  e2e           end-to-end test of the whole core
```
