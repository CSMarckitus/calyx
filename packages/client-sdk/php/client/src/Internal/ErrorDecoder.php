<?php

declare(strict_types=1);

namespace Calyx\Client\Internal;

use Calyx\Client\CalyxException;
use Calyx\Client\FieldError;
use Calyx\Client\Generated\ClientCode;
use Calyx\Client\Source;
use JsonException;
use Throwable;

/**
 * Builds a CalyxException from an answer that is not 2xx, and from a fault that the SDK finds.
 * A port of packages/client-sdk/go/backend/request.go.
 *
 * The SDK adds nothing of the request to the error except the idempotency key. The detail of a
 * backend error is the text of the server.
 *
 * @internal
 */
final class ErrorDecoder
{
    /**
     * Only a JSON body with a string code is a backend error. Any other body gives
     * invalid_response.
     */
    public static function decode(Request $request, Response $response): CalyxException
    {
        $key = $request->header('Idempotency-Key');
        $problem = self::problem($response->body);
        if ($problem === null) {
            $detail = "The backend answered {$response->status} with a body that is not problem+json.";

            return self::client(ClientCode::INVALID_RESPONSE, $detail, $key);
        }

        return new CalyxException(
            Source::Backend,
            $problem['code'],
            $response->status,
            $problem['detail'],
            $problem['errors'],
            $response->header('X-Request-Id'),
            self::retryAfter($response->header('Retry-After')),
            $key,
        );
    }

    /**
     * The error of a 2xx answer with a body that is not JSON.
     */
    public static function notJson(Request $request, int $status, JsonException $cause): CalyxException
    {
        $detail = "The backend answered {$status} with a body that is not JSON.";

        return self::client(ClientCode::INVALID_RESPONSE, $detail, $request->header('Idempotency-Key'), $cause);
    }

    /**
     * An error that the SDK found itself. The cause only goes to getPrevious().
     */
    public static function client(
        string $code,
        string $detail,
        ?string $idempotencyKey = null,
        ?Throwable $cause = null,
    ): CalyxException {
        return new CalyxException(
            Source::Client,
            $code,
            0,
            $detail,
            idempotencyKey: $idempotencyKey,
            previous: $cause,
        );
    }

    /**
     * @return array{code: string, detail: string, errors: list<FieldError>}|null
     */
    private static function problem(string $body): ?array
    {
        try {
            $p = Json::decode($body);
        } catch (JsonException) {
            return null;
        }
        // An empty list is an empty object here. It has no code, so it fails below.
        if (!is_array($p) || array_is_list($p) || !is_string($p['code'] ?? null)) {
            return null;
        }
        $detail = $p['detail'] ?? '';
        $title = $p['title'] ?? '';
        $items = $p['errors'] ?? null;
        if (!is_string($detail) || !is_string($title)) {
            return null;
        }
        if ($items !== null && !(is_array($items) && array_is_list($items))) {
            return null;
        }
        $errors = [];
        foreach ($items ?? [] as $item) {
            $location = is_array($item) ? ($item['location'] ?? '') : null;
            $message = is_array($item) ? ($item['message'] ?? '') : null;
            if (!is_string($location) || !is_string($message)) {
                return null;
            }
            $errors[] = new FieldError($location, $message);
        }

        return [
            'code' => $p['code'],
            'detail' => $detail !== '' ? $detail : $title,
            'errors' => $errors,
        ];
    }

    // The backend sends Retry-After as a number of seconds. The SDK reads only this form.
    private static function retryAfter(?string $value): int
    {
        // A value of 0 or less counts as no value, so a minus sign gives 0 at once.
        if (preg_match('/^\+?0*(\d+)$/', trim($value ?? ''), $match) !== 1) {
            return 0;
        }
        $seconds = (int) $match[1];

        // (int) saturates at PHP_INT_MAX. Go's Atoi refuses a number that does not fit: 0.
        return (string) $seconds === $match[1] ? $seconds : 0;
    }
}
