<?php

declare(strict_types=1);

namespace Calyx\Client\Internal;

/**
 * One HTTP answer from a Transport, 2xx or not.
 *
 * @internal
 */
final readonly class Response
{
    /** @var array<string, list<string>> header names in lower case */
    public array $headers;

    /**
     * @param array<string, list<string>> $headers
     */
    public function __construct(
        public int $status,
        array $headers,
        public string $body,
    ) {
        $lower = [];
        foreach ($headers as $name => $values) {
            $key = strtolower($name);
            $lower[$key] = array_merge($lower[$key] ?? [], $values);
        }
        $this->headers = $lower;
    }

    /** The first value of a header, or null. The name is not case sensitive. */
    public function header(string $name): ?string
    {
        return $this->headers[strtolower($name)][0] ?? null;
    }
}
