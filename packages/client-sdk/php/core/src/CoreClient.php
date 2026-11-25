<?php

declare(strict_types=1);

namespace Calyx\Client\Core;

use Calyx\Client\CalyxException;
use Calyx\Client\Core\Internal\EventConverter;
use Calyx\Client\Core\Internal\StatusDecoder;
use Calyx\Client\Core\Internal\Stub;
use Calyx\Client\Event\Event;
use Calyx\Client\Generated\ClientCode;
use Calyx\Client\IdempotencyKey;
use Calyx\Client\Sent;
use Calyx\Client\Source;
use Calyx\Core\V1\BulkJob;
use Calyx\Core\V1\CancelBulkJobRequest;
use Calyx\Core\V1\CreateInstanceRequest;
use Calyx\Core\V1\CreateInstanceResponse;
use Calyx\Core\V1\Event as CoreEvent;
use Calyx\Core\V1\GetBulkJobRequest;
use Calyx\Core\V1\GetBulkJobsRequest;
use Calyx\Core\V1\GetBulkJobsResponse;
use Calyx\Core\V1\GetInstanceStatusRequest;
use Calyx\Core\V1\GetInstanceStatusResponse;
use Calyx\Core\V1\ListInstancesRequest;
use Calyx\Core\V1\ListInstancesResponse;
use Calyx\Core\V1\RemoveInstanceRequest;
use Calyx\Core\V1\RemoveInstanceResponse;
use Calyx\Core\V1\SendBulkCustomMessagesRequest;
use Calyx\Core\V1\SendBulkSameMessageRequest;
use Calyx\Core\V1\SendPlainTextRequest;
use Calyx\Core\V1\SendPlainTextResponse;
use Calyx\Core\V1\SubscribeRequest;
use Calyx\Core\V1\UpdateInstanceConfigRequest;
use Calyx\Core\V1\UpdateInstanceConfigResponse;
use Generator;
use Google\Protobuf\Internal\Message;
use Grpc\ChannelCredentials;

/**
 * The client of the Calyx core gRPC API. A port of packages/client-sdk/go/core. Use it only on
 * the private network of the core (CONTRACT part 3).
 */
final class CoreClient
{
    // The send limit is longer than the slowest normal send, which waits about 4 minutes in the
    // queue of the core. The values are the ones of the Go SDK.
    private const float SEND_LIMIT = 360.0;
    private const float CALL_LIMIT = 120.0;

    private readonly string $address;

    /** It is not readonly, so a test can give a stub with short keepalive times. */
    private Stub $stub;

    /**
     * The metadata of every call. It is not readonly, so a test can add the case of fakecore.
     *
     * @var array<string, list<string>>
     */
    private array $metadata;

    private bool $closed = false;

    /**
     * $credentials wins over $insecure. Without both, the client uses TLS with the root
     * certificates of grpc/grpc (its etc/roots.pem), not the store of the system. To trust a
     * private CA, give `credentials: ChannelCredentials::createSsl(file_get_contents($caFile))`.
     * ext-grpc's ChannelCredentials::createInsecure() returns null, so here it means "no
     * credentials" (TLS). For a connection without TLS, use `insecure: true`.
     *
     * @throws CalyxException unsupported_runtime without ext-grpc, invalid_config for an empty address
     *                        or a token with a control character
     */
    public function __construct(
        string $address,
        #[\SensitiveParameter]
        string $token,
        bool $insecure = false,
        ?ChannelCredentials $credentials = null,
    ) {
        // Composer refuses the install without ext-grpc. This covers --ignore-platform-reqs.
        if (!extension_loaded('grpc')) {
            throw self::clientError(ClientCode::UNSUPPORTED_RUNTIME, 'The core client needs the PHP extension grpc.');
        }
        if ($address === '') {
            throw self::clientError(ClientCode::INVALID_CONFIG, 'The address of the core is empty.');
        }
        // The rule of the API key of the backend client. The message never holds the token.
        if (preg_match('/[\x00-\x1f\x7f]/', $token) === 1) {
            throw self::clientError(ClientCode::INVALID_CONFIG, 'The token has a control character.');
        }
        $this->address = $address;
        // createInsecure() gives null, so ??= cannot tell it from no credentials.
        if ($credentials === null) {
            $credentials = $insecure ? ChannelCredentials::createInsecure() : ChannelCredentials::createSsl();
        }
        $this->stub = new Stub($address, $credentials);
        // Metadata, not call credentials: call credentials refuse a channel without TLS.
        $this->metadata = ['authorization' => ['Bearer ' . $token]];
    }

    /**
     * What var_dump() and print_r() show. The metadata holds the token, so it stays out.
     *
     * @return array<string, mixed>
     */
    public function __debugInfo(): array
    {
        return ['address' => $this->address, 'token' => '(hidden)', 'closed' => $this->closed];
    }

    /** Closes the channel. A later call gives the client code aborted. */
    public function close(): void
    {
        if (!$this->closed) {
            $this->closed = true;
            $this->stub->close();
        }
    }

    /**
     * Creates an instance, or pairs it again, and yields each pairing update. The call starts
     * when the loop starts. A break cancels the call: the core stops the pairing, and the
     * instance stays NEEDS_AUTH. The stream has no time limit.
     *
     * @return Generator<int, CreateInstanceResponse>
     *
     * @throws CalyxException
     */
    public function createInstance(CreateInstanceRequest $request): Generator
    {
        yield from $this->stream('/calyx.core.v1.InstanceService/CreateInstance', $request, CreateInstanceResponse::class);
    }

    /** @throws CalyxException */
    public function removeInstance(RemoveInstanceRequest $request, ?float $timeLimit = null): RemoveInstanceResponse
    {
        return $this->unary('/calyx.core.v1.InstanceService/RemoveInstance', $request, RemoveInstanceResponse::class, $timeLimit ?? self::CALL_LIMIT);
    }

    /** @throws CalyxException */
    public function updateInstanceConfig(
        UpdateInstanceConfigRequest $request,
        ?float $timeLimit = null,
    ): UpdateInstanceConfigResponse {
        return $this->unary('/calyx.core.v1.InstanceService/UpdateInstanceConfig', $request, UpdateInstanceConfigResponse::class, $timeLimit ?? self::CALL_LIMIT);
    }

    /** @throws CalyxException */
    public function listInstances(ListInstancesRequest $request, ?float $timeLimit = null): ListInstancesResponse
    {
        return $this->unary('/calyx.core.v1.InstanceService/ListInstances', $request, ListInstancesResponse::class, $timeLimit ?? self::CALL_LIMIT);
    }

    /** @throws CalyxException */
    public function getInstanceStatus(
        GetInstanceStatusRequest $request,
        ?float $timeLimit = null,
    ): GetInstanceStatusResponse {
        return $this->unary('/calyx.core.v1.InstanceService/GetInstanceStatus', $request, GetInstanceStatusResponse::class, $timeLimit ?? self::CALL_LIMIT);
    }

    /**
     * Sends one text message. The arguments have the order of the backend client: the request,
     * the key, then the time limit. The key is $idempotencyKey, else the key of the request, else
     * a new one. The answer and every error carry the key.
     *
     * @return Sent<SendPlainTextResponse>
     *
     * @throws CalyxException
     */
    public function sendPlainText(
        SendPlainTextRequest $request,
        ?string $idempotencyKey = null,
        ?float $timeLimit = null,
    ): Sent {
        [$request, $key] = self::withKey($request, $idempotencyKey);
        $answer = $this->unary('/calyx.core.v1.MessageService/SendPlainText', $request, SendPlainTextResponse::class, $timeLimit ?? self::SEND_LIMIT, $key);

        return new Sent($answer, $key);
    }

    /**
     * Starts a bulk job with one message. The key rules are the ones of sendPlainText.
     *
     * @return Sent<BulkJob>
     *
     * @throws CalyxException
     */
    public function sendBulkSameMessage(
        SendBulkSameMessageRequest $request,
        ?string $idempotencyKey = null,
        ?float $timeLimit = null,
    ): Sent {
        [$request, $key] = self::withKey($request, $idempotencyKey);
        $answer = $this->unary('/calyx.core.v1.MessageService/SendBulkSameMessage', $request, BulkJob::class, $timeLimit ?? self::CALL_LIMIT, $key);

        return new Sent($answer, $key);
    }

    /**
     * Starts a bulk job with one message for each recipient. The key rules are the ones of
     * sendPlainText.
     *
     * @return Sent<BulkJob>
     *
     * @throws CalyxException
     */
    public function sendBulkCustomMessages(
        SendBulkCustomMessagesRequest $request,
        ?string $idempotencyKey = null,
        ?float $timeLimit = null,
    ): Sent {
        [$request, $key] = self::withKey($request, $idempotencyKey);
        $answer = $this->unary('/calyx.core.v1.MessageService/SendBulkCustomMessages', $request, BulkJob::class, $timeLimit ?? self::CALL_LIMIT, $key);

        return new Sent($answer, $key);
    }

    /** @throws CalyxException */
    public function getBulkJob(GetBulkJobRequest $request, ?float $timeLimit = null): BulkJob
    {
        return $this->unary('/calyx.core.v1.MessageService/GetBulkJob', $request, BulkJob::class, $timeLimit ?? self::CALL_LIMIT);
    }

    /** @throws CalyxException */
    public function getBulkJobs(GetBulkJobsRequest $request, ?float $timeLimit = null): GetBulkJobsResponse
    {
        return $this->unary('/calyx.core.v1.MessageService/GetBulkJobs', $request, GetBulkJobsResponse::class, $timeLimit ?? self::CALL_LIMIT);
    }

    /** @throws CalyxException */
    public function cancelBulkJob(CancelBulkJobRequest $request, ?float $timeLimit = null): BulkJob
    {
        return $this->unary('/calyx.core.v1.MessageService/CancelBulkJob', $request, BulkJob::class, $timeLimit ?? self::CALL_LIMIT);
    }

    /**
     * Yields the events of the core that match $filter, from the start of the loop. A break
     * cancels the call. The stream has no time limit and no normal end: a broken stream throws
     * the gRPC status name (for example UNAVAILABLE), and an end with status OK throws
     * stream_failed. The SDK never opens the stream again.
     *
     * @return Generator<int, Event>
     *
     * @throws CalyxException
     */
    public function subscribe(Filter $filter): Generator
    {
        $request = new SubscribeRequest([
            'instance_names' => $filter->instanceNames,
            'event_types' => $filter->eventTypes,
        ]);
        foreach ($this->stream('/calyx.core.v1.EventService/Subscribe', $request, CoreEvent::class) as $event) {
            yield EventConverter::toEvent($event);
        }

        throw self::clientError(ClientCode::STREAM_FAILED, 'The core ended the event stream.');
    }

    /**
     * Makes one unary call. An error carries $key.
     *
     * @template T of Message
     *
     * @param class-string<T> $answer
     *
     * @return T
     *
     * @throws CalyxException
     */
    private function unary(string $method, Message $request, string $answer, float $timeLimit, ?string $key = null): Message
    {
        $this->checkOpen($key);
        $start = hrtime(true);
        $options = ['timeout' => (int) ceil($timeLimit * 1_000_000)];
        [$response, $status] = $this->stub->unary($method, $request, $answer, $this->metadata, $options)->wait();
        $code = self::code($status);
        if ($code !== \Grpc\STATUS_OK) {
            // The time limit of the SDK passed. A DEADLINE_EXCEEDED of the core comes earlier.
            if ($code === \Grpc\STATUS_DEADLINE_EXCEEDED && (hrtime(true) - $start) / 1e9 >= $timeLimit) {
                throw self::clientError(ClientCode::TIMEOUT, 'The core did not answer in time.', $key);
            }

            throw self::coreError($status, $key);
        }
        assert($response instanceof $answer);

        return $response;
    }

    /**
     * Runs one server stream. The call starts at the first read. When the program stops the
     * loop early, PHP destroys the generator and the finally block cancels the call.
     *
     * @template T of Message
     *
     * @param class-string<T> $answer
     *
     * @return Generator<int, T>
     *
     * @throws CalyxException
     */
    private function stream(string $method, Message $request, string $answer): Generator
    {
        $this->checkOpen(null);
        $call = $this->stub->serverStream($method, $request, $answer, $this->metadata);
        $ended = false;
        try {
            $items = $call->responses();
            assert(is_iterable($items));
            foreach ($items as $item) {
                assert($item instanceof $answer);

                yield $item;
                // close() does not stop a call that runs, so the next read would wait forever.
                $this->checkOpen(null);
            }
            $status = $call->getStatus();
            $ended = true;
        } finally {
            if (!$ended) {
                $call->cancel();
            }
        }
        if (self::code($status) !== \Grpc\STATUS_OK) {
            $this->checkOpen(null);

            throw self::coreError($status, null);
        }
    }

    // The status of ext-grpc is a stdClass with code, details and metadata (the trailers).
    private static function code(object $status): int
    {
        return isset($status->code) && is_int($status->code) ? $status->code : \Grpc\STATUS_UNKNOWN;
    }

    private static function coreError(object $status, ?string $key): CalyxException
    {
        $details = isset($status->details) && is_string($status->details) ? $status->details : '';
        $trailers = isset($status->metadata) && is_array($status->metadata) ? $status->metadata : [];

        return StatusDecoder::decode(self::code($status), $details, $trailers, $key);
    }

    /**
     * Gives the request to send and its key (CONTRACT part 7). The key goes into a copy, so the
     * request of the program never changes.
     *
     * @template T of SendPlainTextRequest|SendBulkSameMessageRequest|SendBulkCustomMessagesRequest
     *
     * @param T $request
     *
     * @return array{T, string}
     *
     * @throws CalyxException invalid_config for a key that is not valid.
     */
    private static function withKey(Message $request, ?string $key): array
    {
        $inRequest = $request->getIdempotencyKey();
        if ($key === null || $key === '') {
            $key = $inRequest;
        }
        if ($key === '') {
            $key = IdempotencyKey::new();
        } elseif (preg_match('/^[\x21-\x7e]{1,255}$/D', $key) !== 1) {
            // The rule of the backend client. The message never holds the key.
            throw self::clientError(
                ClientCode::INVALID_CONFIG,
                'The idempotency key must have 1 to 255 printable ASCII characters, with no space.',
            );
        }
        if ($key === $inRequest) {
            return [$request, $key];
        }
        // The bytes give a deep copy. clone would share the nested messages.
        $copy = new ($request::class)();
        $copy->mergeFromString($request->serializeToString());
        $copy->setIdempotencyKey($key);

        return [$copy, $key];
    }

    /** @throws CalyxException */
    private function checkOpen(?string $key): void
    {
        if ($this->closed) {
            throw self::clientError(ClientCode::ABORTED, 'The client is closed.', $key);
        }
    }

    private static function clientError(string $code, string $detail, ?string $key = null): CalyxException
    {
        return new CalyxException(Source::Client, $code, 0, $detail, idempotencyKey: $key);
    }
}
