<?php

declare(strict_types=1);

namespace Calyx\Client\Internal;

/**
 * One HTTP request for a Transport.
 *
 * @internal
 */
final readonly class Request
{
    /**
     * @param array<string, string> $headers
     * @param ?float $timeLimit seconds for the whole call; null means no limit
     */
    public function __construct(
        public string $method,
        public string $url,
        public array $headers = [],
        public ?string $body = null,
        public ?float $timeLimit = null,
    ) {}

    public function header(string $name): ?string
    {
        foreach ($this->headers as $key => $value) {
            if (strcasecmp($key, $name) === 0) {
                return $value;
            }
        }

        return null;
    }
}
