<?php

declare(strict_types=1);

namespace Calyx\Client\Core\Internal;

use Calyx\Client\CalyxException;
use Calyx\Client\Source;
use Exception;
use Google\Protobuf\Any;
use Google\Protobuf\Internal\Message;
use Google\Rpc\ErrorInfo;
use Google\Rpc\RetryInfo;
use Google\Rpc\Status;

/**
 * Turns the status of a failed gRPC call into a CalyxException. A port of convertError in
 * packages/client-sdk/go/core/errors.go.
 *
 * @internal
 */
final class StatusDecoder
{
    /** The ErrorInfo domain of the core reasons. */
    private const string DOMAIN = 'calyx.core.v1';
    private const string ERROR_INFO = 'type.googleapis.com/google.rpc.ErrorInfo';
    private const string RETRY_INFO = 'type.googleapis.com/google.rpc.RetryInfo';

    /** The canonical names of the gRPC codes 1 to 16. */
    private const array NAMES = [
        1 => 'CANCELLED',
        2 => 'UNKNOWN',
        3 => 'INVALID_ARGUMENT',
        4 => 'DEADLINE_EXCEEDED',
        5 => 'NOT_FOUND',
        6 => 'ALREADY_EXISTS',
        7 => 'PERMISSION_DENIED',
        8 => 'RESOURCE_EXHAUSTED',
        9 => 'FAILED_PRECONDITION',
        10 => 'ABORTED',
        11 => 'OUT_OF_RANGE',
        12 => 'UNIMPLEMENTED',
        13 => 'INTERNAL',
        14 => 'UNAVAILABLE',
        15 => 'DATA_LOSS',
        16 => 'UNAUTHENTICATED',
    ];

    /**
     * @param array<array-key, mixed> $trailers The trailing metadata: name to list of values.
     */
    public static function decode(int $grpcCode, string $message, array $trailers, ?string $idempotencyKey = null): CalyxException
    {
        // A code outside 1 to 16 is not valid gRPC. UNKNOWN is the gRPC rule for it.
        $code = self::NAMES[$grpcCode] ?? 'UNKNOWN';
        $metadata = [];
        $retryAfter = 0;
        $haveInfo = false;
        foreach (self::details($trailers) as $detail) {
            // Compare the type URL. Any::unpack needs the descriptor pool, which holds only the
            // classes that the program loaded.
            $type = $detail->getTypeUrl();
            if ($type === self::ERROR_INFO) {
                $info = new ErrorInfo();
                if (!self::merge($info, $detail->getValue())) {
                    continue;
                }
                // The first ErrorInfo of the domain wins.
                if ($haveInfo || $info->getDomain() !== self::DOMAIN) {
                    continue;
                }
                $haveInfo = true;
                // An empty reason keeps the canonical name.
                if ($info->getReason() !== '') {
                    $code = $info->getReason();
                }
                foreach ($info->getMetadata() as $key => $value) {
                    if (is_string($key) && is_string($value)) {
                        $metadata[$key] = $value;
                    }
                }
            } elseif ($type === self::RETRY_INFO) {
                $info = new RetryInfo();
                $delay = self::merge($info, $detail->getValue()) ? $info->getRetryDelay() : null;
                if ($delay !== null) {
                    // Round up to whole seconds, so the program never waits less than the core
                    // asks. A negative delay means no wait.
                    $retryAfter = max(0, (int) $delay->getSeconds() + ($delay->getNanos() > 0 ? 1 : 0));
                }
            }
        }

        return new CalyxException(
            Source::Core,
            $code,
            $grpcCode,
            $message,
            retryAfter: $retryAfter,
            idempotencyKey: $idempotencyKey,
            metadata: $metadata,
        );
    }

    /**
     * The details of the Status in grpc-status-details-bin. Bytes that do not decode give none,
     * as a status without details.
     *
     * @param array<array-key, mixed> $trailers
     * @return list<Any>
     */
    private static function details(array $trailers): array
    {
        $raw = $trailers['grpc-status-details-bin'] ?? null;
        $bytes = is_array($raw) ? ($raw[0] ?? null) : $raw;
        if (!is_string($bytes)) {
            return [];
        }
        $status = new Status();
        if (!self::merge($status, $bytes)) {
            return [];
        }
        $list = [];
        foreach ($status->getDetails() as $detail) {
            $list[] = $detail;
        }

        return $list;
    }

    // A detail that does not decode counts as no detail.
    private static function merge(Message $message, string $bytes): bool
    {
        try {
            $message->mergeFromString($bytes);
        } catch (Exception) {
            return false;
        }

        return true;
    }
}
