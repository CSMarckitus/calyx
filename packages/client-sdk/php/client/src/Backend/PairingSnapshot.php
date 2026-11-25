<?php

declare(strict_types=1);

namespace Calyx\Client\Backend;

use Calyx\Client\Generated\Model\Pairing;

/** The answer of pairingSnapshot. */
final readonly class PairingSnapshot
{
    /**
     * @param PairingState $state the state of the pairing
     * @param Pairing|null $pairing the QR text or the pairing code; set only with an update
     * @param string|null $reason the detail of a failed pairing; null when the backend gave none
     */
    public function __construct(
        public PairingState $state,
        public ?Pairing $pairing = null,
        public ?string $reason = null,
    ) {}
}
