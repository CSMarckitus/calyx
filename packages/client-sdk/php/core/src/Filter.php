<?php

declare(strict_types=1);

namespace Calyx\Client\Core;

/** The filter of CoreClient::subscribe. An empty list matches all. */
final readonly class Filter
{
    /**
     * @param list<string> $instanceNames
     * @param list<string> $eventTypes
     */
    public function __construct(
        public array $instanceNames = [],
        public array $eventTypes = [],
    ) {}
}
