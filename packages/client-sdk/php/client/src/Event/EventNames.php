<?php

declare(strict_types=1);

namespace Calyx\Client\Event;

/**
 * The names of the typed events. They are strings, not an enum, because a newer core can send
 * a name that this list does not have. Other events are `whatsmeow.*` and `meta.webhook`.
 */
final class EventNames
{
    public const string CONNECTION_UPDATE = 'connection.update';
    public const string MESSAGE_RECEIVED = 'message.received';
    public const string MESSAGE_STATUS = 'message.status';
    public const string BULK_FINISHED = 'bulk.finished';
    public const string META_WEBHOOK = 'meta.webhook';
}
