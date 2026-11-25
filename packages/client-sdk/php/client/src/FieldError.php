<?php

declare(strict_types=1);

namespace Calyx\Client;

/** One field error of a backend answer. */
final readonly class FieldError
{
    public function __construct(
        public string $location,
        public string $message,
    ) {}
}
