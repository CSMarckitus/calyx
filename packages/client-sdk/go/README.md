# Calyx client SDK for Go

A Go client for Calyx. It has the shared `calyx` package, the `webhook` package, the backend
client and the core client.

Status: version 0.1, early alpha. The module is not published. The versions stay at `0.x` while
the contracts change.

## 1. Install

The module is `github.com/csmarckitus/calyx/packages/client-sdk/go`. Use it inside the
repository only, until publication. The repository `go.work` file already lists it.

Caution: set `GOPRIVATE` for direct `go` commands. Without it, Go asks the public module proxy
for a module that is not there, and the command fails.

1. Run `export GOPRIVATE='github.com/csmarckitus/*'`.
2. Run `go test ./...` in `packages/client-sdk/go`.

The `pnpm --filter @calyx/client-sdk-go` scripts set `GOPRIVATE` for you. The module needs
Go 1.26 or later.

## 2. Choose a client

Import only the package that you need. Each package pays only for what it imports.

| Package | Contents | Links gRPC code |
|---|---|---|
| `calyx` | `Error`, the codes, the retry advice, `Event` and its payloads, `Sent` | no |
| `webhook` | `Verify` | no |
| `backend` | The client for the HTTP API of the backend, with an API key | no |
| `core` | The client for the gRPC API of the core, on the private network of the core | yes |

Use this rule to choose:

1. Use the backend client when your program calls Calyx over HTTP with an API key.
2. Use the core client only when your program runs on the private network of the core.
3. Use `webhook` in the server that receives the webhooks of Calyx.

Only `core` links gRPC code. `go list -deps ./calyx ./webhook ./backend` shows no module
outside the repository. The check `pnpm --filter @calyx/client-sdk-go deps:check` fails when
that changes.

## 3. Webhooks

The core signs a webhook when the webhook has a secret. It sends the headers `X-Calyx-Signature`
and `X-Calyx-Timestamp`, and a missing one gives `invalid_signature`. The handler checks the
signature, then reads the event.

```go
package main

import (
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
	"github.com/csmarckitus/calyx/packages/client-sdk/go/webhook"
)

func handler(secret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}

		event, err := webhook.Verify(body, r.Header, secret, nil)
		if err != nil {
			var calyxErr *calyx.Error
			if errors.As(err, &calyxErr) {
				log.Printf("webhook refused: %s", calyxErr.Code)
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if event.Event == "message.received" {
			msg, err := event.MessageReceived()
			if err != nil {
				http.Error(w, "bad event", http.StatusBadRequest)
				return
			}
			log.Printf("message %s from %s", msg.MessageID, msg.From)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
```

`Verify` returns a `calyx.Event`. The methods `ConnectionUpdate`, `MessageReceived`,
`MessageStatus` and `BulkFinished` return the typed payload. Each method returns a
`*calyx.Error` with the code `invalid_event` when the event has another name.

Caution: pass the raw body to `Verify`, as the handler received it. The signature covers the
exact bytes. When you parse the JSON and encode it again, the bytes can change, and the check
fails. Read the body once with `io.ReadAll`, call `Verify`, and parse only after it passes.

Caution: limit the size of the body with `http.MaxBytesReader` before you read it. Without a
limit, a sender can make the handler read an unbounded body into memory. The check runs after
the read, so the limit is the only guard against this.

Caution: a wrong clock refuses every delivery. `Verify` refuses a timestamp more than 5 minutes
before or after the current time. When the clock of your server is wrong by more than that, every
check fails with `stale_timestamp`. The core does not retry a 4xx answer, so you lose these
events. Keep the clock synchronized (for example with NTP). To change the window, pass
`&webhook.VerifyOptions{Tolerance: ...}`. The field `Now` sets the current time for tests.

A webhook without a secret has no signature, and the SDK offers no check for it. Decode the
body yourself. Accept only a sender that you trust on the network level.

```go
var event calyx.Event
if err := json.Unmarshal(body, &event); err != nil {
	http.Error(w, "bad event", http.StatusBadRequest)
	return
}
```

Import `encoding/json`, and use it inside the same handler.

A retry delivers the same `event.ID` again. The core can also send one event twice. Store the
`ID` values that you handled, and ignore an `ID` that you already have. A reply with a 2xx status
tells the core that the delivery is done.

## 4. Errors

Every SDK failure is a `*calyx.Error`. Use `errors.As` to read it, because other code can wrap
the error.

```go
var calyxErr *calyx.Error
if errors.As(err, &calyxErr) {
	log.Printf("%s %s retry=%s", calyxErr.Source, calyxErr.Code, calyxErr.Retry)
}
if calyx.HasCode(err, calyx.CodeStaleTimestamp) {
	// The clock of the server is wrong.
}
```

The error has these fields: `Source` (`backend`, `core` or `client`), `Code`, `Status` (the HTTP
status for the backend, the gRPC code number for the core, 0 for the client), `Detail`,
`Retry`, `Errors` (field errors), `RequestID`, `RetryAfter`, `IdempotencyKey` and `Metadata`.
`Unwrap` gives the cause, for example a network error.

The field `Retry` tells the program what to do next. It has five values:

| Value | Meaning |
|---|---|
| `retry_now` | The call can work on a retry. Send it again with the same idempotency key. |
| `retry_later` | The call needs a pause first. Wait (`RetryAfter` when it is not 0), then send it again with the same key. |
| `check_if_sent` | The outcome of the first call is unknown. Check the result, then use a new key. |
| `do_not_retry` | A retry fails the same way. Fix the request or the setup. |
| `unknown` | The error has no clear advice. Decide in your program. |

Caution: a message send can fail after the server accepted it. The codes `send_outcome_unknown`
and `bulk_outcome_unknown` have the advice `retry_now`. A retry with the same key is safe,
because the server uses the key to refuse a duplicate. A retry with a new key can send the
message twice. Use `calyx.AdviceFor(source, code)` to read the advice for a code. It knows only the source and the code. The advice of a call is in `Error.Retry`, and the create calls differ (see below).

Caution: a create call can fail after the backend made the client or the instance. The calls
`CreateClient` and `CreateInstance` have no idempotency key. A client error of these two calls
(`timeout`, `network_error`, `invalid_response` or `aborted`) has the advice `unknown`, not
`retry_now`.
After this error, list the clients or the instances before you call again. A second call can make
a duplicate.

The webhook check gives only `client` errors: `invalid_signature`, `stale_timestamp` and
`invalid_event`. All three have the advice `do_not_retry`.

## 5. The backend client

The package `backend` calls the HTTP API of the backend. It has 24 methods and `FollowPairing`.
The types of the requests and the answers come from the OpenAPI document of the backend.

```go
client, err := backend.New(backend.Config{
	BaseURL: "https://calyx.example.com",
	APIKey:  os.Getenv("CALYX_API_KEY"),
})
if err != nil {
	log.Fatal(err)
}
```

`Config` has three fields. `BaseURL` is required, and it must start with `http://` or
`https://`. Any other value gives the code `invalid_config`. `APIKey` is optional. `HTTPClient`
is optional, and `New` uses a copy of it with `Timeout` set to 0. Use `ctx` to limit a call.
The `Client` is safe for concurrent use.

Caution: do not put the API key in a log line or in the source code. The SDK never writes the API
key in an error text. The idempotency key is not secret, and the error text shows it. Read the
API key from the environment, as in the example.

### 5.1 Send one message

A send call returns `calyx.Sent`. It holds the answer in `Data` and the idempotency key in
`IdempotencyKey`. The SDK makes a key when you give none.

```go
sent, err := client.SendMessage(ctx, instanceID, backend.SendMessageRequest{
	To:   "34600000000",
	Text: "Your order is ready.",
}, backend.SendOptions{})
if err != nil {
	var calyxErr *calyx.Error
	if errors.As(err, &calyxErr) {
		log.Printf("%s retry=%s key=%s", calyxErr.Code, calyxErr.Retry, calyxErr.IdempotencyKey)
	}
	return err
}
log.Printf("message %s, key %s", sent.Data.MessageId, sent.IdempotencyKey)
```

The error carries the same idempotency key, so a failed call gives the key for a retry.
`CreateBulkJob` works in the same way.

### 5.2 Send again after a timeout

A call can end with the code `timeout`. The backend can still send the message after that.
Send again with the same key. The backend then refuses a duplicate.

Caution: a new key can send the message twice. Use the key of the error.

```go
sent, err := client.SendMessage(ctx, instanceID, req, backend.SendOptions{})
if calyx.HasCode(err, calyx.CodeTimeout) {
	var calyxErr *calyx.Error
	errors.As(err, &calyxErr)
	sent, err = client.SendMessage(ctx, instanceID, req, backend.SendOptions{
		IdempotencyKey: calyxErr.IdempotencyKey,
	})
}
if err == nil {
	log.Printf("sent with key %s, replayed %t", sent.IdempotencyKey, sent.Data.Replayed)
}
```

The second call has two outcomes:

1. The first send still runs. The backend answers with the code `send_in_progress`. Wait for `RetryAfter`
   seconds; it is the minimum wait. Then send again with the same key.
2. The first send ended. The backend answers with the result and `Replayed` is true. The
   message went out once.

### 5.3 Pair an instance and follow the codes

`PairInstance` starts the pairing. `FollowPairing` reads the stream of the pairing.

```go
if _, err := client.PairInstance(ctx, instanceID, backend.PairInstanceRequest{}); err != nil {
	return err
}
for item, err := range client.FollowPairing(ctx, instanceID) {
	if err != nil {
		return err
	}
	switch item.Event {
	case "update":
		if item.Update.QrText != nil {
			fmt.Println("Scan this QR text:", *item.Update.QrText)
		}
		if item.Update.PairingCode != nil {
			fmt.Println("Enter this code on the phone:", *item.Update.PairingCode)
		}
	case "done":
		fmt.Println("Paired:", item.Done.Status)
	case "failed":
		fmt.Println("Pairing failed:", item.Failed.Code, item.Failed.Detail)
	}
}
```

A 404 on a new follow means that no pairing runs and that the backend keeps no failure. Read the
instance. The loop gives `update` items, then one
`done` or `failed` item, and then it ends. An error is always the last item. A `break` closes
the connection. The pairing continues in the backend until `CancelPairing`. The SDK never opens the stream again. When the stream fails, read the
instance with `GetInstance`, then call `FollowPairing` again.

### 5.4 Remove an instance

`RemoveInstance` returns the HTTP status. Read it, because the two successes differ.

| Status | Meaning |
|---|---|
| 204 | The instance is removed. |
| 202 | The core did not confirm the removal. The instance stays in the state `removing`. |

```go
status, err := client.RemoveInstance(ctx, instanceID)
if err == nil && status == http.StatusAccepted {
	log.Print("the removal is not confirmed yet; read the instance later")
}
```

### 5.5 Time limits

The SDK adds a default limit only when `ctx` has no deadline. A default never shortens a
deadline of your program. To set a shorter limit, use `context.WithTimeout`.

| Call | Default limit | Reason |
|---|---|---|
| `SendMessage` | 6 minutes | The backend waits up to 5 minutes for a send. The limit must be longer, or the SDK stops a call that the backend still serves. |
| `CreateBulkJob` | 150 seconds | The backend waits up to 60 seconds to read the body and 60 seconds for the core. |
| The other calls | 2 minutes | The longest wait on the server is 30 seconds (`ListBulkJobs`). |
| `FollowPairing` | none | The stream stays open until the pairing ends. The SDK stops it after 45 seconds without data, and the backend sends a ping every 15 seconds. |

| What happened | Code |
|---|---|
| The program canceled `ctx` | `aborted` |
| The deadline of `ctx`, or the default limit, passed | `timeout` |
| No connection, or the connection stopped | `network_error` |
| `FollowPairing` got no data for 45 seconds, or the stream broke | `stream_failed` |

The SDK reads at most 32 MiB of one answer and 1 MiB of one pairing event. A larger one gives
`invalid_response`. Each request has the header `User-Agent: calyx-go-sdk/<version>`.

### 5.6 The two identifiers of an instance

An instance has two identifiers. The two clients use different ones.

| Identifier | Form | Used by |
|---|---|---|
| `id` | A UUID | The backend client, and the field `Id` of `backend.Instance` |
| Instance name | `i_` and the UUID without hyphens | The core, and the field `instance` of a webhook event |

To match a webhook event to a backend instance, compare `event.Instance` with
`"i_" + strings.ReplaceAll(id, "-", "")`.

### 5.7 `GetMe`

`GetMe` returns the user of the API key and the organization, a `MeOrganization`. `Id` and
`Name` always have a value. The other six fields are pointers, and they are for an owner.
They are `nil` when `User.Role` is `client`. For an owner, `MetaWebhook` and
`WhatsappWebAcknowledgedAt` can also be `nil`.

```go
me, err := client.GetMe(ctx)
if err == nil && me.Organization.InstanceCount != nil {
	log.Printf("%s has %d instances", me.Organization.Name, *me.Organization.InstanceCount)
}
```

### 5.8 Errors of the backend client

Programs read `Code`, not the generated type `ProblemCode`. `Code` is the same string, it is
the same for the backend and the core, and it has constants such as `calyx.CodeSendInProgress`.
`ProblemCode` is generated from the OpenAPI document, and it is not the type of `Error.Code`.

Caution: the backend client never sends a request again by itself. A retry belongs to your
program. Use the field `Retry` of the error to decide, as section 4 shows.

Caution: the SDK does not make the number safe from a ban. WhatsApp Web is an unofficial
integration, and its use is against the Terms of Service of WhatsApp. The core enforces
delays between sends, and no client can turn them off.

## 6. The core client

The core has no users and no tenants. Use the `core` package only on the private network of the
core. Programs that call Calyx over the internet use the backend client.

Caution: the SDK does not make the number safe from a ban. Calling the core directly does not
remove the delays that the core enforces. The core always applies them.

### 6.1 Connect and listen to events

`core.New` needs an address, the core token and a `Security` value. The first call connects.

```go
client, err := core.New(core.Config{
	Address:  "core.internal:50051",
	Token:    token,
	Security: core.Insecure(),
})
if err != nil {
	return err
}
defer client.Close()

for ev, err := range client.Subscribe(ctx, core.Filter{EventTypes: []string{"message.received"}}) {
	if err != nil {
		return err
	}
	log.Printf("event %s of %s", ev.Event, ev.Instance)
}
```

`Subscribe` gives the same `calyx.Event` type as a webhook. When the stream stops with an error,
the loop gives one `*calyx.Error` (for example `UNAVAILABLE`) and ends.

Caution: `Subscribe` loses events. The program gets no event that the core sends between two
streams. The core also drops new events when the program reads slower than they arrive, because
it keeps 256 events for each subscriber. A program that must get every event uses webhooks.

### 6.2 Security and your own connection

`Security` is required. The zero value gives the code `invalid_config`.

| Value | Use |
|---|---|
| `core.Insecure()` | A private network that you trust. The core token travels as plain text. |
| `core.TLS(cfg)` | Any other network. A `nil` `cfg` uses the root certificates of the system. |

`core.NewFromConn(conn, token)` makes a client on a connection that your program made, for
example with its own interceptors. `Close` does not close that connection. The SDK adds no
keepalive ping to it (section 6.4). Use `grpc.WithKeepaliveParams`, with a `Time` of more than
1 minute and without `PermitWithoutStream`. The core closes a connection that pings more often.

Notes about the token and `Close`:

1. An empty token is sent as `Bearer `. The core answers `UNAUTHENTICATED`.
2. `Close` returns the raw gRPC error, not a `*calyx.Error`.
3. A call after `Close` gives the code `aborted`.

### 6.3 Unary calls

The ten unary calls take the generated request of `corev1` and return the generated answer.
Every error is a `*calyx.Error`. A core error has `Source` set to `SourceCore`. An error that
the SDK found (`timeout`, `aborted`, `invalid_config`, `invalid_event`) has `SourceClient`.

`GetBulkJobs` gives the state of 1 to 50 jobs in one call. The core leaves out an unknown job, and the
order of the answer is not defined.

The three send calls (`SendPlainText`, `SendBulkSameMessage` and `SendBulkCustomMessages`)
return `calyx.Sent`. The SDK makes an idempotency key when the request has none, and it does not
change your request. An empty key counts as no key. Use the key of the error to send again after
a `timeout`, as section 5.2 shows.

### 6.4 Streams

`CreateInstance` and `Subscribe` return `iter.Seq2` values. Follow these rules:

1. The call starts when the loop starts. Each loop is a new call.
2. The stream has no deadline.
3. The end of the loop cancels the call. This holds for `break`, `return` and a panic.
4. An error is always the last item.
5. The SDK never opens a stream again by itself.
6. A client of `core.New` sends a keepalive ping every 2 minutes while a call or a stream runs. When
   the core host disappears, each of them gets `UNAVAILABLE` within about 2 minutes and 20 seconds,
   or its own time limit if that passes first. After `UNAVAILABLE`, the next call opens a new
   connection. After a time limit, the next calls can use the dead connection until the connection
   fails.

Caution: a `break` in a `CreateInstance` loop stops the pairing. The instance stays in the state
`NEEDS_AUTH`. Keep the loop open until the pairing ends.

### 6.5 Time limits

The SDK adds a default limit only when `ctx` has no deadline. A default never shortens a
deadline of your program.

| Call | Default limit | Reason |
|---|---|---|
| `SendPlainText` | 6 minutes | The slowest normal send waits about 4 minutes in the core queue. CONTRACT.md (time rule 1) asks for a core limit longer than 5 minutes, the limit of the backend. If the SDK cancels earlier, it cancels a send that the core still serves. |
| The other unary calls, the two bulk sends included | 2 minutes | These calls end fast. The limit stops a call that hangs. |
| `CreateInstance` and `Subscribe` | none | The stream stays open until the pairing ends or the loop ends. |

A canceled `ctx` gives `aborted`. A passed deadline gives `timeout`.

### 6.6 Update the backend first

A new SDK can send a field that an old backend refuses with `invalid_request`. Update the
backend and the core before you update the SDK.

### 6.7 Release steps

The module is not published. When the author publishes the repository, follow these steps:

1. Remove the `replace` line from `packages/client-sdk/go/go.mod`.
2. Tag `packages/shared-types/go/v0.1.0`.
3. Set `calyx.Version` to the same version.
4. Set the `require` of the SDK to that version, and tag `packages/client-sdk/go/v0.1.0`.
5. Remove `GOPRIVATE` from the scripts.
6. Decide how the tests that read `packages/client-sdk/testdata` behave outside the repository.
   The module zip does not contain that folder.
7. State the oldest backend and core that the SDK supports.

## 7. Go idioms used here

- **`errors.As`** finds an error of one type in a chain of wrapped errors. The type here is the
  pointer `*calyx.Error`, so the variable is `var e *calyx.Error` and the call takes `&e`.
- **A typed string** (`type Code string`) makes a named value. The constants such as
  `calyx.CodeStaleTimestamp` stop a typing mistake at compile time.
- **Generics.** `Sent[T]` holds the answer (`Data`) and the idempotency key of a send. The
  type parameter `T` is the type of the answer.
- **Zero value options.** `Verify` takes a pointer to `VerifyOptions`. A `nil` pointer, or a zero
  field, gives the default.
- **`json.RawMessage`.** `Event.Data` keeps the payload as bytes. A typed method decodes it
  only when you ask.
- **`iter.Seq2`.** `FollowPairing`, `CreateInstance` and `Subscribe` return a function that
  works with `for ... range`. Each turn of the loop gives one item and one error. A `break`
  ends the stream.
- **`context.Context`.** The first argument of each call. It sets the deadline and the cancel.
- **`http.MaxBytesReader`** wraps a request body and stops the read at a limit.
