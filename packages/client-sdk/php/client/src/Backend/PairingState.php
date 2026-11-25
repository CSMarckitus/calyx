<?php

declare(strict_types=1);

namespace Calyx\Client\Backend;

/** The state of a pairing that pairingSnapshot reports. */
enum PairingState: string
{
    /** The pairing runs: scan the QR text or enter the pairing code. */
    case Waiting = 'waiting';
    /** The instance is paired and connected. */
    case Connected = 'connected';
    /** The pairing ended with no connection. */
    case Failed = 'failed';
}
