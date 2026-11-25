# Calyx core client for PHP

A PHP client for the gRPC API of the Calyx core. It makes the 12 calls of the core and reads its
event stream.

Status: version 0.1.0, early alpha. The package is not on Packagist.

Caution: the SDK does not make the number safe from a ban. WhatsApp Web is an unofficial
integration, and its use is against the Terms of Service of WhatsApp. The core enforces delays
between sends, and no client can turn them off.

## 1. When to use this package

The core has no users and no tenants. It sits on a private network. `GRPC_AUTH_TOKEN` is the
only access control. The core serves no TLS. TLS needs a proxy in front of the core.

1. Use the backend client (`calyx/client-sdk`) when your program calls Calyx over HTTP with an
   API key. This is the normal choice, and it runs on shared hosting.
2. Use this package only when your program runs on the private network of the core, and the
   program needs a core call that the backend does not offer, or a stream of events.
3. Do not open the core port to the internet.

| You need | Notes |
|---|---|
| PHP 8.3 or later | Tested on PHP 8.3. The test image uses the Alpine `php83` packages. |
| `ext-grpc` | A native extension. See section 2. |
| `calyx/client-sdk` 0.1.0 | The backend client. This package uses its error and event classes. |
| `grpc/grpc` `~1.84.1`, `google/protobuf` `~4.33.6`, `google/common-protos` `~4.15.0` | See "Versions" below. |

## 2. Install

### The PHP extension

Composer refuses the install without `ext-grpc`. Install the extension first.

1. With PECL: run `pecl install grpc`. The build takes several minutes. Add
   `extension=grpc.so` to a file of the PHP `conf.d` folder.
2. On Alpine: run `apk add php83-pecl-grpc`.
3. On Debian or Ubuntu: use the package `php8.3-grpc` of your PHP repository, if it has one.
   Otherwise use PECL.
4. Run `php -m | grep grpc` to check the extension.

### The package

The core package requires `calyx/client-sdk` 0.1.0. Neither package is published, so add both
packages to the Composer repositories of your program. Both `composer.json` files have the
version `0.1.0`.

1. Add a `path` repository for each package. The paths are relative to your program:

   ```json
   {
       "repositories": [
           {"type": "path", "url": "../calyx/packages/client-sdk/php/client"},
           {"type": "path", "url": "../calyx/packages/client-sdk/php/core"}
       ]
   }
   ```

   Or put the zip of the client (`composer archive` in `packages/client-sdk/php/client`) in a
   folder, and use `{"type": "artifact", "url": "<folder>"}` for the client. An `artifact`
   repository needs the PHP extension `zip`.
2. Run `composer require calyx/client-sdk-core:0.1.0`.

Maintainers: the lock file of this package keeps a reference to `../client`. After a change of
the client, run `docker/run.sh composer-core` in `packages/client-sdk/php`.

The classes of `Calyx\Core\V1` are protoc output in `generated/`. Do not edit them. After a change
of the `.proto` files, run `docker/run.sh protoc`.

### Versions

1. `grpc/grpc` is `~1.84.1`. Composer blocks every older version because of a security advisory
   (the bundled certificate store trusts two compromised intermediate CAs). `google/protobuf`
   is `~4.33.6` for the same reason (CVE-2026-6409).
2. The tests run the library 1.84 on `ext-grpc` 1.76.0 (Alpine 3.23). The streams and the
   errors work on this pair. This is a newer library on an older extension. Test your own
   extension version before you deploy.

## 3. Connect

```php
use Calyx\Client\Core\CoreClient;

$core = new CoreClient(address: 'core.internal:50051', token: getenv('CORE_TOKEN') ?: '', insecure: true);
```

The client connects at the first call. The token goes in the `authorization` header of every
call.

| Argument | Use |
|---|---|
| `insecure: true` | A private network that you trust. The token travels as plain text. |
| `credentials: ChannelCredentials::createSsl(...)` | Any other network. |
| neither | TLS with the root certificates that come with `grpc/grpc`. |

Caution: the default TLS does not read the certificate store of the system. It uses the file
`etc/roots.pem` of `grpc/grpc`. For a private CA, give the certificate of the CA:

```php
use Grpc\ChannelCredentials;

$credentials = ChannelCredentials::createSsl(file_get_contents($caFile));
$core = new CoreClient('core.internal:50051', $token, credentials: $credentials);
```

Caution: `ChannelCredentials::createInsecure()` returns `null` in `ext-grpc`. If you pass it as
`credentials`, the client sees no credentials and uses TLS. Use `insecure: true` instead.

Other rules:

1. The client gives the code `unsupported_runtime` when `ext-grpc` is not loaded. It gives
   `invalid_config` for an empty address, and for a token with a control character. The
   message never holds the token.
2. An empty token goes out as `Bearer `. The core answers `UNAUTHENTICATED`.
3. `close()` closes the channel. A call after `close()`, and a `close()` during a stream, give
   the code `aborted`. The backend client never gives `aborted`. The core client gives it
   because `close()` is the only way to cancel from outside.
4. `var_dump()` and `print_r()` of the client show the address, not the token.

## 4. Calls

There is one method for each of the 12 RPCs. The name is the RPC name in camelCase. The methods
take the request classes of `Calyx\Core\V1` and return the answer classes.

```php
use Calyx\Core\V1\ListInstancesRequest;

$answer = $core->listInstances(new ListInstancesRequest());
```

1. Every unary call takes an optional `timeLimit` in seconds. The default is 360 seconds for
   `sendPlainText` and 120 seconds for the other calls. The streams have no limit.
2. Every error is a `CalyxException`. A core error has `source` `Source::Core`. It has the
   fields `errorCode`, `status` (the gRPC code number), `retry` and `retryAfter`, as the backend
   client has. An error of the SDK has `Source::Client`.
3. A call that passes the time limit of the SDK gives the code `timeout`.
4. The SDK never sends a call again by itself. Use `retry` of the error to decide.
5. Some rare errors of `ext-grpc` and `google/protobuf` are plain PHP exceptions, not a
   `CalyxException`. Examples: a channel argument that `ext-grpc` refuses, and an answer that
   does not decode. A program that must never stop catches `\Throwable` around the calls.

### The three sends

`sendPlainText`, `sendBulkSameMessage` and `sendBulkCustomMessages` take the request, then
`idempotencyKey`, then `timeLimit`. This is the order of the backend client. They return a
`Sent`. Its `data` holds the answer, and its `idempotencyKey` holds the key of the call.
The core client has no `PendingSend`. That is for the backend client only.

The rules for a key are the ones of the backend client:

1. The key is the argument `idempotencyKey`, or else the field `idempotency_key` of the request.
   An empty string counts as no key.
2. With no key, the SDK makes one with `IdempotencyKey::new()`.
3. A key has 1 to 255 characters, each printable ASCII and not a space. Another key gives
   `invalid_config`, and no call goes out.
4. The SDK puts the key in a copy of the request. Your request does not change.
5. Every error of the call carries the same key. To send again after a `timeout`, use it.

```php
use Calyx\Core\V1\SendPlainTextRequest;

$sent = $core->sendPlainText(new SendPlainTextRequest([
    'instance_name' => $instance,
    'to' => '34600000000',
    'text' => 'Your order is ready.',
]), idempotencyKey: $key);
```

## 5. Streams

`createInstance` and `subscribe` return a `Generator`. Follow these rules:

1. The call starts when the `foreach` starts. Each call of the method opens a new stream. A
   generator runs once: a second `foreach` on the same generator throws a plain `\Exception`.
2. The stream has no time limit.
3. An error ends the stream. The loop throws one `CalyxException`, for example `UNAVAILABLE`.
4. The SDK never opens a stream again by itself.
5. The client sends a keepalive ping every 2 minutes while a call or a stream runs. When the core host
   disappears, each of them gets `UNAVAILABLE` within about 2 minutes and 20 seconds, or its own time
   limit if that passes first. After `UNAVAILABLE`, the next call opens a new connection. After a time
   limit, the next calls can use the dead connection until the connection fails.
6. gRPC sends a ping only when its 2-minute timer ends during a call. With short calls only, a ping may
   never go out. A core that hangs while its host still answers TCP then gives `timeout` again and
   again. After `timeout` errors in a row, restart the PHP process. `close()` and a new client do not
   help, because `ext-grpc` keeps the connection for the whole process (seen in 1.76.0).

Caution: a stop of the loop cancels the call, but only when PHP destroys the generator. Use
`break` inside the `foreach`, or call `unset()` on the generator. A generator that stays in a
variable keeps the stream open. For `createInstance`, the pairing then goes on.

Caution: a `break` in a `createInstance` loop stops the pairing. The instance stays in the state
`NEEDS_AUTH`. Keep the loop open until the pairing ends.

```php
use Calyx\Client\Core\Filter;

foreach ($core->subscribe(new Filter(eventTypes: ['message.received'])) as $event) {
    echo $event->event, ' of ', $event->instance, "\n";
}
```

`subscribe` gives the `Event` class of the backend client, the same type as a webhook. A stream
that ends with status OK throws `stream_failed`.

Caution: one event that is not valid ends the `subscribe` loop with `invalid_event`, as in the Go
SDK. A CLI worker must expect this error, then call `subscribe` again.

### Subscribe or webhooks

Caution: `subscribe` loses events. The program gets no event that the core sends between two
streams. The core also drops new events when the program reads slower than they arrive, because
it keeps 256 events for each subscriber.

1. Use webhooks when the program must get every event. The core retries a delivery, and the
   program stores the `id` of each event.
2. Use `subscribe` for a live view, or for a worker that can lose an event.
3. A PHP-FPM or Apache request must not read a stream. Run `subscribe` in a CLI process.
