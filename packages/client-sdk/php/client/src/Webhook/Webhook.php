<?php

declare(strict_types=1);

namespace Calyx\Client\Webhook;

use Calyx\Client\CalyxException;
use Calyx\Client\Event\Event;
use Calyx\Client\Generated\ClientCode;
use Calyx\Client\Internal\ErrorDecoder;
use SensitiveParameter;

/**
 * Checks the signature and the time of a webhook. The steps and their order are CONTRACT
 * part 10. A port of packages/client-sdk/go/webhook/verify.go.
 */
final class Webhook
{
    private const string SIGNATURE_HEADER = 'x-calyx-signature';
    private const string TIMESTAMP_HEADER = 'x-calyx-timestamp';
    private const int DEFAULT_TOLERANCE = 300;

    /**
     * Checks the signature and the time of a webhook, then decodes the body to an Event.
     *
     * Caution: give the raw body, as the program received it (for example
     * `file_get_contents('php://input')`). JSON that a program decoded and encoded again can have
     * other bytes, and the check then fails.
     *
     * A failure gives a CalyxException with the code invalid_signature, stale_timestamp or
     * invalid_event. No message holds the secret or the body.
     *
     * @param array<array-key, mixed> $headers            The request headers. The names ignore letter case. A
     *                                                    value is a string or a list with one string.
     * @param int                     $toleranceSeconds   The largest distance between the timestamp and now.
     *                                                    A value of 0 or less gives the default of 300.
     * @param int|null                $now                The Unix time of the check. The default is time().
     *
     * @throws CalyxException
     */
    public static function verify(
        string $rawBody,
        #[SensitiveParameter]
        array $headers,
        #[SensitiveParameter]
        string $secret,
        int $toleranceSeconds = self::DEFAULT_TOLERANCE,
        ?int $now = null,
    ): Event {
        if ($toleranceSeconds <= 0) {
            $toleranceSeconds = self::DEFAULT_TOLERANCE;
        }
        $now ??= time();

        if ($secret === '') {
            throw self::reject(ClientCode::INVALID_SIGNATURE, 'The webhook secret is empty.');
        }
        $timestamp = self::header($headers, self::TIMESTAMP_HEADER);
        $signature = self::header($headers, self::SIGNATURE_HEADER);
        if ($timestamp === null || $signature === null) {
            throw self::reject(
                ClientCode::INVALID_SIGNATURE,
                'The signature or the timestamp header is missing, empty or has more than one value.',
            );
        }
        // Digits only, as in TypeScript. \z, not $, because $ also matches before a final newline.
        if (preg_match('/\A[0-9]+\z/', $timestamp) !== 1) {
            throw self::reject(ClientCode::INVALID_SIGNATURE, 'The timestamp header is not a number.');
        }
        // (int) saturates for a number that is too large, so it ends as stale.
        if (abs($now - (int) $timestamp) > $toleranceSeconds) {
            throw self::reject(ClientCode::STALE_TIMESTAMP, 'The timestamp is outside the tolerance.');
        }

        $want = 'sha256=' . hash_hmac('sha256', $timestamp . '.' . $rawBody, $secret);
        if (!hash_equals($want, strtolower($signature))) {
            throw self::reject(ClientCode::INVALID_SIGNATURE, 'The signature does not match.');
        }

        return Event::fromJson($rawBody);
    }

    /**
     * Reads one header. The name must be in lower case. It gives null when the header is missing,
     * is empty, is not a string, or is a list that does not have exactly one string.
     *
     * @param array<array-key, mixed> $headers
     */
    private static function header(array $headers, string $name): ?string
    {
        foreach ($headers as $key => $value) {
            if (strtolower((string) $key) !== $name) {
                continue;
            }
            if (is_array($value)) {
                $value = count($value) === 1 ? reset($value) : null;
            }

            return is_string($value) && $value !== '' ? $value : null;
        }

        return null;
    }

    private static function reject(string $code, string $detail): CalyxException
    {
        return ErrorDecoder::client($code, $detail);
    }
}
