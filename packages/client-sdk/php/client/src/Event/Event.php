<?php

declare(strict_types=1);

namespace Calyx\Client\Event;

use Calyx\Client\CalyxException;
use Calyx\Client\Generated\ClientCode;
use Calyx\Client\Internal\ErrorDecoder;
use Calyx\Client\Internal\Json;
use JsonException;

/**
 * One event of the core, as a webhook or the core stream delivers it. Call the method that fits
 * the event name. A port of packages/client-sdk/go/calyx/event.go.
 */
final readonly class Event
{
    /**
     * @param string       $id        Identifies the event. A retry delivers the same id again.
     * @param string       $event     The event name, for example "message.received". See EventNames.
     * @param string       $instance  The core name of the instance.
     * @param string       $timestamp The time of the event in RFC 3339, with the full precision
     *                                (9 digits). It stays a string, because a PHP DateTime keeps 6.
     * @param array<mixed> $data      The payload as decoded JSON.
     */
    public function __construct(
        public string $id,
        public string $event,
        public string $instance,
        public string $timestamp,
        public array $data,
    ) {}

    /**
     * Decodes a webhook body, with no signature check. A body that is not an event gives
     * invalid_event. The error never holds the text of the body.
     *
     * @throws CalyxException
     */
    public static function fromJson(string $body): self
    {
        try {
            $v = Json::decode($body);
        } catch (JsonException $cause) {
            throw self::invalid('The body is not valid JSON.', $cause);
        }

        return self::fromArray($v);
    }

    /**
     * Builds an event from decoded JSON. The rules are CONTRACT part 10, rule 1.
     *
     * @throws CalyxException
     */
    public static function fromArray(mixed $v): self
    {
        // An empty array is an empty object or an empty list. It has no id, so it fails below.
        if (!is_array($v) || array_is_list($v)) {
            throw self::invalid('The body is not a Calyx event.');
        }
        $id = $v['id'] ?? null;
        $name = $v['event'] ?? null;
        $instance = $v['instance'] ?? null;
        $timestamp = $v['timestamp'] ?? null;
        // A present null is an empty payload. A missing field is not an event.
        $data = array_key_exists('data', $v) ? ($v['data'] ?? []) : null;
        if (
            !is_string($id) || $id === ''
            || !is_string($name) || $name === ''
            || !is_string($instance) || $instance === ''
            || !is_string($timestamp)
            || !is_array($data)
        ) {
            throw self::invalid('The body is not a Calyx event.');
        }

        return new self($id, $name, $instance, $timestamp, $data);
    }

    /** @throws CalyxException */
    public function connectionUpdate(): ConnectionUpdate
    {
        return ConnectionUpdate::fromArray($this->payload(EventNames::CONNECTION_UPDATE));
    }

    /** @throws CalyxException */
    public function messageReceived(): MessageReceived
    {
        return MessageReceived::fromArray($this->payload(EventNames::MESSAGE_RECEIVED));
    }

    /** @throws CalyxException */
    public function messageStatus(): MessageStatus
    {
        return MessageStatus::fromArray($this->payload(EventNames::MESSAGE_STATUS));
    }

    /** @throws CalyxException */
    public function bulkFinished(): BulkFinished
    {
        return BulkFinished::fromArray($this->payload(EventNames::BULK_FINISHED));
    }

    /**
     * @return array<mixed>
     *
     * @throws CalyxException
     */
    private function payload(string $name): array
    {
        if ($this->event !== $name) {
            throw self::invalid("The event is {$this->event}, not {$name}.");
        }

        return $this->data;
    }

    private static function invalid(string $detail, ?JsonException $cause = null): CalyxException
    {
        return ErrorDecoder::client(ClientCode::INVALID_EVENT, $detail, null, $cause);
    }
}
