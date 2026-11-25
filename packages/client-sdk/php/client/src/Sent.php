<?php

declare(strict_types=1);

namespace Calyx\Client;

/**
 * The answer of a send call, with the idempotency key that the call used.
 *
 * @template T
 */
final readonly class Sent
{
    /**
     * @param T $data
     */
    public function __construct(
        public mixed $data,
        public string $idempotencyKey,
    ) {}
}
