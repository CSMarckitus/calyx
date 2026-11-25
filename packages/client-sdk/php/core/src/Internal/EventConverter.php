<?php

declare(strict_types=1);

namespace Calyx\Client\Core\Internal;

use Calyx\Client\CalyxException;
use Calyx\Client\Event\Event;
use Calyx\Client\Generated\ClientCode;
use Calyx\Client\Source;
use Calyx\Core\V1\Event as CoreEvent;
use JsonException;
use Throwable;

/**
 * Converts an event of the Subscribe stream to the Event of the backend package (CONTRACT part
 * 10, rule 5). A port of toEvent in packages/client-sdk/go/core/streams.go.
 *
 * @internal
 */
final class EventConverter
{
    // The range of a valid google.protobuf.Timestamp: 0001-01-01 to 9999-12-31, in UTC.
    private const int MIN_SECONDS = -62135596800;
    private const int MAX_SECONDS = 253402300799;

    /**
     * @throws CalyxException
     */
    public static function toEvent(CoreEvent $event): Event
    {
        $time = $event->getOccurredAt();
        $seconds = (int) $time?->getSeconds();
        $nanos = (int) $time?->getNanos();
        if ($time === null || $seconds < self::MIN_SECONDS || $seconds > self::MAX_SECONDS || $nanos < 0 || $nanos > 999_999_999) {
            throw self::invalid('The event has no valid time.');
        }
        try {
            $data = json_decode($event->getPayloadJson(), true, 512, JSON_THROW_ON_ERROR);
        } catch (JsonException $cause) {
            throw self::invalid('The payload of the event is not valid JSON.', $cause);
        }
        // The format of the webhook body (Go RFC3339Nano): no zeros at the end of the fraction.
        $fraction = $nanos === 0 ? '' : '.' . rtrim(sprintf('%09d', $nanos), '0');

        // fromArray refuses an empty id, name or instance, and a payload that is not an object.
        return Event::fromArray([
            'id' => $event->getId(),
            'event' => $event->getType(),
            'instance' => $event->getInstanceName(),
            'timestamp' => gmdate('Y-m-d\TH:i:s', $seconds) . $fraction . 'Z',
            'data' => $data,
        ]);
    }

    private static function invalid(string $detail, ?Throwable $cause = null): CalyxException
    {
        return new CalyxException(Source::Client, ClientCode::INVALID_EVENT, 0, $detail, previous: $cause);
    }
}
