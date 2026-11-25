<?php

declare(strict_types=1);

namespace Calyx\Client;

use Calyx\Client\Generated\ClientCode;

/**
 * A send that the program must check. The program can store `toArray()` and read it again with
 * `fromArray()`. `firstSentAt` is in Unix seconds.
 */
final readonly class PendingSend
{
    private const int VERSION = 1;

    public function __construct(
        public string $instanceId,
        public string $to,
        public string $text,
        public string $idempotencyKey,
        public int $firstSentAt,
    ) {}

    /**
     * @return array{v: int, instanceId: string, to: string, text: string, idempotencyKey: string, firstSentAt: int}
     */
    public function toArray(): array
    {
        return [
            'v' => self::VERSION,
            'instanceId' => $this->instanceId,
            'to' => $this->to,
            'text' => $this->text,
            'idempotencyKey' => $this->idempotencyKey,
            'firstSentAt' => $this->firstSentAt,
        ];
    }

    /**
     * @param array<mixed> $data
     *
     * @throws CalyxException invalid_config, for a missing field, a wrong type or a version
     *                        other than 1
     */
    public static function fromArray(array $data): self
    {
        if (($data['v'] ?? null) !== self::VERSION) {
            throw self::invalid('The field v of a PendingSend must be 1.');
        }

        return new self(
            instanceId: self::string($data, 'instanceId'),
            to: self::string($data, 'to'),
            text: self::string($data, 'text'),
            idempotencyKey: self::string($data, 'idempotencyKey'),
            firstSentAt: is_int($data['firstSentAt'] ?? null)
                ? $data['firstSentAt']
                : throw self::invalid('The field firstSentAt of a PendingSend must be an integer.'),
        );
    }

    /**
     * @param array<mixed> $data
     */
    private static function string(array $data, string $field): string
    {
        $value = $data[$field] ?? null;

        return is_string($value) ? $value : throw self::invalid("The field {$field} of a PendingSend must be a string.");
    }

    private static function invalid(string $detail): CalyxException
    {
        return new CalyxException(Source::Client, ClientCode::INVALID_CONFIG, 0, $detail);
    }
}
