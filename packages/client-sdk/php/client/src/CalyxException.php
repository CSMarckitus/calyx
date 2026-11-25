<?php

declare(strict_types=1);

namespace Calyx\Client;

use RuntimeException;
use Throwable;

/**
 * The one error of the SDK (CONTRACT.md part 4).
 *
 * The message never holds the API key, the core token or a webhook secret: the code that builds
 * the exception keeps them out of `$detail`.
 */
class CalyxException extends RuntimeException
{
    public readonly RetryAdvice $retry;

    /**
     * @param list<FieldError> $errors
     * @param array<string, string> $metadata
     * @param RetryAdvice|null $retry the advice of the call, when it is not the one of the code
     */
    public function __construct(
        public readonly Source $source,
        public readonly string $errorCode,
        public readonly int $status,
        public readonly string $detail,
        public readonly array $errors = [],
        public readonly ?string $requestId = null,
        public readonly int $retryAfter = 0,
        public readonly ?string $idempotencyKey = null,
        public readonly array $metadata = [],
        ?Throwable $previous = null,
        ?RetryAdvice $retry = null,
    ) {
        $this->retry = $retry ?? RetryAdvice::for($source, $errorCode);
        parent::__construct("{$errorCode}: {$detail}", 0, $previous);
    }
}
