<?php

declare(strict_types=1);

namespace Calyx\Client\Event;

/** The payload of "message.received". */
final readonly class MessageReceived
{
    /**
     * @param string      $messageId Identifies the message.
     * @param string      $chatId    The conversation: a phone number JID, a LID or a group ID.
     * @param string|null $chatAltId The other form of chatId. Null when unknown.
     * @param string      $from      The sender. It differs from chatId only in groups.
     * @param string|null $fromAltId The other form of from. Null when unknown.
     * @param bool        $isGroup   True when the chat is a group.
     * @param string|null $pushName  The display name of the sender. Null when unknown.
     * @param int         $timestamp The Unix time of the message, in seconds.
     * @param string      $type      The content type of the provider, for example "text".
     * @param string|null $text      The text or the caption. Null when the message has none.
     */
    public function __construct(
        public string $messageId,
        public string $chatId,
        public ?string $chatAltId,
        public string $from,
        public ?string $fromAltId,
        public bool $isGroup,
        public ?string $pushName,
        public int $timestamp,
        public string $type,
        public ?string $text,
    ) {}

    /**
     * @param array<mixed> $data
     */
    public static function fromArray(array $data): self
    {
        $r = new PayloadReader(EventNames::MESSAGE_RECEIVED, $data);

        return new self(
            $r->string('messageId'),
            $r->string('chatId'),
            $r->stringOrNull('chatAltId'),
            $r->string('from'),
            $r->stringOrNull('fromAltId'),
            $r->bool('isGroup'),
            $r->stringOrNull('pushName'),
            $r->int('timestamp'),
            $r->string('type'),
            $r->stringOrNull('text'),
        );
    }
}
