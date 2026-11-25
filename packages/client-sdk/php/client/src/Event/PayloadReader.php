<?php

declare(strict_types=1);

namespace Calyx\Client\Event;

use Calyx\Client\CalyxException;
use Calyx\Client\Generated\ClientCode;
use Calyx\Client\Internal\ErrorDecoder;

/**
 * Reads the fields of an event payload. Every fault gives invalid_event, not invalid_response.
 * Unknown fields are ignored, so an older SDK reads the events of a newer core.
 *
 * @internal
 */
final readonly class PayloadReader
{
    /**
     * @param array<mixed> $data
     */
    public function __construct(
        private string $name,
        private array $data,
    ) {}

    public function string(string $field): string
    {
        $value = $this->data[$field] ?? null;

        return is_string($value) ? $value : throw $this->fault($field, 'a string');
    }

    public function stringOrNull(string $field): ?string
    {
        $value = $this->data[$field] ?? null;

        return $value === null || is_string($value) ? $value : throw $this->fault($field, 'a string');
    }

    public function int(string $field): int
    {
        $value = $this->data[$field] ?? null;

        return is_int($value) ? $value : throw $this->fault($field, 'an integer');
    }

    public function bool(string $field): bool
    {
        $value = $this->data[$field] ?? null;

        return is_bool($value) ? $value : throw $this->fault($field, 'a boolean');
    }

    // The detail names the event and the field, never the value.
    private function fault(string $field, string $type): CalyxException
    {
        return ErrorDecoder::client(
            ClientCode::INVALID_EVENT,
            "The field {$field} of {$this->name} is missing or is not {$type}.",
        );
    }
}
