# Calyx client SDK for PHP

A PHP client for the HTTP API of the Calyx backend. It checks webhooks and reads events. It
needs only `ext-curl`, `ext-json` and `ext-hash`, so it runs on shared hosting.

Status: version 0.1.0, early alpha. The package is not on Packagist. The versions stay at `0.x`
while the contracts change.

| You need | Notes |
|---|---|
| PHP 8.3 or later | The tests run on 8.3 and 8.5. PHP 8.2 loses security support on 2026-12-31. |
| `ext-curl`, `ext-json`, `ext-hash` | Nothing else. No Composer package in `require`, and no gRPC. |
| A Calyx backend and an API key | Create the key in the panel of the backend. |

This package is the backend client. It never loads gRPC code. The core client (gRPC, needs
`ext-grpc`) is the separate package `calyx/client-sdk-core`. A program that uses only the backend
installs nothing of gRPC.

## Install

The package is not published, so use a Composer `path` repository (or a zip) until it is.

1. Add the repository to the `composer.json` of your program:

   ```json
   {
       "repositories": [
           {"type": "path", "url": "../calyx/packages/client-sdk/php/client"}
       ]
   }
   ```

2. Run `composer require calyx/client-sdk:@dev`.
3. Make the client once, and keep the API key out of the source code.

```php
// getenv() gives false for a missing variable, so use "?: null" with strict_types.
return new BackendClient('https://calyx.example.com', getenv('CALYX_API_KEY') ?: null);
```

The classes are in these namespaces:

| Namespace | Contents |
|---|---|
| `Calyx\Client` | `CalyxException`, `SendPendingException`, `PendingSend`, `Sent`, `IdempotencyKey`, `RetryAdvice`, `Source`, `FieldError` |
| `Calyx\Client\Backend` | `BackendClient`, `PairingSnapshot`, `PairingState`, `PairingItem` |
| `Calyx\Client\Generated\Model` | The request and answer classes, for example `SendMessageRequest` |
| `Calyx\Client\Generated` | `BackendCode`, `ClientCode`, `CoreCode` (the code constants) |
| `Calyx\Client\Webhook` | `Webhook::verify()` |
| `Calyx\Client\Event` | `Event`, `EventNames`, `Statuses` and the payload classes |

The SDK is Apache-2.0. The backend and the core are AGPL-3.0-only.

Rules for the client:

1. The base URL must start with `http://` or `https://`. Another value gives `invalid_config`.
2. A null or empty API key sends no `Authorization` header.
3. Every call has a time limit in seconds (`timeLimit:`). The client never sends a request again
   by itself.
4. A request class holds the wire form. `$request->toArray()` gives the request form that the
   backend reads. It leaves out the fields that are `null`. An answer class does not always read
   back from `toArray()`, so do not store an answer with it.
5. A string that is not valid UTF-8 gives `invalid_config`. No request goes out.

## 1. What you want to do

| I want to... | Read | Example file |
|---|---|---|
| Move from Evolution API | [2](#2-coming-from-evolution-api) | |
| Choose where to call `sendMessage` | [3](#3-where-to-call-sendmessage) | |
| Send a message one time only | [4](#4-the-first-send) | `first-send.php` |
| Find out if a slow send went out | [5](#5-the-check-of-a-pending-send) | `check-send.php` |
| Decide what to do after an error | [6](#6-errors) | `errors.php`, `laravel-send-job.php` |
| Add a WhatsApp number from a web panel | [7](#7-pairing) | `pairing-start.php`, `pairing-poll.php` |
| Receive messages and events | [8](#8-webhooks) | `laravel-webhook.php`, `webhook-plain.php` |
| Run on shared hosting | [9](#9-the-limits-of-shared-hosting) | |
| Look up a client error code | [10](#10-the-php-client-codes) | |
| Send a bulk job | [11](#11-the-risk-notice-and-bulk-jobs) | `bulk-job.php` |
| Find out whose API key you use | [`getMe`](#getme) | |

The folder `examples/` has every code block of this file. The test `ExamplesTest` runs the
examples that need no framework or web server on a fake transport. PHPStan checks all of them.
No example sends a real message.

Each block needs the `use` lines at the top of its example file.

## 2. Coming from Evolution API

Calyx is a different system, not a copy. This table maps the common calls of Evolution API v2 to
this client. Check the names in the documentation of your Evolution version.

| Evolution API | Calyx |
|---|---|
| An `apikey` header (the global key or an instance token) | An API key of your organization, in `Authorization: Bearer`. The client sets it. |
| `instanceName` in the URL | The instance `id` that `createInstance` returns. |
| `POST /instance/create` and a QR code | `createInstance()`, then `pairingSnapshot()` ([7](#7-pairing)). |
| `POST /message/sendText/{instance}` with `number` and `text` | `sendMessage($id, new SendMessageRequest(to: ..., text: ...))` |
| A retry after a timeout, which can send the message again | A retry with the same idempotency key. When you store the key before the call, the instance sends the message one time at most within 24 hours ([4](#4-the-first-send)). |
| Webhook events such as `MESSAGES_UPSERT` | Events such as `message.received`, signed with HMAC-SHA256 ([8](#8-webhooks)). |
| No pacing that the server enforces. A `delay` per message is optional. | Pacing and minimum delays in the core. No option turns them off. |

Differences that matter:

1. A send waits in a queue of the core. A queue with many messages can take minutes. Use a time
   limit and a `PendingSend`.
2. The same text to the same recipient within 10 minutes gives `duplicate_message`. This rule
   also refuses repeated one-time codes and template texts. Add a changing part to the text, for
   example the order number.
3. The WhatsApp Web provider is off until the operator of the backend allows it for your
   organization and the owner accepts its terms ([7](#7-pairing)).

## 3. Where to call `sendMessage`

A send can last longer than a web request. The core sends at the pace of its guardrails, so 8
queued messages can take about 96 s. A PHP-FPM worker that waits that long is lost for other
requests. nginx stops the request after 60 s by default.

| Place | Time limit | Why |
|---|---|---|
| Web request | `timeLimit: 20` or less | Keep it below the limit of your web server. Catch `SendPendingException`. |
| Queue job | The default (360 s) | The job can wait. Set the job timeout above 360 s. |
| CLI or cron | The default (360 s) | Nobody waits for an answer. |

Use this rule: send from a queue or the CLI when you can. In a web request, store the key first,
use a short time limit, and answer the user when `SendPendingException` comes.

The backend continues a send after the client stops waiting. So the message stays in the core
queue, and a later `checkSend()` finds it.

## 4. The first send

1. Make the key with `IdempotencyKey::new()`.
2. Store the key with your record (for example the order) before you call `sendMessage`.
3. Call `sendMessage` with `idempotencyKey: $key`.
4. Catch `SendPendingException`, and store `$e->pendingSend`.

Caution: store the key before the call. When the process stops after the call and before the
store, you lose the key, and a new key can send the message two times.

```php
// 1. Make the key and store it with the order BEFORE you call Calyx.
//    When the process stops after the call and before the store, the key is lost.
$key = IdempotencyKey::new();
$order = ['id' => 42, 'idempotency_key' => $key, 'pending_send' => null, 'message_id' => null];
// Save $order in your database here.

try {
    $sent = $calyx->sendMessage(
        'instance-id',
        new SendMessageRequest(to: '34600000000', text: 'Your order #42 is ready.'),
        idempotencyKey: $key,
        timeLimit: 20,
    );
    $order['message_id'] = $sent->data->messageId;
} catch (SendPendingException $e) {
    // 2. The message can still go out. Store the pending send and answer the buyer.
    $order['pending_send'] = json_encode($e->pendingSend->toArray(), JSON_THROW_ON_ERROR);
}
// Save $order again.
```

The send call returns a `Sent`. Its `data` holds the answer, and its `idempotencyKey` holds the
key of the call. Every error of the call has the same key.

The rules for a key:

1. A key has 1 to 255 characters, each printable ASCII and not a space. Another key gives
   `invalid_config`, and no request goes out.
2. With no key (or `''`), `sendMessage` makes a new key. With a key, the same key and the same
   body send one message at most within 24 hours.
3. The same key with a different body gives `idempotency_key_conflict`.

`PendingSend` holds the text of the message, because a resend must have the same body. It is data
of your customer. Store it with the same care as the message.

## 5. The check of a pending send

`checkSend($pending)` calls `sendMessage` again with the same key and the same body.

| Result | Meaning |
|---|---|
| `Sent` with `replayed: true` | The core sent the message before. `data->messageId` is its id. |
| `Sent` with `replayed: false` | The first attempt did not send it. This call sent it now. |
| `SendPendingException` | The message still waits. `retryAfter` says how long to wait. |
| `CalyxException`, `pending_send_expired` | The pending send is older than 23 hours. No request went out. |

Caution: `checkSend` is a resend, not a lookup. When the first attempt did not send the message,
the message goes out now, maybe hours late. Apply your own age rule before you call it, as the
example does.

```php
// A lock stops two runs of the cron job from overlapping.
$lock = fopen(sys_get_temp_dir() . '/calyx-check-send.lock', 'c');
if ($lock === false) {
    return;
}
if (!flock($lock, LOCK_EX | LOCK_NB)) {
    fclose($lock);

    return;
}

try {
    // Read this array from your database: it is the toArray() of the pending send.
    $stored = [
        'v' => 1,
        'instanceId' => 'instance-id',
        'to' => '34600000000',
        'text' => 'Your order #42 is ready.',
        'idempotencyKey' => 'order-42',
        'firstSentAt' => time() - 120,
    ];
    $pending = PendingSend::fromArray($stored);

    // checkSend() sends the message again. When the first attempt did not send it,
    // the message goes out now, maybe hours late. Apply your own age rule first.
    if (time() - $pending->firstSentAt > 1800) {
        // Too old for your shop: mark the order as not notified, and do not call checkSend().
        return;
    }

    try {
        $sent = $calyx->checkSend($pending, timeLimit: 20);
        // Mark the order as notified. $sent->data->replayed is true when the message went out before.
        $messageId = $sent->data->messageId;
    } catch (SendPendingException $e) {
        // The message still waits in the queue. Run again after $e->retryAfter seconds.
        $wait = $e->retryAfter;
    } catch (CalyxException $e) {
        if ($e->errorCode === ClientCode::PENDING_SEND_EXPIRED) {
            // The pending send is older than 23 hours. Look at the chat before you send it again.
        }
        // Any other error: write it in your log and run again at the next minute.
    }
} finally {
    flock($lock, LOCK_UN);
    fclose($lock);
}
```

Rules:

1. Run the cron job with a lock, as the example does. Two overlapping runs make two calls with the
   same key, and one gets `send_in_progress`.
2. The core remembers a key for 24 hours. After that, the same key sends the message again.
   `checkSend` refuses a pending send older than 23 hours.
3. A queue job has the same limit. Stop its retries before 24 hours (`retryUntil()` in the
   Laravel example of [6](#6-errors)).
4. A check works while the core has no connection to WhatsApp. The core answers from the record of
   the key. A send with a new key gets `instance_not_connected`.
5. `firstSentAt` is in Unix seconds. A value more than 1 hour in the future gives `invalid_config`.
   This catches a time in milliseconds.
6. `PendingSend::toArray()` has a version field (`v: 1`). `PendingSend::fromArray()` gives
   `invalid_config` for a field that is missing or wrong.

## 6. Errors

The SDK throws one class, `CalyxException`. `SendPendingException` is a subclass of it. Catch
`SendPendingException` first.

| Property | Meaning |
|---|---|
| `source` | `Source::Backend`, `Source::Core` or `Source::Client` (an error of the SDK). |
| `errorCode` | The error code. It is `errorCode`, not `code`, because `Exception::$code` is an integer in PHP. |
| `status` | The HTTP status (backend), the gRPC code number (core) or 0 (client). |
| `detail` | A text for people. Write it in your log. |
| `retry` | The advice, a `RetryAdvice` case. |
| `errors` | The field errors of the backend, a list of `FieldError`. |
| `requestId` | The `X-Request-Id` of the backend. Write it in your log. |
| `retryAfter` | The minimum wait in seconds. 0 means no wait. |
| `idempotencyKey` | On a send, the key of the call. |
| `metadata` | The `ErrorInfo` metadata of the core. |

`getMessage()` is `"{errorCode}: {detail}"`. No message holds the API key, the webhook secret or
the body of a webhook.

The retry advice has five values. The SDK never acts on it.

| `RetryAdvice` | What the program does |
|---|---|
| `RetryNow` | The outcome is unknown, or nothing happened. Send again now, with the same key. |
| `RetryLater` | Wait `retryAfter` seconds when it is set, then send again with the same key. |
| `CheckIfSent` | The key cannot send again. Look at the chat first. Then use a new key. |
| `DoNotRetry` | A retry fails the same way. Fix the request or the setup. |
| `Unknown` | The SDK has no advice. Decide in your program. |

```php
$key = IdempotencyKey::new();
$firstSentAt = time();
$request = new SendMessageRequest(to: '34600000000', text: 'Your code is 481516.');

try {
    $calyx->sendMessage('instance-id', $request, idempotencyKey: $key);
} catch (SendPendingException $e) {
    // Store $e->pendingSend. See "The first send".
    $next = 'check the pending send later';
} catch (CalyxException $e) {
    // $e->source is backend, core or client. $e->errorCode is the code, for example "rate_limited".
    $next = match ($e->retry) {
        // The outcome is unknown, and the message can still go out.
        // Store a pending send with the key of the call, and check it later.
        RetryAdvice::RetryNow => new PendingSend('instance-id', $request->to, $request->text, $e->idempotencyKey ?? $key, $firstSentAt),
        // Wait $e->retryAfter seconds, then call sendMessage() again with the same key.
        RetryAdvice::RetryLater => $e->retryAfter,
        // The key cannot send again. Look at the chat first.
        RetryAdvice::CheckIfSent => 'check the chat',
        // The same request fails again. Fix the request or the setup.
        RetryAdvice::DoNotRetry, RetryAdvice::Unknown => $e->detail,
    };
}
```

Caution: a send can fail after the backend accepted it. `send_outcome_unknown` and
`network_error` have the advice `RetryNow`, and the message can still go out. For every
`RetryNow` error of `sendMessage`, store a `PendingSend` with `$e->idempotencyKey`, and call
`checkSend` later. The example does this.

Caution: a create call can fail after the backend made the client or the instance. The calls
`createClient` and `createInstance` have no idempotency key. A client error of these two calls
(`timeout`, `network_error` or `invalid_response`) has the advice `Unknown`, not `RetryNow`. After
this error, list the clients or the instances before you call again. A second call can make a
duplicate.

A Laravel job can map each advice to `release()` or `fail()`. The key is in the job data, so every
retry uses the same key.

```php
namespace App\Jobs;

use Calyx\Client\Backend\BackendClient;
use Calyx\Client\CalyxException;
use Calyx\Client\Generated\Model\SendMessageRequest;
use Calyx\Client\RetryAdvice;
use DateTimeInterface;
use Illuminate\Contracts\Queue\ShouldQueue;
use Illuminate\Foundation\Bus\Dispatchable;
use Illuminate\Foundation\Queue\Queueable;
use Illuminate\Queue\InteractsWithQueue;

// Dispatch it with a key that you made at dispatch time:
// SendOrderMessage::dispatch($instanceId, $to, $text, IdempotencyKey::new());
final class SendOrderMessage implements ShouldQueue
{
    use Dispatchable;
    use InteractsWithQueue;
    use Queueable;

    // A send can last 360 s. The retry_after of the queue connection must be longer than this.
    public int $timeout = 400;

    public function __construct(
        private readonly string $instanceId,
        private readonly string $to,
        private readonly string $text,
        private readonly string $idempotencyKey,
    ) {}

    // The core forgets a key after 24 hours. Stop the retries before that.
    public function retryUntil(): DateTimeInterface
    {
        return now()->addHours(23);
    }

    public function handle(BackendClient $calyx): void
    {
        try {
            $calyx->sendMessage(
                $this->instanceId,
                new SendMessageRequest($this->to, $this->text),
                idempotencyKey: $this->idempotencyKey,
            );
        } catch (CalyxException $e) {
            // A retry always uses the same key, so the instance sends the message one time.
            match ($e->retry) {
                RetryAdvice::RetryNow => $this->release(10),
                RetryAdvice::RetryLater => $this->release(max($e->retryAfter, 30)),
                // Look at the chat first, or fix the request. A person must decide.
                RetryAdvice::CheckIfSent, RetryAdvice::DoNotRetry, RetryAdvice::Unknown => $this->fail($e),
            };
        }
    }
}
```

Bind the client once in the container of Laravel:

```php
namespace App\Providers;

use Calyx\Client\Backend\BackendClient;
use Illuminate\Support\ServiceProvider;

final class CalyxServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        // One client for the whole process. The key comes from config/calyx.php, which reads .env.
        $this->app->singleton(BackendClient::class, fn() => new BackendClient(
            (string) config('calyx.url'),
            (string) config('calyx.api_key'),
        ));
    }
}
```

Set `retry_after` of the queue connection to more than the `$timeout` of the job. Otherwise Laravel
starts a second copy of the job while the first copy waits. Both copies use the same key, so the
instance sends one message.

## 7. Pairing

Pairing links a WhatsApp number to an instance. Two steps come first. The operator of the
backend allows WhatsApp Web for the organization. Then the owner of the organization accepts the
WhatsApp Web terms in the panel. Before that, `createInstance` gives `whatsapp_web_not_allowed`
or `whatsapp_web_not_acknowledged`.

1. Call `createInstance` and show `$result->pairing->qrText` as a QR code.
2. Let the browser call your controller every 3 s.
3. In the controller, call `pairingSnapshot($instanceId)`.
4. Stop when the state is `Connected` or `Failed`.

```php
$result = $calyx->createInstance(new CreateInstanceRequest(
    displayName: 'Shop line',
    provider: CreateInstanceRequest::PROVIDER_WHATSAPP_WEB,
    locale: 'es-ES',
));

// Store the instance id, and show the QR text to the operator with a QR library.
$instanceId = $result->instance->id;
$qrText = $result->pairing->qrText;
```

```php
// The browser calls this controller every 3 s. The call waits 2 s at most.
$snapshot = $calyx->pairingSnapshot('instance-id');

$answer = match ($snapshot->state) {
    PairingState::Waiting => ['state' => 'waiting', 'qr' => $snapshot->pairing?->qrText],
    PairingState::Connected => ['state' => 'connected'],
    PairingState::Failed => ['state' => 'failed', 'reason' => $snapshot->reason],
};

header('Content-Type: application/json');
echo json_encode($answer, JSON_THROW_ON_ERROR);
```

`pairingSnapshot` has a time limit of 2 s by default. It reads the first item of the pairing
stream and closes the stream. It never holds a PHP-FPM worker for more than the limit.

| State | Meaning |
|---|---|
| `Waiting` | The pairing runs. `pairing->qrText` or `pairing->pairingCode` holds the latest value. |
| `Connected` | The instance is connected. |
| `Failed` | The pairing failed or expired, or no pairing runs. `reason` can be null. |

Rules:

1. `Waiting` also comes for a slow connect. The call gives `Waiting` with no `pairing` when the
   time limit passes. It never throws for this.
2. After a 404 of the stream, the call reads the instance. It can throw `timeout` when the
   backend is slow. Call it again at the next poll.
3. Every other error is thrown as it is. A wrong `Failed` can make your program delete a working
   instance, so the SDK does not guess.
4. The backend deletes a new instance when its pairing fails. `Failed` with no reason can mean this.
5. `pairingSnapshot` works for a backend with one process. A pairing on another process of the
   backend gives `Failed`. Run one backend process.
6. `followPairing()` is the full stream, for a CLI. Do not use it in a web request. After a
   `break`, call `unset($stream)`: the connection closes when the generator is destroyed.

## 8. Webhooks

The core signs a webhook when the webhook has a secret. `Webhook::verify` checks the signature and
the time, then returns an `Event`.

Rules for Laravel:

1. Put the route in `routes/api.php`, which has no CSRF check. Laravel 11 and later has no such
   file until you run `php artisan install:api`. The file adds the `/api` prefix, so the webhook
   URL is `https://your-shop/api/calyx/webhook`. A core webhook to a wrong path gets 404, and the
   core does not retry a 4xx answer. Or use `routes/web.php`, and exclude the path from the CSRF
   check in `bootstrap/app.php`. Without this, Laravel answers 419, and you lose the event.
2. Give the raw body: `$request->getContent()`. A body that you decoded and encoded again can have
   other bytes, and the check fails.
3. Answer 204 when you accept the event.
4. Keep a unique index on the event `id`. A retry or a replay inside the 5 minutes of the
   tolerance delivers the same `id` again.
5. Keep the clock of your server correct (NTP). A clock that is wrong by more than 5 minutes
   gives `stale_timestamp` for every delivery.

```php
use Calyx\Client\CalyxException;
use Calyx\Client\Event\EventNames;
use Calyx\Client\Webhook\Webhook;
use Illuminate\Http\Request;
use Illuminate\Http\Response;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Route;

// routes/api.php has no CSRF check. It adds the /api prefix, so the webhook URL is
// https://your-shop/api/calyx/webhook. In routes/web.php, exclude the path from the CSRF check
// in bootstrap/app.php, or Laravel answers 419 and the core does not retry.
Route::post('/calyx/webhook', function (Request $request): Response {
    try {
        // getContent() is the raw body. headers->all() gives lists of one string, and verify() accepts them.
        $event = Webhook::verify($request->getContent(), $request->headers->all(), (string) config('calyx.webhook_secret'));
    } catch (CalyxException) {
        return response('', 401);
    }

    // A unique index on "id" refuses an event that arrives two times.
    $isNew = DB::table('calyx_events')->insertOrIgnore([
        'id' => $event->id,
        'event' => $event->event,
        'payload' => json_encode($event->data, JSON_THROW_ON_ERROR),
    ]);

    if ($isNew === 1 && $event->event === EventNames::MESSAGE_RECEIVED) {
        // Do the slow work in a queue job, and answer fast.
    }

    return response('', 204);
});
```

The unique index of the example:

```sql
CREATE TABLE calyx_events (
    id      VARCHAR(64) PRIMARY KEY,
    event   VARCHAR(64) NOT NULL,
    payload TEXT NOT NULL
);
```

For plain PHP, read the body from `php://input`. The check is the same:

```php
$secret = getenv('CALYX_WEBHOOK_SECRET') ?: '';

try {
    // Give the raw body, as it arrived. Do not decode it and encode it again.
    $event = Webhook::verify(file_get_contents('php://input') ?: '', getallheaders(), $secret);
} catch (CalyxException) {
    // invalid_signature, stale_timestamp or invalid_event. The core does not retry a 4xx answer.
    http_response_code(401);

    return;
}

// A retry delivers the same id again. Store the id with a unique index, and stop when it exists.
if ($event->event === EventNames::MESSAGE_RECEIVED) {
    try {
        $message = $event->messageReceived();
        $text = $message->text;
    } catch (CalyxException) {
        // invalid_event: the payload has an unexpected shape. Read $event->data instead of failing.
    }
}

http_response_code(204);
```

Reading an event:

| Event name | Method | Payload |
|---|---|---|
| `message.received` | `$event->messageReceived()` | `MessageReceived` |
| `message.status` | `$event->messageStatus()` | `MessageStatus` |
| `connection.update` | `$event->connectionUpdate()` | `ConnectionUpdate` |
| `bulk.finished` | `$event->bulkFinished()` | `BulkFinished` |

The class `EventNames` has the names, and `Statuses` has the status values. A newer core can send
an event name that your version does not know, so compare the name as a string.

1. A payload method throws `invalid_event` for another event name or for a payload of a wrong
   shape. Do not fail the delivery for this. Read `$event->data` (an array) instead.
2. `Statuses::FAILED` (`failed`) is the delivery status of a message. `Statuses::BULK_FAILED`
   (`FAILED`) is the status of a bulk job. They differ in letter case.
3. `$event->timestamp` stays a string with 9 digits of precision. A PHP `DateTime` keeps only 6.
4. `Webhook::verify` accepts an `X-Calyx-Timestamp` header of digits only. The default tolerance is 300 s. Pass
   `toleranceSeconds:` to change it.
5. A webhook without a secret has no signature. `Event::fromJson($body)` decodes it with no
   check. Accept only a sender that you trust on the network level.

## 9. The limits of shared hosting

| Limit | What to do |
|---|---|
| No `ext-grpc` and no shell | Use this package. It needs only `ext-curl`, `ext-json` and `ext-hash`. |
| `max_execution_time` of 30 s | Use `timeLimit: 20` or less in a web request. `pairingSnapshot` uses 2 s. |
| No queue worker | Run a cron job that calls `checkSend` with a lock ([5](#5-the-check-of-a-pending-send)). |
| Few PHP workers | Never hold a worker for a long send or a pairing stream. |
| No access to the clock | Ask your host for NTP. A wrong clock refuses every webhook. |
| A firewall for outgoing calls | Allow HTTPS to your Calyx backend. |

A webhook needs a public HTTPS URL that the core can reach. Check that your host does not block
the POST of the core.

## 10. The PHP client codes

A client code occurs in the SDK, not on the server. The constants are in `Calyx\Client\Generated\ClientCode`.

| Code | Advice | Meaning |
|---|---|---|
| `timeout` | `RetryNow` | The time limit passed. On a send, the call throws `SendPendingException`. |
| `network_error` | `RetryNow` | No connection, or the connection stopped. |
| `invalid_response` | `RetryNow` | The answer is not JSON or has a wrong shape. A proxy can send an HTML page. |
| `stream_failed` | `RetryNow` | A stream broke or ended too early. |
| `invalid_signature` | `DoNotRetry` | The webhook signature or a header is wrong. |
| `stale_timestamp` | `DoNotRetry` | The webhook timestamp is outside the tolerance. |
| `invalid_event` | `DoNotRetry` | The body is not an event, or the payload has a wrong shape. |
| `invalid_config` | `DoNotRetry` | The setup or an argument is wrong. No request went out. |
| `pending_send_expired` | `CheckIfSent` | `checkSend` refused a pending send older than 23 hours. |
| `unsupported_runtime` | `DoNotRetry` | A PHP code for the core package: `ext-grpc` is missing. This package never gives it. |
| `aborted` | `RetryNow` | The program canceled the call. The backend client never gives it (see below). |

On `createClient` and `createInstance`, the first three have the advice `Unknown`.

The two PHP-only codes, `pending_send_expired` and `unsupported_runtime`, are not in the shared
contract of the SDKs. `ClientCode` has them.

The backend client never gives `aborted`, because a PHP call has no way to cancel another call
that runs. The time limit gives `timeout`. The core client gives `aborted` after `close()`, or
for a `close()` during a stream.

## 11. The risk notice, and bulk jobs

Calyx is an unofficial integration. Using WhatsApp Web through it is against the Terms of Service
of WhatsApp. WhatsApp can ban a number, and Calyx cannot promise that it will not. The
guardrails of the core make abuse hard, but they do not make a number safe.

Bulk jobs are for transactional or consented messages only. Examples: an appointment reminder
that the customer asked for, or a notice about an order. Do not use a bulk job for unsolicited
messages. The core enforces its pacing and its minimum delays, and no option turns them off.

```php
// Use a bulk job only for transactional or consented messages.
// Store the key first. A second call with the same key gives the same job.
$key = IdempotencyKey::new();

$created = $calyx->createBulkJob(
    'instance-id',
    new CreateBulkJobRequest(
        recipients: ['34600000001', '34600000002'],
        text: 'Your appointment is tomorrow at 10:00.',
    ),
    idempotencyKey: $key,
);

// The core sends the messages with its own pacing. Read the progress when you need it.
$job = $calyx->getBulkJob($created->data->id);
$progress = $job->progressPercent;
```

## `getMe`

`getMe()` returns the user of the API key in `$me->user` and the organization in
`$me->organization`, a `MeOrganization`. `id` and `name` always have a value.

The other six properties are for an owner. They are `null` when `$me->user->role` is `client`.
For an owner, `metaWebhook` and `whatsappWebAcknowledgedAt` can also be `null`.

## Supported backend and core

Use a core and a backend built from the commit `47a39b0` or later. The core change is in commit
`7520635`, and the backend change is in commit `5e90c69`. With them, the core answers a `checkSend`
from the record of the key, also while the instance is not connected. With an older core, a check
gives `instance_not_connected`, or the core queues a send with a new key.
Deploy the core first, then the backend.

The backend refuses request fields that it does not know (400 `invalid_request`). Update the
backend before the SDK.

## Work on this package

Do not install PHP or Composer on the host. Every PHP command runs in Docker.

1. Run `pnpm --filter @calyx/client-sdk-php test` for the PHPUnit tests (PHP 8.3 and 8.5).
2. Run `pnpm --filter @calyx/client-sdk-php lint` for PHPStan (level 10), PHP-CS-Fixer and the Go tools.
3. Run `pnpm --filter @calyx/client-sdk-php deps:check` for the dependency rule.
4. Run `pnpm --filter @calyx/client-sdk-php generate:check` for the generated code.

Never edit `src/Generated`. When `backend.json` changes, update it in two steps:

1. Run `pnpm --filter @calyx/client-sdk-php generate`.
2. Run `go test ./cmd/phpgen -update` in `packages/client-sdk/tools`, to update the golden
   files of the generator.

`ReadmeTest` checks that each PHP code block of this file is the body of a file in `examples/`.
Change the example first, then copy its body here.
