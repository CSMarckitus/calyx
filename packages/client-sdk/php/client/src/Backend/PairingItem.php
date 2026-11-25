<?php

declare(strict_types=1);

namespace Calyx\Client\Backend;

use Calyx\Client\Generated\Model\Done;
use Calyx\Client\Generated\Model\Failed;
use Calyx\Client\Generated\Model\Pairing;

/**
 * One event of the pairing stream. Exactly one of `update`, `done` and `failed` is set, the one
 * that `event` names.
 */
final readonly class PairingItem
{
    /**
     * @param string $event "update", "done" or "failed"
     */
    public function __construct(
        public string $event,
        public ?Pairing $update = null,
        public ?Done $done = null,
        public ?Failed $failed = null,
    ) {}
}
