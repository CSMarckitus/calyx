<?php

declare(strict_types=1);

namespace Calyx\Client\Internal;

use Calyx\Client\CalyxException;
use Calyx\Client\Generated\ClientCode;
use Calyx\Client\Source;

/**
 * Reads the fields of one JSON object for the fromArray() methods of the generated model classes.
 *
 * A method without "OrNull" reads a required field that cannot be null. A method with
 * "OrNull" gives null for a null field, and for a missing field that is not required.
 * Unknown fields are ignored.
 *
 * @internal
 */
final readonly class FieldReader
{
    /**
     * @param array<mixed> $data
     */
    public function __construct(
        private string $schema,
        private array $data,
    ) {}

    public function string(string $field): string
    {
        return $this->stringOrNull($field, true) ?? throw $this->wrongType($field, 'a string');
    }

    public function stringOrNull(string $field, bool $required = false): ?string
    {
        $value = $this->value($field, $required);

        return $value === null || is_string($value) ? $value : throw $this->wrongType($field, 'a string');
    }

    public function int(string $field): int
    {
        return $this->intOrNull($field, true) ?? throw $this->wrongType($field, 'an integer');
    }

    public function intOrNull(string $field, bool $required = false): ?int
    {
        $value = $this->value($field, $required);

        return $value === null || is_int($value) ? $value : throw $this->wrongType($field, 'an integer');
    }

    // A number field also accepts an integer: a JSON encoder can write 50.0 as 50.
    public function float(string $field): float
    {
        return $this->floatOrNull($field, true) ?? throw $this->wrongType($field, 'a number');
    }

    public function floatOrNull(string $field, bool $required = false): ?float
    {
        $value = $this->value($field, $required);
        if ($value === null) {
            return null;
        }

        return is_int($value) || is_float($value) ? (float) $value : throw $this->wrongType($field, 'a number');
    }

    public function bool(string $field): bool
    {
        return $this->boolOrNull($field, true) ?? throw $this->wrongType($field, 'a boolean');
    }

    public function boolOrNull(string $field, bool $required = false): ?bool
    {
        $value = $this->value($field, $required);

        return $value === null || is_bool($value) ? $value : throw $this->wrongType($field, 'a boolean');
    }

    public function mixed(string $field, bool $required = false): mixed
    {
        return $this->value($field, $required);
    }

    /**
     * @template T of object
     *
     * @param callable(array<mixed>): T $read
     *
     * @return T
     */
    public function object(string $field, callable $read): object
    {
        return $this->objectOrNull($field, $read, true) ?? throw $this->wrongType($field, 'an object');
    }

    /**
     * @template T of object
     *
     * @param callable(array<mixed>): T $read
     *
     * @return T|null
     */
    public function objectOrNull(string $field, callable $read, bool $required = false): ?object
    {
        $value = $this->value($field, $required);
        if ($value === null) {
            return null;
        }

        return $read($this->objectValue($field, $value));
    }

    /**
     * @template T of object
     *
     * @param callable(array<mixed>): T $read
     *
     * @return list<T>
     */
    public function list(string $field, callable $read): array
    {
        return $this->listOrNull($field, $read, true) ?? throw $this->wrongType($field, 'a list');
    }

    /**
     * @template T of object
     *
     * @param callable(array<mixed>): T $read
     *
     * @return list<T>|null
     */
    public function listOrNull(string $field, callable $read, bool $required = false): ?array
    {
        $value = $this->value($field, $required);
        if ($value === null) {
            return null;
        }
        $items = [];
        foreach ($this->listValue($field, $value) as $item) {
            $items[] = $read($this->objectValue($field, $item));
        }

        return $items;
    }

    /**
     * @return list<string>
     */
    public function stringList(string $field): array
    {
        return $this->stringListOrNull($field, true) ?? throw $this->wrongType($field, 'a list');
    }

    /**
     * @return list<string>|null
     */
    public function stringListOrNull(string $field, bool $required = false): ?array
    {
        $value = $this->value($field, $required);
        if ($value === null) {
            return null;
        }
        $items = [];
        foreach ($this->listValue($field, $value) as $item) {
            $items[] = is_string($item) ? $item : throw $this->wrongType($field, 'a list of strings');
        }

        return $items;
    }

    /**
     * Returns the value, or null when the field is null or missing. A missing required field
     * throws.
     */
    private function value(string $field, bool $required): mixed
    {
        if (!array_key_exists($field, $this->data)) {
            return $required ? throw $this->invalid("The field {$this->schema}.{$field} is missing.") : null;
        }

        return $this->data[$field];
    }

    /**
     * json_decode() gives an array for a JSON object and for a JSON list. An empty array can be
     * either, so it is accepted as an object.
     *
     * @return array<mixed>
     */
    private function objectValue(string $field, mixed $value): array
    {
        if (!is_array($value) || (array_is_list($value) && $value !== [])) {
            throw $this->wrongType($field, 'an object');
        }

        return $value;
    }

    /**
     * @return list<mixed>
     */
    private function listValue(string $field, mixed $value): array
    {
        return is_array($value) && array_is_list($value) ? $value : throw $this->wrongType($field, 'a list');
    }

    private function wrongType(string $field, string $type): CalyxException
    {
        return $this->invalid("The field {$this->schema}.{$field} is not {$type}.");
    }

    private function invalid(string $detail): CalyxException
    {
        return new CalyxException(Source::Client, ClientCode::INVALID_RESPONSE, 0, $detail);
    }
}
