<?php

declare(strict_types=1);

namespace Calyx\Client;

/**
 * A send whose outcome the program must check: the time limit passed, or the backend still runs
 * a send with the same key. The program stores `$pendingSend`, whatever the retry advice is.
 */
final class SendPendingException extends CalyxException
{
    public function __construct(
        public readonly PendingSend $pendingSend,
        CalyxException $cause,
    ) {
        parent::__construct(
            $cause->source,
            $cause->errorCode,
            $cause->status,
            $cause->detail,
            $cause->errors,
            $cause->requestId,
            $cause->retryAfter,
            $cause->idempotencyKey ?? $pendingSend->idempotencyKey,
            $cause->metadata,
            $cause,
            $cause->retry,
        );
    }
}
