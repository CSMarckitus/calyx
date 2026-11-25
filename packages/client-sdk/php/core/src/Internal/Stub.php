<?php

declare(strict_types=1);

namespace Calyx\Client\Core\Internal;

use Calyx\Client\Version;
use Google\Protobuf\Internal\Message;
use Grpc\BaseStub;
use Grpc\ChannelCredentials;
use Grpc\ServerStreamingCall;
use Grpc\UnaryCall;

/**
 * Makes the protected calls of BaseStub available to CoreClient, so the SDK needs no code from
 * grpc_php_plugin.
 *
 * @internal
 */
final class Stub extends BaseStub
{
    /**
     * The pings find a core host that disappears. The core refuses pings more often than once a
     * minute, and pings while no call runs.
     */
    public const array KEEPALIVE = [
        'grpc.keepalive_time_ms' => 120_000,
        'grpc.keepalive_permit_without_calls' => 0,
        'grpc.keepalive_timeout_ms' => 20_000,
        // gRPC does not use the timeout above for its pings (seen in 1.76). It uses this one.
        'grpc.http2.ping_timeout_ms' => 20_000,
        // Without this, gRPC limits the pings after two pings with no data.
        'grpc.http2.max_pings_without_data' => 0,
    ];

    /**
     * ext-grpc represents insecure credentials as null (ChannelCredentials::createInsecure()).
     *
     * @param array<string, int> $keepalive a test gives short times
     */
    public function __construct(
        string $address,
        ?ChannelCredentials $credentials,
        array $keepalive = self::KEEPALIVE,
    ) {
        parent::__construct($address, [
            'credentials' => $credentials,
            'grpc.primary_user_agent' => 'calyx-php-sdk/' . Version::SDK,
            ...$keepalive,
        ]);
    }

    /**
     * @template T of Message
     *
     * @param class-string<T>             $answer
     * @param array<string, list<string>> $metadata
     * @param array<string, int>          $options
     *
     * @return UnaryCall<T>
     */
    public function unary(
        string $method,
        Message $request,
        string $answer,
        #[\SensitiveParameter]
        array $metadata,
        array $options,
    ): UnaryCall {
        // grpc reads only the class name of this pair, as in the code of grpc_php_plugin.
        // @phpstan-ignore argument.type
        return $this->_simpleRequest($method, $request, [$answer, 'decode'], $metadata, $options);
    }

    /**
     * @param class-string<Message>       $answer
     * @param array<string, list<string>> $metadata
     */
    public function serverStream(
        string $method,
        Message $request,
        string $answer,
        #[\SensitiveParameter]
        array $metadata,
    ): ServerStreamingCall {
        // @phpstan-ignore argument.type
        return $this->_serverStreamRequest($method, $request, [$answer, 'decode'], $metadata);
    }
}
