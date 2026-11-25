<?php

declare(strict_types=1);

namespace Calyx\Client\Event;

/** The payload of "connection.update". */
final readonly class ConnectionUpdate
{
    /**
     * @param string $status The new status of the instance. See Statuses. The value is open.
     */
    public function __construct(
        public string $status,
    ) {}

    /**
     * @param array<mixed> $data
     */
    public static function fromArray(array $data): self
    {
        $r = new PayloadReader(EventNames::CONNECTION_UPDATE, $data);

        return new self($r->string('status'));
    }
}
