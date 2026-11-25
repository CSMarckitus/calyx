<?php

declare(strict_types=1);

namespace Calyx\Client;

use Calyx\Client\Generated\Advice;
use Calyx\Client\Generated\ClientCode;

/** What the program does after an error. The SDK never acts on it. */
enum RetryAdvice: string
{
    case RetryNow = 'retry_now';
    case RetryLater = 'retry_later';
    case CheckIfSent = 'check_if_sent';
    case DoNotRetry = 'do_not_retry';
    case Unknown = 'unknown';

    /**
     * The client codes that only the PHP SDK gives. They are not in retry-advice.json.
     *
     * @var array<string, self>
     */
    public const array PHP_ONLY = [
        ClientCode::UNSUPPORTED_RUNTIME => self::DoNotRetry,
        ClientCode::PENDING_SEND_EXPIRED => self::CheckIfSent,
    ];

    public static function for(Source $source, string $code): self
    {
        $advice = Advice::MAP[$source->value][$code] ?? null;
        if ($advice === null && $source === Source::Client) {
            $advice = self::PHP_ONLY[$code] ?? null;
        }

        return $advice ?? self::Unknown;
    }
}
