<?php

declare(strict_types=1);

namespace Calyx\Client\Event;

/** The payload of "message.status". */
final readonly class MessageStatus
{
    /**
     * @param string      $messageId Identifies the message.
     * @param string      $chatId    The conversation of the message.
     * @param string|null $chatAltId The other form of chatId. Null when unknown.
     * @param string      $status    The new delivery status. See Statuses. The value is open.
     */
    public function __construct(
        public string $messageId,
        public string $chatId,
        public ?string $chatAltId,
        public string $status,
    ) {}

    /**
     * @param array<mixed> $data
     */
    public static function fromArray(array $data): self
    {
        $r = new PayloadReader(EventNames::MESSAGE_STATUS, $data);

        return new self(
            $r->string('messageId'),
            $r->string('chatId'),
            $r->stringOrNull('chatAltId'),
            $r->string('status'),
        );
    }
}
