<?php

declare(strict_types=1);

namespace Calyx\Client\Internal;

/**
 * One server-sent event: the event name, the data lines joined with a line feed, and the id.
 *
 * @internal
 */
final readonly class SseEvent
{
    public function __construct(
        public string $event,
        public string $data,
        public ?string $id = null,
    ) {}
}
