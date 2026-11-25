<?php

declare(strict_types=1);

namespace Calyx\Client;

/** The place where an error starts. */
enum Source: string
{
    case Backend = 'backend';
    case Core = 'core';
    case Client = 'client';
}
