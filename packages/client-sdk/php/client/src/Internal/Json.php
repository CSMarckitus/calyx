<?php

declare(strict_types=1);

namespace Calyx\Client\Internal;

use JsonException;

/**
 * The JSON rules of the SDK. A model with no fields encodes as {}, because the generated models
 * are JsonSerializable and an empty object is a stdClass, never [].
 *
 * @internal
 */
final class Json
{
    /**
     * @throws JsonException
     */
    public static function encode(mixed $value): string
    {
        return json_encode($value, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);
    }

    /**
     * @throws JsonException
     */
    public static function decode(string $text): mixed
    {
        return json_decode($text, true, 512, JSON_THROW_ON_ERROR);
    }
}
